package raster

import "math"

// feTurbulence is Filter Effects 1 §9.21's reference code ("The C code
// below shows the exact algorithm used for this filter effect"), in f64 as
// resvg's turbulence.rs computes it. resvg ports SVG 1.1's version of the
// code; this follows Filter Effects 1's where they differ:
//   - the gradients: Filter Effects 1 draws each gradient again while it is
//     zero, and discards one longer than 1 ("discard the current random
//     vector; try it again"; the specification's changes: "Make
//     feTurbulence algorithms respect uniformity"), where SVG 1.1
//     normalizes every one;
//   - the tile stitched: "The filter primitive subregion is to be passed as
//     the arguments fTileX, fTileY, fTileWidth and fTileHeight", in user
//     space as the point is, where resvg passes the filter region's size in
//     pixels and each pixel's own index as the tile's position;
//   - the seed, "truncated, i.e. rounded to the closest integer value
//     towards zero", is that integer, where resvg saturates it to 32 bits
//     first.
// A pixel is sampled at its top-left corner, mapped to user space by the
// device scale and the region's offset, as resvg (and Firefox) do: the
// specification does not say which point of a pixel is sampled, and
// Chromium (Skia) samples its centre.

const (
	turbRandM   int32 = 2147483647 // 2**31 - 1
	turbRandA   int32 = 16807      // 7**5; primitive root of m
	turbRandQ   int32 = 127773     // m / a
	turbRandR   int32 = 2836       // m % a
	turbBSize         = 0x100
	turbBLen          = turbBSize + turbBSize + 2
	turbBM            = 0xff
	turbPerlinN       = 0x1000
)

// turbNoiseCost is what one noise evaluation of a turbulence is charged, in
// pixel operations: about 11 ns of work, a pixel operation being 3-8 ns.
const turbNoiseCost = 3

// maxTurbulenceOctaves is how many octaves a turbulence computes, at most.
// Octave k adds noise/2^k at the sample point times 2^k. From octave 1024,
// 2^k is infinite in f64 and an octave adds zero, or NaN where its noise is
// not finite; from octave 2098 every point but the origin has overflowed
// (2^-1074 times 2^2098 is past f64's range), its noise is NaN and the sum
// stays NaN. So every octave from 2099 on adds nothing new, and a larger
// numOctaves gives the same image (ADR 0011): resvg computes them all, for
// days at a numOctaves the input chooses.
const maxTurbulenceOctaves = 2099

// turbSpec is a resolved feTurbulence (usvg's Turbulence).
type turbSpec struct {
	freqX, freqY float64 // base frequencies, f32 as usvg's
	octaves      uint32
	seed         int64 // truncated
	stitch       bool
	fractal      bool // fractalNoise, else turbulence
}

// turbulenceOf resolves feTurbulence as usvg's convert_turbulence.
func turbulenceOf(fe *node) turbSpec {
	var t turbSpec
	if list, ok := numList32(fe, aBaseFrequency); ok {
		var x, y float32
		switch len(list) {
		case 2:
			x, y = list[0], list[1]
		case 1:
			x, y = list[0], list[0]
		}
		if !math.Signbit(float64(x)) && !math.Signbit(float64(y)) {
			t.freqX, t.freqY = float64(x), float64(y)
		}
	}
	// numOctaves = "<integer>": a number that is not a whole one is not a
	// value, and the initial 1 holds (usvg rounds it). "Negative values are
	// unsupported": none (usvg's).
	oct := numAttr(fe, aNumOctaves, 1)
	if oct != math.Trunc(oct) {
		oct = 1
	}
	if math.Signbit(oct) {
		oct = 0
	}
	t.octaves = satU32(oct)
	t.seed = satI64(math.Trunc(numAttr(fe, aSeed, 0)))
	t.stitch = fe.str(aStitchTiles) == "stitch"
	t.fractal = fe.str(aType) == "fractalNoise"
	return t
}

// turbTables are the lattice and gradients a seed makes (resvg's init).
type turbTables struct {
	lattice  [turbBLen]int
	gradient [4][turbBLen][2]float64
}

// random is the reference code's Park-Miller step, in i32.
func turbRandom(seed int32) int32 {
	r := turbRandA*(seed%turbRandQ) - turbRandR*(seed/turbRandQ)
	if r <= 0 {
		r += turbRandM
	}
	return r
}

