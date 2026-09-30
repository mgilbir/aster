package wordcloud

import (
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/text"
)

// This file scan-converts glyph outlines the way Cairo's image backend does
// for a path fill or stroke, to the precision that decides whether a pixel is
// touched at all:
//
//   - path points are rounded to 24.8 fixed point, as cairo_path_fixed is;
//   - curves are flattened with Cairo's 0.1 pixel tolerance;
//   - each pixel row is sampled at 15 sub-scanlines (the tor scan converter's
//     GRID_Y) with exact horizontal coverage, filled nonzero;
//   - a pixel whose accumulated coverage rounds to an alpha of at least 1/255
//     is occupied.
//
// A stroke is built as an outline with the canvas's default miter joins (limit
// 10) and butt caps; all its pieces wind the same way so that nonzero filling
// takes their union.

const (
	subRows   = 15
	fixedUnit = 256.0
	flatTol2  = 0.1 * 0.1
	miterMax  = 10.0
	// inkCoverage is the smallest coverage that becomes a non-zero alpha.
	inkCoverage = 0.5 / 255
	maxDepth    = 16
)

type pt struct{ x, y float64 }

// fx rounds a device coordinate to 24.8 fixed point.
func fx(v float64) float64 { return math.Round(v*fixedUnit) / fixedUnit }

type inkEdge struct {
	x0, y0, x1, y1 float64 // y0 < y1
	dxdy           float64
	dir            int
}

// inkRaster accumulates the contours of one word and scan-converts them.
type inkRaster struct {
	contours [][]pt
	cur      []pt
	edges    []inkEdge
	active   []int
	cross    []crossing
	row      []float32
}

type crossing struct {
	x   float64
	dir int
}

func (r *inkRaster) reset() {
	r.contours = r.contours[:0]
	r.cur = r.cur[:0]
}

// addGlyph adds a glyph outline placed at (gx, gy) in the word's frame, which
// is rotated by (c, s) about (tx, ty) on the sheet.
func (r *inkRaster) addGlyph(segs []text.Segment, gx, gy, tx, ty, c, s float64) {
	dev := func(p text.Point) pt {
		lx, ly := gx+p.X, gy+p.Y
		return pt{fx(tx + c*lx - s*ly), fx(ty + s*lx + c*ly)}
	}
	var cur pt
	for _, sg := range segs {
		switch sg.Kind {
		case text.MoveTo:
			r.endContour()
			cur = dev(sg.P[0])
			r.cur = append(r.cur, cur)
		case text.LineTo:
			cur = dev(sg.P[0])
			r.cur = append(r.cur, cur)
		case text.QuadTo:
			// Cairo's outline decomposition raises a quadratic to the
			// cubic with the same shape.
			q1, q2 := dev(sg.P[0]), dev(sg.P[1])
			c1 := pt{fx(cur.x + 2.0/3*(q1.x-cur.x)), fx(cur.y + 2.0/3*(q1.y-cur.y))}
			c2 := pt{fx(q2.x + 2.0/3*(q1.x-q2.x)), fx(q2.y + 2.0/3*(q1.y-q2.y))}
			r.cubic(cur, c1, c2, q2)
			cur = q2
		case text.CubicTo:
			c1, c2, e := dev(sg.P[0]), dev(sg.P[1]), dev(sg.P[2])
			r.cubic(cur, c1, c2, e)
			cur = e
		case text.Close:
			r.endContour()
		}
	}
	r.endContour()
}

func (r *inkRaster) endContour() {
	if len(r.cur) > 2 {
		r.contours = append(r.contours, slices.Clone(r.cur))
	}
	r.cur = r.cur[:0]
}

// cubic appends the flattening of a cubic (excluding its start point), by
// Cairo's rule: split at the midpoint until both control points are within the
// tolerance of the chord.
func (r *inkRaster) cubic(p0, p1, p2, p3 pt) { r.cubicAt(p0, p1, p2, p3, 0) }

func (r *inkRaster) cubicAt(p0, p1, p2, p3 pt, depth int) {
	if depth >= maxDepth || max(segDist2(p1, p0, p3), segDist2(p2, p0, p3)) <= flatTol2 {
		r.cur = append(r.cur, p3)
		return
	}
	mid := func(a, b pt) pt { return pt{(a.x + b.x) / 2, (a.y + b.y) / 2} }
	ab, bc, cd := mid(p0, p1), mid(p1, p2), mid(p2, p3)
	abc, bcd := mid(ab, bc), mid(bc, cd)
	m := mid(abc, bcd)
	r.cubicAt(p0, ab, abc, m, depth+1)
	r.cubicAt(m, bcd, cd, p3, depth+1)
}

// segDist2 is the squared distance from p to the segment a-b.
func segDist2(p, a, b pt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = math.Max(0, math.Min(1, ((p.x-a.x)*dx+(p.y-a.y)*dy)/l2))
	}
	ex, ey := a.x+t*dx-p.x, a.y+t*dy-p.y
	return ex*ex + ey*ey
}

// fill scan-converts the contours as they wind, nonzero.
func (r *inkRaster) fill(m *Mask) {
	r.edges = r.edges[:0]
	for _, c := range r.contours {
		r.addPolygon(c)
	}
	r.scan(m)
}

func (r *inkRaster) addPolygon(c []pt) {
	for i := range c {
		a, b := c[i], c[(i+1)%len(c)]
		if a.y == b.y {
			continue
		}
		dir := 1
		if a.y > b.y {
			a, b = b, a
			dir = -1
		}
		r.edges = append(r.edges, inkEdge{x0: a.x, y0: a.y, x1: b.x, y1: b.y, dxdy: (b.x - a.x) / (b.y - a.y), dir: dir})
	}
}

