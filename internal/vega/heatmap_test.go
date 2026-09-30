package vega

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/svg"
)

// heatmapSpec paints one 4x3 grid through the heatmap transform into an image
// mark's canvas.
const heatmapSpec = `{
  "width": 40, "height": 30,
  "data": [{
    "name": "grids",
    "values": [{"grid": {"width": 4, "height": 3, "values": [0,1,2,3, 4,5,6,7, 8,9,10,11]}}],
    "transform": [{"type": "heatmap", "field": "grid", "color": "#ff0000", "opacity": 1}]
  }],
  "marks": [{
    "type": "image", "from": {"data": "grids"},
    "encode": {"update": {"image": {"field": "image"}, "width": {"signal": "width"}, "height": {"signal": "height"}, "aspect": {"value": false}}}
  }]
}`

func TestHeatmapImageMark(t *testing.T) {
	res, err := renderJSON(t, context.Background(), heatmapSpec)
	if err != nil {
		t.Fatal(err)
	}
	out, err := svg.Render(context.Background(), res.Scenegraph, svg.Options{Width: res.Width, Height: res.Height})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `xlink:href="data:image/png;base64,`) {
		t.Fatalf("the canvas is not inlined as a PNG data URL:\n%s", out)
	}
}

// The grid size and region come from the data: the canvas is charged to the
// render's budget before it is allocated.
func TestHeatmapCanvasBudget(t *testing.T) {
	v, err := jsval.ParseJSONString(heatmapSpec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Render(context.Background(), v, Options{Loader: newTestLoader(), Limits: Limits{MaxCanvasBytes: 4*12 - 1}})
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("want a budget error, got %v", err)
	}
	if _, err = Render(context.Background(), v, Options{Loader: newTestLoader(), Limits: Limits{MaxCanvasBytes: 4 * 12}}); err != nil {
		t.Fatalf("a canvas of exactly the limit fits: %v", err)
	}
}

func TestHeatmapHostileGrid(t *testing.T) {
	for name, grid := range map[string]string{
		"huge grid":     `{"width": 1e9, "height": 1e9}`,
		"huge region":   `{"width": 2, "height": 2, "x2": 1e12, "y2": 1e12}`,
		"infinite":      `{"width": 2, "height": 2, "x1": -1e300, "x2": 1e300}`,
		"negative size": `{"width": -5, "height": 2}`,
	} {
		spec := `{"data":[{"name":"g","values":[{"grid":` + grid + `}],"transform":[{"type":"heatmap","field":"grid"}]}]}`
		// The dataflow logs the failure and goes on, as upstream does.
		res, err := renderJSON(t, context.Background(), spec)
		if err == nil && len(res.Warnings) == 0 {
			t.Errorf("%s: want the transform to fail", name)
		}
	}
}
