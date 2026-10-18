package aster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

// rendezvousLoader passes every Load to inner once n Loads are in flight
// together, or after a while without the others: it lets fetches that can run
// together do so, whatever the scheduler does, and tells the bytes inner
// returned.
type rendezvousLoader struct {
	inner    aster.Loader
	n        int32
	inFlight atomic.Int32
	returned atomic.Int64
}

func (l *rendezvousLoader) Sanitize(ctx context.Context, u string) (string, error) {
	return l.inner.Sanitize(ctx, u)
}

func (l *rendezvousLoader) Load(ctx context.Context, u string) ([]byte, error) {
	l.inFlight.Add(1)
	defer l.inFlight.Add(-1)
	// Not a time bound: a wait that lets the other fetches start (up to
	// 200 ms when they cannot, as when they wait for an allowance).
	for i := 0; i < 200 && l.inFlight.Load() < l.n; i++ {
		time.Sleep(time.Millisecond)
	}
	b, err := l.inner.Load(ctx, u)
	l.returned.Add(int64(len(b)))
	return b, err
}

// Images fetched in parallel for PNG and PDF output read, together, no more
// than the call's loaded-bytes allowance (C19): each fetch took a snapshot of
// what was left at its start, and eight of them read eight times it. The
// Loader is a FileLoader, which reads only what the call gives it.
func TestParallelFetchesStayWithinTheAllowance(t *testing.T) {
	const images, size = 8, 7 << 20
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">`)
	for i := range images {
		name := fmt.Sprintf("img%d.png", i)
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, `<image href="%s" width="10" height="10"/>`, name)
	}
	sb.WriteString(`</svg>`)
	for _, out := range []string{"PNG", "PDF"} {
		fl, err := aster.NewFileLoader(dir)
		if err != nil {
			t.Fatal(err)
		}
		l := &rendezvousLoader{inner: fl, n: images}
		// WithMemoryLimit(16 MiB): the call's loaded-bytes allowance is 8 MiB.
		const allowance = 8 << 20
		c, err := aster.New(aster.WithLoader(l), aster.WithMemoryLimit(2*allowance))
		if err != nil {
			t.Fatal(err)
		}
		if out == "PNG" {
			_, err = c.SVGToPNG(sb.String())
		} else {
			_, err = c.SVGToPDF(sb.String())
		}
		_ = c.Close()
		if !errors.Is(err, aster.ErrLimit) {
			t.Errorf("%s: %v, want ErrLimit: the images are over the allowance together", out, err)
		}
		if got := l.returned.Load(); got > allowance {
			t.Errorf("%s: the Loader returned %d MiB, past the %d MiB allowance", out, got>>20, allowance>>20)
		}
	}
}

// Reserving does not refuse what fits: images that fit the allowance together
// are all fetched, whatever order the fetches run in.
func TestParallelFetchesThatFitAllLoad(t *testing.T) {
	dir := t.TempDir()
	png := redPNG(t)
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">`)
	for i := range 20 {
		name := fmt.Sprintf("img%d.png", i)
		// Each 1 MiB, 20 MiB in all: within the default 64 MiB allowance,
		// far less than 20 per-image reservations of 32 MiB.
		if err := os.WriteFile(filepath.Join(dir, name), append(append([]byte{}, png...), make([]byte, 1<<20)...), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, `<image href="%s" width="10" height="10"/>`, name)
	}
	sb.WriteString(`</svg>`)
	fl, err := aster.NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := &rendezvousLoader{inner: fl, n: 1}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.SVGToPNG(sb.String()); err != nil {
		t.Errorf("PNG: %v", err)
	}
	if _, err := c.SVGToPDF(sb.String()); err != nil {
		t.Errorf("PDF: %v", err)
	}
}

// barrierLoader serves a small PNG for every URI, each Load waiting until n
// Loads are in flight together: it returns only if the call runs n fetches
// at once. The call's context ends the wait, so a call that cannot reach n
// fails at its timeout instead of hanging.
type barrierLoader struct {
	n        int32
	png      []byte
	inFlight atomic.Int32
	peak     atomic.Int32
	once     sync.Once
	all      chan struct{}
}

func (l *barrierLoader) Sanitize(_ context.Context, u string) (string, error) { return u, nil }

func (l *barrierLoader) Load(ctx context.Context, _ string) ([]byte, error) {
	n := l.inFlight.Add(1)
	defer l.inFlight.Add(-1)
	for p := l.peak.Load(); n > p && !l.peak.CompareAndSwap(p, n); p = l.peak.Load() {
	}
	if n >= l.n {
		l.once.Do(func() { close(l.all) })
	}
	select {
	case <-l.all:
		return l.png, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Under the default limits, the eight fetches imageref.Fetch runs at once
// all run at once (C19: reserving each image's 32 MiB bound up front let two
// of them run at a time under the 64 MiB allowance). Counted, not timed:
// the Loader returns only once eight Loads are in flight together.
func TestImageFetchesRunInParallel(t *testing.T) {
	const fetches = 8
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">`)
	for i := range fetches {
		fmt.Fprintf(&sb, `<image href="http://example.com/%d.png" width="10" height="10"/>`, i)
	}
	sb.WriteString(`</svg>`)
	for _, out := range []string{"PNG", "PDF"} {
		l := &barrierLoader{n: fetches, png: redPNG(t), all: make(chan struct{})}
		// The timeout is a hang backstop only: with eight fetches at once
		// the call returns at once.
		c, err := aster.New(aster.WithLoader(l), aster.WithTimeout(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if out == "PNG" {
			_, err = c.SVGToPNG(sb.String())
		} else {
			_, err = c.SVGToPDF(sb.String())
		}
		_ = c.Close()
		if err != nil {
			t.Errorf("%s: %v", out, err)
		}
		if p := l.peak.Load(); p != fetches {
			t.Errorf("%s: at most %d fetches ran at once, want %d", out, p, fetches)
		}
	}
}
