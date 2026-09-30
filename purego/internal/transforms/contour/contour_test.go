package contour

import (
	"context"
	"math"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func TestQuantize(t *testing.T) {
	vals := []float64{3, 1, math.NaN(), 11}
	got, err := Quantize(4, false, false)(vals)
	if err != nil {
		t.Fatal(err)
	}
	// extent [1, 11], step 10/5 = 2, levels 3, 5, 7, 9.
	want := []float64{3, 5, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// zero pulls the start down to 0; nice uses tick steps.
	got, _ = Quantize(5, true, true)([]float64{2, 98})
	if len(got) == 0 || got[0] != 20 {
		t.Fatalf("nice/zero levels %v", got)
	}
	if got, _ := Quantize(4, false, true)(nil); got != nil {
		t.Fatalf("empty input: %v", got)
	}
}

func TestContoursSquare(t *testing.T) {
	// A single high cell in a 3x3 grid gives one diamond-ish polygon.
	v := []float64{0, 0, 0, 0, 1, 0, 0, 0, 0}
	g, err := Contours(context.Background(), v, 3, 3, []float64{0.5}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || len(g[0].Coordinates) != 1 || len(g[0].Coordinates[0]) != 1 {
		t.Fatalf("unexpected geometry %v", g)
	}
	ring := g[0].Coordinates[0][0]
	if len(ring) != 5 || ring[0] != ring[4] {
		t.Fatalf("ring not closed: %v", ring)
	}
	if area(ring) <= 0 {
		t.Fatalf("exterior ring must have positive area: %v", ring)
	}
}

func TestContoursHole(t *testing.T) {
	// A ring of high cells around a low centre produces a polygon with a hole.
	const n = 7
	v := make([]float64, n*n)
	for y := 1; y < n-1; y++ {
		for x := 1; x < n-1; x++ {
			v[y*n+x] = 1
		}
	}
	v[3*n+3] = 0
	g, err := Contours(context.Background(), v, n, n, []float64{0.5}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(g[0].Coordinates) != 1 || len(g[0].Coordinates[0]) != 2 {
		t.Fatalf("want one polygon with one hole, got %v", g[0].Coordinates)
	}
}

func TestBadInput(t *testing.T) {
	ctx := context.Background()
	if _, err := Contours(ctx, nil, -1, 3, []float64{1}, true); err == nil {
		t.Error("negative size accepted")
	}
	if _, err := Contours(ctx, nil, 1<<20, 1<<20, []float64{1}, true); err == nil {
		t.Error("huge grid accepted")
	}
	if _, err := Contours(ctx, nil, 3, 3, make([]float64, MaxThresholds+1), true); err == nil {
		t.Error("too many thresholds accepted")
	}
	if _, err := Density(ctx, nil, DensityParams{Size: []float64{1e9, 1e9}}, true); err == nil {
		t.Error("huge density raster accepted")
	}
	if _, err := Density(ctx, nil, DensityParams{Size: []float64{-1, 5}}, true); err == nil {
		t.Error("negative density size accepted")
	}
	if _, err := Density(ctx, nil, DensityParams{CellSize: 0.5}, true); err == nil {
		t.Error("cell size < 1 accepted")
	}
	if _, err := Contour(ctx, nil, ContourParams{Size: []float64{5}}); err == nil {
		t.Error("short size accepted")
	}
	// Non-grid field values and NaN/garbage data must not panic.
	if _, err := Isocontour(ctx, []jsval.Value{jsval.Num(1)}, IsocontourParams{}); err == nil {
		t.Error("non-grid tuple accepted")
	}
	junk := []jsval.Value{jsval.Undefined, jsval.Null, jsval.Str("x"), jsval.ArrOf(jsval.Num(math.NaN()), jsval.Num(math.Inf(1)))}
	if _, err := Contour(ctx, junk, ContourParams{Size: []float64{50, 50}}); err != nil {
		t.Errorf("junk data: %v", err)
	}
	// Missing values contour as false, without panicking.
	if _, err := Contours(ctx, []float64{1}, 4, 4, []float64{0.5}, true); err != nil {
		t.Error(err)
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := make([]float64, 100*100)
	if _, err := Contours(ctx, v, 100, 100, []float64{0.5}, true); err == nil {
		t.Error("cancelled context ignored")
	}
	if _, err := Density(ctx, make([]jsval.Value, 10), DensityParams{}, true); err == nil {
		t.Error("cancelled context ignored by Density")
	}
}

func TestGridRoundTrip(t *testing.T) {
	g := Grid{Values: []float64{1, 2, 3, 4}, Scale: 4, Width: 2, Height: 2, X1: 0, Y1: 0, X2: 2, Y2: 2}
	back, err := GridFromValue(g.ToValue())
	if err != nil {
		t.Fatal(err)
	}
	if back.Width != 2 || back.Scale != 4 || len(back.Values) != 4 || back.Values[3] != 4 {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestTransformFlip(t *testing.T) {
	ring := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 0}}
	g := []Geometry{{Coordinates: [][][][2]float64{{ring}}}}
	transformGeometries(g, Grid{}, 1, -1, 0, 10)
	got := g[0].Coordinates[0][0]
	// Reversed and y-flipped, keeping the winding order.
	want := [][2]float64{{0, 10}, {1, 9}, {1, 10}, {0, 10}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestHeatmap(t *testing.T) {
	grid := Grid{Values: []float64{0, 1, 2, 4}, Width: 2, Height: 2}
	src := []jsval.Value{grid.ToValue()}
	imgs, err := Heatmap(context.Background(), src, HeatmapParams{})
	if err != nil {
		t.Fatal(err)
	}
	img := imgs[0]
	if img.Width != 2 || img.Height != 2 || len(img.Pix) != 16 {
		t.Fatalf("bad image %+v", img)
	}
	// Default: mid-grey with opacity value/max: 0, 63, 127, 255.
	wantA := []uint8{0, 63, 127, 255}
	for i, a := range wantA {
		if img.Pix[4*i] != 136 || img.Pix[4*i+3] != a {
			t.Errorf("pixel %d = %v want alpha %d", i, img.Pix[4*i:4*i+4], a)
		}
	}
	imgs, err = Heatmap(context.Background(), src, HeatmapParams{
		ColorFn: func(_ jsval.Value, p Pixel) (RGB, error) { return RGB{R: p.Value * 100, G: 0.5, B: p.X}, nil },
		Opacity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := imgs[0].Pix[12:16]; p[0] != 255 || p[1] != 0 || p[2] != 1 || p[3] != 255 {
		t.Errorf("color fn pixel = %v", p) // 0.5 rounds to even (0)
	}
}

func TestKDE2DGroupBy(t *testing.T) {
	mk := func(x, y float64, g string) jsval.Value {
		return jsval.Obj(jsval.ObjectOf("x", jsval.Num(x), "y", jsval.Num(y), "g", jsval.Str(g)))
	}
	src := []jsval.Value{mk(10, 10, "a"), mk(20, 20, "b"), mk(12, 11, "a")}
	out, err := KDE2D(context.Background(), src, KDE2DParams{
		DensityParams: DensityParams{X: field("x"), Y: field("y"), Size: []float64{40, 40}},
		GroupBy:       []GroupField{{Name: "g", Get: field("g")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Get("g").StrValue() != "a" || out[1].Get("g").StrValue() != "b" {
		t.Fatalf("groups wrong: %v", out)
	}
	if !out[0].Get("grid").IsObj() {
		t.Fatal("grid missing")
	}
	// No groupby and no data still yields one (empty) grid.
	out, err = KDE2D(context.Background(), nil, KDE2DParams{DensityParams: DensityParams{Size: []float64{40, 40}}})
	if err != nil || len(out) != 1 {
		t.Fatalf("empty input: %v %v", out, err)
	}
}

func gridValues(w, h int) []float64 {
	v := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)-float64(w)/2, float64(y)-float64(h)/2
			v[y*w+x] = math.Exp(-(dx*dx + dy*dy) / float64(w*h) * 20)
		}
	}
	return v
}

func BenchmarkContours(b *testing.B) {
	v := gridValues(300, 300)
	th := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Contours(context.Background(), v, 300, 300, th, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDensity(b *testing.B) {
	data := make([]jsval.Value, 5000)
	for i := range data {
		data[i] = jsval.Obj(jsval.ObjectOf("x", jsval.Num(float64(i*37%800)), "y", jsval.Num(float64(i*91%500))))
	}
	p := DensityParams{X: field("x"), Y: field("y"), Size: []float64{800, 500}, Bandwidth: []float64{20}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Density(context.Background(), data, p, false); err != nil {
			b.Fatal(err)
		}
	}
}
