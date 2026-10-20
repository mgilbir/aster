package main

import (
	"bytes"
	"errors"
	"io"
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
	code, _, stderr := cliIO(t, strings.NewReader(stdin), args...)
	return code, stderr
}

// cliIO is cli with any stdin (an *os.File is the child's own: a device, as
// a terminal is), returning stdout too.
func cliIO(t *testing.T, stdin io.Reader, args ...string) (code int, stdout, stderr string) {
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
	cmd.Stdin = stdin
	var outb, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outb, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), outb.String(), errb.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, outb.String(), errb.String()
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

// The input is -i FILE, or standard input when it is a pipe or a file: a
// positional argument is a usage error naming it, not silently ignored with
// the flags after it (C49, audit 2026-10-18), and a terminal (a device) is
// never read unless -i - asks for it. Every usage error, the flag package's
// included, is one line prefixed "aster:" (C145).
func TestInputModel(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "bar.vl.json")
	body := `{"data":{"values":[{"a":1}]},"mark":"bar","encoding":{"x":{"field":"a","type":"quantitative"}}}`
	if err := os.WriteFile(spec, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.svg")
	devNull := func() io.Reader {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	for _, tc := range []struct {
		name   string
		stdin  io.Reader
		args   []string
		code   int
		stderr string // a substring; and the whole of stderr is one "aster: " line
	}{
		{"positional input", strings.NewReader(body), []string{"svg", spec, "-o", out}, 2, `unexpected argument "` + spec + `"`},
		{"positional output", strings.NewReader(body), []string{"svg", "-i", spec, out}, 2, `unexpected argument "` + out + `"`},
		{"positional compile", strings.NewReader(body), []string{"compile", spec}, 2, `unexpected argument`},
		{"device stdin", devNull(), []string{"svg", "-o", out}, 2, "-i -"},
		{"device stdin, compile", devNull(), []string{"compile"}, 2, "-i -"},
		{"unknown flag", strings.NewReader(body), []string{"svg", "-bogus"}, 2, "-bogus"},
		{"missing theme file", strings.NewReader(body), []string{"svg", "-i", spec, "-theme", spec + ".missing"}, 2, "-theme"},
		{"missing font file", strings.NewReader(body), []string{"png", "-i", spec, "-font", "X=" + spec + ".missing"}, 2, "-font"},
		{"empty explicit stdin", devNull(), []string{"svg", "-i", "-"}, 1, "not a JSON object"},
	} {
		code, _, stderr := cliIO(t, tc.stdin, tc.args...)
		if code != tc.code || !strings.Contains(stderr, tc.stderr) {
			t.Errorf("%s: aster %s: exit %d, want %d, and stderr %q, want it to contain %q", tc.name, strings.Join(tc.args, " "), code, tc.code, stderr, tc.stderr)
		}
		if !strings.HasPrefix(stderr, "aster: ") || strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 {
			t.Errorf("%s: stderr is not one line prefixed \"aster: \":\n%s", tc.name, stderr)
		}
		// The -from hint is for an input the detection cannot tell, not for
		// one that is not JSON (C59).
		if strings.Contains(stderr, "pass -from") {
			t.Errorf("%s: the -from hint on an error it cannot help: %s", tc.name, stderr)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an invalid command line wrote the output")
	}
}

// `aster version` (and -version, --version before any command) prints the
// command's module version and the versions the engine follows (C143).
func TestVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-version"}, {"--version"}} {
		code, stdout, stderr := cliIO(t, strings.NewReader(""), args...)
		if code != 0 || !strings.HasPrefix(stdout, "aster ") || !strings.Contains(stdout, "Vega 6.4.0") || !strings.Contains(stdout, "Vega-Lite 6.4.3") || !strings.Contains(stdout, "5.8.0") {
			t.Errorf("aster %s: exit %d, stdout %q, stderr %q", args[0], code, stdout, stderr)
		}
	}
	if code, _, _ := cliIO(t, strings.NewReader(""), "version", "extra"); code != 2 {
		t.Errorf("aster version extra: exit %d, want 2", code)
	}
}
