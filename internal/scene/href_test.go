package scene

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Only a string href can become a link: vega-loader's sanitizer starts with
// uri.replace(...), which rejects every other value.
func TestHrefMustBeAString(t *testing.T) {
	for v, want := range map[string]string{"x": "x", "": ""} {
		it := &Item{}
		if _, err := it.Set("href", jsval.Str(v)); err != nil || it.Href != want {
			t.Errorf("%q: %q %v", v, it.Href, err)
		}
	}
	for _, v := range []jsval.Value{jsval.Num(4), jsval.Bool(true), jsval.Null, jsval.ArrOf(jsval.Str("a"))} {
		it := &Item{Href: "stale"}
		if _, err := it.Set("href", v); err != nil || it.Href != "" {
			t.Errorf("%v: %q %v", v, it.Href, err)
		}
	}
}
