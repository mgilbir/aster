package jsval

import (
	"math"
	"testing"
)

// InternKey is d3's InternMap key: valueOf() under SameValueZero.
func TestInternKey(t *testing.T) {
	arr, arr2 := ArrOf(Num(1), Num(2)), ArrOf(Num(1), Num(2))
	obj := Obj(ObjectOf("a", Num(1)))
	pat := PatternValue(&Pattern{Source: "a"})
	same := []struct {
		name string
		a, b Value
	}{
		{"a date and its number", Timestamp(5), Num(5)},
		{"-0 and 0", Num(math.Copysign(0, -1)), Num(0)},
		{"NaN and NaN", Num(math.NaN()), Num(-math.NaN())},
		{"an Invalid Date and NaN", Timestamp(math.NaN()), Num(math.NaN())},
		{"equal strings", Str("ab"), Str(string([]byte{'a', 'b'}))},
		{"an array and itself", arr, arr},
		{"an object and itself", obj, obj},
		{"a pattern and itself", pat, pat},
		{"null and null", Null, Null},
		{"true and true", True, Bool(true)},
	}
	for _, c := range same {
		if c.a.InternKey() != c.b.InternKey() {
			t.Errorf("%s: different keys", c.name)
		}
	}
	differ := []struct {
		name string
		a, b Value
	}{
		{"arrays of the same elements", arr, arr2},
		{"objects of the same members", obj, Obj(ObjectOf("a", Num(1)))},
		{"null and undefined", Null, Undefined},
		{"true and 1", True, Num(1)},
		{"false and 0", False, Num(0)},
		{"\"1\" and 1", Str("1"), Num(1)},
		{"a string and the array of it", Str("1,2"), arr},
		{"an empty string and undefined", Str(""), Undefined},
		{"1 and 2", Num(1), Num(2)},
	}
	for _, c := range differ {
		if c.a.InternKey() == c.b.InternKey() {
			t.Errorf("%s: the same key", c.name)
		}
	}
	m := map[InternKey]int{Num(math.NaN()).InternKey(): 1}
	if m[Num(math.NaN()).InternKey()] != 1 {
		t.Error("a NaN key is not found again")
	}
}
