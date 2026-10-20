package oracle

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// cachedFiles lists the answers cached under root.
func cachedFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(root, cache), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// An oracle that fails for a reason of its own (oracle.mjs's oracleErr) gives
// no answer: the request fails with ErrFailed, and nothing is cached, so a
// failure of the environment (resvg-napi not loading, a generator throwing)
// is never read later as upstream rejecting the input (C51, audit
// 2026-10-18). The process is a stand-in that answers one line.
func TestOracleFailureIsNotAnAnswer(t *testing.T) {
	o := &Oracle{root: t.TempDir(), fingerprint: "test", free: make(chan *proc, 1)}
	lines := make(chan []byte, 1)
	lines <- []byte(`{"id":1,"oracleErr":"resvg-napi did not load in this module set: x"}`)
	cmd := exec.Command("sleep", "600")
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep command: %v", err)
	}
	p := &proc{o: o, cmd: cmd, stdin: discard{io.Discard}, lines: lines}
	t.Cleanup(p.stop)
	o.free <- p
	res, err := o.ask(map[string]any{"op": "png", "svg": "<svg/>", "scale": 1})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("an oracle failure gave %+v, %v; want ErrFailed", res, err)
	}
	if c := cachedFiles(o.root); len(c) != 0 {
		t.Fatalf("an oracle failure was cached: %v", c)
	}
}

// The real oracle reports its own failures as such: a PNG from the module
// set that has no resvg-napi, a sweep generator that does not exist, an
// unknown request. None is cached.
func TestOracleReportsItsOwnFailures(t *testing.T) {
	o := For(t, VL5) // has no resvg-napi
	reqs := map[string]map[string]any{
		"png without resvg-napi": {"op": "png", "svg": `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`, "scale": 1},
		"a generator that fails": {"op": "generate", "sweep": "no-such-sweep", "rev": "x"},
		"a bad generator name":   {"op": "generate", "sweep": "../x", "rev": "x"},
		"an unknown op":          {"op": "nope"},
		"an unknown option":      {"op": "svg", "spec": "{}", "wordcloud": "pixels"},
	}
	for name, req := range reqs {
		path, err := o.cachePath(req)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 2 { // and not cached: the second ask fails the same way
			res, err := o.ask(req)
			if !errors.Is(err, ErrFailed) {
				t.Errorf("%s (ask %d): %+v, %v; want ErrFailed", name, i+1, res, err)
			}
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s: cached as an answer", name)
		}
	}
}

// The version line names what the expectation files key platform-dependent
// statuses to: the Pango version and node's platform and arch.
func TestVersionNamesThePlatform(t *testing.T) {
	v := For(t, VL6).Version()
	for _, re := range []string{`pango \d+\.\d+`, `platform [a-z0-9]+-[a-z0-9]+`, `node v\d+\.\d+\.\d+`, `resvg-napi \d`} {
		if !regexp.MustCompile(re).MatchString(v) {
			t.Errorf("version %q does not match %s", v, re)
		}
	}
}

func TestPinnedNode(t *testing.T) {
	for _, set := range []string{VL6, VL5} {
		root, err := repoRoot()
		if err != nil {
			t.Fatal(err)
		}
		pin, err := pinnedNode(filepath.Join(root, set, "package.json"))
		if err != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(pin) {
			t.Errorf("%s: pin %q, %v", set, pin, err)
		}
	}
	if m := nodeVersion.FindStringSubmatch("vega 6.4.0 / vega-lite 6.4.3 / node v24.21.0 / canvas 3.2.0"); m == nil || m[1] != "24.21.0" {
		t.Errorf("node version read as %v", m)
	}
}
