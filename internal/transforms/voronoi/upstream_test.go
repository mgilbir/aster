package voronoi

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamVegaVoronoi replays vega-voronoi's own tests (see internal/upstream). The recording is
// one call of the voronoi operator per vector: its parameters (the x and y accessors, the clip size),
// the tuples of the pulse that went in and the ones that came out with their cell path.
func TestUpstreamVegaVoronoi(t *testing.T) {
	r := upstream.Start(t, "vega-voronoi")
	field := func(v any) (func(jsval.Value) jsval.Value, bool) {
		m, ok := v.(map[string]any)
		if !ok || m["$"] != "accessor" {
			return nil, false
		}
		fields, _ := m["fields"].([]any)
		if len(fields) != 1 {
			return nil, false
		}
		name, _ := fields[0].(string)
		return func(d jsval.Value) jsval.Value { return d.Get(name) }, true
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Op != "voronoi" {
			continue
		}
		in, _ := c.Input.(map[string]any)
		out, _ := c.Output.(map[string]any)
		params, _ := c.Params.(map[string]any)
		items, okIn := in["add"].([]any)
		want, okOut := out["add"].([]any)
		x, okX := field(params["x"])
		y, okY := field(params["y"])
		if !okIn || !okOut || !okX || !okY {
			r.Skip("voronoi pulses of another shape")
			continue
		}
		p := Params{X: x, Y: y}
		if size, ok := params["size"].([]any); ok {
			for _, s := range size {
				p.Size = append(p.Size, upstream.Number(s))
			}
		}
		if ext, ok := params["extent"].([]any); ok {
			for _, e := range ext {
				p.Extent = append(p.Extent, upstream.Number(e))
			}
		}
		if as, ok := params["as"].(string); ok {
			p.As = as
		}
		data := make([]jsval.Value, len(items))
		for j, it := range items {
			data[j] = upstream.ToValue(it)
		}
		if err := Transform(context.Background(), data, p); err != nil {
			r.CheckAgainst(c, want, nil, true)
			continue
		}
		got := make([]any, len(data))
		for j, d := range data {
			got[j] = upstream.FromValue(d)
		}
		r.CheckAgainst(c, want, got, false)
	}
	r.Done(3)
}
