package svgpdf

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func pointsEqual(a, b Point) bool {
	return almostEqual(a.X, b.X) && almostEqual(a.Y, b.Y)
}

func TestParsePathDataBasicCommands(t *testing.T) {
	// The h/v/Z absolute+relative mix Vega uses for rects:
	// "M0.5,0.5h200v200h-200Z"
	segs, err := parsePathData("M0.5,0.5h200v200h-200Z")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	want := []PathSeg{
		{Op: OpMoveTo, P3: Point{0.5, 0.5}},
		{Op: OpLineTo, P3: Point{200.5, 0.5}},
		{Op: OpLineTo, P3: Point{200.5, 200.5}},
		{Op: OpLineTo, P3: Point{0.5, 200.5}},
		{Op: OpClose},
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(segs), len(want), segs)
	}
	for i := range want {
		if segs[i].Op != want[i].Op || !pointsEqual(segs[i].P3, want[i].P3) {
			t.Errorf("segment %d: got %+v, want %+v", i, segs[i], want[i])
		}
	}
}

func TestParsePathDataImplicitCommands(t *testing.T) {
	// Implicit repetition: "L 1,2 3,4" is two linetos; pairs after M are
	// implicit L.
	segs, err := parsePathData("M0,0 10,10 L20,20 30,30")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	ops := []PathOp{OpMoveTo, OpLineTo, OpLineTo, OpLineTo}
	if len(segs) != len(ops) {
		t.Fatalf("got %d segments, want %d", len(segs), len(ops))
	}
	for i, op := range ops {
		if segs[i].Op != op {
			t.Errorf("segment %d: got op %d, want %d", i, segs[i].Op, op)
		}
	}
	if !pointsEqual(segs[3].P3, Point{30, 30}) {
		t.Errorf("final point: got %+v", segs[3].P3)
	}
}

func TestParsePathDataRelativeCommands(t *testing.T) {
	segs, err := parsePathData("m10,10 l5,-5 h5 v5 z")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	wantPts := []Point{{10, 10}, {15, 5}, {20, 5}, {20, 10}}
	for i, w := range wantPts {
		if !pointsEqual(segs[i].P3, w) {
			t.Errorf("segment %d: got %+v, want %+v", i, segs[i].P3, w)
		}
	}
	if segs[4].Op != OpClose {
		t.Errorf("last segment: got %+v, want close", segs[4])
	}
}

func TestParsePathDataCubicAndQuadratic(t *testing.T) {
	segs, err := parsePathData("M0,0 C1,1 2,1 3,0 Q4,-1 5,0")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("got %d segments, want 3", len(segs))
	}
	c := segs[1]
	if c.Op != OpCubicTo || !pointsEqual(c.P1, Point{1, 1}) || !pointsEqual(c.P2, Point{2, 1}) || !pointsEqual(c.P3, Point{3, 0}) {
		t.Errorf("cubic: got %+v", c)
	}
	// The quadratic must be lowered to the exact degree-elevated cubic.
	q := segs[2]
	if q.Op != OpCubicTo {
		t.Fatalf("quadratic not lowered to cubic: %+v", q)
	}
	wantC1, wantC2 := quadToCubic(Point{3, 0}, Point{4, -1}, Point{5, 0})
	if !pointsEqual(q.P1, wantC1) || !pointsEqual(q.P2, wantC2) || !pointsEqual(q.P3, Point{5, 0}) {
		t.Errorf("quad lowering: got %+v, want c1=%+v c2=%+v", q, wantC1, wantC2)
	}
}

