package vega

import (
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
		_, msgs, err := renderReporting(t, c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(msgs) == 0 || !strings.Contains(msgs[0], c.want) {
			t.Errorf("%s: warnings %q, want %q", c.name, msgs, c.want)
		}
	}
}
