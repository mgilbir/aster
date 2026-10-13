package voronoi

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
)

// triangulation is the result of Delaunator's sweep-hull algorithm. Products
// are wrapped in float64 conversions throughout to forbid FMA fusion, so the
// arithmetic is bit-identical to upstream's IEEE double evaluation.
type triangulation struct {
	coords    []float64 // x0, y0, x1, y1, ...
	triangles []int32   // counter-clockwise vertex triples
	halfedges []int32   // twin half-edge or -1 on the hull
	hull      []int32   // convex hull, counter-clockwise

	trianglesLen int
	cx, cy       float64
	hullStart    int32
	hashSize     int
	hullPrev     []int32
	hullNext     []int32
	hullTri      []int32
	hullHash     []int32
	ids          []int32
	dists        []float64
	edgeStack    [512]int32

	// The typed arrays' properties named "undefined"; see undefIndex.
	uPrev, uNext, uTri int32

	meter budget.Meter // one unit per inserted point
	err   error        // the call's end, or errDegenerate
}

// errDegenerate reports links that cycle: coordinates for which upstream
// would not terminate.
var errDegenerate = fmt.Errorf("%w: voronoi: degenerate triangulation (non-finite or extreme coordinates)", budget.ErrLimit)

const delaunatorEpsilon = 1.0 / (1 << 52)

// newTriangulation triangulates coords. Its error is the context's, or
// errDegenerate.
func newTriangulation(ctx context.Context, coords []float64) (*triangulation, error) {
	n := len(coords) >> 1
	maxTriangles := max(2*n-5, 0)
	t := &triangulation{
		coords:    coords,
		triangles: make([]int32, maxTriangles*3),
		halfedges: make([]int32, maxTriangles*3),
		hashSize:  int(math.Ceil(math.Sqrt(float64(n)))),
		hullPrev:  make([]int32, n),
		hullNext:  make([]int32, n),
		hullTri:   make([]int32, n),
		ids:       make([]int32, n),
		dists:     make([]float64, n),
	}
	t.hullHash = make([]int32, t.hashSize)
	t.uPrev, t.uNext, t.uTri = undefIndex, undefIndex, undefIndex
	t.meter = budget.NewMeter(ctx)
	t.build()
	if t.err != nil {
		return nil, t.err
	}
	return t, nil
}

// coordAt is point i's coordinates, NaN (undefined, in arithmetic) for an
// index upstream holds as undefined: -1 for an unset seed, or undefIndex.
func coordAt(coords []float64, i int32) (float64, float64) {
	if i < 0 {
		return math.NaN(), math.NaN()
	}
	return coords[2*i], coords[2*i+1]
}

func dist2(ax, ay, bx, by float64) float64 {
	dx := ax - bx
	dy := ay - by
	return float64(dx*dx) + float64(dy*dy)
}

