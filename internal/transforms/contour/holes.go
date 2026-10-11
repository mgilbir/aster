package contour

import (
	"math"

	"github.com/mgilbir/aster/internal/budget"
)

// assignHoles is vega-geo's hole assignment: each hole, in order, joins the
// first polygon whose exterior ring contains it (contains != -1); a hole that
// no ring contains is dropped. Upstream tests every hole against every ring
// point by point, O(holes x ring length), which a grid of isolated cells
// makes quadratic in the grid. Here a long ring is indexed once (ringIndex),
// and a point is tested against the edges near it, with upstream's own
// predicates: the result is the same for every input (ADR 0011), and the
// work left is metered so the context ends it.
func assignHoles(m *budget.Meter, polygons [][][][2]float64, holes [][][2]float64) error {
	if len(holes) == 0 {
		return nil
	}
	index := make([]*ringIndex, len(polygons))
	for _, hole := range holes {
		for i := range polygons {
			ring := polygons[i][0]
			var c, work int
			if len(ring) < minIndexedRing {
				c, work = contains(ring, hole), len(ring)
			} else {
				if index[i] == nil {
					index[i] = newRingIndex(ring)
					work += 4 * len(ring)
				}
				var w int
				c, w = index[i].contains(hole)
				work += w
			}
			if err := m.Work(int64(1 + work)); err != nil {
				return err
			}
			if c != -1 {
				polygons[i] = append(polygons[i], hole)
				break
			}
		}
	}
	return nil
}

// minIndexedRing is the ring length from which a ring is indexed: below it,
// upstream's scan is as cheap.
const minIndexedRing = 32

// An axis of a ring of n edges has n/edgesPerBucket buckets, so an edge of
// average length spans one or two, and an edge spanning more than
// maxEdgeBuckets is checked for every point instead.
const (
	edgesPerBucket = 4
	maxEdgeBuckets = 8
)

// ringIndex answers ringContains for one ring by looking only at the edges
// whose ranges contain the point. ringContains is 0 when some edge contains
// the point (segmentContains), and otherwise -1 for an even number of edges
// that cross the point's ray and 1 for an odd one: an order-independent
// function of which edges pass two predicates, so evaluating upstream's
// predicates on a superset of the edges that pass them gives upstream's
// answer exactly. The candidates for a point (x, y):
//
//   - an edge the ray crosses has (yi > y) != (yj > y), so y lies in
//     [min, max] of its two y (a NaN y never compares greater: read as
//     -Infinity);
//   - a vertical edge (a[0] === b[0]) contains the point only with y within
//     its y range, by the within test segmentContains makes on y;
//   - any other edge contains the point only with x within its x range, by
//     the within test it makes on x.
//
// So the edges are filed by y range (all of them) and by x range (the
// non-vertical ones) in buckets of a monotone function of the coordinate,
// and a point's candidates are the edges of its two buckets and the edges
// too long or too far out to file.
type ringIndex struct {
	ring [][2]float64
	y, x axisIndex
}

// axisIndex files edges (by number: edge k joins ring[k] and ring[k-1]) by
// an interval of one coordinate.
type axisIndex struct {
	lo, scale float64
	off       []int32 // edges of bucket b: edges[off[b]:off[b+1]]
	edges     []int32
	long      []int32 // edges checked for every point
}

func newRingIndex(ring [][2]float64) *ringIndex {
	n := len(ring)
	yr := make([][2]float64, n)
	xr := make([][2]float64, n)
	for k := range ring {
		a, b := ring[k], ring[(k+n-1)%n]
		gi, gj := a[1], b[1]
		if math.IsNaN(gi) {
			gi = math.Inf(-1)
		}
		if math.IsNaN(gj) {
			gj = math.Inf(-1)
		}
		yr[k] = [2]float64{math.Min(gi, gj), math.Max(gi, gj)}
		if math.IsInf(yr[k][1], -1) {
			yr[k] = [2]float64{math.NaN(), math.NaN()} // crosses nothing, contains nothing
		}
		if a[0] != b[0] && !math.IsNaN(a[0]) && !math.IsNaN(b[0]) {
			xr[k] = [2]float64{math.Min(a[0], b[0]), math.Max(a[0], b[0])}
		} else {
			xr[k] = [2]float64{math.NaN(), math.NaN()} // vertical, or never within on x
		}
	}
	return &ringIndex{ring: ring, y: newAxisIndex(yr), x: newAxisIndex(xr)}
}