func newTurbTables(seed int64) *turbTables {
	t := &turbTables{}
	// setup_seed, in the integer it is given: the result is in [1, m-1].
	const m = int64(turbRandM)
	if seed <= 0 {
		seed = -(seed % (m - 1)) + 1
	}
	if seed > m-1 {
		seed = m - 1
	}
	s := int32(seed)
	for k := 0; k < 4; k++ {
		for i := 0; i < turbBSize; i++ {
			t.lattice[i] = i
			g := &t.gradient[k][i]
			for {
				for j := 0; j < 2; j++ {
					s = turbRandom(s)
					g[j] = float64((s%(turbBSize+turbBSize))-turbBSize) / turbBSize
				}
				if g[0] != 0 || g[1] != 0 {
					break
				}
			}
			// A gradient is drawn again while it is zero or longer than 1.
			// The draws are the generator's numbers in order, and nowhere
			// in its whole cycle (2^31 - 2 numbers, either way of pairing
			// them) are more than 14 draws in a row thrown away: a gradient
			// takes at most 15, whatever the seed.
			n := math.Sqrt(float64(g[0]*g[0]) + float64(g[1]*g[1]))
			if n > 1 {
				i-- // discard the current random vector; try it again
				continue
			}
			g[0] /= n
			g[1] /= n
		}
	}
	for i := turbBSize - 1; i > 0; i-- {
		k := t.lattice[i]
		s = turbRandom(s)
		j := int(s % turbBSize)
		t.lattice[i] = t.lattice[j]
		t.lattice[j] = k
	}
	for i := 0; i < turbBSize+2; i++ {
		t.lattice[turbBSize+i] = t.lattice[i]
		for g := 0; g < 4; g++ {
			t.gradient[g][turbBSize+i] = t.gradient[g][i]
		}
	}
	return t
}

// satI64 is Rust's `v as i64`.
func satI64(v float64) int64 {
	switch {
	case v != v:
		return 0
	case v >= math.MaxInt64:
		return math.MaxInt64
	case v <= math.MinInt64:
		return math.MinInt64
	}
	return int64(v)
}

type turbStitch struct {
	width, height int32 // how much to subtract to wrap for stitching
	wrapX, wrapY  int32 // minimum value to wrap
}

func sCurve(t float64) float64 { return float64(t*t) * (3 - float64(2*t)) }

func turbLerp(t, a, b float64) float64 { return a + float64(t*(b-a)) }

func (t *turbTables) noise2(ch int, x, y float64, st *turbStitch) float64 {
	v := x + turbPerlinN
	bx0 := satI32(v)
	bx1 := bx0 + 1
	rx0 := v - float64(satI64(v))
	rx1 := rx0 - 1
	v = y + turbPerlinN
	by0 := satI32(v)
	by1 := by0 + 1
	ry0 := v - float64(satI64(v))
	ry1 := ry0 - 1
	if st != nil {
		if bx0 >= st.wrapX {
			bx0 -= st.width
		}
		if bx1 >= st.wrapX {
			bx1 -= st.width
		}
		if by0 >= st.wrapY {
			by0 -= st.height
		}
		if by1 >= st.wrapY {
			by1 -= st.height
		}
	}
	bx0 &= turbBM
	bx1 &= turbBM
	by0 &= turbBM
	by1 &= turbBM
	i := t.lattice[bx0]
	j := t.lattice[bx1]
	b00 := t.lattice[i+int(by0)]
	b10 := t.lattice[j+int(by0)]
	b01 := t.lattice[i+int(by1)]
	b11 := t.lattice[j+int(by1)]
	sx := sCurve(rx0)
	sy := sCurve(ry0)
	g := &t.gradient[ch]
	q := g[b00]
	u := float64(rx0*q[0]) + float64(ry0*q[1])
	q = g[b10]
	w := float64(rx1*q[0]) + float64(ry0*q[1])
	a := turbLerp(sx, u, w)
	q = g[b01]
	u = float64(rx0*q[0]) + float64(ry1*q[1])
	q = g[b11]
	w = float64(rx1*q[0]) + float64(ry1*q[1])
	b := turbLerp(sx, u, w)
	return turbLerp(sy, a, b)
}

