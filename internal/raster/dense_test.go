package raster

import (
	"math/rand/v2"
	"slices"
	"testing"
)

type rowRec struct {
	y, x0 int
	cov   []uint8
}

type recSink struct{ rows []rowRec }

func (s *recSink) blitRow(y, x0 int, cov []uint8) {
	s.rows = append(s.rows, rowRec{y, x0, slices.Clone(cov)})
}

// TestDenseCrossingsMatchSorted: binning a sub-scanline's crossings by column
// (used above denseCrossings active edges) must paint exactly what sorting
// them does, under both fill rules.
func TestDenseCrossingsMatchSorted(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	defer func(v int) { denseCrossings = v }(denseCrossings)
	for iter := 0; iter < 40; iter++ {
		var f flat
		for s := 0; s < 1+iter%4; s++ {
			start := len(f.pts)
			for i := 0; i < 5+rng.IntN(300); i++ {
				f.pts = append(f.pts, point{rng.Float64()*120 - 10, rng.Float64()*80 - 10})
			}
			f.subs = append(f.subs, polyline{start: start, end: len(f.pts)})
		}
		for _, eo := range []bool{false, true} {
			var out [2]recSink
			for i, dense := range []int{1 << 30, 0} {
				denseCrossings = dense
				r := newRasterizer()
				r.begin(irect{0, 0, 100, 60})
				r.addPolys(&f)
				r.fill(eo, &out[i])
			}
			if len(out[0].rows) == 0 {
				t.Fatal("nothing painted")
			}
			if !slices.EqualFunc(out[0].rows, out[1].rows, func(a, b rowRec) bool {
				return a.y == b.y && a.x0 == b.x0 && slices.Equal(a.cov, b.cov)
			}) {
				t.Fatalf("iter %d evenOdd %v: dense and sorted coverage differ", iter, eo)
			}
		}
	}
}
