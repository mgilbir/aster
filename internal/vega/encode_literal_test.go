package vega

import (
	"testing"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
)

// literalReference skips parsing the source of a reference, which upstream
// compiles. It may do so only where that parse cannot fail and registers
// nothing: so every reference it accepts has a source that compiles and
// depends on no signal, data set or field-less scale lookup, and every shape
// that could make the parse fail (a text offset or mult is a signal name, a
// scale given as an object, a field with an access path) is refused.
func TestLiteralReferenceNeedsNothingFromTheParse(t *testing.T) {
	accepted := []string{
		`{"value":1}`, `{"value":"a b"}`, `{"value":null}`, `{"value":true}`,
		`{"field":"x"}`, `{"field":"x_1"}`, `{"scale":"s","field":"x"}`,
		`{"scale":"s","field":"x","offset":3,"mult":2,"round":true}`,
		`{"scale":"s","band":0.5}`, `{"scale":"s","value":"c","band":true}`,
		`{"value":1,"exponent":2}`, `{}`,
	}
	refused := []string{
		`{"value":1,"offset":"nosuch"}`, `{"value":1,"mult":"x"}`, `{"scale":{"signal":"s"},"field":"x"}`,
		`{"field":"a.b"}`, `{"field":"a[0]"}`, `{"field":""}`, `{"field":["a"]}`, `{"field":{"signal":"f"}}`,
		`{"value":[1]}`, `{"value":{"a":1}}`, `{"signal":"x"}`, `{"value":1,"field":"x"}`,
		`{"scale":"s","field":"x","extra":true}`, `{"scale":1,"field":"x"}`, `{"value":1,"offset":{"value":2}}`,
		`{"value":1,"band":"1"}`, `{"gradient":"linear"}`, `{"color":{"r":1}}`, `{"value":1,"round":1}`,
		`{"value":1,"test":"x"}`, `[]`, `"x"`, `3`,
	}
	for _, s := range accepted {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if !literalReference(v) {
			t.Errorf("%s: refused", s)
			continue
		}
		prog, err := expr.Compile(entrySource(v).String())
		if err != nil {
			t.Errorf("%s: its source does not compile: %v", s, err)
			continue
		}
		if d := prog.Deps(); len(d.Signals) != 0 || len(d.Data) != 0 || len(d.RequiredData) != 0 || len(d.Indexes) != 0 {
			t.Errorf("%s: its source depends on %+v", s, d)
		}
	}
	for _, s := range refused {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if literalReference(v) {
			t.Errorf("%s: accepted", s)
		}
	}
}
