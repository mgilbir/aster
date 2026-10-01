// Package fuzzutil holds what the coverage-guided fuzz targets (go test -fuzz)
// share: seeds read from the checked-in test data, and a per-input time bound.
// The nightly workflow (.github/workflows/fuzz.yml) runs the targets listed in
// scripts/fuzz-targets.txt.
package fuzzutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// MaxSeedBytes is the largest file Files returns: a big seed makes every
// mutation of it slow, and the fuzzer finds the depth by itself.
const MaxSeedBytes = 32 << 10

// Files returns the contents of the files matching the glob patterns
// (relative to the calling package's directory), skipping files over
// MaxSeedBytes, for the caller to f.Add. A pattern that matches nothing fails
// the target, so a moved corpus is noticed.
func Files(tb testing.TB, patterns ...string) []string {
	tb.Helper()
	var out []string
	for _, pat := range patterns {
		files, err := filepath.Glob(pat)
		if err != nil || len(files) == 0 {
			tb.Fatalf("no seed files match %q (%v)", pat, err)
		}
		for _, file := range files {
			if fi, err := os.Stat(file); err != nil || fi.Size() > MaxSeedBytes {
				continue
			}
			b, err := os.ReadFile(file)
			if err != nil {
				tb.Fatal(err)
			}
			out = append(out, string(b))
		}
	}
	return out
}

// Within runs fn and fails the test if it takes longer than d: a fuzzer
// reports a hang as nothing at all, so an input that makes fn run away has to
// become a failure here. what names the input in the message.
func Within(t testing.TB, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: did not finish in %v", what, d)
	}
}
