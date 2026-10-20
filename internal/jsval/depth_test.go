package jsval

import (
	"errors"
	"runtime/debug"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
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

// stopped runs f and returns the error of the *budget.Stop it panics with
// (nil when it returns); any other panic is passed on.
func stopped(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(*budget.Stop)
			if !ok {
				panic(r)
			}
			err = s.Err
		}
	}()
	f()
	return nil
}

// walkers are every walk of a value, each as a function of the value a and
// of another, b, built the same way but sharing nothing with it (for the
// comparisons).
var walkers = map[string]func(w Walker, a, b Value){
	"AsString":     func(_ Walker, a, _ Value) { _ = a.AsString() },
	"String":       func(w Walker, a, _ Value) { _ = w.String(a) },
	"StringValue":  func(w Walker, a, _ Value) { _ = w.StringValue(a) },
	"AppendJSON":   func(w Walker, a, _ Value) { _ = w.AppendJSON(nil, a) },
	"JSONIndent":   func(w Walker, a, _ Value) { _ = w.AppendJSONIndent(nil, a, "  ") },
	"Equal":        func(w Walker, a, b Value) { _ = w.Equal(a, b) },
	"Relate":       func(w Walker, a, _ Value) { _, _ = w.Relate(a, Str("x")) },
	"RelateArrays": func(w Walker, a, b Value) { _, _ = w.Relate(a, b) },
}

// C103 (audit 2026-10-18): a value nested past MaxValueDepth is one policy
// for every walk, the expression evaluator's included: the call fails with
// ErrLimit, where these once printed "" or reported unequal. The walks stop
// at the bound rather than recursing once per level (a value built at run
// time can nest as deep as the specification is long); the stack is capped
// well below what 2 million frames would need.
func TestValueWalksPastMaxValueDepthAreALimit(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	const depth = 2_000_000
	for name, mk := range map[string]func(int) Value{"arrays": nestedArrays, "objects": nestedObjects} {
		v, u := mk(depth), mk(depth)
		for wn, walk := range walkers {
			if name == "objects" && (wn == "AsString" || wn == "String" || wn == "Relate" || wn == "RelateArrays") {
				continue // the String of an object is not a walk of it
			}
			for _, w := range []Walker{{}, WalkerFor(t.Context())} {
				err := stopped(func() { walk(w, v, u) })
				if !errors.Is(err, budget.ErrLimit) {
					t.Errorf("%s of nested %s (detached %v): %v, want a limit", wn, name, w.Detached(), err)
				}
			}
		}
	}
	if got := nestedArrays(depth).AsDouble(); got != 1 {
		t.Errorf("AsDouble of a chain of one-element arrays = %v, want 1", got)
	}
	if got := ToNumber(nestedArrays(depth)); got != 1 {
		t.Errorf("ToNumber of a chain of one-element arrays = %v, want 1", got)
	}
	// Within the bound everything is as before.
	a, b := nestedArrays(MaxValueDepth/2), nestedArrays(MaxValueDepth/2)
	if !Equal(a, b) {
		t.Error("values within the bound must compare equal")
	}
	if got := a.AsString(); got != "1" {
		t.Errorf("AsString = %q, want 1", got)
	}
	// A message about the value shows its start and does not fail.
	if got := nestedArrays(depth).String(); len(got) > previewBytes+len("…") {
		t.Errorf("String() is %d bytes, want a preview of at most %d", len(got), previewBytes)
	}
}
