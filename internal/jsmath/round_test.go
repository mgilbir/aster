package jsmath

import (
	"math"
	"testing"
)

// Math.round and Math.sign as node 24.21.0 computes them.
func TestRoundSign(t *testing.T) {
	negz := math.Copysign(0, -1)
	for _, c := range [][2]float64{
		{2.5, 3}, {-2.5, -2}, {0.49999999999999994, 0}, {-0.5, negz}, {-0.3, negz}, {negz, negz},
		{1.5, 2}, {-1.5, -1}, {4503599627370495.5, 4503599627370496}, {-4503599627370495.5, -4503599627370495},
		{9007199254740993, 9007199254740992}, {math.Inf(-1), math.Inf(-1)}, {1e300, 1e300},
	} {
		if got := Round(c[0]); got != c[1] || math.Signbit(got) != math.Signbit(c[1]) {
			t.Errorf("Round(%v) = %v; V8 %v", c[0], got, c[1])
		}
	}
	if !math.IsNaN(Round(math.NaN())) || !math.IsNaN(Sign(math.NaN())) || Sign(-3) != -1 || Sign(2) != 1 || !math.Signbit(Sign(negz)) {
		t.Error("NaN, Sign")
	}
}
