package raster

import (
	"strings"
	"testing"
)

// marker-mid finds, for a vertex after a closepath, where its subpath began.
// Walking back for it made a path of n closed subpaths cost n^2 (C103). The
// guard counts what placement reads instead of timing it: it places the
// start, mid and end markers of "M0 0" and n "L1 1 Z" pairs as an
// orient="auto" marker is placed (each vertex and its angle), and the
// segments read to find subpath starts (markerPath.reads) must double when n
// does, not quadruple. The count is exact, so the guard is the same on any
// machine and under -race.
func TestMarkerMidLinear(t *testing.T) {
	reads := func(n int) int {
		var p path
		parsePathData("M0 0"+strings.Repeat("L1 1Z", n), &p)
		mp := &markerPath{segs: pathSegments(&p)}
		for kind := 0; kind < 3; kind++ {
			mp.eachVertex(kind, func(_ point, idx int) bool {
				mp.vertexAngle(idx)
				return true
			})
		}
		return mp.reads
	}
	const n = 1000
	a, b := reads(n), reads(2*n)
	if a < n {
		t.Fatalf("%d subpath-start reads for %d closed subpaths: placement does not count what it reads", a, n)
	}
	// 2 for linear work, 4 for quadratic; the counts are exact.
	if r := float64(b) / float64(a); r > 2.25 {
		t.Errorf("%d segment reads at n=%d, %d at n=%d: ratio %.2f, past 2.25 (linear is 2, quadratic 4)", a, n, b, 2*n, r)
	}
}
