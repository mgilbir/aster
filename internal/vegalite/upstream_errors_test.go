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
	}
	for name, want := range cases {
		b, err := os.ReadFile(dir + name + ".vl.json")
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(spec, Options{})
		if err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", name, err, want)
		}
	}
}
