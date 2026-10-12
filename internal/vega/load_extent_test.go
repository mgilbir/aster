package vega

import (
	"fmt"
	"os"
	"reflect"
	"testing"
)

// The Load transform reads values only when they are truthy (`if
// (_.values)`); a falsy value loads the url, here none. Node draws 0 rows and
// logs "Loading failed" for each.
func TestLoadValuesAreTestedForTruthiness(t *testing.T) {
	for _, v := range []string{`""`, `0`, `null`, `false`} {
		spec := fmt.Sprintf(`{"signals":[{"name":"s","value":%s}],"data":[{"name":"d","values":{"signal":"s"}}],
		  "marks":[{"type":"text","encode":{"update":{"text":{"signal":"length(data('d'))"}}}}]}`, v)
		res, msgs, err := renderReporting(t, spec)
		if err != nil {
			t.Errorf("values %s: %v", v, err)
			continue
		}
		got := ""
		if m := marks(res); len(m) > 0 && len(m[0].Items) > 0 {
			got = m[0].Items[0].Text().AsString()
		}
		if got != "0" || len(msgs) != 1 || msgs[0] != "Loading failed: no url" {
			t.Errorf("values %s: %s rows, reported %q; want 0 and the failed load", v, got, msgs)
		}
	}
}

// Extent's warning is upstream's, bounds and all (node's text for each).
func TestInfiniteExtentWarning(t *testing.T) {
	b, err := os.ReadFile("testdata/extent-warnings.vg.json")
	if err != nil {
		t.Fatal(err)
	}
	_, msgs, err := renderReporting(t, string(b))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`Infinite extent for field "v": [Infinity, -Infinity]`,
		`Infinite extent for field "v": [1, Infinity]`,
		`Infinite extentundefined: [-Infinity, -Infinity]`,
		`Infinite extent for field "a.b": [Infinity, -Infinity]`,
	}
	if !reflect.DeepEqual(msgs, want) {
		t.Errorf("warnings\n%q\nwant\n%q", msgs, want)
	}
}
