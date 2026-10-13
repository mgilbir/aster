package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// cli builds the command once and runs it with args, returning its exit
// status and stderr: the exit status is what a script sees, which the
// functions the other tests call do not show.
func cli(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aster-cli")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "aster")
		out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			binErr = errors.New(string(out))
		}
	})
	if binErr != nil {
		t.Fatalf("building the command: %v", binErr)
	}
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), stderr.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, stderr.String()
}

// Every invalid flag value exits 2, as docs/cli.md says, not only what the
// flag package rejects (C73: -text x, -from x and -timeout -1s exited 1), and
// so does a command line with no command. A failure of the render exits 1.
func TestExitStatus(t *testing.T) {
	spec := filepath.Join(t.TempDir(), "bar.vl.json")
	if err := os.WriteFile(spec, []byte(`{"data":{"values":[{"a":1}]},"mark":"bar","encoding":{"x":{"field":"a","type":"quantitative"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	theme := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(theme, []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	for _, tc := range []struct {
		args []string
		want int
	}{
		{nil, 2},
		{[]string{"nope"}, 2},
		{[]string{"svg", "-bogus"}, 2},
		{[]string{"pdf", "-i", spec, "-o", out, "-text", "x"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-from", "x"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-timeout", "-1s"}, 2},
		{[]string{"png", "-i", spec, "-o", out, "-scale", "-1"}, 2},
		{[]string{"png", "-i", spec, "-o", out, "-scale", "0"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-version", "9.9"}, 2},
		{[]string{"compile", "-i", spec, "-o", out, "-version", "9.9"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-signal", "novalue"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-timezone", "Not/AZone"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-memory-limit", "lots"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-theme", theme}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-theme", theme + ".missing"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-font", "nofile"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-allow-file", spec + ".missing"}, 2},
		{[]string{"svg", "-i", spec, "-o", out, "-base-url", "::"}, 2},
		{[]string{"svg", "-i", spec, "-o", out}, 0},
		{[]string{"svg", "-h"}, 0},
		{[]string{"help"}, 0},
		{[]string{"svg", "-i", spec + ".missing", "-o", out}, 1},
		{[]string{"svg", "-i", spec, "-o", out, "-signal", "nope=1"}, 1}, // the spec has no such signal
	} {
		if got, stderr := cli(t, "", tc.args...); got != tc.want {
			t.Errorf("aster %s: exit %d, want %d\n%s", strings.Join(tc.args, " "), got, tc.want, stderr)
		}
	}
}

// Local data loads with -allow-file, relative URLs with -base-url, and the
// two compose (C72: the CLI could not render a Vega-Lite example with
// data/cars.json, and said only that no loader was configured).
func TestLocalAndRelativeData(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "d.json"), []byte(`[{"a":"FROMFILE"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "spec.vl.json")
	if err := os.WriteFile(spec, []byte(`{"data":{"url":"data/d.json"},"mark":"text","encoding":{"text":{"field":"a"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.svg")

	// Refused, with what to do.
	err := runSVG([]string{"-i", spec, "-o", out})
	if err == nil || !strings.Contains(err.Error(), "-allow-file") || !strings.Contains(err.Error(), "-base-url") {
		t.Errorf("default: err = %v, want the refused load with a hint naming -allow-file and -base-url", err)
	}
	err = runSVG([]string{"-i", spec, "-o", out, "-allow-http"})
	if err == nil || !strings.Contains(err.Error(), "-base-url") {
		t.Errorf("-allow-http: err = %v, want a hint naming -base-url", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/charts/data/d.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"a":"FROMFILE"}]`))
	}))
	defer ts.Close()
	for _, args := range [][]string{
		{"-allow-file", dir},
		{"-allow-file", t.TempDir(), "-allow-file", dir}, // the first has no such file
		{"-allow-file", dir, "-allow-http"},
		{"-base-url", ts.URL + "/charts/", "-allow-private-networks"},
		{"-allow-file", t.TempDir(), "-base-url", ts.URL + "/charts/", "-allow-private-networks"},
	} {
		if err := runSVG(append([]string{"-i", spec, "-o", out}, args...)); err != nil {
			t.Errorf("%v: %v", args, err)
			continue
		}
		if svg, _ := os.ReadFile(out); !bytes.Contains(svg, []byte("FROMFILE")) {
			t.Errorf("%v: the file's data is not drawn", args)
		}
	}
	// A file outside the directory stays out.
	outside := filepath.Join(t.TempDir(), "spec.vl.json")
	if err := os.WriteFile(outside, []byte(`{"data":{"url":"../`+filepath.Base(dir)+`/data/d.json"},"mark":"text","encoding":{"text":{"field":"a"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runSVG([]string{"-i", outside, "-o", out, "-allow-file", filepath.Join(dir, "data")}); err == nil {
		t.Error("loaded a file outside -allow-file's directory")
	}
}
