package oracle

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// v8Report is what V8 writes to standard error when the heap is exhausted.
const v8Report = `<--- Last few GCs --->

[15533:0x9be80c000]    13551 ms: Scavenge (interleaved) 1987.2 (2004.4) -> 1985.4 (2046.1) MB
[15533:0x9be80c000]    14677 ms: Mark-Compact (reduce) 1985.5 (2046.1) -> 1985.5 (1987.6) MB

FATAL ERROR: Ineffective mark-compacts near heap limit Allocation failed - JavaScript heap out of memory
----- Native stack trace -----

 1: 0x10086ff44 node::(anonymous namespace)::DefaultAbortHandler(char const*, char const*) [node]
 2: 0x1008701a8 node::OOMErrorHandler(char const*, v8::OOMDetails const&) [node]
`

// V8's fatal-error report is not passed on, and its reason is kept; what the
// process writes before it, and a line with no newline, are passed on whole.
func TestStderrWatchKeepsTheReasonAndHidesTheReport(t *testing.T) {
	var out bytes.Buffer
	w := &stderrWatch{out: &out}
	line := "oracle: upstream changed Object.x; this process is replaced\n"
	for _, chunk := range []string{line[:20], line[20:]} { // one line, written in two parts
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Write([]byte(v8Report)); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), line; got != want {
		t.Errorf("passed on %q, want %q", got, want)
	}
	if got, want := w.fatal(), "JavaScript heap out of memory"; got != want {
		t.Errorf("reason %q, want %q", got, want)
	}
}

// fakeOracle is an Oracle whose node runs a script of its own:
//
//	"ok"        answers an SVG
//	"oom"       dies of an exhausted heap, whatever it has answered
//	"oom-old"   dies of an exhausted heap if it has answered a request before
const fakeScript = `
import readline from 'node:readline';
const report = ` + "`" + v8Report + "`" + `;
process.stdout.write(JSON.stringify({ready: true, version: 'fake'}) + '\n');
let answered = 0;
readline.createInterface({input: process.stdin}).on('line', (line) => {
  const req = JSON.parse(line);
  if (req.spec === 'oom' || (req.spec === 'oom-old' && answered > 0)) {
    process.stderr.write(report, () => process.exit(134));
    return;
  }
  answered++;
  process.stdout.write(JSON.stringify({id: req.id, svg: '<svg/>'}) + '\n');
});
`

func fakeOracle(t *testing.T) *Oracle {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "testdata/oracle-node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, script), []byte(fakeScript), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Oracle{root: root, set: "testdata/oracle-node", zone: "UTC", fingerprint: "test", free: make(chan *proc, 1)}
	o.free <- nil
	t.Cleanup(func() {
		if p := <-o.free; p != nil {
			p.stop()
		}
	})
	return o
}

func svgReq(spec string) map[string]any { return map[string]any{"op": "svg", "spec": spec} }

// A request that ends node is ErrDied with the reason node gave, once a fresh
// process has been asked and died too, and is never cached: whether node can
// answer depends on the host's heap limit.
func TestDeathOfNodeIsAnErrorWithItsReason(t *testing.T) {
	o := fakeOracle(t)
	_, err := o.ask(svgReq("oom"))
	if !errors.Is(err, ErrDied) {
		t.Fatalf("a request that ends node gave %v, want ErrDied", err)
	}
	if got, want := DiedReason(err), "JavaScript heap out of memory"; got != want {
		t.Errorf("reason %q, want %q", got, want)
	}
	if c := cachedFiles(o.root); len(c) != 0 {
		t.Errorf("a death was cached: %v", c)
	}
	// The pool recovers: the next request starts a process.
	if res, err := o.ask(svgReq("ok")); err != nil || res.SVG != "<svg/>" {
		t.Fatalf("after a death: %+v, %v", res, err)
	}
}

// A process that dies after answering other requests proves nothing about
// this one: a fresh process is asked, and its answer stands.
func TestDeathOfAUsedProcessIsAskedAgainFresh(t *testing.T) {
	o := fakeOracle(t)
	if res, err := o.ask(svgReq("ok")); err != nil || res.SVG != "<svg/>" {
		t.Fatalf("first request: %+v, %v", res, err)
	}
	res, err := o.ask(svgReq("oom-old"))
	if err != nil || res.SVG != "<svg/>" {
		t.Fatalf("a used process's death was %+v, %v; want the fresh process's answer", res, err)
	}
}
