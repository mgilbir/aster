package jsval

import (
	"slices"
	"testing"
)

// ArrayLike and PlainMap against node 24 (Array.prototype.slice.call(x) and
// Object.keys / `in` of a plain object, recorded with node -e).
func TestArrayLikeAndPlainMapMatchJavaScript(t *testing.T) {
	text := func(v Value) []string {
		a, ok := ToArrayLike(v)
		if !ok {
			return nil
		}
		var out []string
		for i := int64(0); i < a.Len(); i++ {
			out = append(out, string(AppendJSON(nil, a.At(i))))
		}
		return out
	}
	obj := func(kv ...any) Value { return Obj(ObjectOf(kv...)) }
	for _, c := range []struct {
		v    Value
		want []string
	}{
		{Str("a😀b"), []string{`"a"`, `"\ud83d"`, `"\ude00"`, `"b"`}},
		{obj("length", Str("2"), "0", Str("x"), "1", Str("y"), "2", Str("z")), []string{`"x"`, `"y"`}},
		{obj("length", Num(1.9), "0", Str("x")), []string{`"x"`}},
		{obj("length", Num(-3)), nil},
		{Num(5), nil},
		{Timestamp(0), nil},
		{ArrOf(Num(1), Null), []string{"1", "null"}},
	} {
		if got := text(c.v); !slices.Equal(got, c.want) {
			t.Errorf("slice.call(%v) = %v, want %v", c.v, got, c.want)
		}
	}
	if _, ok := ToArrayLike(Null); ok {
		t.Error("null is no array-like (TypeError)")
	}
	if a, _ := ToArrayLike(obj("length", Num(1e300))); a.Len() != 1<<53-1 {
		t.Errorf("length is clamped to 2^53-1: %d", a.Len())
	}
	var p PlainMap[int]
	for _, k := range []string{"b", "2", "1", "a", "10", "__proto__"} {
		p.Set(k, 1)
	}
	if got := p.Keys(); !slices.Equal(got, []string{"1", "2", "10", "b", "a"}) {
		t.Errorf("Object.keys = %q", got)
	}
	if !p.In("toString") || !p.In("__proto__") || p.In("x") || p.Has("toString") {
		t.Error("`in` of a plain object")
	}
	p.Delete("2")
	p.Set("0", 1)
	if got := p.Keys(); !slices.Equal(got, []string{"0", "1", "10", "b", "a"}) {
		t.Errorf("keys after delete and set = %q", got)
	}
}
