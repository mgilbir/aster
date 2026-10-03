package scene

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamD3ShapeArc replays d3-shape's own arc tests (see internal/upstream) through the
// engine's arc mark: the sector, ring or circle drawn for a datum's radii and angles. The engine's
// arc mark reads `item.innerRadius || 0` and its kin where d3 reads the accessor as it is, so the
// vectors that leave a radius or an angle undefined (NaN in d3) are not replayed; nor are those that
// set a pad radius, which Vega does not.
func TestUpstreamD3ShapeArc(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "arc()" {
			continue
		}
		if c.Method != "" || len(c.ViaSteps()) != 0 || len(c.Args) > 1 {
			r.Skip("arc questions of another shape (centroid)")
			continue
		}
		values := map[string]any{}
		if len(c.Args) == 1 {
			if datum, ok := c.Args[0].(map[string]any); ok {
				for k, v := range datum {
					values[k] = v
				}
			}
		}
		configured := true
		digits := 3
		for _, step := range c.ChainSteps() {
			if len(step.Args) != 1 || upstream.Contains(step.Args, "function") {
				configured = false
				break
			}
			switch step.Method {
			case "innerRadius", "outerRadius", "cornerRadius", "startAngle", "endAngle", "padAngle":
				values[step.Method] = step.Args[0]
			case "digits":
				if step.Args[0] == nil {
					digits = -1
				} else {
					digits = int(upstream.Number(step.Args[0]))
				}
			default:
				configured = false
			}
		}
		if !configured {
			r.Skip("arc configuration the engine's arc mark does not have (functions, padRadius)")
			continue
		}
		it := &Item{}
		complete := true
		for _, f := range []struct {
			key      string
			dest     *Num
			optional bool
		}{
			{"innerRadius", &it.geomW().InnerRadius, false}, {"outerRadius", &it.geomW().OuterRadius, false},
			{"startAngle", &it.geomW().StartAngle, false}, {"endAngle", &it.geomW().EndAngle, false},
			{"padAngle", &it.geomW().PadAngle, true}, {"cornerRadius", &it.geomW().CornerRadius, true},
		} {
			raw, present := values[f.key]
			if !present {
				if !f.optional {
					complete = false
					break
				}
				continue
			}
			v := upstream.Number(raw)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				complete = false
				break
			}
			*f.dest = N(v)
		}
		if !complete {
			r.Skip("arcs with an undefined or non-finite radius or angle (d3 reads NaN, Vega 0)")
			continue
		}
		var sp StringPath
		sp.SetDigits(digits)
		if err := Arc(&sp, it); err != nil {
			r.Check(c, nil, true)
			continue
		}
		r.Check(c, sp.String(), false)
	}
	r.Done(80)
}
