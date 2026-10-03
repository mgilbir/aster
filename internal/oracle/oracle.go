// Package oracle drives the test oracle: upstream Vega / Vega-Lite (and resvg)
// running in node, testdata/oracle-node/oracle.mjs. Tests compare the engine
// with what upstream renders, compiles and rasterizes for the same input.
//
// Answers are cached in testdata/oracle-cache (git-ignored) under a key that
// covers the oracle's versions, script and lockfile, so they are recreated
// whenever any of them change and never need to be committed.
//
// Without node or the installed modules the tests that need the oracle skip;
// with ASTER_ORACLE=require (as in CI) they fail instead. Install with
//
//	(cd testdata/oracle-node && npm ci)
//	(cd testdata/oracle-node-vl5 && npm ci)   # Vega-Lite 5.8 compilation
package oracle

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Module sets, relative to the repository root.
const (
	// VL6 is upstream Vega 6.4.0 / Vega-Lite 6.4.3, plus resvg for PNG.
	VL6 = "testdata/oracle-node"
	// VL5 is Vega-Lite 5.8.0 with its Vega 5, for the 5.8 compiler.
	VL5 = "testdata/oracle-node-vl5"
)

const (
	script   = "testdata/oracle-node/oracle.mjs"
	sweepDir = "testdata/oracle-node/sweeps"
	cache    = "testdata/oracle-cache"
	timeout  = 2 * time.Minute
)

// Result is one answer. Err is upstream's error for an input it rejects.
type Result struct {
	SVG  string          `json:"svg,omitempty"`
	Vega json.RawMessage `json:"vega,omitempty"`
	PNG  []byte          `json:"png,omitempty"`
	// Data is a generator's output (Generate).
	Data json.RawMessage `json:"data,omitempty"`
	Err  string          `json:"err,omitempty"`
}

// Oracle serves a module set from a small pool of node processes, so callers
// on several goroutines are answered in parallel. It is safe for concurrent
// use.
type Oracle struct {
	root        string // repository root
	set         string
	zone        string // TZ of the node processes
	fingerprint string // part of every cache key
	version     string
	free        chan *proc // idle processes; nil entries are slots not yet started
}

// proc is one node process answering one request at a time.
type proc struct {
	o      *Oracle
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte // stdout, one response per line; closed when node exits
	nextID int
}

var oracles sync.Map // set → *Oracle

// For returns the oracle for a module set (VL6 or VL5), running in UTC, or
// skips the test (fails with ASTER_ORACLE=require) when it is not installed.
func For(t testing.TB, set string) *Oracle {
	t.Helper()
	return ForZone(t, set, "UTC")
}

// ForZone is For with node running in the IANA time zone zone (TZ), for
// comparing local-time behaviour.
func ForZone(t testing.TB, set, zone string) *Oracle {
	t.Helper()
	key := set + "\x00" + zone
	if o, ok := oracles.Load(key); ok {
		return o.(*Oracle)
	}
	unavailable := func(format string, args ...any) *Oracle {
		t.Helper()
		msg := fmt.Sprintf(format, args...)
		if os.Getenv("ASTER_ORACLE") == "require" {
			t.Fatalf("node oracle required but unavailable: %s", msg)
		}
		t.Skipf("node oracle unavailable (%s); install with: (cd %s && npm ci)", msg, set)
		return nil
	}
	root, err := repoRoot()
	if err != nil {
		return unavailable("%v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		return unavailable("node not on PATH")
	}
	if _, err := os.Stat(filepath.Join(root, set, "node_modules", "vega")); err != nil {
		return unavailable("%s/node_modules missing", set)
	}
	h := sha256.New()
	for _, f := range []string{script, filepath.Join(set, "package-lock.json")} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return unavailable("%v", err)
		}
		h.Write(b)
	}
	o := &Oracle{root: root, set: set, zone: zone, free: make(chan *proc, poolSize())}
	first, err := o.start()
	if err != nil {
		return unavailable("%v", err)
	}
	o.free <- first
	for i := 1; i < cap(o.free); i++ {
		o.free <- nil
	}
	h.Write([]byte(o.version))
	o.fingerprint = hex.EncodeToString(h.Sum(nil))
	actual, loaded := oracles.LoadOrStore(key, o)
	if loaded {
		first.stop()
	}
	return actual.(*Oracle)
}

// poolSize is the number of node processes per module set: ASTER_ORACLE_PROCS,
// else half the CPUs, between 1 and 8.
func poolSize() int {
	if n, err := strconv.Atoi(os.Getenv("ASTER_ORACLE_PROCS")); err == nil && n > 0 {
		return n
	}
	return min(max(runtime.NumCPU()/2, 1), 8)
}

// Version describes the oracle's modules, node and text stack.
func (o *Oracle) Version() string { return o.version }

// SVG is upstream's rendering of spec and, for Vega-Lite, its compiled Vega.
func (o *Oracle) SVG(lite bool, spec []byte) (Result, error) {
	return o.ask(map[string]any{"op": "svg", "lite": lite, "spec": string(spec)})
}

// Compile is upstream's Vega-Lite compilation of spec.
func (o *Oracle) Compile(spec []byte) (Result, error) {
	return o.ask(map[string]any{"op": "compile", "lite": true, "spec": string(spec)})
}

// PNG is resvg's rasterization of svg at scale, with the engine's default
// fonts (VL6 only).
func (o *Oracle) PNG(svg []byte, scale float64) (Result, error) {
	return o.ask(map[string]any{"op": "png", "svg": string(svg), "scale": scale})
}

