package vega

import (
	"context"
	"strings"
	"testing"
)

// Errors an operator raises are logged by upstream's dataflow and the run goes
// on (the SVG renderer then often fails on the half-built scene, which the
// engine does not reproduce: it renders the rest). These specs pin the logged
// message, the part of the behavior the engine shares with upstream.
func TestOperatorErrorsAreLogged(t *testing.T) {
	cases := []struct{ name, spec, want string }{
		{
			// d3.range's length is `... | 0`: 5e20 bins wrap to a negative
			// count, which `new Array(n)` rejects.
			"scale bins count wraps negative",
			`{"width":100,"height":50,"scales":[{"name":"x","type":"linear","domain":[8,1e21],"range":"width","bins":{"step":2}}],"axes":[{"orient":"bottom","scale":"x"}]}`,
			"Invalid array length",
		},
		{
			// extent() applies the accessor to array[n] when no value is valid.
			"regression extent of a field with no values",
			`{"data":[{"name":"t","values":[{"group":"a"},{"group":"b"}],"transform":[{"type":"regression","x":"age","y":"group","method":"log","as":["age","group"]}]}]}`,
			"Cannot read properties of undefined (reading 'age')",
		},
		{
			"scale bins without a step",
			`{"width":100,"height":50,"scales":[{"name":"x","type":"linear","domain":[0,10],"range":"width","bins":{"step":0}}]}`,
			"Scale bins parameter missing step property.",
		},
	}
	for _, c := range cases {
		res, err := renderJSON(t, context.Background(), c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], c.want) {
			t.Errorf("%s: warnings %q, want %q", c.name, res.Warnings, c.want)
		}
	}
}
