package oracle

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type discard struct{ io.Writer }

func (discard) Close() error { return nil }

// A request node does not answer in time is an oracle failure: it is reported
// as ErrNoAnswer, not as upstream's error (which a sweep counts as agreement
// when the engine fails too), and it is never cached, so one slow run cannot
// mask a later engine failure (C67). A timeout an older version cached as an
// answer is asked again.
func TestTimeoutIsNotAnAnswer(t *testing.T) {
	defer func(d time.Duration) { answerTimeout = d }(answerTimeout)
	answerTimeout = 10 * time.Millisecond
	o := &Oracle{root: t.TempDir(), fingerprint: "test", free: make(chan *proc, 1)}
	// A node that never answers: a process that reads nothing and writes no
	// line. The pool's one slot holds it; a timeout stops it.
	silent := func() {
		t.Helper()
		<-o.free
		cmd := exec.Command("sleep", "600")
		ownGroup(cmd)
		if err := cmd.Start(); err != nil {
			t.Skipf("no sleep command: %v", err)
		}
		o.free <- &proc{o: o, cmd: cmd, stdin: discard{io.Discard}, lines: make(chan []byte)}
	}
	o.free <- nil
	silent()
	req := func() map[string]any { return map[string]any{"op": "svg", "spec": "{}"} }

	res, err := o.ask(req())
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a timeout gave %+v, %v; want ErrNoAnswer", res, err)
	}
	var cached []string
	_ = filepath.WalkDir(filepath.Join(o.root, cache), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			cached = append(cached, path)
		}
		return nil
	})
	if len(cached) != 0 {
		t.Fatalf("a timeout was cached: %v", cached)
	}

	// The legacy entry: the timeout recorded as upstream's error.
	path, err := o.cachePath(req())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"err":"oracle: no answer within 2m0s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	silent()
	if res, err := o.ask(req()); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a cached timeout was answered as %+v, %v; want it asked again", res, err)
	}
}
