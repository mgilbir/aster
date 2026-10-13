package vega

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// data() of a data set whose values operator has not run yet is null
// upstream (its Sieve starts with the value null), so length() of it is a
// TypeError: here a trigger of a data set reads one defined after it. Node
// logs "Cannot read properties of null (reading 'length')", the exception
// stops the run, and the text is never drawn.
func TestDataBeforeItsValuesRunIsNull(t *testing.T) {
	res, msgs := mustRenderReporting(t, `{"width":10,"height":10,
	  "data":[{"name":"a","values":[{"a":1}],"on":[{"trigger":"length(data('b'))","insert":"{a:2}"}]},
	    {"name":"b","values":[{"a":1}]}],
	  "marks":[{"type":"text","encode":{"update":{"text":{"signal":"length(data('a'))"}}}}]}`)
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "TypeError: Cannot read properties of null (reading 'length')") {
		t.Fatalf("reported %q, want the TypeError", msgs)
	}
	for _, m := range marks(res) {
		if m != nil && len(m.Items) != 0 {
			t.Errorf("%d text items, want none (the run stopped)", len(m.Items))
		}
	}
}

// A derived relay's copies are tuples of their own (derive ingests each with
// the next id), so a sort's ties follow the order they were copied in, not
// their sources' ids. Node gives 3, 2, 1, 0.
func TestDerivedCopiesHaveTheirOwnIDs(t *testing.T) {
	res := renderSpec(t, `{"width":10,"height":10,"data":[
	  {"name":"d1","transform":[{"type":"sequence","start":0,"stop":4,"as":"v"},{"type":"collect","sort":{"field":"v","order":"descending"}}]},
	  {"name":"d2","source":"d1","transform":[{"type":"formula","expr":"1","as":"k"},{"type":"collect","sort":{"field":"k"}}]}],
	  "marks":[{"type":"text","from":{"data":"d2"},"encode":{"enter":{"text":{"field":"v"}}}}]}`)
	var got []string
	for _, it := range marks(res)[0].Items {
		got = append(got, it.Text().AsString())
	}
	if s := strings.Join(got, ","); s != "3,2,1,0" {
		t.Errorf("order %s, want 3,2,1,0", s)
	}
}

// An expression's accessor has no name upstream (the runtime builds it
// without one and never reads the `as` the parser records), so every
// transform that names its output after an accessor writes the property
// "undefined", a measure is named after its op alone, and fold's key is
// undefined. testdata/expr-accessor-names.node.json is what node gives for
// each data set of testdata/expr-accessor-names.vg.json.
func TestExpressionAccessorsAreUnnamed(t *testing.T) {
	spec, err := os.ReadFile("testdata/expr-accessor-names.vg.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("testdata/expr-accessor-names.node.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	v := debugView(t, mustJSON(t, string(spec)))
	for name, w := range want {
		d, _ := v.root.Data(name)
		if got := string(jsval.AppendJSON(nil, d)); got != w {
			t.Errorf("%s:\n got %s\nwant %s", name, got, w)
		}
	}
	if len(want) < 29 {
		t.Fatalf("%d data sets recorded", len(want))
	}
}
