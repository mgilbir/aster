package contour

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// upstreamAssignHoles is vega-geo's loop, kept to check assignHoles against.
func upstreamAssignHoles(polygons [][][][2]float64, holes [][][2]float64) {
	for _, hole := range holes {
		for i := range polygons {
			if contains(polygons[i][0], hole) != -1 {
				polygons[i] = append(polygons[i], hole)
				break
			}
		}
	}
}

// randomRing draws a ring from a small lattice of coordinates (so points
// fall on vertices and edges, and edges repeat and have zero length), with
// a few NaN, infinite and off-lattice values.
func randomRing(r *rand.Rand, n int) [][2]float64 {
	coord := func() float64 {
		switch r.Intn(40) {
		case 0:
			return math.NaN()
		case 1:
			return math.Inf(1 - 2*r.Intn(2))
		case 2:
			return r.Float64() * 8
		case 3:
			return 1e-200 * float64(r.Intn(3))
		}
		return float64(r.Intn(17)) / 2
	}
	ring := make([][2]float64, n)
	for i := range ring {
		ring[i] = [2]float64{coord(), coord()}
		if i > 0 && r.Intn(5) == 0 {
			ring[i] = ring[i-1] // a zero-length edge
		}
	}
	return ring
}

// TestRingIndexMatchesUpstream checks the indexed point-in-ring test against
// upstream's scan, point by point, over rings long enough to be indexed.
func TestRingIndexMatchesUpstream(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for k := 0; k < 3000; k++ {
		ring := randomRing(r, minIndexedRing+r.Intn(200))
		ri := newRingIndex(ring)
		for q := 0; q < 50; q++ {
			p := randomRing(r, 1)[0]
			if q%3 == 0 {
				p = ring[r.Intn(len(ring))] // on a vertex
			}
			want := ringContains(ring, p)
			if got, _ := ri.ringContains(p); got != want {
				t.Fatalf("ring %v point %v: got %d, want %d", ring, p, got, want)
			}
		}
	}
}

// TestAssignHolesMatchesUpstream checks whole assignments, holes and
// polygons included, against upstream's loop.
func TestAssignHolesMatchesUpstream(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	m := budget.NewMeter(context.Background())
	for k := 0; k < 500; k++ {
		var polys, want [][][][2]float64
		for i := 0; i < 1+r.Intn(5); i++ {
			ring := randomRing(r, 3+r.Intn(3*minIndexedRing))
			polys = append(polys, [][][2]float64{ring})
			want = append(want, [][][2]float64{ring})
		}
		var holes [][][2]float64
		for i := 0; i < r.Intn(30); i++ {
			holes = append(holes, randomRing(r, 1+r.Intn(6)))
		}
		upstreamAssignHoles(want, holes)
		if err := assignHoles(&m, polys, holes); err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if len(polys[i]) != len(want[i]) {
				t.Fatalf("case %d polygon %d: %d rings, want %d", k, i, len(polys[i]), len(want[i]))
			}
			for j := range want[i] {
				if len(polys[i][j]) != len(want[i][j]) || &polys[i][j][0] != &want[i][j][0] {
					t.Fatalf("case %d polygon %d ring %d differs", k, i, j)
				}
			}
		}
	}
}

// isolatedZeros is a size x size grid of ones with a zero at every cell of
// odd row and column: size²/4 holes in one ring.
func isolatedZeros(size int) []float64 {
	v := make([]float64, size*size)
	for i := range v {
		if (i%size)%2 == 0 || (i/size)%2 == 0 {
			v[i] = 1
		}
	}
	return v
}

// TestContourHolesScale is the audit's C27: a grid of ones with isolated
// zeros is one exterior ring around a hole per zero, which upstream's scan
// assigns in O(holes x ring), cubic in the grid's side (8 s for 1024², and
// success after a 1 s deadline). The work charged to the ledger, which is
// deterministic, must grow with the cells: doubling the side quadruples the
// cells, and a scan of the ring per hole would multiply the work by eight.
func TestContourHolesScale(t *testing.T) {
	work := func(size int) int64 {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		ctx := budget.WithLedger(context.Background(), l)
		geoms, err := Contours(ctx, isolatedZeros(size), size, size, []float64{0.5}, true)
		if err != nil {
			t.Fatal(err)
		}
		holes := 0
		for _, p := range geoms[0].Coordinates {
			holes += len(p) - 1
		}
		if want := (size / 2) * (size / 2); holes < want*9/10 {
			t.Fatalf("%d holes, want about %d", holes, want)
		}
		return l.WorkDone()
	}
	a, b := work(256), work(512)
	if r := float64(b) / float64(a); r > 5 {
		t.Fatalf("work %d at 256², %d at 512²: ratio %.1f, want about 4 (linear in the cells)", a, b, r)
	}
}

