package vegalite

import (
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamVegaEventSelector replays vega-event-selector's own tests (see internal/upstream)
// against the engine's event selector parser.
func TestUpstreamVegaEventSelector(t *testing.T) {
	r := upstream.Start(t, "vega-event-selector")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "parseSelector" {
			continue
		}
		selector, ok := c.Arg(0).(string)
		if !ok {
			r.Skip("a selector that is not a string")
			continue
		}
		source := ""
		if s, ok := c.Arg(1).(string); ok {
			source = s
		}
		out, threw := func() (out []any, threw bool) {
			defer func() {
				if recover() != nil {
					threw = true
				}
			}()
			for _, v := range parseSelector(selector, source) {
				out = append(out, upstream.FromValue(v))
			}
			return out, false
		}()
		r.Check(c, out, threw)
	}
	r.Done(15)
}
