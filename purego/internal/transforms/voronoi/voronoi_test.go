package voronoi

import (
	"context"
	"math"
	"math/rand"
	"os"
	"slices"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type goldenCase struct {
	Name      string       `json:"name"`
	Points    [][2]float64 `json:"points"`
	Size      []float64    `json:"size"`
	Extent    []float64    `json:"extent"`
	Paths     []*string    `json:"paths"`
	Triangles []int32      `json:"triangles"`
	Halfedges []int32      `json:"halfedges"`
	Hull      []int32      `json:"hull"`
	Collinear []int32      `json:"collinear"`
}

func loadGolden(t testing.TB) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/voronoi.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []goldenCase
	for _, c := range v.Get("cases").Items() {
		g := goldenCase{Name: c.Get("name").StrValue()}
		for _, p := range c.Get("points").Items() {
			g.Points = append(g.Points, [2]float64{p.Index(0).NumValue(), p.Index(1).NumValue()})
		}
		g.Size = nums(c.Get("size"))
		g.Extent = nums(c.Get("extent"))
		for _, p := range c.Get("paths").Items() {
			if p.IsStr() {
				s := p.StrValue()
				g.Paths = append(g.Paths, &s)
			} else {
				g.Paths = append(g.Paths, nil)
			}
		}
		g.Triangles = ints(c.Get("triangles"))
		g.Halfedges = ints(c.Get("halfedges"))
		g.Hull = ints(c.Get("hull"))
		g.Collinear = ints(c.Get("collinear"))
		out = append(out, g)
	}
	return out
}

func nums(v jsval.Value) []float64 {
	var out []float64
	for _, x := range v.Items() {
		out = append(out, x.NumValue())
	}
	return out
}

func ints(v jsval.Value) []int32 {
	var out []int32
	for _, x := range v.Items() {
		out = append(out, int32(x.NumValue()))
	}
	return out
}

func tuples(pts [][2]float64) []jsval.Value {
	data := make([]jsval.Value, len(pts))
	for i, p := range pts {
		data[i] = jsval.Obj(jsval.ObjectOf("x", jsval.Num(p[0]), "y", jsval.Num(p[1])))
	}
	return data
}

func field(name string) func(jsval.Value) jsval.Value {
	return func(v jsval.Value) jsval.Value { return v.Get(name) }
}

func TestGolden(t *testing.T) {
	cases := loadGolden(t)
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			data := tuples(c.Points)
			err := Transform(context.Background(), data, Params{X: field("x"), Y: field("y"), Size: c.Size, Extent: c.Extent})
			if err != nil {
				t.Fatal(err)
			}
			for i, d := range data {
				got := d.Get("path")
				want := c.Paths[i]
				switch {
				case want == nil && !got.IsNull():
					t.Errorf("point %d: got %v, want null", i, got)
				case want != nil && (!got.IsStr() || got.StrValue() != *want):
					t.Errorf("point %d:\n got  %v\n want %s", i, got, *want)
				}
			}
		})
	}
}

// TestTriangulation compares the raw Delaunator/Delaunay structures, which
// pins the orientation and in-circle decisions independently of clipping.
func TestTriangulation(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			coords := make([]float64, 0, 2*len(c.Points))
			for _, p := range c.Points {
				coords = append(coords, p[0], p[1])
			}
			d := newDelaunay(coords)
			if !slices.Equal(d.triangles, c.Triangles) {
				t.Errorf("triangles differ:\n got  %v\n want %v", d.triangles, c.Triangles)
			}
			if !slices.Equal(d.halfedges, c.Halfedges) {
				t.Errorf("halfedges differ")
			}
			if !slices.Equal(d.hull, c.Hull) {
				t.Errorf("hull: got %v want %v", d.hull, c.Hull)
			}
			if !slices.Equal(d.collinear, c.Collinear) {
				t.Errorf("collinear: got %v want %v", d.collinear, c.Collinear)
			}
		})
	}
}

