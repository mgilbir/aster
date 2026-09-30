package loader

import (
	"bytes"
	"context"
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
