package expr

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of upstream's own tests (see internal/upstream) through the expression function table: the
// d3-ease functions Vega 6.4 exposes, and vega-functions' own.

// callBuiltin calls the expression function name as the generated code would. threw is true when it
// raised an error (a panic here, an exception upstream).
func callBuiltin(name string, args []jsval.Value) (res jsval.Value, threw, found bool) {
	def := lookupFunc(name)
	if def == nil || def.fn == nil {
		return jsval.Undefined, false, false
	}
	defer func() {
		if recover() != nil {
			threw = true
		}
	}()
	return def.fn(NewScope(nil), args), false, true
}

func TestUpstreamD3Ease(t *testing.T) {
	r := upstream.Start(t, "d3-ease")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || upstream.Contains(c.Args, "function") {
			continue
		}
		name := c.Fn
		if len(name) < 4 || name[:4] != "ease" || c.Method != "" || len(c.ConstructedWith) > 0 {
			// easePolyIn.exponent(2), easeBackIn.overshoot(...) and the like have no expression-language
			// equivalent: Vega exposes the families at their default parameters only.
			r.Skip("parameterised families and other than ease functions")
			continue
		}
		args := make([]jsval.Value, len(c.Args))
		for j, a := range c.Args {
			args[j] = upstream.ToValue(a)
		}
		res, threw, found := callBuiltin(name, args)
		if !found {
			r.Skip("unmapped " + name)
			continue
		}
		r.Check(c, upstream.FromValue(res), threw)
	}
	r.Done(300)
}

func TestUpstreamVegaFunctions(t *testing.T) {
	r := upstream.Start(t, "vega-functions")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			continue
		}
		if upstream.Contains(c.Args, "function", "regexp") || c.Method != "" || len(c.ConstructedWith) > 0 {
			r.Skip("arguments that are the test's own functions or patterns")
			continue
		}
		args := make([]jsval.Value, len(c.Args))
		for j, a := range c.Args {
			args[j] = upstream.ToValue(a)
		}
		if len(c.Fn) > 3 && c.Fn[:3] == "geo" {
			r.Skip("projection functions (they read the view's projections)")
			continue
		}
		res, threw, found := callBuiltin(c.Fn, args)
		if !found {
			r.Skip("functions that need the runtime (" + c.Fn + ")")
			continue
		}
		r.Check(c, upstream.FromValue(res), threw)
	}
	r.Done(30)
}