func (t *triangulation) build() {
	coords := t.coords
	n := len(coords) >> 1
	hullPrev, hullNext, hullTri, hullHash := t.hullPrev, t.hullNext, t.hullTri, t.hullHash

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i := 0; i < n; i++ {
		x, y := coords[2*i], coords[2*i+1]
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
		t.ids[i] = int32(i)
	}
	cx := (minX + maxX) / 2
	cy := (minY + maxY) / 2

	// Seed: the point nearest the bbox centre, its nearest neighbour, and the
	// third point giving the smallest circumcircle. Upstream declares the
	// three without a value: when no distance is finite (coordinates whose
	// squares overflow, NaN) one stays undefined, its coordinates read as
	// NaN, and the input is taken for collinear. -1 stands for undefined.
	i0, i1, i2 := int32(-1), int32(-1), int32(-1)
	minDist := math.Inf(1)
	for i := 0; i < n; i++ {
		d := dist2(cx, cy, coords[2*i], coords[2*i+1])
		if d < minDist {
			i0 = int32(i)
			minDist = d
		}
	}
	i0x, i0y := coordAt(coords, i0)

	minDist = math.Inf(1)
	for i := 0; i < n; i++ {
		if int32(i) == i0 {
			continue
		}
		d := dist2(i0x, i0y, coords[2*i], coords[2*i+1])
		if d < minDist && d > 0 {
			i1 = int32(i)
			minDist = d
		}
	}
	i1x, i1y := coordAt(coords, i1)

	minRadius := math.Inf(1)
	for i := 0; i < n; i++ {
		if int32(i) == i0 || int32(i) == i1 {
			continue
		}
		r := circumradius(i0x, i0y, i1x, i1y, coords[2*i], coords[2*i+1])
		if r < minRadius {
			i2 = int32(i)
			minRadius = r
		}
	}
	i2x, i2y := coordAt(coords, i2)

	if minRadius == math.Inf(1) {
		// All points collinear (or coincident): order them along the line and
		// report them as the hull.
		for i := 0; i < n; i++ {
			d := coords[2*i] - coords[0]
			if d == 0 || d != d {
				d = coords[2*i+1] - coords[1]
			}
			t.dists[i] = d
		}
		quicksort(t.ids, t.dists, 0, n-1)
		hull := make([]int32, 0, n)
		d0 := math.Inf(-1)
		for i := 0; i < n; i++ {
			id := t.ids[i]
			d := t.dists[id]
			if d > d0 {
				hull = append(hull, id)
				d0 = d
			}
		}
		t.hull = hull
		t.triangles = nil
		t.halfedges = nil
		return
	}

	if orient2d(i0x, i0y, i1x, i1y, i2x, i2y) < 0 {
		i1, i2 = i2, i1
		i1x, i2x = i2x, i1x
		i1y, i2y = i2y, i1y
	}

	t.cx, t.cy = circumcenter(i0x, i0y, i1x, i1y, i2x, i2y)
	for i := 0; i < n; i++ {
		t.dists[i] = dist2(coords[2*i], coords[2*i+1], t.cx, t.cy)
	}
	quicksort(t.ids, t.dists, 0, n-1)

	t.hullStart = i0
	hullSize := 3

	hullNext[i0], hullPrev[i2] = i1, i1
	hullNext[i1], hullPrev[i0] = i2, i2
	hullNext[i2], hullPrev[i1] = i0, i0

	hullTri[i0] = 0
	hullTri[i1] = 1
	hullTri[i2] = 2

	for i := range hullHash {
		hullHash[i] = -1
	}
	t.setHash(i0x, i0y, i0)
	t.setHash(i1x, i1y, i1)
	t.setHash(i2x, i2y, i2)

	t.trianglesLen = 0
	if _, ok := t.addTriangle(i0, i1, i2, -1, -1, -1); !ok {
		return
	}

	var xp, yp float64
	for k := 0; k < n; k++ {
		i := t.ids[k]
		x, y := coords[2*i], coords[2*i+1]

		// Skip near-duplicate points.
		if k > 0 && math.Abs(x-xp) <= delaunatorEpsilon && math.Abs(y-yp) <= delaunatorEpsilon {
			continue
		}
		xp, yp = x, y

		if i == i0 || i == i1 || i == i2 {
			continue
		}

		if t.err = t.meter.Work(1); t.err != nil {
			return
		}

		// Find a visible hull edge using the angular hash. A NaN key reads
		// hullHash[NaN], undefined, at every probe.
		start := undefIndex
		if key, ok := t.hashKey(x, y); ok {
			for j := 0; j < t.hashSize; j++ {
				start = hullHash[(key+j)%t.hashSize]
				if start != -1 && start != hullNext[start] {
					break
				}
			}
		}

		// Every walk below follows hull links, which non-finite coordinates
		// can leave inconsistent; upstream would walk such a cycle forever.
		// A walk longer than the hull is one, and the input is degenerate.
		start = t.prev(start)
		e := start
		var q int32
		for steps := 0; ; steps++ {
			if steps > n {
				t.degenerate()
				return
			}
			q = t.next(e)
			ex, ey := t.coord(e)
			qx, qy := t.coord(q)
			if !(orient2d(x, y, ex, ey, qx, qy) >= 0) {
				break
			}
			e = q
			if e == start {
				e = -1
				break
			}
		}
		if e == -1 {
			continue // likely a near-duplicate point
		}

		tri, ok := t.addTriangle(e, i, t.next(e), -1, -1, t.tri(e))
		if !ok {
			return
		}
		hullTri[i] = t.legalize(tri + 2)
		t.setTri(e, int32(tri))
		hullSize++

		// Walk forward through the hull, adding triangles.
		nn := t.next(e)
		for steps := 0; ; steps++ {
			if steps > n {
				t.degenerate()
				return
			}
			q = t.next(nn)
			nx, ny := t.coord(nn)
			qx, qy := t.coord(q)
			if !(orient2d(x, y, nx, ny, qx, qy) < 0) {
				break
			}
			if tri, ok = t.addTriangle(nn, i, q, hullTri[i], -1, t.tri(nn)); !ok {
				return
			}
			hullTri[i] = t.legalize(tri + 2)
			t.setNext(nn, nn)
			hullSize--
			nn = q
		}

		// Walk backward from the other side.
		if e == start {
			for steps := 0; ; steps++ {
				if steps > n {
					t.degenerate()
					return
				}
				q = t.prev(e)
				qx, qy := t.coord(q)
				ex, ey := t.coord(e)
				if !(orient2d(x, y, qx, qy, ex, ey) < 0) {
					break
				}
				if tri, ok = t.addTriangle(q, i, e, -1, t.tri(e), t.tri(q)); !ok {
					return
				}
				t.legalize(tri + 2)
				t.setTri(q, int32(tri))
				t.setNext(e, e)
				hullSize--
				e = q
			}
		}
		if t.err != nil {
			return
		}

		t.hullStart = e
		hullPrev[i] = stored(e)
		t.setPrev(nn, i)
		t.setNext(e, i)
		hullNext[i] = stored(nn)

		t.setHash(x, y, i)
		ex, ey := t.coord(e)
		t.setHash(ex, ey, e)
	}

	if hullSize < 0 {
		hullSize = 0
	}
	t.hull = make([]int32, hullSize)
	e := t.hullStart
	for i := 0; i < hullSize; i++ {
		t.hull[i] = stored(e)
		e = t.next(e)
	}

	t.triangles = t.triangles[:t.trianglesLen]
	t.halfedges = t.halfedges[:t.trianglesLen]
}

