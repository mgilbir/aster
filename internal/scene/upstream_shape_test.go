package scene

import (
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// d3Curves maps d3-shape's curve factories to Vega's interpolate and orient names (vega-scenegraph
// curves.js), with the setter that carries the factory's one parameter.
var d3Curves = map[string]struct{ interpolate, orient, setter string }{
	"curveBasis":            {"basis", "", ""},
	"curveBasisClosed":      {"basis-closed", "", ""},
	"curveBasisOpen":        {"basis-open", "", ""},
	"curveBundle":           {"bundle", "", "beta"},
	"curveCardinal":         {"cardinal", "", "tension"},
	"curveCardinalClosed":   {"cardinal-closed", "", "tension"},
	"curveCardinalOpen":     {"cardinal-open", "", "tension"},
	"curveCatmullRom":       {"catmull-rom", "", "alpha"},
	"curveCatmullRomClosed": {"catmull-rom-closed", "", "alpha"},
	"curveCatmullRomOpen":   {"catmull-rom-open", "", "alpha"},
	"curveLinear":           {"linear", "", ""},
	"curveLinearClosed":     {"linear-closed", "", ""},
	"curveMonotoneX":        {"monotone", "vertical", ""},
	"curveMonotoneY":        {"monotone", "horizontal", ""},
	"curveNatural":          {"natural", "", ""},
	"curveStep":             {"step", "", ""},
	"curveStepAfter":        {"step-after", "", ""},
	"curveStepBefore":       {"step-before", "", ""},
}

// curveFromOrigin reads a recorded curve factory: an export (`curveBasis`) or one with its parameter
// set (`curveCardinal.tension(0.5)`).
func curveFromOrigin(v any) (interpolate, orient string, param Num, ok bool) {
	origin, isFn := upstream.IsFunction(v)
	if !isFn || origin == nil {
		return "", "", Num{}, false
	}
	if name, isExport := origin["export"].(string); isExport {
		c, found := d3Curves[name]
		return c.interpolate, c.orient, Num{}, found
	}
	from, _ := origin["from"].(string)
	name, setter, hasSetter := strings.Cut(from, ".")
	c, found := d3Curves[name]
	if !found || !hasSetter || setter != c.setter {
		return "", "", Num{}, false
	}
	args, _ := origin["args"].([]any)
	if len(args) != 1 {
		return "", "", Num{}, false
	}
	return c.interpolate, c.orient, N(upstream.Number(args[0])), true
}

// TestUpstreamD3ShapeLine replays d3-shape's own line tests (see internal/upstream) for the curves
// Vega has, through the engine's line mark: a line over constant points, one curve at a time.
func TestUpstreamD3ShapeLine(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "line()" {
			continue
		}
		if c.Method != "" || len(c.ViaSteps()) != 0 || len(c.Args) != 1 {
			r.Skip("line questions of another shape")
			continue
		}
		points, ok := c.Args[0].([]any)
		if !ok {
			r.Skip("input that is not an array of points")
			continue
		}
		interpolate, orient, param := "linear", "", Num{}
		digits := 3
		configured := true
		for _, step := range c.ChainSteps() {
			switch {
			case step.Method == "curve" && len(step.Args) == 1:
				var ok bool
				interpolate, orient, param, ok = curveFromOrigin(step.Args[0])
				configured = configured && ok
			case step.Method == "digits" && len(step.Args) == 1:
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
			r.Skip("line configuration the engine's line mark does not have (curves it lacks, accessors)")
			continue
		}
		items := make([]*Item, 0, len(points))
		finite := true
		for _, p := range points {
			xy, isPair := p.([]any)
			if !isPair || len(xy) < 2 {
				finite = false
				break
			}
			x, y := upstream.Number(xy[0]), upstream.Number(xy[1])
			if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
				finite = false
				break
			}
			items = append(items, &Item{X: N(x), Y: N(y), line: &lineAttrs{Interpolate: interpolate, Orient: orient, Tension: param}})
		}
		if !finite {
			r.Skip("points that are not finite (Vega reads NaN as 0, d3 draws it)")
			continue
		}
		var sp StringPath
		sp.SetDigits(digits)
		if err := Line(&sp, items); err != nil {
			r.Check(c, nil, true)
			continue
		}
		if sp.Len() == 0 {
			r.Check(c, nil, false) // d3's null
			continue
		}
		r.Check(c, sp.String(), false)
	}
	r.Done(130)
}
