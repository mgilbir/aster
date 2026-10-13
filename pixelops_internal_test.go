package aster

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/raster"
)

// deriveLimits gives every PNG render the work budget max(128 Mi ×
// max(1, scale²), 16 × output pixels), whatever WithMemoryLimit says, and it
// is the budget a direct caller of raster.Render gets by default: the two
// cannot drift.
func TestPixelOpsDerivedForScale(t *testing.T) {
	want := func(s float64, w, h int) int {
		v := max(float64(128<<20)*max(1, s*s), 16*float64(w)*float64(h))
		if v >= float64(math.MaxInt) {
			return math.MaxInt
		}
		return int(v)
	}
	for _, n := range []uint64{0, 4 << 20, 256 << 20, 8 << 30} {
		l := deriveLimits(n)
		for _, s := range []float64{0.25, 1, 1.5, 2, 3, 4} {
			for _, c := range [][2]int{{1, 1}, {800, 600}, {2000, 2000}, {4000, 4000}, {8192, 8192}} {
				got := l.rasterAt(s).PixelOps(s, c[0], c[1])
				if w := want(s, c[0], c[1]); got != w {
					t.Errorf("WithMemoryLimit(%d), scale %v, %dx%d: budget %d, want %d", n, s, c[0], c[1], got, w)
				}
				if d := (raster.Limits{}).PixelOps(s, c[0], c[1]); got != d {
					t.Errorf("WithMemoryLimit(%d), scale %v, %dx%d: derived budget %d, raster's default %d", n, s, c[0], c[1], got, d)
				}
			}
		}
	}
}
