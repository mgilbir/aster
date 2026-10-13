package scene

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// A parsed path is charged to the render's ledger before it is allocated: it
// holds sixteen times the bytes of the path string (C48).
func TestParsePathChargesLedger(t *testing.T) {
	path := "M0,0" + strings.Repeat("l1,1", 100_000)
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	if _, err := parsePath(l, path); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("100,000 commands (6 MB) under a 1 MiB ledger: %v", err)
	}
	l = budget.NewLedger(context.Background(), budget.LedgerLimits{})
	cmds, err := parsePath(l, path)
	if err != nil || len(cmds) != 100_001 {
		t.Fatal(len(cmds), err)
	}
	if got, want := l.Bytes(), int64(cap(cmds))*pathCmdBytes; got < want || got > 2*want {
		t.Fatalf("charged %d bytes for %d", got, want)
	}
	plain, _ := ParsePath(path)
	if len(plain) != len(cmds) || plain[7] != cmds[7] {
		t.Fatal("charging changed the parse")
	}
}

// The custom symbol cache outlives renders, so it is bounded by the bytes of
// the paths it keeps, not their number.
func TestCustomSymbolCacheIsBoundedInBytes(t *testing.T) {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := range 40 {
		// 400 KB of path data, about 6 MB parsed.
		if _, err := customSymbol(nil, fmt.Sprintf("M%d,0", i)+strings.Repeat("l1,1", 100_000)); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&b)
	if kept := int64(b.HeapAlloc) - int64(a.HeapAlloc); kept > 32<<20 {
		t.Fatalf("the symbol cache keeps %d MB", kept>>20)
	}
	small := "M0,0L1,1Z"
	c1, _ := customSymbol(nil, small)
	c2, _ := customSymbol(nil, small)
	if &c1[0] != &c2[0] {
		t.Fatal("a small path must be cached")
	}
}
