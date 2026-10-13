package voronoi

import (
	"errors"
	"math"
	"slices"
)

// ErrInvalidBounds is returned when the clipping extent is inverted or NaN.
var ErrInvalidBounds = errors.New("voronoi: invalid bounds")

// diagram is d3-delaunay's Voronoi: circumcentres, exterior cell rays and the
// clipping of each cell to [xmin,xmax]×[ymin,ymax].
type diagram struct {
	d                      *delaunay
	circumcenters          []float64
	vectors                []float64
	xmin, ymin, xmax, ymax float64
}

func newDiagram(d *delaunay, xmin, ymin, xmax, ymax float64) (*diagram, error) {
	// Written as negated >= so that NaN bounds are rejected too.
	if !(xmax >= xmin) || !(ymax >= ymin) {
		return nil, ErrInvalidBounds
	}
	v := &diagram{d: d, xmin: xmin, ymin: ymin, xmax: xmax, ymax: ymax}
	v.init()
	return v, nil
}

func (v *diagram) point(t int32) (float64, float64) {
	// The one/two-point fake triangle references index -1, which upstream reads
	// as undefined (NaN).
	if t < 0 {
		return math.NaN(), math.NaN()
	}
	return v.d.points[2*t], v.d.points[2*t+1]
}

func (v *diagram) init() {
	d := v.d
	points, hull, triangles := d.points, d.hull, d.triangles
	v.vectors = make([]float64, len(points)*2)
	v.circumcenters = make([]float64, len(triangles)/3*2)
	circ := v.circumcenters

	var bx, by float64
	haveBary := false
	for i, j := 0, 0; i < len(triangles); i, j = i+3, j+2 {
		x1, y1 := v.point(triangles[i])
		x2, y2 := v.point(triangles[i+1])
		x3, y3 := v.point(triangles[i+2])

		dx := x2 - x1
		dy := y2 - y1
		ex := x3 - x1
		ey := y3 - y1
		ab := (float64(dx*ey) - float64(dy*ex)) * 2

		var x, y float64
		if math.Abs(ab) < 1e-9 {
			// A degenerate triangle has its circumcentre at infinity, in a
			// direction orthogonal to the half-edge and away from the hull's
			// barycentre.
			if !haveBary {
				haveBary = true
				for _, h := range hull {
					bx += points[h*2]
					by += points[h*2+1]
				}
				bx /= float64(len(hull))
				by /= float64(len(hull))
			}
			a := 1e9 * sign(float64((bx-x1)*ey)-float64((by-y1)*ex))
			x = (x1+x3)/2 - float64(a*ey)
			y = (y1+y3)/2 + float64(a*ex)
		} else {
			dd := 1 / ab
			bl := float64(dx*dx) + float64(dy*dy)
			cl := float64(ex*ex) + float64(ey*ey)
			x = x1 + float64((float64(ey*bl)-float64(dy*cl))*dd)
			y = y1 + float64((float64(dx*cl)-float64(ex*bl))*dd)
		}
		circ[j] = x
		circ[j+1] = y
	}

	// Exterior cell rays.
	if len(hull) == 0 {
		return
	}
	vectors := v.vectors
	h := hull[len(hull)-1]
	p1 := int(h) * 4
	x1, y1 := points[2*h], points[2*h+1]
	for _, h = range hull {
		p0, x0, y0 := p1, x1, y1
		p1, x1, y1 = int(h)*4, points[2*h], points[2*h+1]
		vectors[p0+2] = y0 - y1
		vectors[p1] = y0 - y1
		vectors[p0+3] = x1 - x0
		vectors[p1+1] = x1 - x0
	}
}

// undef stands for JavaScript's undefined in a coordinate d3-delaunay leaves
// unset (_project when no side of the extent is hit, as with a NaN ray). It
// is a NaN with its own payload: arithmetic on it is NaN, as on undefined,
// but two of them are equal (undefined === undefined) and it prints as
// nothing (Array.prototype.join), where NaN prints "NaN". Arithmetic results
// go through num, since the hardware passes the payload on.
var undef = math.Float64frombits(undefBits)

