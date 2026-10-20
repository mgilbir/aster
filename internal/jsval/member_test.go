package jsval

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPrototypeNamesMatchNode checks Member's tables of the built-in
// prototypes' members against node's (testdata/protonames.json, recorded by
// testdata/gen_protonames.mjs).
func TestPrototypeNamesMatchNode(t *testing.T) {
	b, err := os.ReadFile("testdata/protonames.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]map[string]*int
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for kind, got := range map[string]map[string]int{
		"object": objectProtoNames, "array": arrayProtoNames, "string": stringProtoNames,
		"number": numberProtoNames, "boolean": booleanProtoNames, "function": functionProtoNames,
	} {
		w := want[kind]
		if len(w) != len(got) {
			t.Errorf("%s.prototype: %d names, node has %d", kind, len(got), len(w))
		}
		for name, a := range w {
			wa := -1
			if a != nil {
				wa = *a
			}
			if ga, ok := got[name]; !ok || ga != wa {
				t.Errorf("%s.prototype.%s: length %d (listed %v), node %d", kind, name, ga, ok, wa)
			}
		}
	}
	// Inherited answers every method of Object.prototype, with its length.
	for name, a := range want["object"] {
		f, ok := Inherited(name)
		switch {
		case a == nil:
			if ok {
				t.Errorf("Inherited(%q) answers a function", name)
			}
		case !ok || !Member(f, "length").IsNum() || Member(f, "length").NumValue() != float64(*a):
			t.Errorf("Inherited(%q): %v, length %v, want %d", name, ok, Member(f, "length"), *a)
		}
	}
}

func TestMember(t *testing.T) {
	arr := ArrOf(Int(1), Int(2))
	for _, c := range []struct {
		v    Value
		key  string
		want string // AsString of the member, "fn" for a function
	}{
		{arr, "fill", "fn"}, {arr, "length", "2"}, {arr, "1", "2"}, {arr, "2", "undefined"}, {arr, "01", "undefined"},
		{arr, "toString", "fn"}, {arr, "hasOwnProperty", "fn"}, {arr, "signal", "undefined"},
		{Str("abé"), "length", "3"}, {Str("abé"), "2", "é"}, {Str("😀b"), "length", "3"}, {Str("😀b"), "2", "b"}, {Str("😀b"), "3", "undefined"}, {Str("ab"), "fontsize", "fn"}, {Str("ab"), "fill", "undefined"},
		{Num(1), "toFixed", "fn"}, {Num(1), "length", "undefined"}, {True, "valueOf", "fn"}, {True, "fill", "undefined"},
		{Obj(ObjectOf("a", Int(1))), "a", "1"}, {Obj(ObjectOf("a", Int(1))), "constructor", "fn"}, {Obj(ObjectOf("a", Int(1))), "fill", "undefined"},
		{Member(arr, "slice"), "length", "2"}, {Member(arr, "slice"), "name", "slice"}, {Member(Num(1), "toString"), "length", "1"},
		{Member(Str("a"), "toString"), "length", "0"}, {Member(arr, "map"), "call", "fn"}, {Member(arr, "map"), "fill", "undefined"},
		{HostFunction("function (context) {\n}", nil), "length", "1"}, {HostFunction("function (context) {\n}", nil), "name", ""},
		{HostFunction("function (a, b) {}", nil), "length", "2"}, {HostFunction("function () {}", nil), "length", "0"},
	} {
		got := Member(c.v, c.key)
		s := got.AsString()
		if IsFunction(got) {
			s = "fn"
		}
		if s != c.want {
			t.Errorf("%v[%q] = %s, want %s", c.v, c.key, s, c.want)
		}
	}
}

// TestMemberStringUnitsMatchArrayLike: a string's elements read one by one
// are the code units ToArrayLike decodes.
func TestMemberStringUnitsMatchArrayLike(t *testing.T) {
	for _, s := range []string{"", "abc", "é😀x", "\xed\xa0\x80z", "a\xffb"} {
		v := Str(s)
		a, _ := ToArrayLike(v)
		if n := Member(v, "length").NumValue(); n != float64(a.Len()) {
			t.Errorf("%q: length %v, want %d", s, n, a.Len())
		}
		for i := int64(0); i <= a.Len(); i++ {
			if got, want := Member(v, JSNumberString(float64(i))), a.At(i); !StrictEqual(got, want) {
				t.Errorf("%q[%d] = %q, want %q", s, i, got.AsString(), want.AsString())
			}
		}
	}
}
