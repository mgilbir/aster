package vega

import (
	"context"
	"strings"
	"testing"
)

// Each expression is compiled into a function with the parameters of its
// context; a free variable it lacks is a ReferenceError when read.
func TestExpressionContextVariables(t *testing.T) {
	cases := []struct{ name, spec, want string }{
		{
			"datum in a signal update",
			`{"signals":[{"name":"s","update":"datum"}]}`,
			"ReferenceError: datum is not defined",
		},
		{
			"event in a transform parameter",
			`{"data":[{"name":"t","values":[{"a":1}],"transform":[{"type":"filter","expr":"event"}]}]}`,
			"ReferenceError: event is not defined",
		},
		{
			"event in an encoder",
			`{"data":[{"name":"t","values":[{"a":1}]}],"marks":[{"type":"rect","from":{"data":"t"},"encode":{"update":{"width":{"signal":"event"}}}}]}`,
			"ReferenceError: event is not defined",
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
