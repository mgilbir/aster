package vega

import (
	"context"
	"slices"
	"testing"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
)

// literalReference skips parsing the source of a reference, which upstream
// compiles. It may do so only where that parse cannot fail and registers
// nothing beyond what the reference's own signals register when entry
// parses them: so every reference it accepts has a source that compiles and
// depends on no signal, data set or field-less scale lookup that its
// signals, parsed on their own, do not, and every shape that could make the
// parse fail (a text offset or mult is a signal name, a scale given as an
// object, a field with an access path or one the parser reads as an
// identifier, a signal that parses only in the source's parentheses) is
// refused.
func TestLiteralReferenceNeedsNothingFromTheParse(t *testing.T) {
	c := &encCompiler{scope: newScope(jsval.Undefined, &parseOptions{ctx: context.Background()})}
	accepted := []string{
		`{"value":1}`, `{"value":"a b"}`, `{"value":null}`, `{"value":true}`,
		`{"field":"x"}`, `{"field":"x_1"}`, `{"scale":"s","field":"x"}`,
		`{"scale":"s","field":"x","offset":3,"mult":2,"round":true}`,
		`{"scale":"s","band":0.5}`, `{"scale":"s","value":"c","band":true}`,
		`{"value":1,"exponent":2}`, `{}`,
		`{"scale":"s","range":0.5}`, `{"scale":"s","range":1,"mult":0.5,"offset":-1}`,
		`{"value":0,"mult":0.5,"offset":{"value":2,"mult":3}}`,
		`{"field":{"group":"width"}}`, `{"field":{"parent":"k"}}`, `{"field":{"group":"height","level":2},"mult":0.5}`,
		`{"signal":"width"}`, `{"signal":"a ? b : c"}`, `{"signal":"datum.x + 1","mult":2,"offset":3}`,
		`{"signal":"x","offset":{"value":2,"offset":1}}`, `{"value":1,"mult":{"signal":"width / 2"}}`,
		`{"signal":"data('t')[0].v"}`, `{"signal":"scale('x', 3)"}`,
	}
	refused := []string{
		`{"value":1,"offset":"nosuch"}`, `{"value":1,"mult":"x"}`, `{"scale":{"signal":"s"},"field":"x"}`,
		`{"field":"a.b"}`, `{"field":"a[0]"}`, `{"field":""}`, `{"field":["a"]}`, `{"field":{"signal":"f"}}`,
		`{"value":[1]}`, `{"value":{"a":1}}`, `{"value":1,"field":"x"}`,
		`{"scale":"s","field":"x","extra":true}`, `{"scale":1,"field":"x"}`,
		`{"value":1,"band":"1"}`, `{"gradient":"linear"}`, `{"color":{"r":1}}`, `{"value":1,"round":1}`,
		`{"value":1,"test":"x"}`, `[]`, `"x"`, `3`,
		`{"field":"constructor"}`, `{"field":"if"}`, `{"field":{"group":"a.b"}}`, `{"field":{"group":"toString"}}`,
		`{"field":{"group":"w","parent":"k"}}`, `{"field":{"group":"w","extra":1}}`, `{"field":{"group":"w","level":"2"}}`,
		`{"signal":"a) || (b"}`, `{"signal":""}`, `{"signal":1}`, `{"signal":"x","value":1}`,
		`{"scale":"s","range":"1"}`, `{"value":1,"offset":{"value":2,"extra":1}}`, `{"value":1,"mult":{"signal":"a) + (b"}}`,
		`{"value":1,"offset":{"field":"a.b"}}`,
	}
	for _, s := range accepted {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if !c.literalReference(v) {
			t.Errorf("%s: refused", s)
			continue
		}
		prog, err := expr.Compile(entrySource(v).String())
		if err != nil {
			t.Errorf("%s: its source does not compile: %v", s, err)
			continue
		}
		// What the signals parsed on their own depend on.
		var own expr.Deps
		for _, sig := range signalsOf(v) {
			p, err := expr.Compile(sig)
			if err != nil {
				t.Fatalf("%s: signal %q: %v", s, sig, err)
			}
			d := p.Deps()
			own.Signals = append(own.Signals, d.Signals...)
			own.Data = append(own.Data, d.Data...)
			own.RequiredData = append(own.RequiredData, d.RequiredData...)
			own.Indexes = append(own.Indexes, d.Indexes...)
		}
		d := prog.Deps()
		for _, x := range d.Signals {
			if !slices.Contains(own.Signals, x) {
				t.Errorf("%s: its source reads signal %q, which its signals do not", s, x)
			}
		}
		for _, x := range append(d.Data, d.RequiredData...) {
			if !slices.Contains(own.Data, x) && !slices.Contains(own.RequiredData, x) {
				t.Errorf("%s: its source reads data %q, which its signals do not", s, x)
			}
		}
		if len(d.Indexes) > len(own.Indexes) {
			t.Errorf("%s: its source reads indexes %v beyond its signals' %v", s, d.Indexes, own.Indexes)
		}
	}
	for _, s := range refused {
		v, err := jsval.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if c.literalReference(v) {
			t.Errorf("%s: accepted", s)
		}
	}
}

// signalsOf lists the signal expressions of a reference and of the
// references nested in it.
func signalsOf(v jsval.Value) []string {
	if !v.IsObj() {
		return nil
	}
	var out []string
	o := v.ObjValue()
	for i := 0; i < o.Len(); i++ {
		if o.KeyAt(i) == "signal" && o.ValueAt(i).IsStr() {
			out = append(out, o.ValueAt(i).StrValue())
		}
		out = append(out, signalsOf(o.ValueAt(i))...)
	}
	return out
}
