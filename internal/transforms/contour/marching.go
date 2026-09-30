package contour

import (
	"context"
	"math"
)

// Geometry is a GeoJSON MultiPolygon with the contour level attached, as
// vega-geo emits it: {type: "MultiPolygon", value, coordinates}. Each
// polygon is a list of rings, the first being the exterior.
type Geometry struct {
	Value       float64
	Coordinates [][][][2]float64
}

type seg [2][2]float64

// cases lists the line segments of each of the 16 marching-squares
// configurations (relative to the cell), in upstream's order: that order
// determines ring start points and therefore output vertex order.
var cases = [16][]seg{
	{},
	{{{1.0, 1.5}, {0.5, 1.0}}},
	{{{1.5, 1.0}, {1.0, 1.5}}},
	{{{1.5, 1.0}, {0.5, 1.0}}},
	{{{1.0, 0.5}, {1.5, 1.0}}},
	{{{1.0, 1.5}, {0.5, 1.0}}, {{1.0, 0.5}, {1.5, 1.0}}},
	{{{1.0, 0.5}, {1.0, 1.5}}},
	{{{1.0, 0.5}, {0.5, 1.0}}},
	{{{0.5, 1.0}, {1.0, 0.5}}},
	{{{1.0, 1.5}, {1.0, 0.5}}},
	{{{0.5, 1.0}, {1.0, 0.5}}, {{1.5, 1.0}, {1.0, 1.5}}},
	{{{1.5, 1.0}, {1.0, 0.5}}},
	{{{0.5, 1.0}, {1.5, 1.0}}},
	{{{1.0, 1.5}, {1.5, 1.0}}},
	{{{0.5, 1.0}, {1.0, 1.5}}},
	{},
}

