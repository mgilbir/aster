package expr

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// allocated is the bytes f allocates: deterministic, unlike the heap a
// collector has not yet swept.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// bigPattern compiles to 150,452 instructions: about 40 MB held, over 100 MB
// allocated while compiling, against a few kilobytes to parse it.
const bigPattern = "(a{1000}){150}"

// compiled is what a compile of bigPattern allocates at least; refusedMax
// what a refusal may allocate at most.
const (
	compiled   = 16 << 20
	refusedMax = 1 << 20
)

// ledgerForOne is a ledger with room for one program of bigPattern, not two.
func ledgerForOne(t *testing.T, ctx context.Context) *budget.Ledger {
	t.Helper()
	cost, err := jsval.EstimatePattern(bigPattern+"x", "")
	if err != nil {
		t.Fatal(err)
	}
	return budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: cost.Bytes + cost.Bytes/2})
}

// Every place that compiles a pattern pays for it first: a program past what
// the call's ledger has left is refused with ErrLimit having cost a parse,
// not a compile (C8, #115: regexp() over data, and literals, compiled a
// second program of tens of megabytes before refusing it). The test counts
// the bytes allocated, so it holds on any machine; the race detector
// allocates on its own, so it is skipped there.
func TestPatternRefusedBeforeCompile(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	t.Run("regexp", func(t *testing.T) {
		led := ledgerForOne(t, context.Background())
		eval := func(src string) error {
			p, err := Compile("test('" + src + "', 'a')")
			if err != nil {
				t.Fatal(err)
			}
			s := NewScope(nil)
			s.Context = budget.WithLedger(context.Background(), led)
			_, err = p.Eval(s)
			return err
		}
		var err1, err2 error
		first := allocated(func() { err1 = eval(bigPattern + "x") })
		second := allocated(func() { err2 = eval(bigPattern + "y") })
		if err1 != nil || !errors.Is(err2, budget.ErrLimit) {
			t.Fatalf("first: %v; second: %v, want ErrLimit", err1, err2)
		}
		if first < compiled || second > refusedMax {
			t.Errorf("a compile allocates %d bytes, a refused one %d", first, second)
		}
		// The refusal depends on the budget, not the pattern: with room, it
		// compiles.
		if _, ok := patternErrors.Get("/" + bigPattern + "y"); ok {
			t.Error("a budget refusal was remembered as the pattern's error")
		}
	})
	t.Run("literal", func(t *testing.T) {
		led := ledgerForOne(t, context.Background())
		ctx := budget.WithLedger(context.Background(), led)
		var err1, err2 error
		first := allocated(func() { _, err1 = CompileContext(ctx, "test(/"+bigPattern+"x/, 'a')") })
		second := allocated(func() { _, err2 = CompileContext(ctx, "test(/"+bigPattern+"y/, 'a')") })
		if err1 != nil || !errors.Is(err2, budget.ErrLimit) {
			t.Fatalf("first: %v; second: %v, want ErrLimit", err1, err2)
		}
		if first < compiled || second > refusedMax {
			t.Errorf("a compile allocates %d bytes, a refused one %d", first, second)
		}
	})
	t.Run("program-cap", func(t *testing.T) {
		p, err := Compile("test('(a{1000}){210}', 'a')")
		if err != nil {
			t.Fatal(err)
		}
		var got error
		n := allocated(func() { _, got = p.Eval(NewScope(nil)) })
		if !errors.Is(got, budget.ErrLimit) || n > refusedMax {
			t.Errorf("past goecma262's program cap: %v, %d bytes allocated", got, n)
		}
	})
	t.Run("raw", func(t *testing.T) {
		// The second compile of a pattern, for a search from lastIndex.
		p, err := jsval.NewPattern(bigPattern+"z", "g", unpaid)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(nil)
		s.Context = budget.WithLedger(context.Background(), budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: p.Bytes() - 1}))
		var stopped any
		n := allocated(func() {
			defer func() { stopped = recover() }()
			compileRaw(s, p)
		})
		if lp, ok := stopped.(*limitPanic); !ok || !errors.Is(lp.err, budget.ErrLimit) || n > refusedMax {
			t.Errorf("past the ledger: %v, %d bytes allocated", stopped, n)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		// A call already over compiles nothing more.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctx = budget.WithLedger(ctx, budget.NewLedger(ctx, budget.LedgerLimits{}))
		var err1, err2 error
		n := allocated(func() {
			_, err1 = CompileContext(ctx, "test(/"+bigPattern+"w/, 'a')")
			p, err := Compile("test('" + bigPattern + "v', 'a')")
			if err != nil {
				t.Fatal(err)
			}
			s := NewScope(nil)
			s.Context = ctx
			_, err2 = p.Eval(s)
		})
		if !errors.Is(err1, context.Canceled) || !errors.Is(err2, context.Canceled) || n > 2*refusedMax {
			t.Errorf("after the deadline: literal %v, regexp %v, %d bytes allocated", err1, err2, n)
		}
	})
}
