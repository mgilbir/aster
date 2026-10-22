package vega

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A field reference nested in another (field.js's resolveField of a nested
// reference, `object[inner]`) and a scale named by a field reference
// (entry.js's scaleRef, `field(scale)`) compile to the encoder's closures,
// which read the parent group's datum directly. Compiled from their source
// instead, they read `item.mark.group.datum`, which makes every evaluation
// build the item, its mark and the group chain as values: the gallery's
// brushing scatter plots took five times as long to render. A nested
// reference of a shape the closures do not compile as the source reads it
// is still compiled from the source.
func TestNestedReferencesAreCanonical(t *testing.T) {
	accepted := []string{
		`{"field":{"datum":{"parent":"x.data"}}}`,
		`{"field":{"parent":{"datum":"data"}}}`,
		`{"field":{"group":{"datum":"k"},"level":2}}`,
		`{"field":{"datum":{"signal":"f"}}}`,
		`{"field":{"datum":{"datum":{"parent":"a"}}}}`,
		`{"scale":{"parent":"xscale"},"field":{"datum":{"parent":"x.data"}}}`,
		`{"scale":{"datum":"data"},"field":{"parent":{"datum":"data"}}}`,
		`{"scale":{"group":"s"},"value":1}`,
	}
	refused := []string{
		`{"field":{"datum":{}}}`,
		`{"field":{"datum":{"parent":1}}}`,
		`{"field":{"datum":{"parent":""}}}`,
		`{"field":{"datum":[{"parent":"a"}]}}`,
		`{"field":{"parent":{"datum":"a"},"level":"2"}}`,
		`{"field":{"datum":{"signal":1}}}`,
		`{"scale":{"parent":1},"value":1}`,
		`{"scale":{"datum":{}},"value":1}`,
		`{"scale":{},"value":1}`,
		`{"scale":["s"],"value":1}`,
	}
	for _, s := range accepted {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if !canonicalEntry(v, 0) {
			t.Errorf("%s: refused", s)
		}
	}
	for _, s := range refused {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if canonicalEntry(v, 0) {
			t.Errorf("%s: accepted", s)
		}
	}
}
