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
		l := deriveLimits(n, 0)
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

// WithPixelWorkLimit's value is the base at scale 1 of every derived budget:
// the PNG render's (rasterAt), the label transform's and a PDF's colour glyph
// images' (both at scale 1), whatever WithMemoryLimit says; past what an int
// holds it saturates.
func TestPixelWorkLimitDerived(t *testing.T) {
	for _, n := range []uint64{0, 64 << 20} {
		for _, ops := range []uint64{1 << 20, 300 << 20, math.MaxUint64} {
			l := deriveLimits(n, ops)
			base := int(min(ops, math.MaxInt))
			if l.pixelOps != base || l.vega.RasterPixelOps != base {
				t.Errorf("WithMemoryLimit(%d), WithPixelWorkLimit(%d): base %d, label's %d, want %d", n, ops, l.pixelOps, l.vega.RasterPixelOps, base)
			}
			for _, s := range []float64{1, 3} {
				want := raster.ScaledPixelOps(base, s)
				if got := l.rasterAt(s).PixelOps(s, 100, 100); got != want {
					t.Errorf("WithPixelWorkLimit(%d) at scale %v: %d, want %d", ops, s, got, want)
				}
			}
		}
		if l := deriveLimits(n, 0); l.pixelOps != defaultPixelOps || l.vega.RasterPixelOps != defaultPixelOps {
			t.Errorf("WithMemoryLimit(%d) alone: base %d, label's %d", n, l.pixelOps, l.vega.RasterPixelOps)
		}
	}
	if got := raster.ScaledPixelOps(math.MaxInt, 3); got != math.MaxInt {
		t.Errorf("a saturated base at scale 3: %d", got)
	}
}
