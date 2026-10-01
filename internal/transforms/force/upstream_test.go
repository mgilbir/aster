package force

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamVegaForce replays vega-force's own test (see internal/upstream). The recording is one
// call of the force operator: its parameters (static, so the iterations all run in the one pass, and
// the forces), the tuples of the pulse that went in and the ones that came out with the index,
// position and velocity the simulation gave them. d3-force draws its jiggle from a fixed linear
// congruential generator, which the engine reproduces, so the positions are replayed exactly.
func TestUpstreamVegaForce(t *testing.T) {
	r := upstream.Start(t, "vega-force")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Op != "force" {
			continue
		}
		in, _ := c.Input.(map[string]any)
		out, _ := c.Output.(map[string]any)
		params, _ := c.Params.(map[string]any)
		items, okIn := in["add"].([]any)
		want, okOut := out["add"].([]any)
		forces, okForces := params["forces"].([]any)
		if !okIn || !okOut || !okForces {
			r.Skip("force pulses of another shape")
			continue
		}
		p := DefaultParams()
		p.Static, _ = params["static"].(bool)
		known := true
		for _, f := range forces {
			spec, _ := f.(map[string]any)
			switch spec["force"] {
			case "x":
				x := NewX()
				if v, ok := spec["x"]; ok {
					x.X = Constant(upstream.Number(v))
				}
				p.Forces = append(p.Forces, x)
			case "y":
				y := NewY()
				if v, ok := spec["y"]; ok {
					y.Y = Constant(upstream.Number(v))
				}
				p.Forces = append(p.Forces, y)
			case "nbody":
				p.Forces = append(p.Forces, NewNBody())
			default:
				known = false
			}
		}
		if !known {
			r.Skip("forces the replay does not build")
			continue
		}
		nodes := make([]jsval.Value, len(items))
		for j, it := range items {
			nodes[j] = upstream.ToValue(it)
		}
		if err := Run(context.Background(), nodes, p); err != nil {
			r.CheckAgainst(c, want, nil, true)
			continue
		}
		got := make([]any, len(nodes))
		for j, n := range nodes {
			got[j] = upstream.FromValue(n)
		}
		r.CheckAgainst(c, want, got, false)
	}
	r.Done(1)
}
