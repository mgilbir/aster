package vega

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A render reads the specification it is given and never writes into it:
// format.parse converts copies of the inline values, not the caller's
// objects, so the same specification renders again (or on another
// goroutine) the same way.
func TestParseLeavesTheCallersValuesAlone(t *testing.T) {
	for name, spec := range map[string]string{
		"inline values": `{"data":[{"name":"d","values":[{"a":"1"}],"format":{"type":"json","parse":{"a":"number"}}}],
		  "marks":[{"type":"text","from":{"data":"d"},"encode":{"enter":{"text":{"signal":"isNumber(datum.a) ? 'number' : 'not a number'"}}}}]}`,
		"values from a signal": `{"signals":[{"name":"s","value":[{"a":"1"}]}],
		  "data":[{"name":"d","values":{"signal":"s"},"format":{"type":"json","parse":{"a":"number"}}}],
		  "marks":[{"type":"text","from":{"data":"d"},"encode":{"enter":{"text":{"signal":"isNumber(datum.a) ? 'number' : 'not a number'"}}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			v := mustJSON(t, spec)
			before := string(jsval.AppendJSON(nil, v))
			for range 2 {
				res, err := Render(context.Background(), v, Options{})
				if err != nil {
					t.Fatal(err)
				}
				if got := marks(res)[0].Items[0].Text().AsString(); got != "number" {
					t.Errorf("datum.a is a %s, want the parsed number", got)
				}
			}
			if after := string(jsval.AppendJSON(nil, v)); after != before {
				t.Errorf("the specification changed:\n%s\nto\n%s", before, after)
			}
		})
	}
}
