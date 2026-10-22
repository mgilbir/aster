package vega

import (
	"testing"

	"github.com/mgilbir/aster/internal/scene"
)

// nopContext is a PathContext that draws nothing.
type nopContext struct{}

func (nopContext) MoveTo(x, y float64)                        {}
func (nopContext) LineTo(x, y float64)                        {}
func (nopContext) QuadraticCurveTo(x1, y1, x, y float64)      {}
func (nopContext) BezierCurveTo(x1, y1, x2, y2, x, y float64) {}
func (nopContext) Arc(x, y, r, a0, a1 float64, ccw bool)      {}
func (nopContext) Rect(x, y, w, h float64)                    {}
func (nopContext) ClosePath()                                 {}

// A geoshape's item keeps the path data its bounds made for the SVG writer
// only while it holds the datum the data was made from: an item given another
// datum after its bounds were drawn gets the path of the datum it holds, and
// the kept data serves one request.
func TestGeoshapeTextFollowsTheDatum(t *testing.T) {
	res := renderSpec(t, `{
	  "width": 200, "height": 200,
	  "projections": [{"name": "p", "type": "mercator", "scale": 400, "translate": [100, 100]}],
	  "data": [{"name": "shapes", "values": {"type": "FeatureCollection", "features": [
	    {"type": "Feature", "properties": {}, "geometry": {"type": "Polygon", "coordinates": [[[0, 0], [10, 0], [10, 10], [0, 10], [0, 0]]]}},
	    {"type": "Feature", "properties": {}, "geometry": {"type": "Polygon", "coordinates": [[[-20, -5], [-5, -5], [-12, 8], [-20, -5]]]}}]}, "format": {"type": "json", "property": "features"}}],
	  "marks": [{"type": "shape", "from": {"data": "shapes"}, "transform": [{"type": "geoshape", "projection": "p"}]}]}`)
	var shape *scene.Mark
	for _, m := range marks(res) {
		if m != nil && m.Type == scene.MarkShape {
			shape = m
		}
	}
	if shape == nil || len(shape.Items) != 2 {
		t.Fatal("no shape mark of two items")
	}
	a, b := shape.Items[0], shape.Items[1]
	fn := a.Shape.Func
	if shape.Shape != nil {
		fn = shape.Shape
	}
	// Both items were bounded: each holds the text of its own datum.
	textB := fn(nil, b)
	if textB == "" || textB == fn(nil, a) {
		t.Fatalf("the items' paths %q and %q are not two shapes", fn(nil, a), textB)
	}
	// Bound a again, then give it b's datum before its text is asked for.
	fn(nopContext{}, a)
	a.Datum = b.Datum
	if got := fn(nil, a); got != textB {
		t.Errorf("an item given another datum after its bounds wrote %q, want that datum's %q", got, textB)
	}
	// A kept text serves one request; the next is made from the datum.
	fn(nopContext{}, b)
	if got := fn(nil, b); got != textB {
		t.Errorf("kept text %q, want %q", got, textB)
	}
	if got := fn(nil, b); got != textB {
		t.Errorf("text made again %q, want %q", got, textB)
	}
}
