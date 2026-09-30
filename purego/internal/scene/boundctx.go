package scene

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// boundContext is a PathContext that accumulates the bounding box of what is
// drawn into it, computing exact extrema of Bézier segments and arcs (vega's
// bound/boundContext.js). An optional rotation (degrees) is applied to every
// point, which is how rotated symbols and paths are bounded without a matrix.
type boundContext struct {
	b              *Bounds
	lx, ly         float64
	rot            float64
	ma, mb, mc, md float64
}

var circleThreshold = tau - 1e-8

// reset starts a bounds pass. rotate is upstream's truthiness test on the
// item's angle; when it holds, deg is used as is, so a non-numeric angle
// (NaN) makes every point NaN and the item contributes nothing.
func (c *boundContext) reset(b *Bounds, rotate bool, deg float64) {
	c.b = b
	c.lx, c.ly = 0, 0
	if rotate {
		c.rot = deg * degToRad
		c.ma = jsmath.Cos(c.rot)
		c.md = c.ma
		c.mb = jsmath.Sin(c.rot)
		c.mc = -c.mb
	} else {
		c.ma, c.md = 1, 1
		c.rot, c.mb, c.mc = 0, 0, 0
	}
}

// The float64(a*b) conversions keep products rounded: arm64 would otherwise fuse
// them into FMAs and differ from V8 in the last bit.
func (c *boundContext) px(x, y float64) float64 { return float64(c.ma*x) + float64(c.mc*y) }
func (c *boundContext) py(x, y float64) float64 { return float64(c.mb*x) + float64(c.md*y) }
func (c *boundContext) addp(x, y float64)       { c.b.Add(c.px(x, y), c.py(x, y)) }
func (c *boundContext) addL(x, y float64) {
	c.lx, c.ly = x, y
	c.b.Add(x, y)
}
func (c *boundContext) addpL(x, y float64) { c.addL(c.px(x, y), c.py(x, y)) }

func (c *boundContext) MoveTo(x, y float64) { c.addpL(x, y) }
func (c *boundContext) LineTo(x, y float64) { c.addpL(x, y) }
func (c *boundContext) ClosePath()          {}

func (c *boundContext) Rect(x, y, w, h float64) {
	if c.rot != 0 {
		c.addp(x+w, y)
		c.addp(x+w, y+h)
		c.addp(x, y+h)
		c.addpL(x, y)
	} else {
		c.b.Add(x+w, y+h)
		c.addL(x, y)
	}
}

func (c *boundContext) QuadraticCurveTo(x1, y1, x2, y2 float64) {
	px1, py1 := c.px(x1, y1), c.py(x1, y1)
	px2, py2 := c.px(x2, y2), c.py(x2, y2)
	if t, ok := quadExtrema(c.lx, px1, px2); ok {
		c.b.Add(t, c.b.Y1) // addX
	}
	if t, ok := quadExtrema(c.ly, py1, py2); ok {
		c.b.Add(c.b.X1, t) // addY
	}
	c.addL(px2, py2)
}

func (c *boundContext) BezierCurveTo(x1, y1, x2, y2, x3, y3 float64) {
	px1, py1 := c.px(x1, y1), c.py(x1, y1)
	px2, py2 := c.px(x2, y2), c.py(x2, y2)
	px3, py3 := c.px(x3, y3), c.py(x3, y3)
	var out [2]float64
	n := cubicExtrema(c.lx, px1, px2, px3, &out)
	for i := 0; i < n; i++ {
		c.b.Add(out[i], c.b.Y1)
	}
	n = cubicExtrema(c.ly, py1, py2, py3, &out)
	for i := 0; i < n; i++ {
		c.b.Add(c.b.X1, out[i])
	}
	c.addL(px3, py3)
}

func (c *boundContext) Arc(cx, cy, r, sa, ea float64, ccw bool) {
	sa += c.rot
	ea += c.rot

	// Store the last point on the path.
	c.lx = float64(r*jsmath.Cos(ea)) + cx
	c.ly = float64(r*jsmath.Sin(ea)) + cy

	if math.Abs(ea-sa) > circleThreshold {
		// Treat as a full circle.
		c.b.Add(cx-r, cy-r)
		c.b.Add(cx+r, cy+r)
		return
	}
	update := func(a float64) { c.b.Add(float64(r*jsmath.Cos(a))+cx, float64(r*jsmath.Sin(a))+cy) }

	// Sample the end points.
	update(sa)
	update(ea)

	// Sample interior points aligned with 90 degrees.
	if ea != sa {
		sa = math.Mod(sa, tau)
		if sa < 0 {
			sa += tau
		}
		ea = math.Mod(ea, tau)
		if ea < 0 {
			ea += tau
		}
		if ea < sa {
			ccw = !ccw // flip direction
			sa, ea = ea, sa
		}
		if ccw {
			ea -= tau
			s := sa - math.Mod(sa, halfPi)
			for i := 0; i < 4 && s > ea; i, s = i+1, s-halfPi {
				update(s)
			}
		} else {
			s := sa - math.Mod(sa, halfPi) + halfPi
			for i := 0; i < 4 && s < ea; i, s = i+1, s+halfPi {
				update(s)
			}
		}
	}
}

// quadExtrema finds the interior extremum of a quadratic Bézier in one axis.
func quadExtrema(x0, x1, x2 float64) (float64, bool) {
	t := (x0 - x1) / (x0 + x2 - 2*x1)
	if 0 < t && t < 1 {
		return x0 + float64((x1-x0)*t), true
	}
	return 0, false
}

// cubicExtrema writes the interior extrema of a cubic Bézier in one axis and
// returns how many there are.
func cubicExtrema(x0, x1, x2, x3 float64, out *[2]float64) int {
	a := x3 - x0 + float64(3*x1) - float64(3*x2)
	b := x0 + x2 - 2*x1
	c := x0 - x1

	var t0, t1 float64
	if math.Abs(a) > 1e-14 {
		// Quadratic equation.
		r := float64(b*b) + float64(c*a)
		if r >= 0 {
			r = math.Sqrt(r)
			t0 = (-b + r) / a
			t1 = (-b - r) / a
		}
	} else {
		// Linear equation.
		t0 = 0.5 * c / b
	}
	n := 0
	if 0 < t0 && t0 < 1 {
		out[n] = cubicAt(t0, x0, x1, x2, x3)
		n++
	}
	if 0 < t1 && t1 < 1 {
		out[n] = cubicAt(t1, x0, x1, x2, x3)
		n++
	}
	return n
}

func cubicAt(t, x0, x1, x2, x3 float64) float64 {
	s := 1 - t
	s2, t2 := s*s, t*t
	return float64(s2*s*x0) + float64(3*s2*t*x1) + float64(3*s*t2*x2) + float64(t2*t*x3)
}

// ReusableContext lets geo.Path keep a stream for this context.
func (c *boundContext) ReusableContext() {}
