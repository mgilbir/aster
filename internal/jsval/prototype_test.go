package jsval

import "testing"

func TestInheritedProperties(t *testing.T) {
	o := ObjectOf("a", Num(1), "valueOf", Str("own"))
	if got := o.Prop("a"); !got.IsNum() || got.NumValue() != 1 {
		t.Errorf("own value: %v", got)
	}
	if got := o.Prop("valueOf"); !got.IsStr() || got.StrValue() != "own" {
		t.Errorf("an own property wins: %v", got)
	}
	if got := o.Prop("missing"); !got.IsUndefined() {
		t.Errorf("missing: %v", got)
	}
	for key, want := range map[string]string{
		"toString":       "function toString() { [native code] }",
		"constructor":    "function Object() { [native code] }",
		"hasOwnProperty": "function hasOwnProperty() { [native code] }",
	} {
		got := o.Prop(key)
		if !got.IsObj() || got.AsString() != want || !got.IsTruthy() || !got.AsBoolean() {
			t.Errorf("%s: %q", key, got.AsString())
		}
		if !SameRef(got, o.Prop(key)) {
			t.Errorf("%s: two reads gave two functions", key)
		}
		if ToNumber(got) == ToNumber(got) {
			t.Errorf("%s: a function is not a number", key)
		}
	}
	if _, ok := Inherited("length"); ok {
		t.Error("length is not inherited from Object.prototype")
	}
}

// TestInheritedFunctionsAreImmutable: the members Inherited answers are one
// value per process, so a write on one must not reach a later read
// (upstream's writes on built-ins are kept per compilation by its caller).
func TestInheritedFunctionsAreImmutable(t *testing.T) {
	f, _ := Inherited("constructor")
	f.ObjValue().Set("mean", Str("x"))
	if g, _ := Inherited("constructor"); g.ObjValue().Has("mean") {
		t.Fatal("a write on an inherited function is visible to the next reader")
	}
	n := NativeFunction("values")
	n.ObjValue().Set("a", Int(1))
	if n.ObjValue().Has("a") {
		t.Fatal("a native function was written to")
	}
}
