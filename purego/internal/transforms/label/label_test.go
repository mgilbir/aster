package label

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// The golden vectors were recorded from upstream vega-label with
// testdata/gen_label.mjs, which fixes text width at 0.6*fontSize per
// character and rasterizes marks with a stand-in canvas: even-odd fill at
// pixel centres, strokes as pixels within lineWidth/2 of a segment.

type gSub struct {
	Pts    [][2]float64
	Closed bool
}
type gShape struct {
	Fill, Stroke bool
	Lw           float64
	Subs         []gSub
}
type gShapes struct{ Normal, Outline []gShape }

type gPoint struct {
	X, Y   float64
	X2, Y2 *float64
}
type gBase struct {
	Marktype string
	X, Y     float64
	Bounds   struct{ X1, Y1, X2, Y2 float64 }
	Marks    []struct {
		Marktype string
		Points   []gPoint
	}
}
type gInput struct {
	Text     string
	FontSize float64
	X, Y     float64
	Sortv    float64
	Base     *gBase
}
type gOut struct {
	X, Y     *float64
	Opacity  float64
	Align    *string
	Baseline *string
}
type gCase struct {
	Name          string
	Size          [2]float64
	Anchor        []string
	Offset        []float64
	Padding       float64
	PaddingNull   bool
	LineAnchor    string
	MarkIndex     int
	AvoidBaseMark *bool
	Method        string
	Sorted        bool
	Inputs        []gInput
	BaseShapes    []*gShapes
	AvoidShapes   []gShapes
	Output        []gOut
}

// shapeRaster paints recorded shapes with the same two rules as the stand-in
// canvas used to record the vectors.
type shapeRaster struct{}

func (shapeRaster) Draw(m *Mask, items []any, outline bool) {
	for _, it := range items {
		gs, ok := it.(*gShapes)
		if !ok || gs == nil {
			continue
		}
		list := gs.Normal
		if outline {
			list = gs.Outline
		}
		for _, s := range list {
			paintShape(m, s)
		}
	}
}

func paintShape(m *Mask, s gShape) {
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if s.Fill {
				inside := false
				for _, sub := range s.Subs {
					p := sub.Pts
					for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
						xi, yi, xj, yj := p[i][0], p[i][1], p[j][0], p[j][1]
						if (yi > py) != (yj > py) && px < (xj-xi)*(py-yi)/(yj-yi)+xi {
							inside = !inside
						}
					}
				}
				if inside {
					m.Set(x, y)
				}
			}
			if s.Stroke {
				r := s.Lw / 2
				hit := false
				for _, sub := range s.Subs {
					p := sub.Pts
					n := len(p)
					cnt := n - 1
					if sub.Closed {
						cnt = n
					}
					for i := 0; i < cnt && !hit; i++ {
						a, b := p[i], p[(i+1)%n]
						dx, dy := b[0]-a[0], b[1]-a[1]
						l2 := dx*dx + dy*dy
						t := 0.0
						if l2 != 0 {
							t = ((px-a[0])*dx + (py-a[1])*dy) / l2
						}
						t = math.Max(0, math.Min(1, t))
						ex, ey := a[0]+t*dx-px, a[1]+t*dy-py
						if math.Sqrt(ex*ex+ey*ey) <= r {
							hit = true
						}
					}
				}
				if hit {
					m.Set(x, y)
				}
			}
		}
	}
}

func testWidth(l *Label) float64 { return 0.6 * l.FontSize * float64(len([]rune(l.Text))) }

