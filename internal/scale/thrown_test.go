package scale

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// d3.piecewise indexes its interpolators with floor(t * n): NaN, or fewer than
// two values, leaves I[i] undefined and the call throws a TypeError.
func TestPiecewiseThrowsWhereD3Does(t *testing.T) {
	thrown := func(f func()) (got bool) {
		defer func() {
			if r := recover(); r != nil {
				_, got = r.(*Thrown)
			}
		}()
		f()
		return false
	}
	three := Piecewise(nil, []jsval.Value{jsval.Num(0), jsval.Num(10), jsval.Num(20)})
	if got := jsval.ToNumber(three(0.75)); got != 15 {
		t.Errorf("piecewise(0.75) = %v", got)
	}
	if !thrown(func() { three(math.NaN()) }) {
		t.Error("NaN did not throw")
	}
	if !thrown(func() { Piecewise(nil, []jsval.Value{jsval.Num(1)})(0.5) }) {
		t.Error("one value did not throw")
	}
	if !thrown(func() { Piecewise(nil, nil)(0.5) }) {
		t.Error("no values did not throw")
	}
}