// TestAssignHolesCancellable: the polygons a hole is tested against before
// one contains it are still upstream's (all of them, in order), so a grid of
// many separate exteriors and holes stays quadratic; the context ends it.
func TestAssignHolesCancellable(t *testing.T) {
	var polys [][][][2]float64
	var holes [][][2]float64
	for i := 0; i < 2000; i++ {
		x := float64(2 * i)
		polys = append(polys, [][][2]float64{{{x, 0}, {x + 1, 0}, {x + 1, 1}, {x, 1}}})
		holes = append(holes, [][2]float64{{x + 0.5, 5}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := budget.NewMeter(ctx)
	if err := assignHoles(&m, polys, holes); err != context.Canceled {
		t.Fatalf("got %v, want the context's error", err)
	}
}

// withLedger is a context carrying a ledger of max bytes.
func withLedger(max int64) (context.Context, *budget.Ledger) {
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: max})
	return budget.WithLedger(context.Background(), l), l
}

// TestContourRingsCharged is the audit's C27 memory half: a checkerboard
// makes a ring per cell at every level, and nothing charged them. The
// ledger's counters, not the heap, say what was charged.
func TestContourRingsCharged(t *testing.T) {
	const size = 256
	values := make([]float64, size*size)
	for i := range values {
		values[i] = float64((i%size + i/size) % 2)
	}
	thresholds := make([]float64, 40)
	for i := range thresholds {
		thresholds[i] = 0.5
	}
	const max = 64 << 20
	ctx, l := withLedger(max)
	_, err := Contour(ctx, nil, ContourParams{Values: values, Size: []float64{size, size}, Thresholds: thresholds})
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("got %v, want ErrLimit", err)
	}
	if l.PeakBytes() > max {
		t.Fatalf("peak %d over the limit", l.PeakBytes())
	}
	// One level fits, and what it charged for its rings is given back.
	ctx, l = withLedger(max)
	out, err := Contour(ctx, nil, ContourParams{Values: values, Size: []float64{size, size}, Thresholds: thresholds[:1]})
	if err != nil || len(out) != 1 {
		t.Fatal(err)
	}
	if l.Bytes() == 0 || l.Bytes() >= l.PeakBytes() {
		t.Fatalf("charged %d bytes, peak %d: the output is charged and the rings and scratch given back", l.Bytes(), l.PeakBytes())
	}
}

// TestKDE2DGroupsCharged is the audit's C24: every group's density grid
// (two float32 rasters, the values and their jsval form) was allocated with
// no budget seeing it, 512 MB a group at the largest size.
func TestKDE2DGroupsCharged(t *testing.T) {
	var data []jsval.Value
	for i := 0; i < 400; i++ {
		data = append(data, jsval.Obj(jsval.ObjectOf("g", jsval.Int(i%4), "x", jsval.Int(i), "y", jsval.Int(i*2))))
	}
	get := func(k string) Accessor { return func(v jsval.Value) jsval.Value { return v.Get(k) } }
	p := KDE2DParams{
		DensityParams: DensityParams{X: get("x"), Y: get("y"), Size: []float64{1024, 1024}, CellSize: 1, Bandwidth: []float64{10}},
		GroupBy:       []GroupField{{Name: "g", Get: get("g")}},
	}
	const max = 48 << 20
	ctx, l := withLedger(max)
	if _, err := KDE2D(ctx, data, p); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("got %v, want ErrLimit", err)
	}
	if l.PeakBytes() > max {
		t.Fatalf("peak %d over the limit", l.PeakBytes())
	}
	ctx, _ = withLedger(max)
	p.GroupBy = nil
	if _, err := KDE2D(ctx, data, p); err != nil {
		t.Fatalf("one group: %v", err)
	}
}

// TestInfiniteSideIsTooLarge: 0 x Infinity is NaN, which passed the area
// check, and the infinite side was converted to an int (implementation
// defined: MinInt64 on amd64, MaxInt64 on arm64).
func TestInfiniteSideIsTooLarge(t *testing.T) {
	ctx := context.Background()
	if _, err := Contour(ctx, nil, ContourParams{Values: []float64{}, Size: []float64{0, math.Inf(1)}}); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("contour: %v", err)
	}
	grid := jsval.Obj(jsval.ObjectOf("width", jsval.Num(0), "height", jsval.Num(math.Inf(1)), "values", jsval.ArrOf()))
	if _, err := GridFromValue(ctx, grid); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("grid: %v", err)
	}
}