const undefBits = 0x7ff8_0000_0000_0bad

func isUndef(x float64) bool { return math.Float64bits(x) == undefBits }

// same is JavaScript's === on coordinates that may be undefined.
func same(a, b float64) bool { return a == b || isUndef(a) && isUndef(b) }

// num is the result of arithmetic: undefined in, NaN out.
func num(x float64) float64 {
	if x != x {
		return math.NaN()
	}
	return x
}

// truthy is a number's truthiness: 0 and NaN are false.
func truthy(x float64) bool { return x != 0 && x == x }

func sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x // ±0 and NaN
}

// cellPolygon returns cell i as x,y pairs closed by repeating the first point,
// or nil when the cell is empty or i is a coincident point (d3's cellPolygon,
// which is renderCell into a Polygon).
func (v *diagram) cellPolygon(i int32) []float64 {
	pts := v.clip(i)
	if len(pts) == 0 {
		return nil
	}
	out := make([]float64, 0, len(pts)+2)
	out = append(out, pts[0], pts[1])
	n := len(pts)
	for same(pts[0], pts[n-2]) && same(pts[1], pts[n-1]) && n > 1 {
		n -= 2
		if n < 2 {
			break
		}
	}
	for i := 2; i < n; i += 2 {
		if !same(pts[i], pts[i-2]) || !same(pts[i+1], pts[i-1]) {
			out = append(out, pts[i], pts[i+1])
		}
	}
	out = append(out, out[0], out[1])
	return out
}

func (v *diagram) contains(i int32, x, y float64) bool {
	if x != x || y != y {
		return false
	}
	return v.d.step(i, x, y) == i
}

func (v *diagram) cell(i int32) []float64 {
	d := v.d
	e0 := d.inedges[i]
	if e0 == -1 {
		return nil // coincident point
	}
	var points []float64
	e := e0
	// One turn around point i visits each of its half-edges once; a walk
	// longer than there are half-edges is cycling (upstream would not stop,
	// and the cell would grow until memory ran out), and is degenerate.
	for steps := 0; ; steps++ {
		if steps > len(d.halfedges) {
			d.err = errDegenerate
			return nil
		}
		t := int(e / 3)
		points = append(points, v.circumcenters[t*2], v.circumcenters[t*2+1])
		if e%3 == 2 {
			e -= 2
		} else {
			e++
		}
		if d.triangles[e] != i {
			break // bad triangulation
		}
		e = d.halfedges[e]
		if e == e0 || e == -1 {
			break
		}
	}
	return points
}

func (v *diagram) clip(i int32) []float64 {
	if i == 0 && len(v.d.hull) == 1 {
		return []float64{v.xmax, v.ymin, v.xmax, v.ymax, v.xmin, v.ymax, v.xmin, v.ymin}
	}
	points := v.cell(i)
	if points == nil {
		return nil
	}
	V := v.vectors
	k := int(i) * 4
	if truthy(V[k]) || truthy(V[k+1]) { // V[v] || V[v + 1]
		return v.simplify(v.clipInfinite(i, points, V[k], V[k+1], V[k+2], V[k+3]))
	}
	return v.simplify(v.clipFinite(i, points))
}