// Contours computes one MultiPolygon per threshold over a dx-by-dy row-major
// grid. Cells missing from a short values slice compare false (upstream reads
// undefined). smooth enables linear interpolation of crossings.
func Contours(ctx context.Context, values []float64, dx, dy int, thresholds []float64, smooth bool) ([]Geometry, error) {
	if dx < 0 || dy < 0 {
		return nil, errInvalidSize
	}
	if int64(dx)*int64(dy) > MaxGridCells {
		return nil, errTooLarge
	}
	if len(thresholds) > MaxThresholds {
		return nil, errTooManyThresholds
	}
	m := &marcher{values: values, dx: dx, dy: dy, smooth: smooth}
	out := make([]Geometry, len(thresholds))
	for i, t := range thresholds {
		g, err := m.contour(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = g
	}
	return out, nil
}

type node struct {
	p    [2]float64
	next int32
}

type fragment struct {
	start, end int // grid-point indices keyed in the fragment maps
	head, tail int32
}

type marcher struct {
	values []float64
	dx, dy int
	smooth bool

	// Scratch reused across thresholds.
	nodes      []node
	frags      []fragment
	byStart    map[int]int32
	byEnd      map[int]int32
	x, y       int
	value      float64
	rings      [][][2]float64
	ringBuffer [][2]float64
}

func (m *marcher) at(i int) float64 {
	if i < 0 || i >= len(m.values) {
		return math.NaN()
	}
	return m.values[i]
}

func (m *marcher) ge(i int) int {
	if m.at(i) >= m.value {
		return 1
	}
	return 0
}

// contour accumulates, smooths and nests the rings of one level. Rings with
// positive shoelace area are exteriors, the rest holes; each hole joins the
// first exterior containing any of its points (holes with none are dropped).
func (m *marcher) contour(ctx context.Context, value float64) (Geometry, error) {
	m.value = value
	m.rings = m.rings[:0]
	if err := m.isorings(ctx); err != nil {
		return Geometry{}, err
	}
	var polygons [][][][2]float64
	var holes [][][2]float64
	for _, ring := range m.rings {
		if m.smooth {
			m.smoothRing(ring)
		}
		if area(ring) > 0 {
			polygons = append(polygons, [][][2]float64{ring})
		} else {
			holes = append(holes, ring)
		}
	}
	for _, hole := range holes {
		for i := range polygons {
			if contains(polygons[i][0], hole) != -1 {
				polygons[i] = append(polygons[i], hole)
				break
			}
		}
	}
	if polygons == nil {
		polygons = [][][][2]float64{}
	}
	return Geometry{Value: value, Coordinates: polygons}, nil
}

func (m *marcher) index(p [2]float64) int {
	return int(p[0]*2 + p[1]*float64(m.dx+1)*4)
}

// isorings runs marching squares, stitching segments into closed rings.
func (m *marcher) isorings(ctx context.Context) error {
	m.nodes = m.nodes[:0]
	m.frags = m.frags[:0]
	if m.byStart == nil {
		m.byStart = map[int]int32{}
		m.byEnd = map[int]int32{}
	} else {
		clear(m.byStart)
		clear(m.byEnd)
	}
	dx, dy := m.dx, m.dy
	var t0, t1, t2, t3 int

	// First row (y = -1, t2 = t3 = 0).
	m.x, m.y = -1, -1
	t1 = m.ge(0)
	m.stitchAll(cases[t1<<1])
	for m.x++; m.x < dx-1; m.x++ {
		t0, t1 = t1, m.ge(m.x+1)
		m.stitchAll(cases[t0|t1<<1])
	}
	m.stitchAll(cases[t1])

	// Intermediate rows.
	for m.y++; m.y < dy-1; m.y++ {
		if err := checkCtx(ctx); err != nil {
			return err
		}
		y := m.y
		m.x = -1
		t1 = m.ge(y*dx + dx)
		t2 = m.ge(y * dx)
		m.stitchAll(cases[t1<<1|t2<<2])
		for m.x++; m.x < dx-1; m.x++ {
			x := m.x
			t0, t1 = t1, m.ge(y*dx+dx+x+1)
			t3, t2 = t2, m.ge(y*dx+x+1)
			m.stitchAll(cases[t0|t1<<1|t2<<2|t3<<3])
		}
		m.stitchAll(cases[t1|t2<<3])
	}

	// Last row (y = dy - 1, t0 = t1 = 0).
	y := m.y
	m.x = -1
	t2 = m.ge(y * dx)
	m.stitchAll(cases[t2<<2])
	for m.x++; m.x < dx-1; m.x++ {
		t3, t2 = t2, m.ge(y*dx+m.x+1)
		m.stitchAll(cases[t2<<2|t3<<3])
	}
	m.stitchAll(cases[t2<<3])
	return nil
}

func (m *marcher) stitchAll(lines []seg) {
	for _, l := range lines {
		m.stitch(l)
	}
}

func (m *marcher) newNode(p [2]float64) int32 {
	m.nodes = append(m.nodes, node{p: p, next: -1})
	return int32(len(m.nodes) - 1)
}

func (m *marcher) newFrag(f fragment) int32 {
	m.frags = append(m.frags, f)
	return int32(len(m.frags) - 1)
}

// stitch joins one segment to the open fragments. Upstream's second branch
// also tests for a fragment ending at start, which cannot exist once the first
// test failed, so it is omitted.
func (m *marcher) stitch(l seg) {
	fx, fy := float64(m.x), float64(m.y)
	start := [2]float64{l[0][0] + fx, l[0][1] + fy}
	end := [2]float64{l[1][0] + fx, l[1][1] + fy}
	si, ei := m.index(start), m.index(end)

	if fi, ok := m.byEnd[si]; ok {
		f := &m.frags[fi]
		if gi, ok := m.byStart[ei]; ok {
			g := m.frags[gi]
			delete(m.byEnd, f.end)
			delete(m.byStart, g.start)
			if fi == gi {
				n := m.newNode(end)
				m.nodes[f.tail].next = n
				f.tail = n
				m.emit(f.head)
			} else {
				m.nodes[f.tail].next = g.head
				nf := m.newFrag(fragment{start: f.start, end: g.end, head: f.head, tail: g.tail})
				m.byStart[f.start] = nf
				m.byEnd[g.end] = nf
			}
		} else {
			delete(m.byEnd, f.end)
			n := m.newNode(end)
			m.nodes[f.tail].next = n
			f.tail = n
			f.end = ei
			m.byEnd[ei] = fi
		}
	} else if fi, ok := m.byStart[ei]; ok {
		f := &m.frags[fi]
		delete(m.byStart, f.start)
		n := m.newNode(start)
		m.nodes[n].next = f.head
		f.head = n
		f.start = si
		m.byStart[si] = fi
	} else {
		a, b := m.newNode(start), m.newNode(end)
		m.nodes[a].next = b
		fi := m.newFrag(fragment{start: si, end: ei, head: a, tail: b})
		m.byStart[si] = fi
		m.byEnd[ei] = fi
	}
}

func (m *marcher) emit(head int32) {
	n := 0
	for i := head; i >= 0; i = m.nodes[i].next {
		n++
	}
	ring := make([][2]float64, 0, n)
	for i := head; i >= 0; i = m.nodes[i].next {
		ring = append(ring, m.nodes[i].p)
	}
	m.rings = append(m.rings, ring)
}

// smoothRing moves each on-grid crossing along its axis to the linearly
// interpolated position of the threshold between neighbouring samples.
func (m *marcher) smoothRing(ring [][2]float64) {
	dx, dy := m.dx, m.dy
	for k := range ring {
		x, y := ring[k][0], ring[k][1]
		xt, yt := int(toInt32(x)), int(toInt32(y))
		v1 := m.at(yt*dx + xt)
		if x > 0 && x < float64(dx) && float64(xt) == x {
			v0 := m.at(yt*dx + xt - 1)
			ring[k][0] = x + (m.value-v0)/(v1-v0) - 0.5
		}
		if y > 0 && y < float64(dy) && float64(yt) == y {
			v0 := m.at((yt-1)*dx + xt)
			ring[k][1] = y + (m.value-v0)/(v1-v0) - 0.5
		}
	}
}

func area(ring [][2]float64) float64 {
	n := len(ring)
	if n == 0 {
		return math.NaN()
	}
	// float64() blocks fused multiply-add, which would change the last bits
	// (and with them the sign of the area of tiny rings) relative to upstream.
	a := float64(ring[n-1][1]*ring[0][0]) - float64(ring[n-1][0]*ring[0][1])
	for i := 1; i < n; i++ {
		a += float64(ring[i-1][1]*ring[i][0]) - float64(ring[i-1][0]*ring[i][1])
	}
	return a
}

// contains reports whether any point of hole is inside (1), on (0) or
// outside (-1) ring, deciding on the first point that is not outside.
func contains(ring, hole [][2]float64) int {
	for _, p := range hole {
		if c := ringContains(ring, p); c != 0 {
			return c
		}
	}
	return 0
}

// ringContains is even-odd ray casting; a point on the boundary yields 0.
// Note the upstream quirk that "outside" is -1 and the loop returns the
// toggled sign, so the caller only distinguishes -1 from the rest.
func ringContains(ring [][2]float64, p [2]float64) int {
	x, y := p[0], p[1]
	c := -1
	n := len(ring)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		pi, pj := ring[i], ring[j]
		xi, yi, xj, yj := pi[0], pi[1], pj[0], pj[1]
		if segmentContains(pi, pj, p) {
			return 0
		}
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			c = -c
		}
	}
	return c
}

func segmentContains(a, b, c [2]float64) bool {
	i := 0
	if a[0] == b[0] {
		i = 1
	}
	return collinear(a, b, c) && within(a[i], c[i], b[i])
}

func collinear(a, b, c [2]float64) bool {
	return (b[0]-a[0])*(c[1]-a[1]) == (c[0]-a[0])*(b[1]-a[1])
}

func within(p, q, r float64) bool {
	return p <= q && q <= r || r <= q && q <= p
}
