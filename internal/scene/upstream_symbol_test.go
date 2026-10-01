package scene

import (
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamD3ShapeSymbol replays d3-shape's own symbol tests (see internal/upstream) through the
// engine's symbol mark. Vega's symbol shapes are its own functions, not d3's: d3 sizes a circle by
// sqrt(size / pi) where Vega uses sqrt(size) / 2, and its cross, diamond, triangle and the rest differ
// in the same way. The square is the one shape they draw alike, a rect of side sqrt(size) centred on
// the origin. The recording keeps a symbol type only as an object with an anonymous `draw`, so the
// type is read from the answer: the one d3 type that draws a rect, whose path moves by `h` and `v`,
// is symbolSquare. The tests pass the size as the datum.
func TestUpstreamD3ShapeSymbol(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "symbol()" {
			continue
		}
		want, isPath := c.Result.(string)
		if c.Method != "" || len(c.ViaSteps()) != 0 || len(c.Args) != 1 || !isPath || upstream.Contains(c.ConstructedWith, "function") {
			r.Skip("symbol questions of another shape (accessor reads, a context, nothing drawn)")
			continue
		}
		size := upstream.Number(c.Args[0])
		if math.IsNaN(size) || !strings.ContainsAny(want, "hv") {
			r.Skip("symbol types Vega draws differently (circle, cross, diamond, star, triangle, wye, asterisk, plus, times, square2, diamond2, triangle2)")
			continue
		}
		var sp StringPath
		sp.SetDigits(3)
		if err := Symbol(&sp, &Item{Shape: Shape{Name: "square"}, Size: N(size)}); err != nil {
			r.Check(c, nil, true)
			continue
		}
		r.Check(c, sp.String(), false)
	}
	r.Done(3)
}
