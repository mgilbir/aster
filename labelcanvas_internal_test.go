package aster

import (
	"errors"
	"strings"
	"testing"
)

// The label transform's rasterization holds its pixels under the bound a PNG
// render has (WithMemoryLimit's), not the rasterizer's default.
func TestLabelCanvasBytesDerived(t *testing.T) {
	if l := deriveLimits(0, 0); l.vega.RasterCanvasBytes != 0 {
		t.Errorf("no memory limit: label canvas bytes %d, want the rasterizer's default", l.vega.RasterCanvasBytes)
	}
	for _, n := range []uint64{1 << 20, 4 << 20, 256 << 20, 8 << 30} {
		l := deriveLimits(n, 0)
		if l.vega.RasterCanvasBytes != l.raster.MaxCanvasBytes || l.vega.RasterCanvasBytes == 0 {
			t.Errorf("WithMemoryLimit(%d): label canvas bytes %d, PNG's %d", n, l.vega.RasterCanvasBytes, l.raster.MaxCanvasBytes)
		}
	}
}

// A label transform avoiding a mark drawn through a layer as large as the
// layout (a rectangle filled and stroked at half opacity) needs that layer's
// pixels, four samples a pixel each way: 64 MB for 1000 x 1000. A memory
// limit below it fails the call; without one it renders.
func TestLabelRasterBoundedByMemoryLimit(t *testing.T) {
	spec := []byte(`{
  "width": 1000, "height": 1000,
  "data": [{"name": "d", "values": [{"k": "a"}]}],
  "marks": [
    {"type": "rect", "name": "base", "from": {"data": "d"}, "encode": {"enter": {
      "x": {"value": 0}, "y": {"value": 0}, "width": {"value": 1000}, "height": {"value": 1000},
      "fill": {"value": "steelblue"}, "stroke": {"value": "black"}, "opacity": {"value": 0.5}}}},
    {"type": "text", "from": {"data": "base"}, "encode": {"enter": {"text": {"field": "datum.k"}}},
     "transform": [{"type": "label", "size": [1000, 1000]}]}
  ]
}`)
	c, err := New(WithMemoryLimit(32 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.VegaToSVG(spec)
	if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "pixel memory") {
		t.Fatalf("WithMemoryLimit(32 MiB): want the pixel memory bound, got %v", err)
	}
	c2, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.VegaToSVG(spec); err != nil {
		t.Fatalf("default limits: %v", err)
	}
}
