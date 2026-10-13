package scene

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Upstream's line, area and trail generators take `item.defined === false`
// as the only break (vega-scenegraph path/shapes.js: def = item =>
// !(item.defined === false)); 0, "", NaN and "false" are defined. The same
// strict test holds for aspect, smooth and aria, and each property reads back
// as it was given.
func TestOnlyFalseIsFalse(t *testing.T) {
	vals := []jsval.Value{jsval.Bool(true), jsval.Num(0), jsval.Str(""), jsval.Num(math.NaN()), jsval.Null, jsval.Bool(false), jsval.Str("false"), jsval.Num(1)}
	var items []*Item
	for i, v := range vals {
		it := &Item{}
		for k, val := range map[string]jsval.Value{"x": jsval.Num(float64(10 * i)), "y": jsval.Num(float64(10 * (i % 2))), "defined": v} {
			if _, err := it.Set(k, val); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, it)
	}
	var sp StringPath
	d, ok, err := MultiPathData(&sp, MarkLine, items)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	// node: M0,0L10,10L20,0L30,10L40,0M60,0L70,10
	if want := "M0,0L10,10L20,0L30,10L40,0M60,0L70,10"; string(d) != want {
		t.Errorf("line %q, want %q", d, want)
	}
	for _, prop := range []string{"defined", "aspect", "smooth", "aria"} {
		for _, v := range vals {
			it := &Item{}
			if _, err := it.Set(prop, v); err != nil {
				t.Fatal(err)
			}
			got := it.Get(prop)
			isFalse := map[string]Tri{"defined": it.Defined, "aspect": it.Aspect(), "smooth": it.Smooth(), "aria": it.Aria}[prop].IsFalse()
			if want := v.IsBool() && !v.BoolValue(); isFalse != want {
				t.Errorf("%s=%v: false %v, want %v", prop, v, isFalse, want)
			}
			if !v.IsNull() && !jsval.Equal(got, v) {
				t.Errorf("%s=%v reads back as %v", prop, v, got)
			}
			// The Setter an encoder uses agrees with Set.
			it2 := &Item{}
			if err := SetterFor(prop)(it2, v); err != nil {
				t.Fatal(err)
			}
			if !jsval.Equal(it2.Get(prop), got) {
				t.Errorf("%s=%v: Setter reads back %v, Set %v", prop, v, it2.Get(prop), got)
			}
		}
	}
}

// vega's width estimate is ~~(0.8 * length * fontSize): ToInt32, which wraps
// a huge product modulo 2^32 where a Go conversion of it is
// implementation-defined. The expected values are node's.
func TestEstimateWidthIsToInt32(t *testing.T) {
	for _, tc := range []struct {
		text string
		size float64
		want float64
	}{
		{"a", 1.25e20, 1661992960},
		{"abc", 1e15, -1955201024},
		{"abc", -1e15, 1955201024},
		{"ab", 3e9, 505032704},
		{"ab", 1e300, 0},
		{"😀", 2e9, -1094967296},
		{"abc", 10, 24},
	} {
		if got := estimateWidth(tc.text, tc.size); got != tc.want {
			t.Errorf("estimateWidth(%q, %v) = %v, want %v", tc.text, tc.size, got, tc.want)
		}
	}
}