func TestQuadToCubic(t *testing.T) {
	p0, q, p3 := Point{0, 0}, Point{3, 6}, Point{6, 0}
	c1, c2 := quadToCubic(p0, q, p3)
	if !pointsEqual(c1, Point{2, 4}) {
		t.Errorf("c1: got %+v, want (2,4)", c1)
	}
	if !pointsEqual(c2, Point{4, 4}) {
		t.Errorf("c2: got %+v, want (4,4)", c2)
	}
	// Both curves must agree at the midpoint: quadratic at t=0.5 is
	// (p0 + 2q + p3)/4; cubic at t=0.5 is (p0 + 3c1 + 3c2 + p3)/8.
	quadMid := Point{(p0.X + 2*q.X + p3.X) / 4, (p0.Y + 2*q.Y + p3.Y) / 4}
	cubicMid := Point{(p0.X + 3*c1.X + 3*c2.X + p3.X) / 8, (p0.Y + 3*c1.Y + 3*c2.Y + p3.Y) / 8}
	if !pointsEqual(quadMid, cubicMid) {
		t.Errorf("midpoints differ: quad %+v, cubic %+v", quadMid, cubicMid)
	}
}

// TestParsePathDataArc checks the vega circle-symbol idiom: a full circle as
// two 180° arcs, e.g. "M5,0A5,5,0,1,1,-5,0A5,5,0,1,1,5,0".
func TestParsePathDataArc(t *testing.T) {
	const r = 5.0
	segs, err := parsePathData("M5,0A5,5,0,1,1,-5,0A5,5,0,1,1,5,0")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	if segs[0].Op != OpMoveTo {
		t.Fatalf("first segment: got %+v", segs[0])
	}
	// Every arc piece must be a cubic whose endpoint lies on the circle.
	for i, s := range segs[1:] {
		if s.Op != OpCubicTo {
			t.Fatalf("segment %d: got op %d, want cubic", i+1, s.Op)
		}
		dist := math.Hypot(s.P3.X, s.P3.Y)
		if math.Abs(dist-r) > 1e-6 {
			t.Errorf("segment %d endpoint %+v not on circle (r=%g, got %g)", i+1, s.P3, r, dist)
		}
	}
	// The last endpoint must land exactly on the arc target.
	last := segs[len(segs)-1]
	if !pointsEqual(last.P3, Point{5, 0}) {
		t.Errorf("final endpoint: got %+v, want (5,0)", last.P3)
	}
	// Sample each cubic's midpoint: must stay within the flattening
	// tolerance of the true circle (<=90° pieces are accurate to ~0.03%).
	prev := Point{5, 0}
	for _, s := range segs[1:] {
		mid := cubicPoint(prev, s.P1, s.P2, s.P3, 0.5)
		dist := math.Hypot(mid.X, mid.Y)
		if math.Abs(dist-r) > r*1e-3 {
			t.Errorf("cubic midpoint %+v deviates from circle: %g vs %g", mid, dist, r)
		}
		prev = s.P3
	}
}

func cubicPoint(p0, c1, c2, p3 Point, t float64) Point {
	u := 1 - t
	return Point{
		X: u*u*u*p0.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*p3.X,
		Y: u*u*u*p0.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*p3.Y,
	}
}

func TestParsePathDataZeroRadiusArcIsLine(t *testing.T) {
	segs, err := parsePathData("M0,0A0,0,0,0,1,10,10")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	if len(segs) != 2 || segs[1].Op != OpLineTo || !pointsEqual(segs[1].P3, Point{10, 10}) {
		t.Errorf("zero-radius arc: got %+v, want line to (10,10)", segs)
	}
}

func TestParsePathDataScientificNotation(t *testing.T) {
	segs, err := parsePathData("M1e2,2.5e-1L-1E1,3")
	if err != nil {
		t.Fatalf("parsePathData: %v", err)
	}
	if !pointsEqual(segs[0].P3, Point{100, 0.25}) || !pointsEqual(segs[1].P3, Point{-10, 3}) {
		t.Errorf("scientific notation: got %+v", segs)
	}
}

func TestParsePathDataErrors(t *testing.T) {
	for _, d := range []string{
		"X10,10",     // unknown command
		"M",          // missing coordinates
		"M0,0 L1",    // incomplete pair
		"M0,0 A5,5",  // incomplete arc
		"M0,0 12 34", // ok (implicit lineto)... see below
	} {
		_, err := parsePathData(d)
		if d == "M0,0 12 34" {
			if err != nil {
				t.Errorf("%q: unexpected error %v", d, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: expected error, got none", d)
		}
	}
}