// pseudoAngle increases monotonically with the real angle without
// trigonometry; the result is in [0, 1].
func pseudoAngle(dx, dy float64) float64 {
	p := dx / (math.Abs(dx) + math.Abs(dy))
	if dy > 0 {
		return (3 - p) / 4
	}
	return (1 + p) / 4
}

func (t *triangulation) hashKey(x, y float64) (int, bool) {
	f := math.Floor(pseudoAngle(x-t.cx, y-t.cy) * float64(t.hashSize))
	if f != f || t.hashSize == 0 {
		return 0, false // NaN: upstream indexes the hash with NaN
	}
	return int(f) % t.hashSize, true
}

// setHash is hullHash[_hashKey(x, y)] = i, which a NaN key makes a no-op.
func (t *triangulation) setHash(x, y float64, i int32) {
	if k, ok := t.hashKey(x, y); ok {
		t.hullHash[k] = i
	}
}

// undefIndex stands for a hull index upstream holds as undefined: hullPrev
// read at -1, or the hash probed with a NaN key. Its coordinates are NaN,
// and a typed array stores it as 0. Reading or writing a typed array AT
// undefined uses an ordinary property named "undefined" (it is not a
// numeric index), which keeps whatever is written there: hullNext[undefined]
// = i is read back. uPrev, uNext and uTri hold those properties (undefIndex
// while unset).
const undefIndex int32 = -2

// stored is a hull index as a Uint32Array stores it.
func stored(i int32) int32 {
	if i == undefIndex {
		return 0
	}
	return i
}

func (t *triangulation) coord(i int32) (float64, float64) { return coordAt(t.coords, i) }

func (t *triangulation) prev(i int32) int32 {
	switch {
	case i >= 0:
		return t.hullPrev[i]
	case i == undefIndex:
		return t.uPrev
	}
	return undefIndex // hullPrev[-1]
}

