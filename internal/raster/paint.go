package raster

import (
	"math"
	"sort"
)

type spread uint8

const (
	spreadPad spread = iota
	spreadReflect
	spreadRepeat
)

type gstop struct {
	off float64
	c   rgba
}

const lutSize = 1024

// gradient is a linear or radial gradient shader. inv maps device pixel
// centres into gradient space.
type gradient struct {
	radial bool
	inv    matrix
	// linear: unit vector projection; radial: focal geometry.
	x1, y1, dx, dy, invLen2 float64
	cx, cy, r, fx, fy       float64
	spread                  spread
	lut                     [lutSize]uint32 // premultiplied RGBA packed little-endian
}

func (g *gradient) build(stops []gstop, opacity float64) {
	// Stops are already sanitised: offsets monotonic in [0,1].
	for i := 0; i < lutSize; i++ {
		t := float64(i) / (lutSize - 1)
		var c [4]float64
		switch {
		case t <= stops[0].off:
			c = colorF(stops[0].c)
		case t >= stops[len(stops)-1].off:
			c = colorF(stops[len(stops)-1].c)
		default:
			j := sort.Search(len(stops), func(k int) bool { return stops[k].off > t })
			a, b := stops[j-1], stops[j]
			f := 0.0
			if b.off > a.off {
				f = (t - a.off) / (b.off - a.off)
			}
			ca, cb := colorF(a.c), colorF(b.c)
			for k := 0; k < 4; k++ {
				c[k] = ca[k] + (cb[k]-ca[k])*f
			}
		}
		al := c[3] * opacity
		if !(al > 0) {
			al = 0 // NaN as transparent: no NaN is converted to an integer
		}
		r := uint32(math.Round(c[0] * al))
		gg := uint32(math.Round(c[1] * al))
		b := uint32(math.Round(c[2] * al))
		aa := uint32(math.Round(al * 255))
		g.lut[i] = r | gg<<8 | b<<16 | aa<<24
	}
}

func colorF(c rgba) [4]float64 {
	return [4]float64{float64(c.r), float64(c.g), float64(c.b), float64(c.a)}
}

