package layout

import (
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// group builds a group item holding the given marks.
func group(marks ...*scene.Mark) *scene.Item {
	g := &scene.Item{Items: marks, Width: scene.N(100), Height: scene.N(50)}
	g.Bounds = scene.NewBounds()
	for _, m := range marks {
		m.Group = g
	}
	return g
}

func mark(role string, t scene.MarkType, items ...*scene.Item) *scene.Mark {
	m := &scene.Mark{Role: role, Type: t, Bounds: scene.NewBounds(), Items: items}
	for _, it := range items {
		it.Mark = m
		if it.Bounds == (scene.Bounds{}) {
			it.Bounds = scene.NewBounds()
		}
	}
	return m
}

// Malformed scenes (guide groups missing their children, absent properties,
// hostile grid options) must never panic.
func TestLayoutMalformedScenes(t *testing.T) {
	datum := func(kv ...any) jsval.Value { return jsval.Obj(jsval.ObjectOf(kv...)) }
	yes := jsval.True

	axis := &scene.Item{Orient: "left", Datum: datum("grid", yes, "ticks", yes, "labels", yes, "domain", yes, "title", yes)}
	legend := &scene.Item{Orient: "right", Datum: datum("title", yes, "type", jsval.Str("symbol"))}
	title := &scene.Item{Orient: "top"}
	empty := mark("", scene.MarkRect)
	g := group(
		mark("axis", scene.MarkGroup, axis),
		mark("axis", scene.MarkGroup), // no items at all
		mark("legend", scene.MarkGroup, legend),
		mark("title", scene.MarkGroup, title),
		mark("row-header", scene.MarkGroup, &scene.Item{}),
		empty,
	)
	root := mark("frame", scene.MarkGroup, g)
	auto := ParseAutosize(jsval.Str("fit"))
	view := &View{Width: 100, Height: 100, AutosizeActive: true}

	specs := []jsval.Value{
		jsval.Undefined,
		jsval.Obj(jsval.ObjectOf("columns", jsval.Int(-3))),
		jsval.Obj(jsval.ObjectOf("columns", jsval.Num(2.5))),
		jsval.Obj(jsval.ObjectOf("columns", jsval.Num(1e12), "align", jsval.Str("each"), "center", jsval.True)),
		jsval.Obj(jsval.ObjectOf("columns", jsval.Int(0), "align", jsval.Str("all"), "padding", jsval.Str("junk"))),
	}
	for _, sp := range specs {
		p := Params{Autosize: &auto}
		if sp.IsObj() {
			p.Grid = ParseGridSpec(sp)
		}
		_ = ViewLayout(root, view, p)
	}
	if ViewLayout(nil, view, Params{}) != nil || ViewLayout(root, nil, Params{}) != nil {
		t.Fatal("nil inputs should produce no adjustment")
	}

	// a grid with headers but no cells, and more headers than cells
	cell := &scene.Item{Width: scene.N(10), Height: scene.N(10)}
	hdrs := []*scene.Item{{}, {}, {}, {}}
	g2 := group(mark("scope", scene.MarkGroup, cell), mark("column-header", scene.MarkGroup, hdrs...), mark("row-title", scene.MarkGroup), mark("column-title", scene.MarkGroup))
	warned := 0
	ViewLayout(mark("frame", scene.MarkGroup, g2), &View{Warn: func(string) { warned++ }},
		Params{Grid: ParseGridSpec(jsval.Obj(jsval.ObjectOf("columns", jsval.Int(1))))})
	if warned == 0 {
		t.Error("expected a header limit warning")
	}
	ViewLayout(mark("frame", scene.MarkGroup, group(mark("column-header", scene.MarkGroup, &scene.Item{}))), view,
		Params{Grid: ParseGridSpec(jsval.Obj(jsval.NewObject(0)))})
}

func TestParseOverlap(t *testing.T) {
	spec := jsval.Obj(jsval.ObjectOf(
		"method", jsval.Str("greedy"), "separation", jsval.Int(3), "order", jsval.Str("datum.index"),
		"bound", jsval.Obj(jsval.ObjectOf("scale", jsval.Str("x"), "orient", jsval.Str("bottom"), "tolerance", jsval.True))))
	p := ParseOverlap(spec, func(n string) ([2]float64, bool) { return [2]float64{0, 200}, n == "x" })
	if p.Separation != 3 || p.Order != "datum.index" || p.Bound == nil || p.Bound.Tolerance != 1 || p.Bound.Range[1] != 200 {
		t.Fatalf("%+v %+v", p, p.Bound)
	}
	if ParseOverlap(spec, func(string) ([2]float64, bool) { return [2]float64{}, false }).Bound != nil {
		t.Fatal("bound on unknown scale kept")
	}
}
