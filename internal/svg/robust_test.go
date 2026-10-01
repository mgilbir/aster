package svg

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func oddNum(r *rand.Rand) scene.Num {
	switch r.Intn(14) {
	case 0:
		return scene.Num{}
	case 1:
		return scene.N(math.NaN())
	case 2:
		return scene.N(math.Inf(1))
	case 3:
		return scene.N(math.Inf(-1))
	case 4:
		return scene.N(0)
	case 5:
		return scene.N(math.MaxFloat64)
	case 6:
		return scene.N(-1e-300)
	case 7:
		return scene.N(-r.Float64() * 100)
	default:
		return scene.N(r.Float64() * 200)
	}
}

func oddPaint(r *rand.Rand) scene.Paint {
	switch r.Intn(6) {
	case 0:
		return scene.Paint{}
	case 1:
		return scene.NullPaint()
	case 2:
		return scene.Color("transparent")
	case 3:
		return scene.GradientPaint(&scene.Gradient{Radial: r.Intn(2) == 0, Stops: []scene.GradientStop{{Offset: 0, Color: "red"}, {Offset: math.NaN(), Color: "\"<&"}}})
	}
	return scene.Color("#" + string(rune('a'+r.Intn(6))) + "00")
}

// TestRandomScenesNeverPanic throws nonsense values (NaN, infinities, negative
// sizes, junk strings) at every mark type: bounds and rendering must return
// normally, with an error where upstream would throw.
func TestRandomScenesNeverPanic(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	interps := []string{"", "linear", "basis", "cardinal", "catmull-rom", "monotone", "natural", "step", "bundle", "bogus", "basis-open", "cardinal-closed"}
	shapes := []string{"", "circle", "cross", "M0,0L1,1z", "not a path", "M0,0A1,1 0 1,1 2,2"}
	texts := []jsval.Value{jsval.Undefined, jsval.Null, jsval.Str(""), jsval.Str("hello world"), jsval.Num(math.NaN()), jsval.ArrOf(jsval.Str("a"), jsval.Null, jsval.Num(3)), jsval.Str("a\nb\n")}
	bd := scene.NewBounder(nil)
	types := []scene.MarkType{scene.MarkArc, scene.MarkArea, scene.MarkImage, scene.MarkLine, scene.MarkPath, scene.MarkRect, scene.MarkRule, scene.MarkShape, scene.MarkSymbol, scene.MarkText, scene.MarkTrail, scene.MarkGroup}
	for iter := 0; iter < 300; iter++ {
		sg := scene.New()
		var build func(parent *scene.Item, depth int)
		build = func(parent *scene.Item, depth int) {
			for _, typ := range types {
				if typ == scene.MarkGroup && depth > 2 {
					continue
				}
				m := sg.AddMark(scene.MarkDef{Type: typ, Role: "r", Zindex: float64(r.Intn(3) - 1), Clip: r.Intn(4) == 0}, parent, -1)
				for i, n := 0, r.Intn(6); i < n; i++ {
					it := m.AddItem()
					it.X, it.Y, it.X2, it.Y2 = oddNum(r), oddNum(r), oddNum(r), oddNum(r)
					it.Width, it.Height = oddNum(r), oddNum(r)
					it.Fill, it.Stroke = oddPaint(r), oddPaint(r)
					it.StrokeWidth, it.Opacity, it.StrokeOpacity = oddNum(r), oddNum(r), oddNum(r)
					it.StrokeMiterLimit, it.StrokeOffset = oddNum(r), oddNum(r)
					it.CornerRadius, it.CornerRadiusTopLeft = oddNum(r), oddNum(r)
					it.StartAngle, it.EndAngle, it.PadAngle, it.InnerRadius, it.OuterRadius = oddNum(r), oddNum(r), oddNum(r), oddNum(r), oddNum(r)
					it.Angle, it.Size, it.Tension, it.Radius, it.Theta = oddNum(r), oddNum(r), oddNum(r), oddNum(r), oddNum(r)
					it.FontSize, it.LineHeight, it.Limit, it.Dx, it.Dy = oddNum(r), oddNum(r), oddNum(r), oddNum(r), oddNum(r)
					it.ScaleX, it.ScaleY = oddNum(r), oddNum(r)
					it.Interpolate = interps[r.Intn(len(interps))]
					it.Orient = []string{"", "horizontal", "vertical", "x"}[r.Intn(4)]
					it.Shape.Name = shapes[r.Intn(len(shapes))]
					it.Text = texts[r.Intn(len(texts))]
					it.Align = []string{"", "left", "center", "right"}[r.Intn(4)]
					it.Baseline = []string{"", "top", "middle", "bottom", "line-top", "line-bottom"}[r.Intn(6)]
					it.Dir = []string{"", "rtl"}[r.Intn(2)]
					it.LineBreak = []string{"", "\n"}[r.Intn(2)]
					it.Defined = scene.Tri(r.Intn(3))
					it.StrokeForeground = scene.Tri(r.Intn(3))
					it.Clip = scene.Tri(r.Intn(3))
					it.Aria = scene.Tri(r.Intn(3))
					if r.Intn(3) == 0 {
						it.Path = scene.P([]string{"M0,0L10,10", "junk", "M0,0 a1,1 0 1,1 5,5 t1,1", "m1,1"}[r.Intn(4)])
					}
					if r.Intn(4) == 0 {
						it.Href = []string{"https://x", "javascript:x", "", "//h"}[r.Intn(4)]
					}
					if r.Intn(3) == 0 {
						it.StrokeDash = jsval.Arr([]jsval.Value{jsval.Num(1), jsval.Num(math.NaN())})
					}
					if typ == scene.MarkGroup {
						build(it, depth+1)
					}
				}
			}
		}
		build(sg.RootItem(), 0)
		_ = bd.BoundTree(sg.Root) // may fail on invalid paths or curves
		if _, err := Render(context.Background(), sg, Options{Width: 100, Height: 100}); err != nil {
			continue
		}
	}
}
