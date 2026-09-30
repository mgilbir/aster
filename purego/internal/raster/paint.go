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
			t = g.applySpread(t)
			v = g.lut[int(t*(lutSize-1)+0.5)]
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
