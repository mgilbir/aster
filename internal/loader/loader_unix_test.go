//go:build unix

package loader

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/testutil"
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
	waitStart := time.Now()
	select {
	case err := <-done:
		testutil.Timing(t, "Load of a FIFO returns (hang guard)", time.Since(waitStart), testutil.Scaled(5*time.Second))
		if err == nil {
			t.Error("FIFO loaded")
		}
	case <-time.After(testutil.Scaled(5 * time.Second)):
		t.Fatal("Load blocked on a FIFO")
	}
}