// newAxisIndex files each interval (NaN: none) in the buckets its ends map
// to; an interval with an infinite end, or spanning more than
// maxEdgeBuckets buckets, goes to the long list.
func newAxisIndex(iv [][2]float64) axisIndex {
	nb := max(len(iv)/edgesPerBucket, 1)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range iv {
		for _, v := range r {
			if !math.IsInf(v, 0) && !math.IsNaN(v) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	ix := axisIndex{lo: lo, off: make([]int32, nb+1)}
	if span := hi - lo; span > 0 && !math.IsInf(span, 0) {
		ix.scale = float64(nb) / span
	}
	// Count, then fill: a compact bucket list.
	for _, r := range iv {
		if b0, b1, ok := ix.span(r); ok {
			for b := b0; b <= b1; b++ {
				ix.off[b+1]++
			}
		}
	}
	for b := 1; b <= nb; b++ {
		ix.off[b] += ix.off[b-1]
	}
	ix.edges = make([]int32, ix.off[nb])
	fill := make([]int32, nb)
	copy(fill, ix.off[:nb])
	for k, r := range iv {
		if math.IsNaN(r[0]) {
			continue
		}
		b0, b1, ok := ix.span(r)
		if !ok {
			ix.long = append(ix.long, int32(k))
			continue
		}
		for b := b0; b <= b1; b++ {
			ix.edges[fill[b]] = int32(k)
			fill[b]++
		}
	}
	return ix
}

// span is the buckets of an interval, ok false for one that is not filed
// (none, or long).
func (ix *axisIndex) span(r [2]float64) (b0, b1 int, ok bool) {
	if math.IsNaN(r[0]) || math.IsInf(r[0], 0) || math.IsInf(r[1], 0) {
		return 0, 0, false
	}
	b0, b1 = ix.bucket(r[0]), ix.bucket(r[1])
	return b0, b1, b1-b0 < maxEdgeBuckets
}

// bucket maps a finite coordinate to its bucket. It is monotone (a rounded
// subtraction, product and floor all are), so a value within an interval
// falls in a bucket between those of the interval's ends.
func (ix *axisIndex) bucket(v float64) int {
	if ix.scale == 0 {
		return 0
	}
	f := math.Floor(float64((v - ix.lo) * ix.scale))
	switch {
	case !(f > 0):
		return 0
	case f >= float64(len(ix.off)-2):
		return len(ix.off) - 2
	}
	return int(f)
}

// candidates is the bucket of v and the long list.
func (ix *axisIndex) candidates(v float64) (bucket, long []int32) {
	if len(ix.off) < 2 {
		return nil, ix.long
	}
	b := ix.bucket(v)
	return ix.edges[ix.off[b]:ix.off[b+1]], ix.long
}

// contains is contains(ring, hole) and the work it took.
func (ri *ringIndex) contains(hole [][2]float64) (int, int) {
	work := 0
	for _, p := range hole {
		c, w := ri.ringContains(p)
		work += w
		if c != 0 {
			return c, work
		}
	}
	return 0, work
}

// ringContains is ringContains(ring, p): the same predicates on the
// candidate edges.
func (ri *ringIndex) ringContains(p [2]float64) (int, int) {
	x, y := p[0], p[1]
	if x != x || y != y {
		// No predicate holds for a NaN coordinate: outside.
		return -1, 1
	}
	ring, n := ri.ring, len(ri.ring)
	edge := func(k int32) ([2]float64, [2]float64) { return ring[k], ring[(int(k)+n-1)%n] }
	c, work := -1, 1
	yb, yl := ri.y.candidates(y)
	for _, list := range [2][]int32{yb, yl} {
		work += len(list)
		for _, k := range list {
			pi, pj := edge(k)
			if segmentContains(pi, pj, p) {
				return 0, work
			}
			xi, yi, xj, yj := pi[0], pi[1], pj[0], pj[1]
			if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
				c = -c
			}
		}
	}
	xb, xl := ri.x.candidates(x)
	for _, list := range [2][]int32{xb, xl} {
		work += len(list)
		for _, k := range list {
			if pi, pj := edge(k); segmentContains(pi, pj, p) {
				return 0, work
			}
		}
	}
	return c, work
}
