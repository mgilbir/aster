package voronoi

import (
	"math"
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
}

const delaunatorEpsilon = 1.0 / (1 << 52)

func newTriangulation(coords []float64) *triangulation {
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
	t.build()
	return t
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

	var i0, i1, i2 int32

	// Seed: the point nearest the bbox centre, its nearest neighbour, and the
	// third point giving the smallest circumcircle.
	minDist := math.Inf(1)
	for i := 0; i < n; i++ {
		d := dist2(cx, cy, coords[2*i], coords[2*i+1])
		if d < minDist {
			i0 = int32(i)
			minDist = d
		}
	}
	i0x, i0y := coords[2*i0], coords[2*i0+1]

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
	i1x, i1y := coords[2*i1], coords[2*i1+1]

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
	i2x, i2y := coords[2*i2], coords[2*i2+1]

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
	hullHash[t.hashKey(i0x, i0y)] = i0
	hullHash[t.hashKey(i1x, i1y)] = i1
	hullHash[t.hashKey(i2x, i2y)] = i2

	t.trianglesLen = 0
	t.addTriangle(i0, i1, i2, -1, -1, -1)

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

		// Find a visible hull edge using the angular hash.
		start := int32(0)
		key := t.hashKey(x, y)
		for j := 0; j < t.hashSize; j++ {
			start = hullHash[(key+j)%t.hashSize]
			if start != -1 && start != hullNext[start] {
				break
			}
		}
		if start < 0 {
			continue // only reachable with non-finite coordinates
		}

		start = hullPrev[start]
		e := start
		var q int32
		for {
			q = hullNext[e]
			if !(orient2d(x, y, coords[2*e], coords[2*e+1], coords[2*q], coords[2*q+1]) >= 0) {
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

		tri := t.addTriangle(e, i, hullNext[e], -1, -1, hullTri[e])

		hullTri[i] = t.legalize(tri + 2)
		hullTri[e] = int32(tri)
		hullSize++

		// Walk forward through the hull, adding triangles. The step bound
		// only matters for degenerate non-finite input, where the walk could
		// otherwise cycle.
		nn := hullNext[e]
		for steps := 0; steps <= n; steps++ {
			q = hullNext[nn]
			if !(orient2d(x, y, coords[2*nn], coords[2*nn+1], coords[2*q], coords[2*q+1]) < 0) {
				break
			}
			tri = t.addTriangle(nn, i, q, hullTri[i], -1, hullTri[nn])
			hullTri[i] = t.legalize(tri + 2)
			hullNext[nn] = nn
			hullSize--
			nn = q
		}

		// Walk backward from the other side.
		if e == start {
			for steps := 0; steps <= n; steps++ {
				q = hullPrev[e]
				if !(orient2d(x, y, coords[2*q], coords[2*q+1], coords[2*e], coords[2*e+1]) < 0) {
					break
				}
				tri = t.addTriangle(q, i, e, -1, hullTri[e], hullTri[q])
				t.legalize(tri + 2)
				hullTri[q] = int32(tri)
				hullNext[e] = e
				hullSize--
				e = q
			}
		}

		t.hullStart = e
		hullPrev[i] = e
		hullNext[e] = i
		hullPrev[nn] = i
		hullNext[i] = nn

		hullHash[t.hashKey(x, y)] = i
		hullHash[t.hashKey(coords[2*e], coords[2*e+1])] = e
	}

	if hullSize < 0 {
		hullSize = 0
	}
	t.hull = make([]int32, hullSize)
	e := t.hullStart
	for i := 0; i < hullSize; i++ {
		t.hull[i] = e
		e = hullNext[e]
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

func (t *triangulation) hashKey(x, y float64) int {
	f := math.Floor(pseudoAngle(x-t.cx, y-t.cy) * float64(t.hashSize))
	if f != f || t.hashSize == 0 {
		return 0
	}
	return int(f) % t.hashSize
}

// legalize flips edges that violate the Delaunay condition, using a fixed
// stack instead of recursion.
func (t *triangulation) legalize(a int) int32 {
	triangles, halfedges, coords := t.triangles, t.halfedges, t.coords
	i := 0
	ar := 0
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
			triangles[a] = p1
			triangles[b] = p0

			hbl := halfedges[bl]

			// Edge swapped on the other side of the hull (rare): fix the
			// half-edge reference kept for that hull edge.
			if hbl == -1 {
				e := t.hullStart
				for {
					if int(t.hullTri[e]) == bl {
						t.hullTri[e] = int32(a)
						break
					}
					e = t.hullPrev[e]
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
	t.halfedges[a] = int32(b)
	if b != -1 {
		t.halfedges[b] = int32(a)
	}
}

func (t *triangulation) addTriangle(i0, i1, i2 int32, a, b, c int32) int {
	n := t.trianglesLen
	t.triangles[n] = i0
	t.triangles[n+1] = i1
	t.triangles[n+2] = i2
	t.link(n, int(a))
	t.link(n+1, int(b))
	t.link(n+2, int(c))
	t.trianglesLen += 3
	return n
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
