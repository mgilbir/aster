package scene

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// The text bound adds offsets to an anchor that may be a string.
func TestJSPlusAndLess(t *testing.T) {
	cases := []struct {
		a, b jsval.Value
		want string
	}{
		{jsval.Num(-11), jsval.Str("0"), "-110"},
		{jsval.Str("5"), jsval.Num(2), "52"},
		{jsval.Num(1), jsval.Bool(true), "2"},
		{jsval.Num(1), jsval.Num(2), "3"},
		{jsval.Str(""), jsval.Num(0.5), "0.5"},
	}
	for _, c := range cases {
		if got := jsPlus(jsval.Walker{}, c.a, c.b); got.AsString() != c.want {
			t.Errorf("%v + %v = %q, want %q", c.a, c.b, got.AsString(), c.want)
		}
	}
	less := func(a, b jsval.Value) bool { return coordLess(coordOf(jsval.Walker{}, a), coordOf(jsval.Walker{}, b)) }
	if !less(jsval.Str("-11"), jsval.Str("0")) || less(jsval.Str("9"), jsval.Str("10")) {
		t.Error("two strings compare as text")
	}
	if !less(jsval.Str("9"), jsval.Num(10)) || less(jsval.Str("x"), jsval.Num(10)) {
		t.Error("a string and a number compare as numbers")
	}
}