func (t *triangulation) next(i int32) int32 {
	switch {
	case i >= 0:
		return t.hullNext[i]
	case i == undefIndex:
		return t.uNext
	}
	return undefIndex
}

func (t *triangulation) tri(i int32) int32 {
	switch {
	case i >= 0:
		return t.hullTri[i]
	case i == undefIndex:
		return t.uTri
	}
	return undefIndex
}

func (t *triangulation) setPrev(i, v int32) {
	switch {
	case i >= 0:
		t.hullPrev[i] = stored(v)
	case i == undefIndex:
		t.uPrev = v
	}
}

func (t *triangulation) setNext(i, v int32) {
	switch {
	case i >= 0:
		t.hullNext[i] = stored(v)
	case i == undefIndex:
		t.uNext = v
	}
}

func (t *triangulation) setTri(i, v int32) {
	switch {
	case i >= 0:
		t.hullTri[i] = stored(v)
	case i == undefIndex:
		t.uTri = v
	}
}

// degenerate records that a walk over the triangulation's links overran its
// size: the links form a cycle upstream would follow forever.
func (t *triangulation) degenerate() {
	if t.err == nil {
		t.err = errDegenerate
	}
}

// legalize flips edges that violate the Delaunay condition, using a fixed
// stack instead of recursion.
func (t *triangulation) legalize(a int) int32 {
	triangles, halfedges, coords := t.triangles, t.halfedges, t.coords
	i := 0
	ar := 0
	// In exact arithmetic each flip adds an edge at the new point, so a
	// legalization flips fewer edges than there are half-edges; past that
	// the links are cycling, which upstream would follow forever.
	flips := 0
	for {
		b := int(halfedges[a])
		a0 := a - a%3
		ar = a0 + (a+2)%3

		if b == -1 { // convex hull edge
			if i == 0 {
				break
			}
			i--
			a = int(t.edgeStack[i])
			continue
		}

		b0 := b - b%3
		al := a0 + (a+1)%3
		bl := b0 + (b+2)%3

		p0 := triangles[ar]
		pr := triangles[a]
		pl := triangles[al]
		p1 := triangles[bl]

		illegal := inCircle(
			coords[2*p0], coords[2*p0+1],
			coords[2*pr], coords[2*pr+1],
			coords[2*pl], coords[2*pl+1],
			coords[2*p1], coords[2*p1+1])

		if illegal {
			if flips++; flips > len(triangles) {
				t.degenerate()
				return int32(ar)
			}
			triangles[a] = p1
			triangles[b] = p0

			hbl := halfedges[bl]

			// Edge swapped on the other side of the hull (rare): fix the
			// half-edge reference kept for that hull edge.
			if hbl == -1 {
				e := t.hullStart
				for steps := 0; ; steps++ {
					if steps > len(t.hullPrev) {
						t.degenerate()
						return int32(ar)
					}
					if int(t.tri(e)) == bl {
						t.setTri(e, int32(a))
						break
					}
					e = t.prev(e)
					if e == t.hullStart {
						break
					}
				}
			}
			t.link(a, int(hbl))
			t.link(b, int(halfedges[ar]))
			t.link(ar, bl)

			br := b0 + (b+1)%3
			// The cap can only be hit on extremely degenerate input.
			if i < len(t.edgeStack) {
				t.edgeStack[i] = int32(br)
				i++
			}
		} else {
			if i == 0 {
				break
			}
			i--
			a = int(t.edgeStack[i])
		}
	}
	return int32(ar)
}

func (t *triangulation) link(a, b int) {
	t.halfedges[a] = stored(int32(b))
	switch {
	case b >= 0:
		t.halfedges[b] = int32(a)
	}
	// b undefined: the back link goes to a property named "undefined" that
	// nothing reads.
}

