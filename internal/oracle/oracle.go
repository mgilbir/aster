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
	script  = "testdata/oracle-node/oracle.mjs"
	cache   = "testdata/oracle-cache"
	timeout = 2 * time.Minute
)

// Result is one answer. Err is upstream's error for an input it rejects.
type Result struct {
	SVG  string          `json:"svg,omitempty"`
	Vega json.RawMessage `json:"vega,omitempty"`
	PNG  []byte          `json:"png,omitempty"`
	Err  string          `json:"err,omitempty"`
}

// Oracle is one node process serving a module set. It is safe for concurrent
// use; requests are answered one at a time.
type Oracle struct {
	root        string // repository root
	set         string
	fingerprint string // part of every cache key
	version     string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte // stdout, one response per line; closed when node exits
	nextID int
}

var oracles sync.Map // set → *Oracle

// For returns the oracle for a module set (VL6 or VL5), or skips the test
// (fails with ASTER_ORACLE=require) when it is not installed.
func For(t testing.TB, set string) *Oracle {
	t.Helper()
	if o, ok := oracles.Load(set); ok {
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
	o := &Oracle{root: root, set: set}
	if err := o.start(); err != nil {
		return unavailable("%v", err)
	}
	h.Write([]byte(o.version))
	o.fingerprint = hex.EncodeToString(h.Sum(nil))
	actual, loaded := oracles.LoadOrStore(set, o)
	if loaded {
		o.stop()
	}
	return actual.(*Oracle)
}

// Version describes the oracle's modules and node, e.g.
// "vega 6.4.0 / vega-lite 6.4.3 / node v24.21.0 / canvas 3.2.3".
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

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cmd == nil {
		if err := o.start(); err != nil {
			return res, err
		}
	}
	o.nextID++
	req["id"] = o.nextID
	line, err := json.Marshal(req)
	if err != nil {
		return res, err
	}
	if _, err := o.stdin.Write(append(line, '\n')); err != nil {
		o.stop()
		return res, fmt.Errorf("oracle: %w", err)
	}
	select {
	case out, ok := <-o.lines:
		if !ok {
			o.stop()
			return res, errors.New("oracle: node exited")
		}
		var got struct {
			ID  int             `json:"id"`
			SVG string          `json:"svg"`
			Veg json.RawMessage `json:"vega"`
			PNG string          `json:"png"`
			Err string          `json:"err"`
		}
		if err := json.Unmarshal(out, &got); err != nil || got.ID != o.nextID {
			o.stop()
			return res, fmt.Errorf("oracle: bad response %.200q", out)
		}
		res = Result{SVG: got.SVG, Vega: got.Veg, Err: got.Err}
		if got.PNG != "" {
			if res.PNG, err = base64.StdEncoding.DecodeString(got.PNG); err != nil {
				return res, fmt.Errorf("oracle: bad PNG: %w", err)
			}
		}
	case <-time.After(timeout):
		// An input upstream cannot finish is an answer too; restart node
		// for the next request.
		o.stop()
		res.Err = fmt.Sprintf("oracle: no answer within %v", timeout)
	}
	if b, err := json.Marshal(res); err == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}
	return res, nil
}

func (o *Oracle) start() error {
	// Run in the module set's directory, so a node version manager (volta)
	// applies the version pinned in its package.json; CI installs the same.
	cmd := exec.Command("node", filepath.Join(o.root, script))
	cmd.Dir = filepath.Join(o.root, o.set)
	cmd.Env = append(os.Environ(), "TZ=UTC", "NODE_PATH="+filepath.Join(o.root, o.set, "node_modules"))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	lines := make(chan []byte, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<30)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	o.cmd, o.stdin, o.lines = cmd, stdin, lines
	select {
	case line, ok := <-lines:
		var ready struct {
			Ready   bool   `json:"ready"`
			Version string `json:"version"`
		}
		if !ok || json.Unmarshal(line, &ready) != nil || !ready.Ready {
			o.stop()
			return errors.New("oracle did not start")
		}
		o.version = ready.Version
		return nil
	case <-time.After(time.Minute):
		o.stop()
		return errors.New("oracle start timed out")
	}
}

func (o *Oracle) stop() {
	if o.cmd == nil {
		return
	}
	o.stdin.Close()
	o.cmd.Process.Kill()
	o.cmd.Wait()
	o.cmd = nil
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
