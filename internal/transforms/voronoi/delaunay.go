package voronoi

import (
	"cmp"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/jsmath"
)

// delaunay is d3-delaunay's Delaunay: a triangulation plus the incoming
// half-edge index per point that Voronoi cell traversal needs.
type delaunay struct {
	points    []float64 // shared with the triangulation; jittered in place when collinear
	tri       *triangulation
	inedges   []int32
	hullIndex []int32
	halfedges []int32
	hull      []int32
	triangles []int32
	collinear []int32 // point indices sorted along the line; nil unless all points are collinear
}

func newDelaunay(points []float64) *delaunay {
	n := len(points) / 2
	d := &delaunay{
		points:    points,
		tri:       newTriangulation(points),
		inedges:   make([]int32, n),
		hullIndex: make([]int32, n),
	}
	d.init()
	return d
}

// isCollinear reports whether every triangle is (numerically) flat.
func (d *delaunay) isCollinear() bool {
	c := d.tri.coords
	tr := d.tri.triangles
	for i := 0; i < len(tr); i += 3 {
		a, b, cc := 2*tr[i], 2*tr[i+1], 2*tr[i+2]
		cross := float64((c[cc]-c[a])*(c[b+1]-c[a+1])) - float64((c[b]-c[a])*(c[cc+1]-c[a+1]))
		if cross > 1e-10 {
			return false
		}
	}
	return true
}

func jitter(x, y, r float64) (float64, float64) {
	return x + float64(jsmath.Sin(x+y)*r), y + float64(jsmath.Cos(x-y)*r)
}

// hypot mirrors V8's Math.hypot (scaled, Kahan-compensated sum) so the jitter
// radius agrees with upstream to the last bit.
func hypot(a, b float64) float64 {
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return math.Inf(1)
	}
	if a != a || b != b {
		return math.NaN()
	}
	m := math.Max(math.Abs(a), math.Abs(b))
	if m == 0 {
		return 0
	}
	sum, comp := 0.0, 0.0
	for _, v := range [2]float64{a, b} {
		n := v / m
		summand := float64(n*n) - comp
		prelim := sum + summand
		comp = (prelim - sum) - summand
		sum = prelim
	}
	return math.Sqrt(sum) * m
}

func (d *delaunay) init() {
	points := d.points
	t := d.tri

	if len(t.hull) > 2 && d.isCollinear() {
		n := len(points) / 2
		col := make([]int32, n)
		for i := range col {
			col[i] = int32(i)
		}
		// Stable, like Int32Array.prototype.sort; NaN differences fall
		// through to the y comparison just as `a-b || c-d` does.
		slices.SortStableFunc(col, func(i, j int32) int {
			if dx := points[2*i] - points[2*j]; dx != 0 && dx == dx {
				return cmp.Compare(dx, 0)
			}
			dy := points[2*i+1] - points[2*j+1]
			if dy != 0 && dy == dy {
				return cmp.Compare(dy, 0)
			}
			return 0
		})
		d.collinear = col
		e, f := col[0], col[n-1]
		r := 1e-8 * hypot(points[2*f+1]-points[2*e+1], points[2*f]-points[2*e])
		for i := 0; i < n; i++ {
			points[2*i], points[2*i+1] = jitter(points[2*i], points[2*i+1], r)
		}
		t = newTriangulation(points)
		d.tri = t
	} else {
		d.collinear = nil
	}

	d.halfedges = t.halfedges
	d.hull = t.hull
	d.triangles = t.triangles
	for i := range d.inedges {
		d.inedges[i] = -1
		d.hullIndex[i] = -1
	}

	// An arbitrary incoming half-edge per point; on the hull, exterior
	// half-edges win so that the first neighbour is the hull one.
	for e := 0; e < len(d.halfedges); e++ {
		var p int32
		if e%3 == 2 {
			p = d.triangles[e-2]
		} else {
			p = d.triangles[e+1]
		}
		if d.halfedges[e] == -1 || d.inedges[p] == -1 {
			d.inedges[p] = int32(e)
		}
	}
	for i, h := range d.hull {
		d.hullIndex[h] = int32(i)
	}

	// One or two distinct points: a fake triangle keeps the cell code uniform.
	if len(d.hull) <= 2 && len(d.hull) > 0 {
		d.triangles = []int32{-1, -1, -1}
		d.halfedges = []int32{-1, -1, -1}
		d.triangles[0] = d.hull[0]
		d.inedges[d.hull[0]] = 1
		if len(d.hull) == 2 {
			d.inedges[d.hull[1]] = 0
			d.triangles[1] = d.hull[1]
			d.triangles[2] = d.hull[1]
		}
	}
}

func sq(v float64) float64 { return float64(v * v) }

// step is one hop of the walk used by find/contains: it returns the neighbour
// of i closest to (x, y), or i itself when i is the closest.
func (d *delaunay) step(i int32, x, y float64) int32 {
	points := d.points
	if d.inedges[i] == -1 || len(points) == 0 {
		return int32((int(i) + 1) % (len(points) >> 1))
	}
	c := i
	dc := sq(x-points[i*2]) + sq(y-points[i*2+1])
	e0 := d.inedges[i]
	e := e0
	for {
		tt := d.triangles[e]
		dt := sq(x-points[tt*2]) + sq(y-points[tt*2+1])
		if dt < dc {
			dc, c = dt, tt
		}
		if e%3 == 2 {
			e -= 2
		} else {
			e++
		}
		if d.triangles[e] != i {
			break // bad triangulation
		}
		e = d.halfedges[e]
		if e == -1 {
			e = d.hull[(int(d.hullIndex[i])+1)%len(d.hull)]
			if e != tt {
				if sq(x-points[e*2])+sq(y-points[e*2+1]) < dc {
					return e
				}
			}
			break
		}
		if e == e0 {
			break
		}
	}
	return c
}
