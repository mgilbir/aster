package vegalite

import (
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Each regression spec makes upstream throw a TypeError where the engine used
// to carry on (or dereference nil: pan and zoom handlers, and the
// bound-scales push in a concat, look their signal up by name). The compiler
// returns the TypeError as an error with upstream's message, never as an
// internal error.
func TestUpstreamTypeErrorsAreErrors(t *testing.T) {
	const dir = "../../testdata/corpus/regress/"
	cases := map[string]string{
		"pan-zoom-on-channel-without-signal":               "Cannot read properties of undefined (reading 'on')",
		"bound-scales-in-concat-on-channel-without-signal": "Cannot set properties of undefined (setting 'push')",
		"facet-window-without-groupby":                     "Cannot read properties of undefined (reading 'concat')",
		"facet-joinaggregate-without-groupby":              "Cannot read properties of undefined (reading 'concat')",
		"path-mark-without-encoding":                       "Cannot read properties of undefined (reading 'shape')",
		"secondary-channel-without-main":                   "Cannot read properties of undefined (reading 'type')",
		"boxplot-of-fieldless-aggregate":                   "Cannot read properties of undefined (reading 'length')",
		"facet-field-null":                                 "Cannot read properties of null (reading 'length')",
		// A scale component read unchecked (mustScale), for a channel whose
		// scale is null.
		"offset-step-over-position-without-scale":    "Cannot read properties of undefined (reading 'get')",
		"offset-without-scale-under-step-for-offset": "Cannot read properties of undefined (reading 'get')",
		// What upstream raises for a malformed spec, mark or condition.
		"concat-of-a-number":      `Invalid specification 5. Make sure the specification includes at least one of the following properties: "mark", "layer", "facet", "hconcat", "vconcat", "concat", or "repeat".`,
		"mark-that-names-no-mark": "Cannot read properties of undefined (reading 'filled')",
		"mark-named-constructor":  "markCompiler[mark].encodeEntry is not a function",
		"layer-of-null":           "Cannot destructure property 'transform' of 'spec' as it is null.",
		"condition-list-of-null":  "Cannot destructure property 'selection' of 'c' as it is null.",
		"mark-null":               "Cannot read properties of undefined (reading 'filled')",
	}
	// 5.8 only: 6.4 checks the position scale before it reads it.
	cases58 := map[string]string{
		"offset-range-over-position-without-scale": "Cannot read properties of undefined (reading 'get')",
		// 5.8's spec, facet and op predicates use the in operator, and
		// its isMarkDef reads mark['type'].
		"concat-of-a-number":              "Cannot use 'in' operator to search for 'facet' in 5",
		"facet-of-a-string-under-5.8":     "Cannot use 'in' operator to search for 'row' in abc",
		"window-op-of-a-string-under-5.8": "Cannot use 'in' operator to search for 'op' in abc",
		"mark-null":                       "Cannot read properties of null (reading 'type')",
	}
	for _, c := range []struct {
		version string
		cases   map[string]string
	}{{"", cases}, {"5.8", cases58}} {
		for name, want := range c.cases {
			b, err := os.ReadFile(dir + name + ".vl.json")
			if err != nil {
				t.Fatal(err)
			}
			spec, err := jsval.ParseJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(spec, Options{Version: c.version})
			if err == nil || err.Error() != want {
				t.Errorf("%s %s: error %v, want %q", c.version, name, err, want)
			}
		}
	}
}