// stroke scan-converts the outline of the contours stroked with the given
// width.
func (r *inkRaster) stroke(m *Mask, width float64) {
	r.edges = r.edges[:0]
	hw := width / 2
	for _, c := range r.contours {
		// Drop repeated points: a zero-length segment has no direction.
		pts := make([]pt, 0, len(c))
		for _, p := range c {
			if len(pts) == 0 || p != pts[len(pts)-1] {
				pts = append(pts, p)
			}
		}
		for len(pts) > 1 && pts[0] == pts[len(pts)-1] {
			pts = pts[:len(pts)-1]
		}
		n := len(pts)
		if n < 2 {
			continue
		}
		dirs := make([]pt, n)
		for i := range pts {
			a, b := pts[i], pts[(i+1)%n]
			dx, dy := b.x-a.x, b.y-a.y
			l := math.Hypot(dx, dy)
			dirs[i] = pt{dx / l, dy / l}
		}
		for i := range pts {
			a, b := pts[i], pts[(i+1)%n]
			d := dirs[i]
			nx, ny := -d.y*hw, d.x*hw
			r.addOriented([]pt{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}})
			// The join at b, between this segment and the next.
			d1 := dirs[(i+1)%n]
			cross := d.x*d1.y - d.y*d1.x
			dot := d.x*d1.x + d.y*d1.y
			if cross == 0 && dot > 0 {
				continue
			}
			side := 1.0
			if cross > 0 {
				side = -1
			}
			ax, ay := side*nx, side*ny
			bx, by := side*(-d1.y*hw), side*(d1.x*hw)
			// miter length / width = 1 / cos(turn / 2).
			if dot > -1 && 1/math.Sqrt((1+dot)/2) <= miterMax {
				k := 1 / (1 + dot)
				r.addOriented([]pt{b, {b.x + ax, b.y + ay}, {b.x + (ax+bx)*k, b.y + (ay+by)*k}, {b.x + bx, b.y + by}})
			} else {
				r.addOriented([]pt{b, {b.x + ax, b.y + ay}, {b.x + bx, b.y + by}})
			}
		}
	}
	r.scan(m)
}

// addOriented adds a polygon wound positively whatever its input order.
func (r *inkRaster) addOriented(p []pt) {
	area := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		area += p[i].x*p[j].y - p[j].x*p[i].y
	}
	if area < 0 {
		slices.Reverse(p)
	}
	for i := range p {
		p[i] = pt{fx(p[i].x), fx(p[i].y)}
	}
	r.addPolygon(p)
}

// scan fills the accumulated edges nonzero and marks every occupied pixel.
func (r *inkRaster) scan(m *Mask) {
	if len(r.edges) == 0 {
		return
	}
	slices.SortFunc(r.edges, func(a, b inkEdge) int {
		switch {
		case a.y0 < b.y0:
			return -1
		case a.y0 > b.y0:
			return 1
		}
		return 0
	})
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := r.edges[0].y0, math.Inf(-1)
	for _, e := range r.edges {
		minX = math.Min(minX, math.Min(e.x0, e.x1))
		maxX = math.Max(maxX, math.Max(e.x0, e.x1))
		maxY = math.Max(maxY, e.y1)
	}
	px0 := int(math.Max(math.Floor(minX), 0))
	px1 := int(math.Min(math.Ceil(maxX), sheetW))
	py0 := int(math.Max(math.Floor(minY), 0))
	py1 := int(math.Min(math.Ceil(maxY), sheetH))
	if px0 >= px1 || py0 >= py1 {
		return
	}
	if cap(r.row) < px1-px0 {
		r.row = make([]float32, px1-px0)
	}
	row := r.row[:px1-px0]
	next := 0
	r.active = r.active[:0]
	const w = float32(1.0 / subRows)
	for y := py0; y < py1; y++ {
		clear(row)
		for k := 0; k < subRows; k++ {
			ys := float64(y) + (float64(k)+0.5)/subRows
			for next < len(r.edges) && r.edges[next].y0 <= ys {
				r.active = append(r.active, next)
				next++
			}
			r.cross = r.cross[:0]
			live := r.active[:0]
			for _, i := range r.active {
				e := &r.edges[i]
				if e.y1 <= ys {
					continue
				}
				live = append(live, i)
				r.cross = append(r.cross, crossing{e.x0 + (ys-e.y0)*e.dxdy, e.dir})
			}
			r.active = live
			slices.SortFunc(r.cross, func(a, b crossing) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for i := 0; i+1 < len(r.cross); i++ {
				wind += r.cross[i].dir
				if wind == 0 {
					continue
				}
				addSpan(row, px0, r.cross[i].x, r.cross[i+1].x, w)
			}
		}
		for i, v := range row {
			if v >= inkCoverage {
				m.Set(px0+i, y)
			}
		}
	}
}

// addSpan adds the horizontal coverage of [xa, xb) to the pixels of row, each
// scaled by w; row[0] is pixel x0.
func addSpan(row []float32, x0 int, xa, xb float64, w float32) {
	xa = math.Max(xa, float64(x0))
	xb = math.Min(xb, float64(x0+len(row)))
	if xa >= xb {
		return
	}
	ia, ib := int(xa), int(xb)
	if ia == ib {
		row[ia-x0] += float32(xb-xa) * w
		return
	}
	row[ia-x0] += float32(float64(ia+1)-xa) * w
	for i := ia + 1; i < ib; i++ {
		row[i-x0] += w
	}
	if ib-x0 < len(row) {
		row[ib-x0] += float32(xb-float64(ib)) * w
	}
}
