package jsval

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/testutil"
)

// dag is b_levels of b0 = [1], b_i = [b_{i-1} × fan]: levels+1 arrays in
// memory that stand for fan^levels elements, the shape a few expressions
// build for free (each array holds references to the previous one).
func dag(levels, fan int) Value {
	v := ArrOf(Num(1))
	for range levels {
		items := make([]Value, fan)
		for i := range items {
			items[i] = v
		}
		v = Arr(items)
	}
	return v
}

// countingCtx is a context that reports itself done from the n-th call of
// Err on: a deadline counted in polls rather than in time.
type countingCtx struct {
	context.Context
	calls, n atomic.Int64
}

func (c *countingCtx) Err() error {
	if c.calls.Add(1) >= c.n.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func callCtx(polls int64, maxBytes int64) (context.Context, *budget.Ledger, *countingCtx) {
	c := &countingCtx{Context: context.Background()}
	c.n.Store(polls)
	if polls <= 0 {
		c.n.Store(1 << 62)
	}
	l := budget.NewLedger(c, budget.LedgerLimits{MaxBytes: maxBytes})
	return budget.WithLedger(c, l), l, c
}

// C1 and C13 (audit 2026-10-18): no walk of a shared-reference value can
// pass the call's budgets or run past its deadline. Each walk of a value
// whose unfolded size is 10^8 (dag(4, 100)), or 10^10, must stop:
//
//   - under a call whose memory is limited, with ErrLimit, having allocated
//     within a small multiple of the limit;
//   - under a call whose context is done, at the poll after it is, so
//     within a bounded number of values visited (a deadline counted in
//     polls, not in time);
//   - without a call, at the detached bound.
func TestValueWalksOfSharedReferencesAreBounded(t *testing.T) {
	for _, levels := range []int{4, 5} {
		a, b := dag(levels, 100), dag(levels, 100)
		for name, walk := range walkers {
			t.Run(name, func(t *testing.T) {
				// Memory: a 1 MiB ledger, no deadline. Equal builds
				// nothing: only the deadline (below) ends it.
				if name != "Equal" {
					ctx, l, _ := callCtx(0, 1<<20)
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					err := stopped(func() { walk(WalkerFor(ctx), a, b) })
					runtime.ReadMemStats(&after)
					if !errors.Is(err, budget.ErrLimit) {
						t.Errorf("levels %d under a 1 MiB ledger: %v, want ErrLimit", levels, err)
					}
					if got := after.TotalAlloc - before.TotalAlloc; !testutil.RaceEnabled && got > 8<<20 {
						t.Errorf("levels %d: allocated %d KiB under a 1 MiB ledger", levels, got>>10)
					}
					if l.Bytes() > 1<<20 {
						t.Errorf("levels %d: %d bytes charged, past the limit", levels, l.Bytes())
					}
				}

				// Time: an unlimited ledger whose context is done at its
				// 10th poll.
				ctx, l, c := callCtx(10, 0)
				err := stopped(func() { walk(WalkerFor(ctx), a, b) })
				if name != "Equal" && errors.Is(err, budget.ErrLimit) {
					// Text past MaxStringLength first: also a stop.
				} else if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("levels %d with the context done: %v, want the deadline", levels, err)
				}
				// The ledger polls every 1024 units of work; the walk
				// charges one a value, so it stops within a poll of the
				// context saying it is done.
				if work := l.WorkDone(); work > (c.n.Load()+2)*1024 {
					t.Errorf("levels %d: %d values visited, the context was done after %d polls", levels, work, c.n.Load())
				}

				// Without a call: the detached bound.
				err = stopped(func() { walk(Walker{}, a, b) })
				if !errors.Is(err, budget.ErrLimit) {
					t.Errorf("levels %d detached: %v, want ErrLimit", levels, err)
				}
			})
		}
	}
}

// C13: an array or object equals itself without a walk, however large.
func TestEqualOfTheSameValueIsNotAWalk(t *testing.T) {
	v := dag(5, 100) // 10^10 elements unfolded
	ctx, l, _ := callCtx(0, 0)
	if !WalkerFor(ctx).Equal(v, v) || !Equal(v, v) {
		t.Fatal("a value must equal itself")
	}
	inner := ArrOf(v, v)
	if !WalkerFor(ctx).Equal(inner, ArrOf(v, v)) {
		t.Fatal("arrays of the same elements must be equal")
	}
	if w := l.WorkDone(); w > 4 {
		t.Errorf("Equal visited %d values; the shared elements are the same arrays", w)
	}
	o := Obj(ObjectOf("a", v))
	if !Equal(o, o) {
		t.Fatal("an object must equal itself")
	}
}

