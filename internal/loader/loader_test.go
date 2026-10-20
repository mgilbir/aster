package loader

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoaderMaxBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.csv"), bytes.Repeat([]byte("a"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// Default cap is far above 100 bytes.
	if data, err := l.Load(ctx, "big.csv"); err != nil || len(data) != 100 {
		t.Fatalf("default cap: len=%d err=%v", len(data), err)
	}

	l.MaxBytes = 99
	if _, err := l.Load(ctx, "big.csv"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("MaxBytes=99: want size error, got %v", err)
	}
	l.MaxBytes = 100 // exactly at the cap is allowed
	if data, err := l.Load(ctx, "big.csv"); err != nil || len(data) != 100 {
		t.Fatalf("MaxBytes=100: len=%d err=%v", len(data), err)
	}
	l.MaxBytes = -1
	if _, err := l.Load(ctx, "big.csv"); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
}

func TestFileLoaderRejectsNonRegularAndCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Load(context.Background(), "sub"); err == nil {
		t.Error("directory should not load")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Load(ctx, "sub"); err != context.Canceled {
		t.Errorf("cancelled context: got %v", err)
	}
}

// A FileLoader's error says that a relative URL resolves against its
// BaseDir, not against the spec's directory, without naming the directory
// (TestFileLoaderErrorsDoNotLeakPaths) or os.Root's internal operation name;
// the cause still unwraps (C70, audit 2026-10-18).
func TestFileLoaderErrorSaysWhereItLooked(t *testing.T) {
	dir := t.TempDir()
	for name, l := range map[string]*FileLoader{"lazy": {BaseDir: dir}, "eager": mustFileLoader(t, dir)} {
		_, err := l.Load(context.Background(), "data/cars.json")
		if err == nil {
			t.Fatalf("%s: loaded a missing file", name)
		}
		msg := err.Error()
		if !strings.Contains(msg, "relative to its BaseDir") || strings.Contains(msg, dir) || strings.Contains(msg, "statat") || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %q: want BaseDir named as what the URL is relative to, not its path, no statat, and fs.ErrNotExist", name, msg)
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Errorf("%s: %v no longer unwraps to the *fs.PathError", name, err)
		}
	}
}

func mustFileLoader(t *testing.T, dir string) *FileLoader {
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// failLoader fails every URI with its error, at Sanitize or at Load.
type failLoader struct {
	sanitize, load error
}

func (f failLoader) Sanitize(_ context.Context, uri string) (string, error) {
	return uri, f.sanitize
}

func (f failLoader) Load(context.Context, string) ([]byte, error) { return nil, f.load }

// A FallbackLoader whose children all fail reports every child's failure,
// in order, each still matched by errors.Is; it used to report only the
// last, so a file missing under -allow-file read as an HTTP refusal (C70).
func TestFallbackLoaderReportsEveryChild(t *testing.T) {
	errA, errB, errC := errors.New("first refused"), errors.New("second missing"), errors.New("third refused")
	f := NewFallbackLoader(failLoader{sanitize: errA}, failLoader{load: errB}, failLoader{sanitize: errC})
	for _, call := range []struct {
		name string
		err  error
		want []error
	}{
		{"Load", func() error { _, err := f.Load(context.Background(), "x"); return err }(), []error{errA, errB, errC}},
		{"Sanitize", func() error {
			_, err := NewFallbackLoader(failLoader{sanitize: errA}, failLoader{sanitize: errC}).Sanitize(context.Background(), "x")
			return err
		}(), []error{errA, errC}},
	} {
		if call.err == nil {
			t.Fatalf("%s: no error", call.name)
		}
		last := -1
		for _, w := range call.want {
			i := strings.Index(call.err.Error(), w.Error())
			if !errors.Is(call.err, w) || i <= last {
				t.Errorf("%s: %q: want %q, in order, matched by errors.Is", call.name, call.err, w)
			}
			last = i
		}
	}
	// One child's failure is that failure, unchanged.
	one := NewFallbackLoader(failLoader{load: errB}, failLoader{sanitize: errA})
	if _, err := one.Load(context.Background(), "x"); !errors.Is(err, errB) || !errors.Is(err, errA) {
		t.Errorf("Load: %v", err)
	}
	if _, err := NewFallbackLoader(failLoader{load: errB}).Load(context.Background(), "x"); err != errB {
		t.Errorf("a single child: %v, want its error unchanged", err)
	}
}

// Closing a FileLoader that was never used does not open its directory to
// close it (C141), and a Load after the Close fails as closed, without
// opening it either.
func TestFileLoaderCloseUnusedOpensNothing(t *testing.T) {
	// A directory that does not exist: opening it would leave its error.
	l := &FileLoader{BaseDir: filepath.Join(t.TempDir(), "missing")}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if l.root != nil || l.err != nil {
		t.Errorf("Close opened the directory (root %v, err %v)", l.root, l.err)
	}
	if _, err := l.Load(context.Background(), "a.json"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Load after Close: %v", err)
	}
	if l.root != nil {
		t.Error("Load after Close opened the directory")
	}
}
