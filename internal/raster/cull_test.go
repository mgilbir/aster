package raster

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

// randomFarPath is a path of lines, quadratics and cubics whose points fall
// anywhere from far beyond the clip on every side to inside it, so runs of
// points beyond one side alternate with points inside or beyond another.
func randomFarPath(rng *rand.Rand, clip irect) *path {
	return randomPath(rng, clip, 1e6)
}

// randomPath is a path of points about clip, up to far beyond each side.
func randomPath(rng *rand.Rand, clip irect, far float64) *path {
	coord := func(lo, hi int) float64 {
		switch rng.IntN(4) {
		case 0:
			return float64(lo) - rng.Float64()*far
		case 1:
			return float64(hi) + rng.Float64()*far
		default:
			return float64(lo) - 20 + rng.Float64()*float64(hi-lo+40)
		}
	}
	pt := func() (float64, float64) { return coord(clip.x0, clip.x1), coord(clip.y0, clip.y1) }
	var p path
	for range 1 + rng.IntN(3) {
		p.moveTo(pt())
		for range 1 + rng.IntN(12) {
			switch rng.IntN(3) {
			case 0:
				p.lineTo(pt())
			case 1:
				x1, y1 := pt()
				x, y := pt()
				p.quadTo(x1, y1, x, y)
			default:
				x1, y1 := pt()
				x2, y2 := pt()
				x, y := pt()
				p.cubicTo(x1, y1, x2, y2, x, y)
			}
		}
		if rng.IntN(2) == 0 {
			p.close()
		}
	}
	return &p
}

func coverage(f *flat, clip irect, evenOdd, aa bool) []uint8 {
	rs := newRasterizer()
	rs.setAA(aa)
	mk := &mask{r: clip, a: make([]uint8, clip.w()*clip.h())}
	rs.begin(clip)
	rs.addPolys(f)
	rs.fill(evenOdd, maskSink{mk})
	return mk.a
}

// Culling while flattening keeps of a run of points beyond one side of the
// clip only its ends. The fill must be the same, pixel for pixel, under both
// winding rules and with anti-aliasing on and off: the scan converter takes
// every crossing beyond a side at that side, so only the run's signed
// crossings count, and its chord has the same.
func TestCulledFlattenFillsTheSame(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	clip := irect{3, 5, 43, 37}
	culledSome := false
	for i := range 3000 {
		p := randomFarPath(rng, clip)
		m := matrix{1 + rng.Float64(), rng.Float64() - 0.5, rng.Float64() - 0.5, 1 + rng.Float64(), rng.Float64() * 5, rng.Float64() * 5}
		var full, culled flat
		full.flatten(p, m, shapeTol)
		culled.setCull(clip)
		culled.flatten(p, m, shapeTol)
		if len(culled.pts) < len(full.pts) {
			culledSome = true
		}
		for _, evenOdd := range []bool{false, true} {
			for _, aa := range []bool{true, false} {
				if !bytes.Equal(coverage(&full, clip, evenOdd, aa), coverage(&culled, clip, evenOdd, aa)) {
					t.Fatalf("path %d (evenOdd %v, aa %v): the culled flattening (%d points) fills differently from the full one (%d)", i, evenOdd, aa, len(culled.pts), len(full.pts))
				}
			}
		}
	}
	if !culledSome {
		t.Fatal("no path had points culled: the test does not test culling")
	}
}

// A path mayCover reports as missing the region must paint nothing there,
// filled or stroked as far as strokeReach says.
func TestMayCoverIsConservative(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	region := irect{10, 10, 50, 40}
	r := &renderer{lim: Limits{}.withDefaults()}
	skipped := 0
	for i := range 4000 {
		// A small path somewhere about the region: often beyond it, often
		// across its edge.
		cx, cy := -40+rng.IntN(140), -40+rng.IntN(120)
		p := randomPath(rng, irect{cx, cy, cx + 1 + rng.IntN(20), cy + 1 + rng.IntN(20)}, 15)
		m := matrix{0.5 + rng.Float64(), rng.Float64() - 0.5, rng.Float64() - 0.5, 0.5 + rng.Float64(), rng.Float64()*40 - 20, rng.Float64()*40 - 20}
		st := initialState(0, 0)
		st.ctm = m
		st.strokeWidth = rng.Float64() * 30
		st.join = lineJoin(rng.IntN(4))
		st.cap = lineCap(rng.IntN(3))
		st.miter = 1 + rng.Float64()*10
		if !r.mayCover(p, m, region, 0) {
			skipped++
			var f flat
			f.flatten(p, m, shapeTol)
			for _, c := range coverage(&f, region, false, true) {
				if c != 0 {
					t.Fatalf("path %d: mayCover is false for a fill that paints the region", i)
				}
			}
		}
		if !r.mayCover(p, m, region, strokeReach(&st)) {
			var sf, so flat
			sf.flatten(p, identity, shapeTol/m.maxScale())
			var s stroker
			s.stroke(&sf, strokeStyle{width: st.strokeWidth, cap: st.cap, join: st.join, miterLimit: st.miter, lin: m}, m.maxScale(), &so)
			for k, pt := range so.pts {
				so.pts[k] = m.apply(pt)
			}
			for _, c := range coverage(&so, region, false, true) {
				if c != 0 {
					t.Fatalf("path %d: mayCover is false for a stroke that paints the region", i)
				}
			}
		}
	}
	if skipped < 100 {
		t.Fatalf("only %d paths missed the region: the test does not test culling", skipped)
	}
}

// MaxPathSegments bounds the geometry a path holds (C6; it was declared and
// never read). 40,000 cubics each sweeping the canvas flatten to up to 400
// points each, 16 million in all, more than a million points allow; the
// audit's 40,000 cubics that reach far beyond the canvas keep only the
// points of their visible parts, and fit.
func TestPathGeometryBounded(t *testing.T) {
	lim := Limits{MaxPathSegments: 1 << 20}
	sweeping := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><path d="M50 50` + strings.Repeat(`c9e8 1 -9e8 1 0 0`, 40000) + `"/></svg>`
	if _, err := Render([]byte(sweeping), Options{Limits: lim}); !errors.Is(err, errLimit) {
		t.Errorf("16M points visible: err = %v", err)
	}
	far := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><path d="M0 0` + strings.Repeat(`c9e8 0 0 9e8 1 1`, 40000) + `"/></svg>`
	if _, err := Render([]byte(far), Options{Limits: lim}); err != nil {
		t.Errorf("40,000 cubics mostly beyond the canvas: %v", err)
	}
}