func TestOrient2d(t *testing.T) {
	// A near-collinear triple whose naive determinant has the wrong sign.
	if got := orient2d(0.5, 0.5, 12, 12, 24, 24); got != 0 {
		t.Errorf("exactly collinear: got %v", got)
	}
	if got := orient2d(0.5, 0.5, 12, 12, 24, 24.000000000000004); got >= 0 {
		t.Errorf("above line: got %v", got)
	}
	if got := orient2d(0, 0, 1, 0, 0, 1); got >= 0 {
		t.Errorf("ccw (y up): got %v", got)
	}
	if got := orient2d(0, 0, 0, 1, 1, 0); got <= 0 {
		t.Errorf("cw (y up): got %v", got)
	}
}

func TestInvalidBounds(t *testing.T) {
	data := tuples([][2]float64{{1, 1}, {2, 3}, {5, 1}})
	for _, ext := range [][]float64{{10, 0, 0, 10}, {0, 10, 10, 0}, {math.NaN(), 0, 1, 1}} {
		err := Transform(context.Background(), data, Params{X: field("x"), Y: field("y"), Extent: ext})
		if err != ErrInvalidBounds {
			t.Errorf("extent %v: got %v", ext, err)
		}
	}
}

func TestEmptyAndNilAccessors(t *testing.T) {
	if err := Transform(context.Background(), nil, Params{}); err != nil {
		t.Fatal(err)
	}
	if err := Transform(context.Background(), tuples([][2]float64{{1, 1}}), Params{}); err == nil {
		t.Fatal("expected error for missing accessors")
	}
}

func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pts := make([][2]float64, 10)
	for i := range pts {
		pts[i] = [2]float64{float64(i * i % 7), float64(i)}
	}
	if err := Transform(ctx, tuples(pts), Params{X: field("x"), Y: field("y")}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestCustomOutputField(t *testing.T) {
	data := tuples([][2]float64{{10, 10}, {90, 90}, {10, 90}, {90, 10}})
	if err := Transform(context.Background(), data, Params{X: field("x"), Y: field("y"), Size: []float64{100, 100}, As: "cell"}); err != nil {
		t.Fatal(err)
	}
	if !data[0].Get("cell").IsStr() || data[0].Get("path").IsStr() {
		t.Fatalf("unexpected fields: %v", data[0])
	}
}

// TestHostileInputs feeds non-finite and extreme coordinates: the result may
// be garbage but the transform must return without hanging or panicking.
func TestHostileInputs(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	sets := [][][2]float64{
		{{nan, nan}, {1, 2}, {3, 4}, {5, 1}},
		{{inf, 0}, {1, 2}, {3, 4}, {5, 1}, {-inf, 3}},
		{{1e308, 1e308}, {-1e308, -1e308}, {0, 0}, {1e-320, 3}},
		{{nan, 1}, {nan, 2}, {nan, 3}},
		{{0, 0}, {0, 0}, {0, 0}, {0, 0}},
	}
	rnd := rand.New(rand.NewSource(1))
	for k := 0; k < 200; k++ {
		n := 3 + rnd.Intn(12)
		s := make([][2]float64, n)
		for i := range s {
			for j := range s[i] {
				switch rnd.Intn(8) {
				case 0:
					s[i][j] = nan
				case 1:
					s[i][j] = inf
				case 2:
					s[i][j] = float64(rnd.Intn(3))
				default:
					s[i][j] = rnd.Float64() * 100
				}
			}
		}
		sets = append(sets, s)
	}
	for i, s := range sets {
		if err := Transform(context.Background(), tuples(s), Params{X: field("x"), Y: field("y"), Size: []float64{100, 100}}); err != nil {
			t.Logf("set %d: %v", i, err)
		}
	}
}

func randomData(n int) []jsval.Value {
	rnd := rand.New(rand.NewSource(42))
	pts := make([][2]float64, n)
	for i := range pts {
		pts[i] = [2]float64{rnd.Float64() * 960, rnd.Float64() * 500}
	}
	return tuples(pts)
}

func BenchmarkTransform1k(b *testing.B) {
	data := randomData(1000)
	p := Params{X: field("x"), Y: field("y"), Size: []float64{960, 500}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Transform(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTriangulate1k(b *testing.B) {
	data := randomData(1000)
	coords := make([]float64, 0, 2000)
	for _, d := range data {
		coords = append(coords, d.Get("x").NumValue(), d.Get("y").NumValue())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newTriangulation(coords)
	}
}
