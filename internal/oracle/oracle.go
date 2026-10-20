// Package oracle drives the test oracle: upstream Vega / Vega-Lite (and resvg)
// running in node, testdata/oracle-node/oracle.mjs. Tests compare the engine
// with what upstream renders, compiles and rasterizes for the same input.
//
// Answers are cached in testdata/oracle-cache (git-ignored) under a key that
// covers every input the oracle's answers depend on (see inputFiles): its
// versions and platform, its scripts and lockfile, the datasets it serves and
// the fonts it measures with. So they are recreated whenever any of them
// change and never need to be committed.
//
// Without node or the installed modules the tests that need the oracle skip;
// with ASTER_ORACLE=require (as in CI) they fail instead. Install with
//
//	(cd testdata/oracle-node && npm ci)
//	(cd testdata/oracle-node-vl5 && npm ci)   # Vega-Lite 5.8 compilation
//
// node must be the version the module set pins (package.json's volta.node):
// another V8 rounds some arithmetic differently in the last bit, and those
// differences would read as the engine's. Another node is refused, as if the
// oracle were not installed, unless ASTER_ORACLE_ANY_NODE=1 says to use it
// anyway (its version is part of the cache key, so its answers never mix with
// the pinned node's).
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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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
)

// answerTimeout is how long node may take over one request.
var answerTimeout = 2 * time.Minute

// ErrNoAnswer is the error of a request node did not answer in time. It is an
// oracle failure, not upstream's answer: it is never cached, and a test must
// not count it as upstream rejecting the input (which would make "both
// fail" look like agreement).
var ErrNoAnswer = errors.New("oracle: no answer in time")

// ErrFailed is the error of a request the oracle could not answer for a
// reason of its own (oracle.mjs reports it as oracleErr: resvg-napi did not
// load, a generator threw, a request it cannot parse). Like ErrNoAnswer it is
// not upstream's answer: it is never cached, and the test that asked fails.
var ErrFailed = errors.New("oracle: failed")

// legacyTimeout is how a timeout used to be recorded, as upstream's error, in
// the cache; such an entry is not an answer and is asked again.
const legacyTimeout = "oracle: no answer within "