// addTriangle reports false, and the triangulation degenerate, when the
// triangle does not fit: a valid triangulation of n points has at most
// 2n-5 triangles (upstream would drop the writes past its typed arrays).
func (t *triangulation) addTriangle(i0, i1, i2 int32, a, b, c int32) (int, bool) {
	n := t.trianglesLen
	if n+3 > len(t.triangles) {
		t.degenerate()
		return 0, false
	}
	t.triangles[n] = stored(i0)
	t.triangles[n+1] = stored(i1)
	t.triangles[n+2] = stored(i2)
	t.link(n, int(a))
	t.link(n+1, int(b))
	t.link(n+2, int(c))
	t.trianglesLen += 3
	return n, true
}

func inCircle(ax, ay, bx, by, cx, cy, px, py float64) bool {
	dx := ax - px
	dy := ay - py
	ex := bx - px
	ey := by - py
	fx := cx - px
	fy := cy - py

	ap := float64(dx*dx) + float64(dy*dy)
	bp := float64(ex*ex) + float64(ey*ey)
	cp := float64(fx*fx) + float64(fy*fy)

	return float64(dx*(float64(ey*cp)-float64(bp*fy)))-
		float64(dy*(float64(ex*cp)-float64(bp*fx)))+
		float64(ap*(float64(ex*fy)-float64(ey*fx))) < 0
}

func circumradius(ax, ay, bx, by, cx, cy float64) float64 {
	dx := bx - ax
	dy := by - ay
	ex := cx - ax
	ey := cy - ay

	bl := float64(dx*dx) + float64(dy*dy)
	cl := float64(ex*ex) + float64(ey*ey)
	d := 0.5 / (float64(dx*ey) - float64(dy*ex))

	x := float64((float64(ey*bl) - float64(dy*cl)) * d)
	y := float64((float64(dx*cl) - float64(ex*bl)) * d)
	return float64(x*x) + float64(y*y)
}

func circumcenter(ax, ay, bx, by, cx, cy float64) (float64, float64) {
	dx := bx - ax
	dy := by - ay
	ex := cx - ax
	ey := cy - ay

	bl := float64(dx*dx) + float64(dy*dy)
	cl := float64(ex*ex) + float64(ey*ey)
	d := 0.5 / (float64(dx*ey) - float64(dy*ex))

	x := ax + float64((float64(ey*bl)-float64(dy*cl))*d)
	y := ay + float64((float64(dx*cl)-float64(ex*bl))*d)
	return x, y
}

// quicksort orders ids by dists[id]; the median-of-three partitioning and the
// insertion-sort cutoff match upstream because the order of equidistant
// points changes the triangulation.
func quicksort(ids []int32, dists []float64, left, right int) {
	for {
		if right-left <= 20 {
			for i := left + 1; i <= right; i++ {
				temp := ids[i]
				tempDist := dists[temp]
				j := i - 1
				for j >= left && dists[ids[j]] > tempDist {
					ids[j+1] = ids[j]
					j--
				}
				ids[j+1] = temp
			}
			return
		}
		median := (left + right) >> 1
		i := left + 1
		j := right
		ids[median], ids[i] = ids[i], ids[median]
		if dists[ids[left]] > dists[ids[right]] {
			ids[left], ids[right] = ids[right], ids[left]
		}
		if dists[ids[i]] > dists[ids[right]] {
			ids[i], ids[right] = ids[right], ids[i]
		}
		if dists[ids[left]] > dists[ids[i]] {
			ids[left], ids[i] = ids[i], ids[left]
		}

		temp := ids[i]
		tempDist := dists[temp]
		for {
			for {
				i++
				if i > right || !(dists[ids[i]] < tempDist) {
					break
				}
			}
			for {
				j--
				if j < left || !(dists[ids[j]] > tempDist) {
					break
				}
			}
			if j < i {
				break
			}
			ids[i], ids[j] = ids[j], ids[i]
		}
		ids[left+1] = ids[j]
		ids[j] = temp

		// The two partitions are independent: recurse into the smaller and loop on
		// the larger, so the recursion is at most log2(n) deep even for an input
		// that defeats the median-of-three pivot.
		if right-i+1 >= j-left {
			quicksort(ids, dists, left, j-1)
			left = i
		} else {
			quicksort(ids, dists, i, right)
			right = j - 1
		}
	}
}