// SignalWrite is one View.signal(name, value) call.
type SignalWrite struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// Signals is upstream's rendering of a Vega spec after the writes: it renders
// once, sets each signal in order (View.signal, then runAsync), and renders
// again; the second SVG is the answer.
func (o *Oracle) Signals(spec []byte, writes []SignalWrite) (Result, error) {
	return o.ask(map[string]any{"op": "signals", "spec": string(spec), "writes": writes})
}

// Generate runs the generator testdata/oracle-node/sweeps/<name>.mjs and
// returns its output (Result.Data). The generator's source is part of the
// cache key, so editing it regenerates; so is the corpus manifest
// testdata/corpora/<name>.sha256, when there is one, for a generator that
// reads a fetched corpus.
func (o *Oracle) Generate(name string) (Result, error) {
	src, err := os.ReadFile(filepath.Join(o.root, sweepDir, name+".mjs"))
	if err != nil {
		return Result{}, err
	}
	if m, err := os.ReadFile(filepath.Join(o.root, "testdata/corpora", name+".sha256")); err == nil {
		src = append(src, m...)
	}
	sum := sha256.Sum256(src)
	return o.ask(map[string]any{"op": "generate", "sweep": name, "rev": hex.EncodeToString(sum[:])})
}

// ask answers from the cache or from node. The error reports an oracle
// failure (node died or misbehaved), not an input upstream rejects.
func (o *Oracle) ask(req map[string]any) (Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(append([]byte(o.fingerprint+"\x00"), body...))
	key := hex.EncodeToString(sum[:])
	path := filepath.Join(o.root, cache, key[:2], key+".json")
	var res Result
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &res) == nil {
		return res, nil
	}

	p := <-o.free
	defer func() { o.free <- p }()
	if p == nil || p.cmd == nil {
		if p, err = o.start(); err != nil {
			p = nil
			return res, err
		}
	}
	res, err = p.ask(req)
	if err != nil {
		return res, err
	}
	if b, err := json.Marshal(res); err == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			// Written whole and renamed, so a concurrent reader never sees a
			// partial answer.
			tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
			if os.WriteFile(tmp, b, 0o644) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	return res, nil
}

func (p *proc) ask(req map[string]any) (Result, error) {
	var res Result
	p.nextID++
	req["id"] = p.nextID
	line, err := json.Marshal(req)
	if err != nil {
		return res, err
	}
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		p.stop()
		return res, fmt.Errorf("oracle: %w", err)
	}
	select {
	case out, ok := <-p.lines:
		if !ok {
			p.stop()
			return res, errors.New("oracle: node exited")
		}
		var got struct {
			ID   int             `json:"id"`
			SVG  string          `json:"svg"`
			Veg  json.RawMessage `json:"vega"`
			PNG  string          `json:"png"`
			Data json.RawMessage `json:"data"`
			Err  string          `json:"err"`
		}
		if err := json.Unmarshal(out, &got); err != nil || got.ID != p.nextID {
			p.stop()
			return res, fmt.Errorf("oracle: bad response %.200q", out)
		}
		res = Result{SVG: got.SVG, Vega: got.Veg, Data: got.Data, Err: got.Err}
		if got.PNG != "" {
			if res.PNG, err = base64.StdEncoding.DecodeString(got.PNG); err != nil {
				return res, fmt.Errorf("oracle: bad PNG: %w", err)
			}
		}
	case <-time.After(timeout):
		// An input upstream cannot finish is an answer too; restart node
		// for the next request.
		p.stop()
		res.Err = fmt.Sprintf("oracle: no answer within %v", timeout)
	}
	return res, nil
}

func (o *Oracle) start() (*proc, error) {
	// Run in the module set's directory, so a node version manager (volta)
	// applies the version pinned in its package.json; CI installs the same.
	cmd := exec.Command("node", filepath.Join(o.root, script))
	cmd.Dir = filepath.Join(o.root, o.set)
	cmd.Env = append(os.Environ(), "TZ="+o.zone, "NODE_PATH="+filepath.Join(o.root, o.set, "node_modules"),
		"ASTER_ORACLE_PARENT="+strconv.Itoa(os.Getpid()))
	cmd.Stderr = os.Stderr
	ownGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	lines := make(chan []byte, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<31)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	p := &proc{o: o, cmd: cmd, stdin: stdin, lines: lines}
	select {
	case line, ok := <-lines:
		var ready struct {
			Ready   bool   `json:"ready"`
			Version string `json:"version"`
		}
		if !ok || json.Unmarshal(line, &ready) != nil || !ready.Ready {
			p.stop()
			return nil, errors.New("oracle did not start")
		}
		if o.version != "" && ready.Version != o.version {
			p.stop()
			return nil, fmt.Errorf("oracle: a new process reports %q, the first reported %q", ready.Version, o.version)
		}
		if o.version == "" { // the first process, started before the pool is shared
			o.version = ready.Version
		}
		return p, nil
	case <-time.After(time.Minute):
		p.stop()
		return nil, errors.New("oracle start timed out")
	}
}

func (p *proc) stop() {
	if p.cmd == nil {
		return
	}
	_ = p.stdin.Close()
	killGroup(p.cmd)
	_ = p.cmd.Wait() // killed: the error says so
	p.cmd = nil
}

// repoRoot finds the module root from the working directory (a test runs in
// its package directory).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, script)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}