func (g *gradient) applySpread(t float64) float64 {
	switch g.spread {
	case spreadRepeat:
		t -= math.Floor(t)
	case spreadReflect:
		t = math.Mod(t, 2)
		if t < 0 {
			t += 2
		}
		if t > 1 {
			t = 2 - t
		}
	default:
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	return t
}

// lutIndex is the entry of the colour table for t in [0, 1]. A t that is not
// a number, as a gradient whose geometry overflows gives (Inf - Inf), reads
// the first entry, as tiny-skia's clamp to [0, 1] (a max and a min, which
// return the non-NaN operand) does; it must never become an index, which
// would be implementation-defined and out of range (C61).
func lutIndex(t float64) int {
	f := t*(lutSize-1) + 0.5
	if !(f >= 0) {
		return 0
	}
	if f >= lutSize-1 {
		return lutSize - 1
	}
	return int(f)
}

// degenerateThreshold is tiny-skia's DEGENERATE_THRESHOLD: a linear gradient
// whose vector is no longer than this, in its own units, is drawn as one
// colour.
const degenerateThreshold = float32(1.0 / (1 << 15))

// nearlyZero is tiny-skia's SCALAR_NEARLY_ZERO: a radial gradient whose
// radius is no larger than this paints nothing.
const nearlyZero = float32(1.0 / (1 << 12))

// f32Length is tiny-skia's Point::length of the vector (dx, dy), computed in
// f32 as usvg's coordinates are; it falls back to f64 where the f32 square
// overflows.
func f32Length(x0, y0, x1, y1 float64) float32 {
	dx := float32(x1) - float32(x0)
	dy := float32(y1) - float32(y0)
	mag2 := float32(dx*dx) + float32(dy*dy)
	if !math.IsInf(float64(mag2), 0) && mag2 == mag2 {
		return float32(math.Sqrt(float64(mag2)))
	}
	xx, yy := float64(dx), float64(dy)
	return float32(math.Sqrt(float64(xx*xx) + float64(yy*yy)))
}

// averageStopColour is tiny-skia's average_gradient_color, the colour a
// repeating or reflecting gradient too short to draw is painted with: the
// mean of its piecewise-linear colour line over [0, 1], in f32, its stops
// as resvg hands them over (8-bit colour, alpha the stop's times the paint's
// opacity rounded to 8 bits), then premultiplied and rounded as tiny-skia
// stores a solid colour.
func averageStopColour(stops []gstop, opacity float64) paintSrc {
	load := func(s gstop) [4]float32 {
		a := float32(math.Round(math.Max(0, math.Min(1, float64(s.c.a)*opacity))*255)) / 255
		return [4]float32{float32(s.c.r) / 255, float32(s.c.g) / 255, float32(s.c.b) / 255, a}
	}
	var blend [4]float32
	for i := 0; i+1 < len(stops); i++ {
		c0, c1 := load(stops[i]), load(stops[i+1])
		w := float32(stops[i+1].off) - float32(stops[i].off)
		for k := range blend {
			blend[k] += float32(float32(0.5)*w) * float32(c1[k]+c0[k])
		}
	}
	if p0 := float32(stops[0].off); p0 > 0 {
		c := load(stops[0])
		for k := range blend {
			blend[k] += float32(p0 * c[k])
		}
	}
	if pn := float32(stops[len(stops)-1].off); pn < 1 {
		c := load(stops[len(stops)-1])
		for k := range blend {
			blend[k] += float32((1 - pn) * c[k])
		}
	}
	a := min(max(blend[3], 0), 1)
	u8 := func(v float32) uint8 {
		if !(v > 0) {
			return 0
		}
		return uint8(min(v, 1)*255 + 0.5)
	}
	col := [4]uint8{u8(blend[0]), u8(blend[1]), u8(blend[2]), u8(a)}
	if a != 1 {
		col = [4]uint8{u8(float32(blend[0] * a)), u8(float32(blend[1] * a)), u8(float32(blend[2] * a)), u8(a)}
	}
	return paintSrc{solid: true, col: col}
}

func (g *gradient) shadeRow(y, x0 int, dst []uint8) {
	n := len(dst) / 4
	py := float64(y) + 0.5
	for i := 0; i < n; i++ {
		px := float64(x0+i) + 0.5
		gx := g.inv.a*px + g.inv.c*py + g.inv.e
		gy := g.inv.b*px + g.inv.d*py + g.inv.f
		var t float64
		ok := true
		if !g.radial {
			t = ((gx-g.x1)*g.dx + (gy-g.y1)*g.dy) * g.invLen2
		} else {
			t, ok = g.radialT(gx, gy)
		}
		var v uint32
		if ok {
			v = g.lut[lutIndex(g.applySpread(t))]
		}
		dst[i*4] = uint8(v)
		dst[i*4+1] = uint8(v >> 8)
		dst[i*4+2] = uint8(v >> 16)
		dst[i*4+3] = uint8(v >> 24)
	}
}

// radialT solves for the gradient parameter of the circle family that runs
// from the focal point (radius 0) to (cx,cy,r).
func (g *gradient) radialT(x, y float64) (float64, bool) {
	dx, dy := x-g.fx, y-g.fy
	cdx, cdy := g.cx-g.fx, g.cy-g.fy
	a := cdx*cdx + cdy*cdy - g.r*g.r
	if math.Abs(cdx) < 1e-12 && math.Abs(cdy) < 1e-12 {
		if g.r == 0 {
			return 0, false
		}
		return math.Hypot(dx, dy) / g.r, true
	}
	b := dx*cdx + dy*cdy
	c := dx*dx + dy*dy
	// (a) t^2 - 2 b t + c = 0
	if math.Abs(a) < 1e-12 {
		if b == 0 {
			return 0, false
		}
		return c / (2 * b), true
	}
	disc := b*b - a*c
	if disc < 0 {
		return 0, false
	}
	sq := math.Sqrt(disc)
	t1 := (b + sq) / a
	t2 := (b - sq) / a
	t := math.Max(t1, t2)
	if t < 0 {
		return 0, false
	}
	return t, true
}
