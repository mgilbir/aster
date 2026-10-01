package transforms

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// vega-util's extent(array, f) applies f to array[n] when no value is valid,
// and a field accessor throws on undefined.
func TestExtentOfThrowsWhenNoValueIsValid(t *testing.T) {
	obj := func(k string, v jsval.Value) jsval.Value { return jsval.Obj(jsval.ObjectOf(k, v)) }
	rows := []jsval.Value{obj("a", jsval.Null), obj("b", jsval.Num(1))}
	if _, _, _, err := ExtentOf(rows, FieldOf("a")); err == nil || !strings.Contains(err.Error(), "reading 'a'") {
		t.Errorf("all-null field: %v", err)
	}
	lo, hi, ok, err := ExtentOf([]jsval.Value{obj("a", jsval.Null), obj("a", jsval.Num(3)), obj("a", jsval.Num(2))}, FieldOf("a"))
	if err != nil || !ok || jsval.ToNumber(lo) != 2 || jsval.ToNumber(hi) != 3 {
		t.Errorf("valid values: %v %v %v %v", lo, hi, ok, err)
	}
	if _, _, ok, err := ExtentOf(nil, FieldOf("a")); err != nil || ok {
		t.Errorf("no rows: ok=%v err=%v", ok, err)
	}
	if _, _, _, err := ExtentOf(rows, Field{}); err != nil {
		t.Errorf("null field: %v", err)
	}
}

// A field accessor reads its path as JavaScript does: a missing step of a
// nested path, or a missing tuple, is a TypeError (a *jsval.Thrown panic).
func TestFieldAccessorThrowsOnMissingSteps(t *testing.T) {
	thrown := func(f func()) (msg string) {
		defer func() {
			if r := recover(); r != nil {
				if th, ok := r.(*jsval.Thrown); ok {
					msg = th.Msg
				}
			}
		}()
		f()
		return ""
	}
	row := jsval.Obj(jsval.ObjectOf("a", jsval.Obj(jsval.ObjectOf("b", jsval.Num(1)))))
	if got := FieldOfStrict("a.b").Get(row); jsval.ToNumber(got) != 1 {
		t.Errorf("a.b = %v", got)
	}
	if got := FieldOfStrict("a.c").Get(row); !got.IsUndefined() {
		t.Errorf("a.c = %v", got)
	}
	if msg := thrown(func() { FieldOfStrict("x.y").Get(row) }); msg != "Cannot read properties of undefined (reading 'y')" {
		t.Errorf("x.y: %q", msg)
	}
	if msg := thrown(func() { FieldOfStrict("x").Get(jsval.Undefined) }); msg != "Cannot read properties of undefined (reading 'x')" {
		t.Errorf("x of undefined: %q", msg)
	}
	if msg := thrown(func() { FieldOfStrict("x").Get(jsval.Null) }); msg != "Cannot read properties of null (reading 'x')" {
		t.Errorf("x of null: %q", msg)
	}
	if got := FieldOfStrict("a.b.c").Get(row); !got.IsUndefined() {
		t.Errorf("a.b.c of a number = %v", got)
	}
}