// Result is one answer. Err is upstream's error for an input it rejects.
type Result struct {
	SVG  string          `json:"svg,omitempty"`
	Vega json.RawMessage `json:"vega,omitempty"`
	PNG  []byte          `json:"png,omitempty"`
	// Data is a generator's output (Generate).
	Data json.RawMessage `json:"data,omitempty"`
	Err  string          `json:"err,omitempty"`
	// Warnings are what the Vega-Lite compiler logged (Compile only).
	Warnings []string `json:"warnings,omitempty"`

	// fontRejected: the answer involved a font string the canvas refused
	// (oracle.mjs), so it depends on what the process measured before.
	fontRejected bool
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
	// fontDir is the process's fontconfig directory (oracle.mjs): made here
	// and removed when the process is stopped, since a process killed in a
	// replacement or a timeout runs no exit handler of its own.
	fontDir string
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
	pin, err := pinnedNode(filepath.Join(root, set, "package.json"))
	if err != nil {
		return unavailable("%v", err)
	}
	h := sha256.New()
	inputs, err := inputsHash(root)
	if err != nil {
		return unavailable("%v", err)
	}
	h.Write(inputs)
	lock, err := os.ReadFile(filepath.Join(root, set, "package-lock.json"))
	if err != nil {
		return unavailable("%v", err)
	}
	h.Write(lock)
	o := &Oracle{root: root, set: set, zone: zone, free: make(chan *proc, poolSize())}
	first, err := o.start()
	if err != nil {
		return unavailable("%v", err)
	}
	if v := nodeVersion.FindStringSubmatch(o.version); v == nil || v[1] != pin {
		got := "an unknown node"
		if v != nil {
			got = "node " + v[1]
		}
		if os.Getenv("ASTER_ORACLE_ANY_NODE") != "1" {
			first.stop()
			return unavailable("%s is not the node %s pins (volta.node %s): another V8 differs in the last bit; "+
				"put node %s first on PATH, or set ASTER_ORACLE_ANY_NODE=1 to use this one", got, set, pin, pin)
		}
		t.Logf("oracle: %s, not the pinned node %s (ASTER_ORACLE_ANY_NODE=1): its last-bit arithmetic may differ", got, pin)
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

// nodeVersion reads "node v24.21.0" from the oracle's version line.
var nodeVersion = regexp.MustCompile(`node v(\d+\.\d+\.\d+)`)

// pinnedNode is the node version a module set pins: volta.node in its
// package.json, which CI's setup-node reads too.
func pinnedNode(pkgJSON string) (string, error) {
	b, err := os.ReadFile(pkgJSON)
	if err != nil {
		return "", err
	}
	var p struct {
		Volta struct {
			Node string `json:"node"`
		} `json:"volta"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return "", fmt.Errorf("%s: %w", pkgJSON, err)
	}
	if p.Volta.Node == "" {
		return "", fmt.Errorf("%s pins no node version (volta.node)", pkgJSON)
	}
	return p.Volta.Node, nil
}

// inputFiles are the files and directories, relative to the repository root,
// whose content oracle.mjs reads: its scripts, the dataset URL patterns and
// the datasets it serves, and the fonts it measures and rasterizes with. All
// of them, with the module set's lockfile and the version line (node, its
// platform, the text stack), make the cache key, so editing any input asks
// node again instead of reusing an answer made from the old one (C77, audit
// 2026-10-18). A sweep generator's source is part of its own request
// (Generate).
var inputFiles = []string{
	script,
	"testdata/oracle-node/wordcloud-box.mjs",
	"testdata/oracle-node/dataset-urls.json",
	"testdata/vega-datasets",
	"testdata/data",
	"internal/fonts/dejavu",
	"internal/fonts/liberation",
	"internal/fonts/notoemoji",
}

// inputsHash hashes the names and contents of inputFiles, once per process.
var inputsHash = func() func(root string) ([]byte, error) {
	var once sync.Once
	var sum []byte
	var err error
	return func(root string) ([]byte, error) {
		once.Do(func() {
			h := sha256.New()
			for _, in := range inputFiles {
				err = filepath.WalkDir(filepath.Join(root, in), func(path string, d os.DirEntry, werr error) error {
					if werr != nil || d.IsDir() {
						return werr
					}
					b, rerr := os.ReadFile(path)
					if rerr != nil {
						return rerr
					}
					rel, _ := filepath.Rel(root, path)
					_, _ = fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b)) // a hash never fails a write
					h.Write(b)
					return nil
				})
				if err != nil {
					return
				}
			}
			sum = h.Sum(nil)
		})
		return sum, err
	}
}()

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
// fonts (VL6 only). resvg-napi missing from the module set is ErrFailed, not
// a rejection of svg.
func (o *Oracle) PNG(svg []byte, scale float64) (Result, error) {
	return o.ask(map[string]any{"op": "png", "svg": string(svg), "scale": scale})
}

// WordcloudBoxSVG is upstream's rendering of a Vega spec with the wordcloud
// layout drawing on the deterministic canvas of
// testdata/oracle-node/wordcloud-box.mjs (which wordcloud.BoxRenderer
// reproduces) instead of node-canvas, whose glyph rasterization depends on the
// platform: the transform compared the same on every platform.
func (o *Oracle) WordcloudBoxSVG(spec []byte) (Result, error) {
	return o.ask(map[string]any{"op": "svg", "spec": string(spec), "wordcloud": "box"})
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
	path, err := o.cachePath(req)
	if err != nil {
		return Result{}, err
	}
	var res Result
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &res) == nil && !strings.HasPrefix(res.Err, legacyTimeout) {
		return res, nil
	}
	res = Result{}

	if res, err = o.node(req); err != nil {
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

// node answers from one of the pool's processes, starting it if it is not
// running (it never ran, or it stopped: it timed out, died, or upstream
// changed a built-in while answering, so a later answer would not be a
// fresh process's).
func (o *Oracle) node(req map[string]any) (Result, error) {
	p := <-o.free
	defer func() { o.free <- p }()
	if p == nil || p.cmd == nil {
		var err error
		if p, err = o.start(); err != nil {
			p = nil
			return Result{}, err
		}
	}
	res, err := p.ask(req)
	if err == nil && res.fontRejected && p.nextID > 1 {
		// The canvas kept the font it held when it refused one, and the
		// process held what earlier requests left: the answer is a fresh
		// process's only if this process was fresh. Ask a fresh one.
		p.stop()
		var np *proc
		if np, err = o.start(); err != nil {
			return Result{}, err
		}
		p = np
		res, err = p.ask(req)
	}
	return res, err
}

// cachePath is where the answer to req is cached: its key covers the
// oracle's fingerprint and the whole request.
func (o *Oracle) cachePath(req map[string]any) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(o.fingerprint+"\x00"), body...))
	key := hex.EncodeToString(sum[:])
	return filepath.Join(o.root, cache, key[:2], key+".json"), nil
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
			Warn []string        `json:"warnings"`
			// Tainted names a built-in upstream changed while answering
			// (oracle.mjs): the answer stands, but a later one from this
			// process would not be a fresh process's, so it is replaced.
			Tainted string `json:"tainted"`
			// FontRejected: see Result.
			FontRejected bool `json:"fontRejected"`
			// OracleErr is the oracle failing to answer: no answer.
			OracleErr string `json:"oracleErr"`
		}
		if err := json.Unmarshal(out, &got); err != nil || got.ID != p.nextID {
			p.stop()
			return res, fmt.Errorf("oracle: bad response %.200q", out)
		}
		if got.Tainted != "" {
			p.stop()
		}
		if got.OracleErr != "" {
			return Result{}, fmt.Errorf("%w: %s", ErrFailed, got.OracleErr)
		}
		res = Result{SVG: got.SVG, Vega: got.Veg, Data: got.Data, Err: got.Err, Warnings: got.Warn, fontRejected: got.FontRejected}
		if got.PNG != "" {
			if res.PNG, err = base64.StdEncoding.DecodeString(got.PNG); err != nil {
				return res, fmt.Errorf("oracle: bad PNG: %w", err)
			}
		}
	case <-time.After(answerTimeout):
		// Restart node for the next request. A slow runner is not upstream
		// rejecting the input, so this is a failure of the oracle (and not
		// cached), not an answer.
		p.stop()
		return Result{}, fmt.Errorf("%w (%v)", ErrNoAnswer, answerTimeout)
	}
	return res, nil
}

func (o *Oracle) start() (*proc, error) {
	// Run in the module set's directory, so a node version manager (volta)
	// applies the version pinned in its package.json; CI installs the same.
	fontDir, err := os.MkdirTemp("", "aster-oracle-fonts-")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("node", filepath.Join(o.root, script))
	cmd.Dir = filepath.Join(o.root, o.set)
	cmd.Env = append(os.Environ(), "TZ="+o.zone, "NODE_PATH="+filepath.Join(o.root, o.set, "node_modules"),
		"ASTER_ORACLE_PARENT="+strconv.Itoa(os.Getpid()), "ASTER_ORACLE_FONTDIR="+fontDir)
	cmd.Stderr = os.Stderr
	ownGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(fontDir)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(fontDir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(fontDir)
		return nil, err
	}
	lines := make(chan []byte, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), math.MaxInt32)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	p := &proc{o: o, cmd: cmd, stdin: stdin, lines: lines, fontDir: fontDir}
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
	_ = os.RemoveAll(p.fontDir)
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
