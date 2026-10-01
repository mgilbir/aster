package scale

import (
	"runtime/debug"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A range value nested past jsval.MaxValueDepth (a signal built at run time)
// is interpolated to its end value, not recursed into until the stack runs out.
func TestInterpolateDeepValueIsBounded(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	a, b := jsval.Num(0), jsval.Num(1)
	for i := 0; i < 2_000_000; i++ {
		a, b = jsval.ArrOf(a), jsval.ArrOf(b)
	}
	f := InterpolateValue(a, b)
	v := f(0.5)
	for depth := 0; v.IsArr() && depth <= jsval.MaxValueDepth+2; depth++ {
		v = v.Index(0)
	}
	if !v.IsArr() {
		t.Fatal("the interpolated value lost its nesting before MaxValueDepth")
	}
}
