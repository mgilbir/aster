package loader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// zeros is an endless body that counts what is read from it.
type zeros struct{ n int64 }

func (z *zeros) Read(p []byte) (int, error) {
	clear(p)
	z.n += int64(len(p))
	return len(p), nil
}

// A loader reads no more than the render may keep: what is left of the load
// budget lowers the cap, so a small memory limit is not exceeded by the
// loader's own buffer before the budget is charged.
func TestLoadersStopAtTheLoadBudget(t *testing.T) {
	ctx := budget.With(context.Background(), &budget.Budget{MaxLoadBytes: 1000})

	body := &zeros{}
	h := &HTTPLoader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := respond(r, 200, "")
		resp.ContentLength = -1
		resp.Body = io.NopCloser(body)
		return resp, nil
	})}}
	if _, err := h.Load(ctx, "http://example.com/x"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("HTTP: err = %v, want a limit", err)
	}
	if body.n > 4096 {
		t.Errorf("HTTP: read %d bytes against a budget of 1000", body.n)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.csv"), bytes.Repeat([]byte("a"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Load(ctx, "big.csv"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("file: err = %v, want a limit", err)
	}
	f.MaxBytes = -1 // the budget lowers even "no cap"
	if _, err := f.Load(ctx, "big.csv"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("file, no cap: err = %v, want a limit", err)
	}
	// An exhausted budget still lets an empty body through, and a body that
	// fits is loaded.
	small := budget.With(context.Background(), &budget.Budget{MaxLoadBytes: 5000})
	if data, err := f.Load(small, "big.csv"); err != nil || len(data) != 5000 {
		t.Errorf("file within budget: %d bytes, %v", len(data), err)
	}
}

// A child's limit is the answer, not a miss: the next child must not be tried,
// and its error must not take the place of the limit.
func TestFallbackLoaderLimitIsFinal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.csv"), bytes.Repeat([]byte("a"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.MaxBytes = 10
	var asked bool
	next := &HTTPLoader{BaseURL: "http://example.com/", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		asked = true
		return respond(r, 200, "[]"), nil
	})}}
	fb := NewFallbackLoader(f, next, DenyLoader{})
	defer fb.Close()
	if _, err := fb.Load(context.Background(), "big.csv"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("err = %v, want a limit", err)
	}
	if asked {
		t.Error("the next child was asked after the limit")
	}
}

// Loads that share an allowance (the images of one call, fetched in
// parallel) charge it as they read, a chunk at a time, so together they read
// no more than it and one chunk each, however they interleave (C19), the
// file and the HTTP loader alike: what they charge stays within it, and each
// reads at most one chunk past what it charged (the chunk it was refused).
func TestLoadsSharingAnAllowanceStopTogether(t *testing.T) {
	const loads, size, allowance = 8, 3 << 20, 8 << 20
	dir := t.TempDir()
	body := bytes.Repeat([]byte("x"), size)
	if err := os.WriteFile(filepath.Join(dir, "f"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length: the loader cannot refuse before it reads.
		for off := 0; off < size; off += 64 << 10 {
			n, err := w.Write(body[off : off+64<<10])
			served.Add(int64(n))
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(ts.Close)
	for name, l := range map[string]Loader{
		"file": &FileLoader{BaseDir: dir},
		"http": &HTTPLoader{AllowPrivateNetworks: true},
	} {
		uri := "f"
		if name == "http" {
			uri = ts.URL + "/f"
		}
		shared := budget.NewAllowance(allowance)
		var wg sync.WaitGroup
		var ok, limited atomic.Int32
		var streamed atomic.Int64
		for range loads {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b := &budget.Budget{MaxLoadBytes: 32 << 20, Shared: shared}
				data, err := l.Load(budget.With(context.Background(), b), uri)
				streamed.Add(b.Streamed())
				switch {
				case err == nil && len(data) == size:
					ok.Add(1)
				case errors.Is(err, budget.ErrLimit):
					limited.Add(1)
				default:
					t.Errorf("%s: %d bytes, %v", name, len(data), err)
				}
			}()
		}
		wg.Wait()
		if got := streamed.Load(); got > allowance {
			t.Errorf("%s: the loads charged %d bytes, past the %d allowance", name, got, allowance)
		}
		if ok.Load() > allowance/size || limited.Load() == 0 {
			t.Errorf("%s: %d loads succeeded and %d hit the limit, of %d of %d bytes in %d", name, ok.Load(), limited.Load(), loads, size, allowance)
		}
	}
}