// stitchFreq is the reference code's adjustment of a base frequency f for
// stitching a tile of side n: whichever of the nearest frequencies making a
// whole number of periods over the tile is nearer (by ratio), so that its
// borders are continuous ("if(fBaseFreqX != 0.0)").
func stitchFreq(f, n float64) float64 {
	if f == 0 {
		return f
	}
	lo := math.Floor(float64(n*f)) / n
	hi := math.Ceil(float64(n*f)) / n
	if f/lo < hi/f {
		return lo
	}
	return hi
}

// stitchAt is the reference code's initial stitch values for a tile at (x,
// y), w x h, at (stitch-adjusted) frequencies fx, fy, each converted to an
// int as C does (truncated; saturated here).
func stitchAt(x, y, w, h, fx, fy float64) turbStitch {
	var st turbStitch
	st.width = satI32(float64(w*fx) + 0.5)
	st.height = satI32(float64(h*fy) + 0.5)
	st.wrapX = satI32(float64(x*fx) + turbPerlinN + float64(st.width))
	st.wrapY = satI32(float64(y*fy) + turbPerlinN + float64(st.height))
	return st
}

// sum is resvg's turbulence(): the sum of the octaves of one channel at
// (x, y), at frequencies fx, fy, stitched from st0 when it is not nil.
func (t *turbTables) sum(ch int, x, y, fx, fy float64, st0 *turbStitch, fractal bool, octaves uint32) float64 {
	var st *turbStitch
	var stv turbStitch
	if st0 != nil {
		stv = *st0
		st = &stv
	}
	sum := 0.0
	x *= fx
	y *= fy
	ratio := 1.0
	for o := uint32(0); o < octaves; o++ {
		n := t.noise2(ch, x, y, st)
		if !fractal {
			n = math.Abs(n)
		}
		sum += n / ratio
		if sum != sum {
			break // NaN stays NaN: the octaves left change nothing
		}
		x *= 2
		y *= 2
		ratio *= 2
		if st != nil {
			// Subtracting PerlinN before the multiplication and adding it
			// afterwards simplifies to subtracting it once (i32, wrapping).
			st.width *= 2
			st.wrapX = 2*st.wrapX - turbPerlinN
			st.height *= 2
			st.wrapY = 2*st.wrapY - turbPerlinN
		}
	}
	return sum
}

// turbulence is resvg's apply_turbulence: noise over the region's pixels
// (from the image's corner, as resvg's region-sized image is drawn), in the
// primitive's colour space (§9.21: "The generated color and alpha values
// are in the color space determined by the color-interpolation-filters
// property"), made premultiplied. Stitched, the tile is the primitive's
// sub-region in user space. Each pixel is its octaves times four channels
// of work, charged as such.
func (run *filterRun) turbulence(p *fprim) (fimage, bool) {
	w := run.w
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, linear: p.linear, owned: true}
	sx, sy := tsScale(run.ts)
	if approxZero32(float32(sx)) || approxZero32(float32(sy)) {
		return res, true
	}
	tb := p.turb
	octaves := min(tb.octaves, maxTurbulenceOctaves)
	tables := newTurbTables(tb.seed)
	rw, rh := run.region.w(), run.region.h()
	offX := float64(run.region.x0) - float64(float32(run.ts.e))
	offY := float64(run.region.y0) - float64(float32(run.ts.f))
	fx, fy := tb.freqX, tb.freqY
	var st *turbStitch
	if tb.stitch {
		tile := p.rect
		fx, fy = stitchFreq(fx, tile.w()), stitchFreq(fy, tile.h())
		s := stitchAt(tile.x0, tile.y0, tile.w(), tile.h(), fx, fy)
		st = &s
	}
	// Four channels of octaves a pixel, and its setting up, each charged as
	// a noise evaluation: w x h x numOctaves x channels.
	cost := 4 * turbNoiseCost * (int(octaves) + 1)
	ok := bandRows(w, rh, cost, run.work, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < rw; x++ {
				tx, ty := (float64(x)+offX)/sx, (float64(y)+offY)/sy
				o := pix[(y*w+x)*4 : (y*w+x)*4+4]
				for ch := 0; ch < 4; ch++ {
					n := tables.sum(ch, tx, ty, fx, fy, st, tb.fractal, octaves)
					if tb.fractal {
						n = (float64(n*255) + 255) / 2
					} else {
						n *= 255
					}
					o[ch] = satU8(f32Bound(0, float32(n), 255) + 0.5)
				}
			}
			multiply(rowsOf(pix, w, y, y+1))
		}
	})
	return res, ok
}
