package raster

import (
	"math"
	"math/rand"
	"testing"
)

// resvgMorphology is resvg's morphology::apply as written: the window of
// every pixel visited whole. The reference the separable version must equal.
func resvgMorphology(dilate bool, rx, ry float32, pix []uint8, w, h int) []uint8 {
	cols, rows := morphSize(rx, w), morphSize(ry, h)
	tx, ty := cols/2, rows/2
	out := make([]uint8, len(pix))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := [4]uint8{}
			if !dilate {
				v = [4]uint8{255, 255, 255, 255}
			}
			for oy := 0; oy < rows; oy++ {
				for ox := 0; ox < cols; ox++ {
					sx, sy := x-tx+ox, y-ty+oy
					if sx < 0 || sx >= w || sy < 0 || sy >= h {
						continue
					}
					for c := 0; c < 4; c++ {
						p := pix[(sy*w+sx)*4+c]
						if dilate == (p > v[c]) {
							v[c] = p
						}
					}
				}
			}
			copy(out[(y*w+x)*4:], v[:])
		}
	}
	return out
}

// The separable morphology (two sliding windows) gives resvg's result
// exactly, whatever the radius: under, at and past the image's size, and
// one whose size wraps to zero.
func TestMorphologyMatchesResvgLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	radii := []float32{0.3, 1, 1.5, 2, 3.7, 9, 40, 1e6, 2147483648}
	for _, size := range [][2]int{{1, 1}, {2, 3}, {17, 5}, {31, 23}} {
		w, h := size[0], size[1]
		src := make([]uint8, w*h*4)
		for i := range src {
			src[i] = uint8(rng.Intn(256))
		}
		for _, rx := range radii {
			for _, ry := range radii[:5] {
				for _, dilate := range []bool{false, true} {
					want := resvgMorphology(dilate, rx, ry, src, w, h)
					run := &filterRun{w: w, h: h, region: irect{0, 0, w, h}, src: src, ts: identity}
					res, ok := run.morphology(&fprim{kind: tagFeMorphology, dilate: dilate, rx: float64(rx), ry: float64(ry)})
					if !ok {
						t.Fatal("not done")
					}
					for i := range want {
						if res.pix[i] != want[i] {
							t.Fatalf("%dx%d, radius %g %g, dilate %v: byte %d is %d, resvg's %d", w, h, rx, ry, dilate, i, res.pix[i], want[i])
						}
					}
				}
			}
		}
	}
}

// A morphology pass is linear in its line whatever the window: a window of
// the whole line costs at most twice what a window of two pixels does
// (resvg's loop costs the window's length a pixel). Counted, not timed.
func TestMorphologyLinearInRadius(t *testing.T) {
	const n = 4096
	src, dst := make([]uint8, n*4), make([]uint8, n*4)
	steps := func(k int) int {
		m := &morphLine{init: 255, buf: make([]uint8, (n+k)*4*3)}
		m.run(dst, src, 4, n, k, k/2)
		return m.steps
	}
	small, whole := steps(2), steps(n)
	t.Logf("window 2: %d steps; window %d: %d", small, n, whole)
	if whole > 2*small {
		t.Errorf("a window of the whole line takes %d steps, %d for a window of 2: not linear in the line", whole, small)
	}
}

// resvgTurbulence is resvg's turbulence() loop as written: every octave,
// no early end.
func resvgTurbulence(t *turbTables, ch int, x, y float64, tb *turbSpec, octaves int) float64 {
	sum, ratio := 0.0, 1.0
	x *= tb.freqX
	y *= tb.freqY
	for o := 0; o < octaves; o++ {
		n := t.noise2(ch, x, y, nil)
		if !tb.fractal {
			n = math.Abs(n)
		}
		sum += n / ratio
		x *= 2
		y *= 2
		ratio *= 2
	}
	return sum
}

// Octaves past maxTurbulenceOctaves change nothing, so computing at most
// that many is exact: at points from the origin to the extremes of f64, the
// sum of 2099 octaves (ending early once it is NaN) is the sum of 6,000 as
// resvg computes them. The bound is not loose for nothing: stopping where
// 2^k overflows (1,024 octaves) would not be exact.
func TestTurbulenceOctaveCap(t *testing.T) {
	coords := []float64{0, math.SmallestNonzeroFloat64, 1e-300, 3e-200, 1e-10, 0.37, -2.5, 1e10, 1e300, -1e300}
	tables := newTurbTables(5)
	least := 0 // the fewest octaves that give every sum here
	for _, fractal := range []bool{false, true} {
		for _, x := range coords {
			for _, y := range coords {
				for ch := 0; ch < 4; ch++ {
					want := resvgTurbulence(tables, ch, x, y, &turbSpec{freqX: 1, freqY: 1, fractal: fractal}, 6000)
					same := func(n uint32) bool {
						got := tables.sum(ch, x, y, 1, 1, nil, fractal, n)
						return math.Float64bits(got) == math.Float64bits(want) || (got != got && want != want)
					}
					if !same(maxTurbulenceOctaves) {
						t.Errorf("(%g, %g) channel %d fractal %v: %d octaves differ from 6000", x, y, ch, fractal, maxTurbulenceOctaves)
					}
					n := sortSearch(maxTurbulenceOctaves, same)
					least = max(least, n)
				}
			}
		}
	}
	t.Logf("the fewest octaves that give every sum: %d (the cap: %d)", least, maxTurbulenceOctaves)
	if least <= 1024 {
		t.Errorf("%d octaves give every sum: the test points do not reach past where 2^k overflows", least)
	}
}

// sortSearch is the least n in [0, hi] for which ok holds from n on (ok is
// monotone over the octaves past where the sum stops changing).
func sortSearch(hi uint32, ok func(uint32) bool) int {
	lo := uint32(0)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if ok(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return int(lo)
}

// A numOctaves of a billion renders, as the 2,099 octaves it computes do,
// and gives their image: the cap holds in the primitive, not only in the
// sum.
func TestTurbulenceHugeOctaves(t *testing.T) {
	render := func(octaves string) []uint8 {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="12"><defs><filter id="f" x="-3" y="-2" width="20" height="15" filterUnits="userSpaceOnUse">` +
			`<feTurbulence baseFrequency="1e-30 0.3" numOctaves="` + octaves + `" stitchTiles="stitch"/></filter></defs><rect width="16" height="12" filter="url(#f)"/></svg>`
		img, err := Render([]byte(svg), Options{})
		if err != nil {
			t.Fatalf("numOctaves %s: %v", octaves, err)
		}
		return img.Pix
	}
	capped, huge := render("2099"), render("1e9")
	for i := range capped {
		if capped[i] != huge[i] {
			t.Fatalf("byte %d: %d with 2099 octaves, %d with 1e9", i, capped[i], huge[i])
		}
	}
}
