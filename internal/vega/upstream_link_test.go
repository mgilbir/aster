package vega

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamD3ShapeLink replays d3-shape's own link tests (see internal/upstream) through the
// engine's LinkPath shapes, where they are the same function: link(curveLinear) is the `line` shape,
// and linkHorizontal, linkVertical and linkRadial (d3's bump curves) are the `diagonal` shape in the
// horizontal, vertical and radial orientations, the bezier whose control points sit halfway between
// the two positions along the axis (along the radius, for the radial one). Vega writes the path as
// text and d3 through d3-path, which rounds to three digits and separates a bezier's points by
// commas, so the adapter parses Vega's path and writes it again through the engine's path. d3's
// radial link takes its angle from the top (pointRadial subtracts a quarter turn first), Vega's from
// the x axis, so the adapter subtracts the quarter turn too. The recording holds the tests' source,
// target, x and y accessors as anonymous functions, so only the default ones are replayed: a source
// and a target that are [x, y] pairs. Other curves are not in LinkPath.
func TestUpstreamD3ShapeLink(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	js := linkJS{func(v jsval.Value) string { return v.AsString() }}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "link()" && c.Fn != "linkHorizontal()" && c.Fn != "linkVertical()" && c.Fn != "linkRadial()" {
			continue
		}
		if c.Method != "" || len(c.Chain) != 0 || len(c.ViaSteps()) != 0 || len(c.Args) != 1 || c.Result == nil {
			r.Skip("link questions of another shape (accessor reads and writes, a context, extra arguments)")
			continue
		}
		var curve string // the curve factory: curveLinear, curveBumpX, ...
		switch c.Fn {
		case "linkHorizontal()":
			curve = "curveBumpX"
		case "linkVertical()":
			curve = "curveBumpY"
		case "linkRadial()":
			curve = "curveBumpRadial"
		default:
			if len(c.ConstructedWith) == 1 {
				origin, _ := upstream.IsFunction(c.ConstructedWith[0])
				curve, _ = origin["export"].(string)
			}
		}
		shape := map[string]string{
			"curveLinear":     "line",
			"curveBumpX":      "diagonal-horizontal",
			"curveBumpY":      "diagonal-vertical",
			"curveBumpRadial": "diagonal-radial",
		}[curve]
		if shape == "" {
			r.Skip("link curves LinkPath does not have")
			continue
		}
		datum, _ := c.Args[0].(map[string]any)
		src, _ := datum["source"].([]any)
		dst, _ := datum["target"].([]any)
		if len(src) != 2 || len(dst) != 2 {
			r.Skip("links whose ends are not [x, y] pairs")
			continue
		}
		pts := [4]float64{upstream.Number(src[0]), upstream.Number(src[1]), upstream.Number(dst[0]), upstream.Number(dst[1])}
		if shape == "diagonal-radial" {
			pts[0] -= math.Pi / 2
			pts[2] -= math.Pi / 2
		}
		cmds, err := scene.ParsePath(linkPaths[shape](js, jsval.Num(pts[0]), jsval.Num(pts[1]), jsval.Num(pts[2]), jsval.Num(pts[3])))
		if err != nil {
			t.Fatalf("%s: %v", c.Signature(), err)
		}
		var sp scene.StringPath
		sp.SetDigits(3)
		if err := scene.RenderPath(&sp, cmds, 0, 0, 1, 1); err != nil {
			t.Fatalf("%s: %v", c.Signature(), err)
		}
		r.Check(c, sp.String(), false)
	}
	r.Done(6)
}