// The text a walk builds is charged to the call's ledger before it is
// allocated, past smallWalk bytes: the caller keeps it. A comparison gives
// back what it built once it has compared.
func TestWalksChargeTheirText(t *testing.T) {
	items := make([]Value, 10000)
	for i := range items {
		items[i] = Num(12345)
	}
	v := Arr(items)
	ctx, l, _ := callCtx(0, 0)
	w := WalkerFor(ctx)
	s := w.String(v)
	if len(s) != 10000*6-1 {
		t.Fatalf("String is %d bytes", len(s))
	}
	if got := l.Bytes(); got < int64(len(s))-smallWalk {
		t.Errorf("String of %d bytes charged %d", len(s), got)
	}
	before := l.Bytes()
	if c, ok := w.Relate(v, Arr(append([]Value(nil), items...))); !ok || c != 0 {
		t.Errorf("Relate = %d, %v", c, ok)
	}
	if l.Bytes() != before {
		t.Errorf("Relate kept %d bytes charged", l.Bytes()-before)
	}
	if got := w.StringValue(ArrOf(Str("a"), Null, Undefined, ArrOf(Num(1)))); got != `["a",null,,[1]]` {
		t.Errorf("StringValue = %s", got)
	}
	// A small text charges nothing.
	ctx, l, _ = callCtx(0, 0)
	_ = WalkerFor(ctx).String(ArrOf(Num(1), Num(2)))
	if l.Bytes() != 0 {
		t.Errorf("String of [1,2] charged %d bytes", l.Bytes())
	}
}

// MaxStringLength bounds the String of a value as it bounds the strings
// expressions build.
func TestStringOfAValueIsBoundedByMaxStringLength(t *testing.T) {
	half := Str(strings.Repeat("a", MaxStringLength/2+1))
	err := stopped(func() { _ = WalkerFor(t.Context()).String(ArrOf(half, half)) })
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("String of %d code units: %v, want ErrLimit", MaxStringLength+3, err)
	}
	// Under the bound it is built (a code unit can be three bytes).
	wide := Str(strings.Repeat("€", MaxStringLength/4))
	if got := WalkerFor(t.Context()).String(ArrOf(wide, wide)); len(got) != 2*len(wide.StrValue())+1 {
		t.Errorf("String of %d code units: %d bytes", MaxStringLength/2+1, len(got))
	}
}

// A message about a value shows its start: String() and the prefix walks
// stop at their limit, whatever the value's size.
func TestPreviewsAreBounded(t *testing.T) {
	v := dag(5, 100)
	if got := v.String(); len(got) > previewBytes+len("…") || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "[[[[[[1],[1]") {
		t.Errorf("String() = %.40q… (%d bytes)", got, len(got))
	}
	var detached Walker
	for _, f := range []func([]byte, Value, int) []byte{detached.AppendStringPrefix, detached.AppendStringValuePrefix} {
		if got := f([]byte("x "), v, 100); len(got) > 2+101 || !strings.HasPrefix(string(got), "x ") {
			t.Errorf("prefix = %q (%d bytes)", got, len(got))
		}
	}
	long := Str(strings.Repeat("\n", 1<<20))
	if got := detached.AppendStringValuePrefix(nil, ArrOf(long), 100); len(got) > prefixBytes(100) {
		t.Errorf("stringValue prefix of escapes: %d bytes", len(got))
	}
	if got := long.String(); len(got) > previewBytes+len("…") {
		t.Errorf("String() of a long string: %d bytes", len(got))
	}
}

// The keyword views read at most what they compare, whatever the value.
func TestKeywordViewsAreBounded(t *testing.T) {
	v := dag(5, 100)
	if v.StringIs("1,1") || !ArrOf(Str("radial")).StringIs("radial") || ArrOf(Str("radial"), Null).StringIs("radial") {
		t.Error("StringIs")
	}
	if !ArrOf(ArrOf(Null)).StringIs("") || !Undefined.StringIs("undefined") || !Num(1e21).StringIs("1e+21") {
		t.Error("StringIs of primitives")
	}
	if k, ok := v.PropertyKey(16); ok || k != "" {
		t.Errorf("PropertyKey of a long text = %q, %v", k, ok)
	}
	if k, ok := ArrOf(Str("a"), Num(2)).PropertyKey(16); !ok || k != "a,2" {
		t.Errorf("PropertyKey = %q, %v", k, ok)
	}
	if k, ok := Undefined.PropertyKey(16); !ok || k != "undefined" {
		t.Errorf("PropertyKey(undefined) = %q", k)
	}
	if _, ok := ArrOf(Str("linear")).Strict(); ok {
		t.Error("Strict of an array")
	}
	if s, ok := Str("linear").Strict(); !ok || s != "linear" {
		t.Error("Strict of a string")
	}
	if got := StringPrefix(v, 10); len(got) > 10 || !strings.HasPrefix(got, "1,1,1") {
		t.Errorf("StringPrefix = %q", got)
	}
	if got := StringPrefix(Str("éééé"), 3); got != "é" {
		t.Errorf("StringPrefix cut inside a character: %q", got)
	}
}
