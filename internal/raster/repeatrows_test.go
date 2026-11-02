package raster

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestRepeatedRowsMatchCrossed: a row fill sends again because its edges are
// all vertical and unchanged (repeatRows) must be exactly the row crossing
// its edges paints, with the same work charged and the same cut-off under a
// work cap. The shapes are rectilinear polygons with edges at fractional
// coordinates, whose steps start and end inside pixel rows, some running out
// of the clip, alone and with slanted polygons over them.
func TestRepeatedRowsMatchCrossed(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	defer func(v bool) { repeatRows = v }(repeatRows)
	coord := func(lo, hi float64) float64 {
		v := lo + rng.Float64()*(hi-lo)
		switch rng.IntN(4) {
		case 0:
			v = float64(int(v)) // on a pixel boundary
		case 1:
			v = float64(int(v*4)) / 4 // on a sub-scanline
		}
		return v
	}
	repeated := 0
	for iter := 0; iter < 400; iter++ {
		var f flat
		for s := 0; s < 1+rng.IntN(6); s++ {
			start := len(f.pts)
			switch rng.IntN(5) {
			case 0: // slanted
				for i := 0; i < 3+rng.IntN(6); i++ {
					f.pts = append(f.pts, point{coord(-10, 110), coord(-10, 70)})
				}
			case 1: // staircase: vertical and horizontal edges only
				x, y := coord(-10, 100), coord(-10, 60)
				f.pts = append(f.pts, point{x, y})
				for i := 0; i < 2+rng.IntN(5); i++ {
					x = coord(-10, 110)
					f.pts = append(f.pts, point{x, y})
					y = coord(-10, 70)
					f.pts = append(f.pts, point{x, y})
				}
				f.pts = append(f.pts, point{f.pts[start].x, y})
			default: // rectangle
				x0, y0 := coord(-10, 110), coord(-10, 70)
				x1, y1 := coord(-10, 110), coord(-10, 70)
				f.pts = append(f.pts, point{x0, y0}, point{x1, y0}, point{x1, y1}, point{x0, y1})
			}
			f.subs = append(f.subs, polyline{start: start, end: len(f.pts)})
		}
		for _, eo := range []bool{false, true} {
			for _, aa := range []bool{true, false} {
				for _, cap := range []int{0, 1 + rng.IntN(3000)} {
					var out [2]recSink
					var work [2]int
					var stopped [2]bool
					for i, on := range []bool{false, true} {
						repeatRows = on
						r := newRasterizer()
						r.setAA(aa)
						r.begin(irect{0, 0, 100, 60})
						r.addPolys(&f)
						r.workCap = cap
						r.fill(eo, &out[i])
						work[i], stopped[i] = r.work, r.stopped
					}
					if !slices.EqualFunc(out[0].rows, out[1].rows, func(a, b rowRec) bool {
						return a.y == b.y && a.x0 == b.x0 && slices.Equal(a.cov, b.cov)
					}) {
						t.Fatalf("iter %d evenOdd %v aa %v cap %d: repeated rows differ from crossed ones", iter, eo, aa, cap)
					}
					if work != [2]int{work[0], work[0]} || stopped[0] != stopped[1] {
						t.Fatalf("iter %d evenOdd %v aa %v cap %d: work %v stopped %v, want equal", iter, eo, aa, cap, work, stopped)
					}
					repeated += len(out[1].rows)
				}
			}
		}
	}
	if repeated == 0 {
		t.Fatal("nothing painted")
	}
}

// TestRepeatedRowsUsed: the interior rows of a rectangle are repeated, not
// crossed (else the test above compares the crossing with itself).
func TestRepeatedRowsUsed(t *testing.T) {
	var f flat
	f.pts = []point{{10.3, 5.6}, {80.7, 5.6}, {80.7, 50.2}, {10.3, 50.2}}
	f.subs = []polyline{{start: 0, end: 4}}
	r := newRasterizer()
	r.begin(irect{0, 0, 100, 60})
	r.addPolys(&f)
	var calls int
	r.fill(false, sinkFunc(func(int, int, []uint8) { calls++ }))
	if calls != 46 {
		t.Fatalf("%d rows painted, want 46", calls)
	}
	if r.repeated < 40 {
		t.Fatalf("%d of %d rows repeated, want the 43 interior ones or so", r.repeated, calls)
	}
}

type sinkFunc func(y, x0 int, cov []uint8)

func (f sinkFunc) blitRow(y, x0 int, cov []uint8) { f(y, x0, cov) }
