package jsval

import (
	"runtime/debug"
	"testing"
)

// nestedArrays is [[...[1]...]] with depth arrays, built without recursion.
func nestedArrays(depth int) Value {
	v := Num(1)
	for i := 0; i < depth; i++ {
		v = ArrOf(v)
	}
	return v
}

func nestedObjects(depth int) Value {
	v := Num(1)
	for i := 0; i < depth; i++ {
		v = Obj(ObjectOf("a", v))
	}
	return v
}

// The walks over a value must stop at MaxValueDepth, not recurse once per
// level: a value built at run time can nest as deep as the specification is
// long. The stack is capped well below what 2 million frames would need.
func TestValueWalksAreBounded(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	const depth = 2_000_000
	for name, mk := range map[string]func(int) Value{"arrays": nestedArrays, "objects": nestedObjects} {
		a, b := mk(depth), mk(depth)
		if Equal(a, b) {
			t.Errorf("%s: values nested past MaxValueDepth must not compare equal", name)
		}
		_ = a.AsString()
		out := AppendJSON(nil, a)
		if len(out) > 20*MaxValueDepth {
			t.Errorf("%s: JSON of %d bytes: the walk did not stop at MaxValueDepth", name, len(out))
		}
	}
	if got := nestedArrays(depth).AsDouble(); got != 1 {
		t.Errorf("AsDouble of a chain of one-element arrays = %v, want 1", got)
	}
	// Within the bound everything is as before.
	a, b := nestedArrays(MaxValueDepth/2), nestedArrays(MaxValueDepth/2)
	if !Equal(a, b) {
		t.Error("values within the bound must compare equal")
	}
	if got := a.AsString(); got != "1" {
		t.Errorf("AsString = %q, want 1", got)
	}
}