func loadCases(t testing.TB) []gCase {
	t.Helper()
	b, err := os.ReadFile("testdata/label.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []gCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func buildBase(g *gBase, ref any) *Base {
	if g == nil {
		return nil
	}
	b := &Base{MarkType: g.Marktype, X: g.X, Y: g.Y, Bounds: Box{g.Bounds.X1, g.Bounds.Y1, g.Bounds.X2, g.Bounds.Y2}, Ref: ref}
	for _, m := range g.Marks {
		sm := SubMark{MarkType: m.Marktype}
		for _, p := range m.Points {
			pt := Point{X: p.X, Y: p.Y}
			if p.X2 != nil {
				pt.X2, pt.HasX2 = *p.X2, true
			}
			if p.Y2 != nil {
				pt.Y2, pt.HasY2 = *p.Y2, true
			}
			sm.Points = append(sm.Points, pt)
		}
		b.Marks = append(b.Marks, sm)
	}
	return b
}

func (c *gCase) build() ([]Label, Options) {
	labels := make([]Label, len(c.Inputs))
	sortv := make(map[*Label]float64)
	for i, in := range c.Inputs {
		labels[i] = Label{Text: in.Text, FontSize: in.FontSize, X: in.X, Y: in.Y}
		if in.Base != nil {
			labels[i].Base = buildBase(in.Base, c.BaseShapes[i])
		}
		sortv[&labels[i]] = in.Sortv
	}
	o := Options{
		Size: c.Size, Anchor: c.Anchor, Offset: c.Offset, Padding: c.Padding, UnboundedPadding: c.PaddingNull,
		LineAnchor: c.LineAnchor, MarkIndex: c.MarkIndex, Method: c.Method,
		NoAvoidBaseMark: c.AvoidBaseMark != nil && !*c.AvoidBaseMark,
		TextWidth:       testWidth, Rasterizer: shapeRaster{},
	}
	if c.Sorted {
		o.Sort = func(a, b *Label) float64 { return sortv[a] - sortv[b] }
	}
	for i := range c.AvoidShapes {
		o.AvoidMarks = append(o.AvoidMarks, []any{&c.AvoidShapes[i]})
	}
	return labels, o
}

func TestGolden(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			labels, o := c.build()
			ps, err := Layout(context.Background(), labels, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(ps) != len(labels) {
				t.Fatalf("got %d placements", len(ps))
			}
			for _, p := range ps {
				i := int(uintptr(0))
				for k := range labels {
					if p.Label == &labels[k] {
						i = k
					}
				}
				want := c.Output[i]
				if p.Opacity != want.Opacity {
					t.Errorf("label %d: opacity %v want %v", i, p.Opacity, want.Opacity)
				}
				if (want.X != nil) != p.HasPos {
					t.Errorf("label %d: HasPos %v want %v", i, p.HasPos, want.X != nil)
				} else if p.HasPos && (math.Abs(p.X-*want.X) > 1e-9 || math.Abs(p.Y-*want.Y) > 1e-9) {
					t.Errorf("label %d: pos (%v,%v) want (%v,%v)", i, p.X, p.Y, *want.X, *want.Y)
				}
				wa, wb := "", ""
				if want.Align != nil {
					wa, wb = *want.Align, *want.Baseline
				}
				if p.Align != wa || p.Baseline != wb {
					t.Errorf("label %d: align/baseline %q/%q want %q/%q", i, p.Align, p.Baseline, wa, wb)
				}
			}
		})
	}
}

func TestApply(t *testing.T) {
	obj := jsval.NewObject(0)
	labels := []Label{{Text: "a", FontSize: 10, X: 50, Y: 50, Tuple: jsval.Obj(obj)}}
	ps, err := Layout(context.Background(), labels, Options{Size: [2]float64{100, 100}, TextWidth: testWidth})
	if err != nil {
		t.Fatal(err)
	}
	Apply(ps, DefaultOutput)
	if obj.Lookup("opacity").NumValue() != 1 || !obj.Lookup("x").IsNum() || !obj.Lookup("align").IsStr() {
		t.Fatalf("unexpected tuple %v", jsval.Obj(obj))
	}
}

func TestBadInput(t *testing.T) {
	ctx := context.Background()
	l := []Label{{Text: "a", FontSize: 10}}
	for _, o := range []Options{
		{Size: [2]float64{math.NaN(), 10}},
		{Size: [2]float64{-1, 10}},
		{Size: [2]float64{math.Inf(1), 10}},
		{Size: [2]float64{1e6, 1e6}, Padding: 0, Method: "bogus"},
		{Size: [2]float64{100, 100}, Padding: 1e9},
		{Size: [2]float64{100, 100}, Padding: math.NaN()},
		{Size: [2]float64{100, 100}, Offset: []float64{}, Anchor: []string{}},
		{Size: [2]float64{100, 100}, Anchor: []string{"nowhere"}, Offset: []float64{math.NaN()}},
	} {
		if _, err := Layout(ctx, l, o); err != nil {
			t.Logf("err ok: %v", err)
		}
	}
	// Missing base data for a group must not panic.
	g := []Label{{Text: "a", FontSize: 10, Base: &Base{MarkType: "group"}}}
	for _, m := range []string{"naive", "reduced-search", "floodfill"} {
		if _, err := Layout(ctx, g, Options{Size: [2]float64{100, 100}, Method: m, MarkIndex: 3}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Layout(ctx, []Label{{Text: "a", FontSize: 10}}, Options{Size: [2]float64{100, 100}})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func BenchmarkLayoutPoints(b *testing.B) {
	labels := make([]Label, 1000)
	for i := range labels {
		labels[i] = Label{Text: "label", FontSize: 10, X: float64(i*37%800) + 10, Y: float64(i*91%500) + 10}
	}
	o := Options{Size: [2]float64{800, 500}, TextWidth: testWidth}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Layout(context.Background(), labels, o); err != nil {
			b.Fatal(err)
		}
	}
}
