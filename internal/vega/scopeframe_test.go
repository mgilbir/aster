package vega

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// A colour value reference evaluates its helper with the components as the
// datum, whatever the context's last evaluation left in the shared scope: in
// a group with a signal, that was an operator update, with no datum at all.
func TestColourReferenceInANestedGroup(t *testing.T) {
	const rect = `{"type":"rect","encode":{"enter":{"width":{"value":5},"height":{"value":5},
	  "fill":{"color":{"r":{"value":255},"g":{"value":0},"b":{"value":0}}}}}}`
	for name, spec := range map[string]string{
		"top level":         `{"marks":[` + rect + `]}`,
		"group with signal": `{"marks":[{"type":"group","signals":[{"name":"q","update":"2"}],"marks":[` + rect + `]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, msgs := mustRenderReporting(t, spec)
			if len(msgs) != 0 {
				t.Fatalf("reported %q", msgs)
			}
			m := marks(res)[0]
			if name != "top level" {
				m = m.Items[0].Items[0]
			}
			if got := m.Items[0].Get("fill").AsString(); got != "rgb(255, 0, 0)" {
				t.Errorf("fill %q, want rgb(255, 0, 0)", got)
			}
		})
	}
}

// The scope's evaluation frame (datum, item, event and which of them the
// expression has) is set in one place, whole, so no caller can set one
// variable and leave the flags of the last evaluation in place: the shared
// scope is reached only through rtContext.scope, which only evalProgram
// calls.
func TestScopeFrameIsSetInOnePlace(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, ".scope()") || (name != "context.go" && strings.Contains(line, "exprScope")) {
				calls = append(calls, fmt.Sprintf("%s:%d: %s", name, i+1, strings.TrimSpace(line)))
			}
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0], "context.go") {
		t.Errorf("the scope is reached from %d places, want one (evalProgram):\n%s", len(calls), strings.Join(calls, "\n"))
	}
}

// A boolean property set to null reads back as null, as upstream stores it
// (node draws "null/null"), not as undefined.
func TestBooleanPropertySetToNullReadsBackNull(t *testing.T) {
	res := renderSpec(t, `{"marks":[{"type":"text","encode":{"enter":{
	  "defined":{"value":null},"aria":{"value":null},
	  "text":{"signal":"(item.defined === null ? 'null' : isDefined(item.defined) ? 'other' : 'undef') + '/' + (item.aria === null ? 'null' : isDefined(item.aria) ? 'other' : 'undef')"}}}}]}`)
	if got := marks(res)[0].Items[0].Text().AsString(); got != "null/null" {
		t.Errorf("text %q, want null/null", got)
	}
}
