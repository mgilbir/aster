package scale

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// d3.piecewise indexes its interpolator array with Math.max(0, Math.min(n - 1,
// Math.floor(t * n))): a NaN position, or a single value (no interpolator at
// all), finds nothing to call and throws "I[i] is not a function", which
// upstream surfaces as an operator error rather than a color.
func TestPiecewiseThrowsWhereD3Does(t *testing.T) {
	throws := func(f func() jsval.Value) (th *Thrown) {
		defer func() {
			if r := recover(); r != nil {
				th, _ = r.(*Thrown)
				if th == nil {
					panic(r)
				}
			}
		}()
		f()
		return nil
	}
	two := Piecewise(nil, []jsval.Value{jsval.Num(0), jsval.Num(10)})
	if th := throws(func() jsval.Value { return two(math.NaN()) }); th == nil || th.Name != "TypeError" || th.Msg != "I[i] is not a function" {
		t.Errorf("NaN position: got %v, want the TypeError d3 throws", th)
	}
	for _, x := range []float64{-1, 0, 0.5, 1, 2, math.Inf(1), math.Inf(-1)} {
		if th := throws(func() jsval.Value { return two(x) }); th != nil {
			t.Errorf("position %v threw %v", x, th)
		}
	}
	one := Piecewise(nil, []jsval.Value{jsval.Num(7)})
	for _, x := range []float64{0, 0.5, math.NaN()} {
		if th := throws(func() jsval.Value { return one(x) }); th == nil {
			t.Errorf("single value at %v did not throw", x)
		}
	}
	if th := throws(func() jsval.Value { return Piecewise(nil, nil)(0.5) }); th == nil {
		t.Error("no values did not throw")
	}
}
