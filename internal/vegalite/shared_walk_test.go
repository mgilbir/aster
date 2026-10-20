package vegalite

import (
	"context"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// sharedDAG is a value of depth d whose every level holds the level below
// twice, as an array and in an object: 2^d paths to the leaf, 2d+1 values.
func sharedDAG(d int) Value {
	v := mkv("signal", "x")
	for i := 0; i < d; i++ {
		v = mkv("a", arr(v, v), "b", v)
	}
	return v
}

// walkWork is the work the compilation's walk f charges; -1 when it runs
// into its deadline (a hang guard, not a calibrated bound: the walks
// compared take microseconds, a walk of the tree never ends).
func walkWork(t *testing.T, f func(cc *compileCtx)) (n int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l := budget.NewLedger(ctx, budget.LedgerLimits{})
	ctx = budget.WithLedger(ctx, l)
	cc := &compileCtx{ctx: ctx, meter: budget.NewMeter(ctx)}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(cancelError); !ok {
				panic(r)
			}
			n = -1
		}
	}()
	f(cc)
	cc.flushWork()
	return l.WorkDone()
}

// TestSharedValueWalksAreLinear: the compiler's walks of a whole value
// (resolveLazyWalk over a compiled specification, deepEqual) visit a shared
// part once, so a value whose parts are shared at each of d levels costs
// O(d), not O(2^d) (a walk of the tree it stands for). Counted, not timed.
func TestSharedValueWalksAreLinear(t *testing.T) {
	for _, c := range []struct {
		name string
		walk func(cc *compileCtx, d int)
	}{
		{"resolveLazyWalk", func(cc *compileCtx, d int) { resolveLazyWalk(cc, sharedDAG(d)) }},
		{"deepEqual", func(cc *compileCtx, d int) {
			if !deepEqual(cc, sharedDAG(d), sharedDAG(d)) {
				panic("unequal")
			}
		}},
	} {
		// Deep enough that the work reaches the ledger (it is handed over
		// a thousand units at a time).
		n1 := walkWork(t, func(cc *compileCtx) { c.walk(cc, 500) })
		n2 := walkWork(t, func(cc *compileCtx) { c.walk(cc, 1000) })
		if n1 <= 0 || n2 <= 0 || n2 > 4*n1 {
			t.Errorf("%s: work %d at depth 500, %d at depth 1000 (-1: past a 10 s deadline): not linear in the values", c.name, n1, n2)
		}
	}
	// The answer is the walk's: a difference deep in a shared part is found.
	a, b := sharedDAG(500), sharedDAG(498)
	b = mkv("a", arr(b, b), "b", mkv("a", arr(b, b), "b", jsval.Int(1)))
	if n := walkWork(t, func(cc *compileCtx) {
		if deepEqual(cc, a, b) {
			t.Error("deepEqual of values that differ deep down is true")
		}
	}); n < 0 || n > 1<<16 {
		t.Errorf("deepEqual of a shared value walks its tree: work %d", n)
	}
}

// TestLazySignalsAreResolvedFromTheRecord: resolveLazy resolves the signals
// the compiler made, and the copies made of them, without walking the
// output: work in proportion to the signals, not to a shared output of
// 2^d paths. A signal whose expression fails is an error only when the
// output holds it.
func TestLazySignalsAreResolvedFromTheRecord(t *testing.T) {
	text := func(v Value) string { return string(jsval.AppendJSON(nil, v)) }
	var out Value
	n := walkWork(t, func(cc *compileCtx) {
		a := lazySignal(cc, func() string { return "a" })
		b := lazySignal(cc, func() string { return "b" })
		// A copy made after the signal, and a signal inside a shared output.
		c := jsval.Obj(b.ObjValue().Clone())
		_ = lazySignal(cc, func() string { return "dropped" })
		out = mkv("x", arr(a, c), "y", sharedDAG(1000), "z", b)
		resolveLazy(cc, out)
		cc.flushWork()
	})
	if n < 0 || n > 64 {
		t.Errorf("resolveLazy of a shared output with 4 signals: work %d, want a few steps", n)
	}
	if got, want := text(out.Get("x")), `[{"signal":"a"},{"signal":"b"}]`; got != want {
		t.Errorf("x = %s, want %s", got, want)
	}
	if got := text(out.Get("z")); got != `{"signal":"b"}` {
		t.Errorf("z = %s", got)
	}
	// A failing expression of a dropped signal is no error; of one in the
	// output it is.
	failing := func(cc *compileCtx) Value {
		return lazySignal(cc, func() string { throw("boom"); return "" })
	}
	cc := &compileCtx{}
	_ = failing(cc)
	resolveLazy(cc, mkv("a", 1))
	cc = &compileCtx{}
	in := failing(cc)
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("a failing signal in the output was not an error")
			}
		}()
		resolveLazy(cc, mkv("a", in))
	}()
}
