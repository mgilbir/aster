//go:build unix

package loader

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO inside the base directory must be rejected without blocking on open.
func TestFileLoaderFIFODoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skip(err)
	}
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() { _, err := l.Load(context.Background(), "pipe"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("FIFO loaded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO")
	}
}
