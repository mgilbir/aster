package loader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
