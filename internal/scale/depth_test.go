package scale

import (
	"errors"
	"runtime/debug"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// A range value nested past jsval.MaxValueDepth (a signal built at run time)
// ends the interpolation, and the call, with ErrLimit, as every walk of a
// value does (C103, audit 2026-10-18): it is not recursed into until the
// stack runs out, nor cut short silently.
func TestInterpolateDeepValueIsBounded(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	a, b := jsval.Num(0), jsval.Num(1)
	for i := 0; i < 2_000_000; i++ {
		a, b = jsval.ArrOf(a), jsval.ArrOf(b)
	}
	var err error
	func() {
		defer func() {
			if s, ok := recover().(*budget.Stop); ok {
				err = s.Err
			}
		}()
		InterpolateValue(jsval.WalkerFor(t.Context()), a, b)
	}()
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("interpolating a value nested 2,000,000 deep: %v, want ErrLimit", err)
	}
	// Within the bound it interpolates as before.
	a, b = jsval.Num(0), jsval.Num(1)
	for i := 0; i < jsval.MaxValueDepth/2; i++ {
		a, b = jsval.ArrOf(a), jsval.ArrOf(b)
	}
	v := InterpolateValue(jsval.WalkerFor(t.Context()), a, b)(0.5)
	for v.IsArr() {
		v = v.Index(0)
	}
	if v.NumValue() != 0.5 {
		t.Fatalf("interpolated %v, want 0.5", v)
	}
}
