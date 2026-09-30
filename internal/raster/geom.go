package raster

import "math"

// point is a 2-D coordinate.
type point struct{ x, y float64 }

// matrix is an affine transform [a c e; b d f; 0 0 1] in SVG's column order:
//
//	x' = a*x + c*y + e
//	y' = b*x + d*y + f
type matrix struct{ a, b, c, d, e, f float64 }

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) apply(p point) point {
	return point{m.a*p.x + m.c*p.y + m.e, m.b*p.x + m.d*p.y + m.f}
}

// mul returns m*n: n is applied first, then m (SVG "m n" transform-list order
// where the rightmost transform is closest to the geometry).
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

func translate(x, y float64) matrix { return matrix{1, 0, 0, 1, x, y} }
func scaleM(x, y float64) matrix    { return matrix{x, 0, 0, y, 0, 0} }

func (m matrix) det() float64 { return m.a*m.d - m.b*m.c }

func (m matrix) invertible() bool {
	d := m.det()
	return d != 0 && !math.IsNaN(d) && !math.IsInf(d, 0) && math.Abs(d) > 1e-18
}

func (m matrix) invert() (matrix, bool) {
	d := m.det()
	if d == 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return identity, false
	}
	id := 1 / d
	return matrix{
		a: m.d * id,
		b: -m.b * id,
		c: -m.c * id,
		d: m.a * id,
		e: (m.c*m.f - m.d*m.e) * id,
		f: (m.b*m.e - m.a*m.f) * id,
	}, true
}

// meanScale is the geometric-mean scale factor, used to pick flattening
// tolerances in local space.
func (m matrix) meanScale() float64 { return math.Sqrt(math.Abs(m.det())) }

// maxScale bounds how much the transform can stretch a unit vector.
func (m matrix) maxScale() float64 {
	sx := math.Hypot(m.a, m.b)
	sy := math.Hypot(m.c, m.d)
	return math.Max(sx, sy)
}

func (m matrix) isFinite() bool {
	for _, v := range [...]float64{m.a, m.b, m.c, m.d, m.e, m.f} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// rect is an axis-aligned rectangle.
type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) empty() bool { return !(r.x1 > r.x0 && r.y1 > r.y0) }

func (r rect) w() float64 { return r.x1 - r.x0 }
func (r rect) h() float64 { return r.y1 - r.y0 }

func (r rect) intersect(o rect) rect {
	return rect{math.Max(r.x0, o.x0), math.Max(r.y0, o.y0), math.Min(r.x1, o.x1), math.Min(r.y1, o.y1)}
}

// irect is an integer pixel rectangle [x0,x1) x [y0,y1).
type irect struct{ x0, y0, x1, y1 int }

func (r irect) empty() bool { return r.x1 <= r.x0 || r.y1 <= r.y0 }
func (r irect) w() int      { return r.x1 - r.x0 }
func (r irect) h() int      { return r.y1 - r.y0 }

func (r irect) intersect(o irect) irect {
	if o.x0 > r.x0 {
		r.x0 = o.x0
	}
	if o.y0 > r.y0 {
		r.y0 = o.y0
	}
	if o.x1 < r.x1 {
		r.x1 = o.x1
	}
	if o.y1 < r.y1 {
		r.y1 = o.y1
	}
	return r
}

func (r irect) union(o irect) irect {
	if r.empty() {
		return o
	}
	if o.empty() {
		return r
	}
	if o.x0 < r.x0 {
		r.x0 = o.x0
	}
	if o.y0 < r.y0 {
		r.y0 = o.y0
	}
	if o.x1 > r.x1 {
		r.x1 = o.x1
	}
	if o.y1 > r.y1 {
		r.y1 = o.y1
	}
	return r
}

// bbox accumulates a bounding box.
type bbox struct {
	r  rect
	ok bool
}

func (b *bbox) add(p point) {
	if !b.ok {
		b.r = rect{p.x, p.y, p.x, p.y}
		b.ok = true
		return
	}
	if p.x < b.r.x0 {
		b.r.x0 = p.x
	}
	if p.x > b.r.x1 {
		b.r.x1 = p.x
	}
	if p.y < b.r.y0 {
		b.r.y0 = p.y
	}
	if p.y > b.r.y1 {
		b.r.y1 = p.y
	}
}

// clampCoord keeps coordinates finite and within a range where float64
// arithmetic stays exact enough; untrusted input can contain 1e308.
const coordLimit = 1e9

func clampCoord(v float64) float64 {
	if v > coordLimit {
		return coordLimit
	}
	if v < -coordLimit {
		return -coordLimit
	}
	if v != v {
		return 0
	}
	return v
}