func (v *diagram) clipFinite(i int32, points []float64) []float64 {
	n := len(points)
	var P []float64
	var x0, y0 float64
	x1, y1 := points[n-2], points[n-1]
	var c0 int
	c1 := v.regionCode(x1, y1)
	var e0 int
	e1 := 0
	for j := 0; j < n; j += 2 {
		x0, y0, x1, y1 = x1, y1, points[j], points[j+1]
		c0, c1 = c1, v.regionCode(x1, y1)
		if c0 == 0 && c1 == 0 {
			e1 = 0
			P = append(P, x1, y1)
			continue
		}
		var s [4]float64
		var ok bool
		var sx1, sy1 float64
		if c0 == 0 {
			if s, ok = v.clipSegment(x0, y0, x1, y1, c0, c1); !ok {
				continue
			}
			sx1, sy1 = s[2], s[3]
		} else {
			if s, ok = v.clipSegment(x1, y1, x0, y0, c1, c0); !ok {
				continue
			}
			sx1, sy1 = s[0], s[1]
			sx0, sy0 := s[2], s[3]
			e0, e1 = e1, v.edgeCode(sx0, sy0)
			if e0 != 0 && e1 != 0 {
				P, _ = v.edge(i, e0, e1, P, len(P))
			}
			P = append(P, sx0, sy0)
		}
		e0, e1 = e1, v.edgeCode(sx1, sy1)
		if e0 != 0 && e1 != 0 {
			P, _ = v.edge(i, e0, e1, P, len(P))
		}
		P = append(P, sx1, sy1)
	}
	if P != nil {
		e0, e1 = e1, v.edgeCode(P[0], P[1])
		if e0 != 0 && e1 != 0 {
			P, _ = v.edge(i, e0, e1, P, len(P))
		}
	} else if v.contains(i, (v.xmin+v.xmax)/2, (v.ymin+v.ymax)/2) {
		return []float64{v.xmax, v.ymin, v.xmax, v.ymax, v.xmin, v.ymax, v.xmin, v.ymin}
	}
	return P
}

// clipSegment clips a segment to the extent by Cohen–Sutherland. It always
// treats the segment in the same orientation for robustness, as upstream does.
func (v *diagram) clipSegment(x0, y0, x1, y1 float64, c0, c1 int) ([4]float64, bool) {
	flip := c0 < c1
	if flip {
		x0, y0, x1, y1, c0, c1 = x1, y1, x0, y0, c1, c0
	}
	// Each iteration moves one endpoint onto a boundary, so a handful of
	// iterations suffice; the bound only guards against NaN input.
	for iter := 0; iter < 64; iter++ {
		if c0 == 0 && c1 == 0 {
			if flip {
				return [4]float64{x1, y1, x0, y0}, true
			}
			return [4]float64{x0, y0, x1, y1}, true
		}
		if c0&c1 != 0 {
			return [4]float64{}, false
		}
		var x, y float64
		c := c0
		if c == 0 {
			c = c1
		}
		switch {
		case c&0b1000 != 0:
			x = x0 + (x1-x0)*(v.ymax-y0)/(y1-y0)
			y = v.ymax
		case c&0b0100 != 0:
			x = x0 + (x1-x0)*(v.ymin-y0)/(y1-y0)
			y = v.ymin
		case c&0b0010 != 0:
			y = y0 + (y1-y0)*(v.xmax-x0)/(x1-x0)
			x = v.xmax
		default:
			y = y0 + (y1-y0)*(v.xmin-x0)/(x1-x0)
			x = v.xmin
		}
		x, y = num(x), num(y)
		if c0 != 0 {
			x0, y0 = x, y
			c0 = v.regionCode(x0, y0)
		} else {
			x1, y1 = x, y
			c1 = v.regionCode(x1, y1)
		}
	}
	return [4]float64{}, false
}

func (v *diagram) clipInfinite(i int32, points []float64, vx0, vy0, vxn, vyn float64) []float64 {
	P := slices.Clone(points)
	if px, py, ok := v.project(P[0], P[1], vx0, vy0); ok {
		P = slices.Insert(P, 0, px, py)
	}
	if px, py, ok := v.project(P[len(P)-2], P[len(P)-1], vxn, vyn); ok {
		P = append(P, px, py)
	}
	if P = v.clipFinite(i, P); P != nil {
		n := len(P)
		c1 := v.edgeCode(P[n-2], P[n-1])
		for j := 0; j < n; j += 2 {
			c0 := c1
			c1 = v.edgeCode(P[j], P[j+1])
			if c0 != 0 && c1 != 0 {
				P, j = v.edge(i, c0, c1, P, j)
				n = len(P)
			}
		}
	} else if v.contains(i, (v.xmin+v.xmax)/2, (v.ymin+v.ymax)/2) {
		P = []float64{v.xmin, v.ymin, v.xmax, v.ymin, v.xmax, v.ymax, v.xmin, v.ymax}
	}
	return P
}

