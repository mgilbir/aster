package jsval

import "testing"

// The expected strings are node's `${stringValue(x)}` (vega-util 2).
func TestStringValue(t *testing.T) {
	obj := NewObject(1)
	obj.Set("b", Null)
	obj2 := NewObject(1)
	obj2.Set("k", Str("x\u2028"))
	for _, tc := range []struct {
		v    Value
		want string
	}{
		{Arr([]Value{Num(4), Null, Num(2)}), "[4,null,2]"},
		{Arr([]Value{Num(1), Arr([]Value{Null, Str("a")}), Obj(obj)}), `[1,[null,"a"],{"b":null}]`},
		{Str("a\u2028b\u2029c"), `"a\u2028b\u2029c"`},
		{Obj(obj2), `{"k":"x\u2028"}`},
		{Null, "null"},
		{True, "true"},
		{Num(1.5), "1.5"},
		{Arr(nil), "[]"},
		{Arr([]Value{Num(1), Undefined, Num(2)}), "[1,,2]"},
	} {
		if got := StringValue(tc.v); got != tc.want {
			t.Errorf("StringValue(%v) = %s, want %s", tc.v, got, tc.want)
		}
	}
}
