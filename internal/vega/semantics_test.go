package vega

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func renderSpec(t *testing.T, spec string, writes ...SignalWrite) *Result {
	t.Helper()
	res, err := Render(context.Background(), mustJSON(t, spec), Options{SignalWrites: writes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// marks returns the child marks of the view's group, by position.
func marks(res *Result) []*scene.Mark { return res.Scenegraph.RootItem().Items }

// A sort breaks ties by tuple id, the order the tuples were ingested in, not by
// where the tuples sit (vega-dataflow's stableCompare). Here the first collect
// reverses the order of the tuples; the second sorts on a constant, so only the
// tie-break orders them, and it restores the order they were created in.
func TestCollectBreaksTiesByTupleID(t *testing.T) {
	res := renderSpec(t, `{
	  "data": [
	    {"name": "d1", "transform": [
	      {"type": "sequence", "start": 0, "stop": 4, "as": "v"},
	      {"type": "formula", "expr": "1", "as": "k"},
	      {"type": "collect", "sort": {"field": "v", "order": "descending"}}]},
	    {"name": "d2", "source": "d1", "transform": [{"type": "collect", "sort": {"field": "k"}}]}],
	  "marks": [{"type": "text", "from": {"data": "d2"}, "encode": {"enter": {"text": {"field": "v"}}}}]}`)
	var got []float64
	for _, it := range marks(res)[0].Items {
		got = append(got, it.Text.NumValue())
	}
	if want := []float64{0, 1, 2, 3}; !equalFloats(got, want) {
		t.Errorf("order after the tie-breaking sort = %v, want %v", got, want)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// quantizeInterpolator builds new Array(count): a bin-ordinal scale over an
// empty domain has count -1, which is a RangeError. The error ends that
// operator's evaluation, so the scale (and what depends on it) is never built;
// the run goes on and reports it.
func TestSchemeRangeErrorOnNegativeCount(t *testing.T) {
	res := renderSpec(t, `{
	  "data": [{"name": "t", "values": []}],
	  "scales": [{"name": "c", "type": "bin-ordinal", "domain": {"data": "t", "field": "x"}, "range": {"scheme": "viridis"}}],
	  "marks": [{"type": "rect", "from": {"data": "t"}, "encode": {"enter": {"fill": {"scale": "c", "field": "x"}}}}]}`)
	found := false
	for _, w := range res.Warnings {
		found = found || strings.Contains(w, "Invalid array length")
	}
	if !found {
		t.Errorf("warnings %q do not report the RangeError", res.Warnings)
	}
}

// A scale that interpolates through colors throws where d3.piecewise does when
// the scale maps a value to NaN (an unbounded domain, no data): the mark that
// asked for the color is not encoded, as upstream's.
func TestInterpolatingScaleThrowsOnNaNPosition(t *testing.T) {
	res := renderSpec(t, `{
	  "data": [{"name": "t", "values": [{"x": 1}]}, {"name": "empty", "values": []}],
	  "scales": [{"name": "c", "type": "linear", "domain": {"data": "empty", "field": "x"}, "range": ["red", "yellow", "green"]}],
	  "marks": [{"type": "rect", "from": {"data": "t"}, "encode": {"enter": {"fill": {"scale": "c", "field": "x"}}}}]}`)
	found := false
	for _, w := range res.Warnings {
		found = found || strings.Contains(w, "I[i] is not a function")
	}
	if !found {
		t.Errorf("warnings %q do not report the TypeError", res.Warnings)
	}
}

// A force simulation places its nodes (phyllotaxis, for a node without a
// position) before it initializes the forces, and a link force that cannot
// find a node throws then: the operator fails, but the nodes keep the places
// they were given, because the simulation works on the items themselves.
func TestForceFailureLeavesPlacedNodes(t *testing.T) {
	res := renderSpec(t, `{
	  "data": [{"name": "nodes", "values": [{"a": 1}, {"a": 2}]}, {"name": "links", "values": [{"b": 1}]}],
	  "marks": [{"type": "symbol", "from": {"data": "nodes"},
	    "transform": [{"type": "force", "forces": [{"force": "link", "links": "links"}]}]}]}`)
	items := marks(res)[0].Items
	if len(items) != 2 {
		t.Fatalf("%d items", len(items))
	}
	if x, y := items[0].X.Val(), items[0].Y.Val(); math.Abs(x-10*math.Sqrt(0.5)) > 1e-12 || y != 0 {
		t.Errorf("first node at (%v, %v), want the phyllotaxis position (%v, 0)", x, y, 10*math.Sqrt(0.5))
	}
	if items[1].X.Val() == 0 && items[1].Y.Val() == 0 {
		t.Error("second node was not placed")
	}
}

const forceSpec = `{
  "signals": [{"name": "cx", "value": 100}],
  "data": [{"name": "nodes", "values": [{"a": 1}, {"a": 2}, {"a": 3}]}],
  "marks": [{"type": "symbol", "from": {"data": "nodes"},
    "transform": [{"type": "force", "iterations": 100, "forces": [
      {"force": "center", "x": {"signal": "cx"}, "y": 100}, {"force": "collide", "radius": 5}]}]}]}`

// vega-force's simulation outlives its first pass: a later pass (here, a
// signal write that rebuilds the forces) reconfigures it and restarts it on the
// wall-clock timer, whose ticks a static render never sees. Only the first
// pass ticks, so the nodes are where the first tick left them.
func TestForceSecondPassDoesNotTick(t *testing.T) {
	positions := func(res *Result) [][2]float64 {
		var out [][2]float64
		for _, it := range marks(res)[0].Items {
			out = append(out, [2]float64{it.X.Val(), it.Y.Val()})
		}
		return out
	}
	first := positions(renderSpec(t, forceSpec))
	again := positions(renderSpec(t, forceSpec, SignalWrite{Name: "cx", Value: jsval.Num(250)}))
	if len(first) != 3 || len(again) != 3 {
		t.Fatalf("%d and %d nodes", len(first), len(again))
	}
	for i := range first {
		if first[i] != again[i] {
			t.Errorf("node %d moved on the second pass: %v then %v", i, first[i], again[i])
		}
	}
}

// A geoshape transform that gets a new projection (here refit to the size the
// layout settles on) reflows its items: they are encoded and bounded again, so
// the mark's bounds are those of the shapes it draws now, not of the first
// pass's.
func TestGeoshapeBoundsFollowProjectionRefit(t *testing.T) {
	res := renderSpec(t, `{
	  "autosize": "fit", "padding": 15,
	  "signals": [{"name": "width", "value": 400}, {"name": "height", "value": 400}],
	  "title": {"text": "A title", "subtitle": "and a subtitle", "anchor": "start", "offset": 20},
	  "projections": [{"name": "p", "size": {"signal": "[width, height]"}, "fit": {"signal": "data('shapes')"}}],
	  "data": [{"name": "shapes", "values": {"type": "FeatureCollection", "features": [
	    {"type": "Feature", "properties": {}, "geometry": {"type": "Polygon", "coordinates": [[[0, 0], [30, 0], [30, 10], [0, 10], [0, 0]]]}}]}}],
	  "marks": [{"type": "shape", "from": {"data": "shapes"}, "transform": [{"type": "geoshape", "projection": "p"}]}]}`)
	var shape *scene.Mark
	for _, m := range marks(res) {
		if m != nil && m.Type == scene.MarkShape {
			shape = m
		}
	}
	if shape == nil || len(shape.Items) != 1 {
		t.Fatal("no shape mark")
	}
	if got := shape.Items[0].Bounds; got != shape.Bounds {
		t.Errorf("the mark's bounds %v are not its item's %v", shape.Bounds, got)
	}
	// The projection is refit to the view the layout settles on (smaller than
	// the 400 x 400 it started with, to make room for the title); a stale
	// bound would still be that of the first fit. The stroke adds a few pixels.
	root := res.Scenegraph.RootItem()
	if b := shape.Bounds; b.X1 < -3 || b.Y1 < -3 || b.X2 > root.Width.Val()+3 || b.Y2 > root.Height.Val()+3 {
		t.Errorf("bounds %v are not those of a shape fit to the %v x %v view", b, root.Width.Val(), root.Height.Val())
	}
}
