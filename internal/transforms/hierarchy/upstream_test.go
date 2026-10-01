package hierarchy

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamD3HierarchyPackSiblings replays d3-hierarchy's own tests of packSiblings (see
// internal/upstream): circles of given radii, placed tangent to each other around the origin.
func TestUpstreamD3HierarchyPackSiblings(t *testing.T) {
	r := upstream.Start(t, "d3-hierarchy")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "packSiblings" {
			continue
		}
		items, ok := c.Arg(0).([]any)
		if !ok {
			r.Skip("input that is not an array")
			continue
		}
		circles := make([]*Node, len(items))
		valid := true
		for j, it := range items {
			m, isMap := it.(map[string]any)
			if !isMap {
				valid = false
				break
			}
			circles[j] = &Node{R: upstream.Number(m["r"])}
			if _, has := m["x"]; has {
				circles[j].X = upstream.Number(m["x"])
				circles[j].Y = upstream.Number(m["y"])
			}
		}
		if !valid {
			r.Skip("circles of another shape")
			continue
		}
		if _, err := packSiblings(context.Background(), circles, newLCG()); err != nil {
			r.Check(c, nil, true)
			continue
		}
		// d3 returns the array it was given, each circle now with its position
		out := make([]any, len(circles))
		for j, n := range circles {
			out[j] = map[string]any{"r": upstream.Enc(n.R), "x": upstream.Enc(n.X), "y": upstream.Enc(n.Y)}
		}
		r.Check(c, out, false)
	}
	r.Done(4000)
}
