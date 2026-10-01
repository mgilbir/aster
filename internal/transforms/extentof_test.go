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
	if _, _, _, err := ExtentOf(rows, FieldOf("x.y")); err == nil || !strings.Contains(err.Error(), "reading 'x'") {
		t.Errorf("missing nested field: %v", err)
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
