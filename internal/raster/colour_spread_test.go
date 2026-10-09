package raster

import (
	"math"
	"testing"

	"github.com/mgilbir/forme/shape"
)

// TestSpreadReflect: the reflection, a constant-time t mod 2, agrees with
// math.Mod's.
func TestSpreadReflect(t *testing.T) {
	g := &colrGradient{extend: shape.ExtendReflect}
	for _, v := range []float64{0, 0.25, 0.5, 1, 1.25, 1.999, 2, 2.5, 3.75, -0.25, -1, -1.5, -2.75, 1e6 + 0.5, -1e6 - 0.25, 1e38, -1e38} {
		m := math.Mod(v, 2)
		if m < 0 {
			m += 2
		}
		if m > 1 {
			m = 2 - m
		}
		if got := g.spread(v); math.Abs(got-m) > 1e-9 {
			t.Errorf("reflect(%v) = %v, math.Mod's %v", v, got, m)
		}
	}
}