// edge inserts the extent corners between two boundary points, walking the
// boundary from e0 to e1 (edge codes), keeping only corners inside the cell.
func (v *diagram) edge(i int32, e0, e1 int, P []float64, j int) ([]float64, int) {
	for e0 != e1 {
		var x, y float64
		switch e0 {
		case 0b0101:
			e0 = 0b0100
			continue // top-left
		case 0b0100:
			e0, x, y = 0b0110, v.xmax, v.ymin // top
		case 0b0110:
			e0 = 0b0010
			continue // top-right
		case 0b0010:
			e0, x, y = 0b1010, v.xmax, v.ymax // right
		case 0b1010:
			e0 = 0b1000
			continue // bottom-right
		case 0b1000:
			e0, x, y = 0b1001, v.xmin, v.ymax // bottom
		case 0b1001:
			e0 = 0b0001
			continue // bottom-left
		case 0b0001:
			e0, x, y = 0b0101, v.xmin, v.ymin // left
		default:
			return P, j // not a valid edge code; cannot advance
		}
		// Out-of-range P[j] reads as undefined upstream, so the comparison
		// succeeds when j is at (or past) the end.
		differs := j+1 >= len(P) || P[j] != x || P[j+1] != y
		if differs && v.contains(i, x, y) {
			P = slices.Insert(P, j, x, y)
			j += 2
		}
	}
	return P, j
}

func (v *diagram) project(x0, y0, vx, vy float64) (x, y float64, ok bool) {
	t := math.Inf(1)
	x, y = undef, undef // left unset when no side is hit
	if vy < 0 {         // top
		if y0 <= v.ymin {
			return 0, 0, false
		}
		if c := (v.ymin - y0) / vy; c < t {
			y = v.ymin
			t = c
			x = num(x0 + float64(t*vx))
		}
	} else if vy > 0 { // bottom
		if y0 >= v.ymax {
			return 0, 0, false
		}
		if c := (v.ymax - y0) / vy; c < t {
			y = v.ymax
			t = c
			x = num(x0 + float64(t*vx))
		}
	}
	if vx > 0 { // right
		if x0 >= v.xmax {
			return 0, 0, false
		}
		if c := (v.xmax - x0) / vx; c < t {
			x = v.xmax
			t = c
			y = num(y0 + float64(t*vy))
		}
	} else if vx < 0 { // left
		if x0 <= v.xmin {
			return 0, 0, false
		}
		if c := (v.xmin - x0) / vx; c < t {
			x = v.xmin
			t = c
			y = num(y0 + float64(t*vy))
		}
	}
	return x, y, true
}

func (v *diagram) edgeCode(x, y float64) int {
	c := 0
	switch x {
	case v.xmin:
		c = 0b0001
	case v.xmax:
		c = 0b0010
	}
	switch y {
	case v.ymin:
		c |= 0b0100
	case v.ymax:
		c |= 0b1000
	}
	return c
}

func (v *diagram) regionCode(x, y float64) int {
	c := 0
	if x < v.xmin {
		c = 0b0001
	} else if x > v.xmax {
		c = 0b0010
	}
	if y < v.ymin {
		c |= 0b0100
	} else if y > v.ymax {
		c |= 0b1000
	}
	return c
}

func (v *diagram) simplify(P []float64) []float64 {
	if len(P) > 4 {
		for i := 0; i < len(P); i += 2 {
			j := (i + 2) % len(P)
			k := (i + 4) % len(P)
			if same(P[i], P[j]) && same(P[j], P[k]) || same(P[i+1], P[j+1]) && same(P[j+1], P[k+1]) {
				P = slices.Delete(P, j, j+2)
				i -= 2
				if len(P) == 0 {
					break
				}
			}
		}
		if len(P) == 0 {
			P = nil
		}
	}
	return P
}
