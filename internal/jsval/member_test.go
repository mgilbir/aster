package jsval

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPrototypeNamesMatchNode checks the tables of the built-in prototypes'
// members (protos.go), the one table the evaluator reads its members through,
// against node's (testdata/protonames.json, recorded by
// testdata/gen_protonames.mjs): every member, the name and the length of the
// function it holds.
func TestPrototypeNamesMatchNode(t *testing.T) {
	b, err := os.ReadFile("testdata/protonames.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]map[string]*struct {
		Name   string `json:"name"`
		Length int    `json:"length"`
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	tables := map[string]ProtoTable{
		"object": objectProtoTable, "array": arrayProtoTable, "string": stringProtoTable,
		"number": numberProtoTable, "boolean": booleanProtoTable, "function": functionProtoTable,
		"regexp": regexpProtoTable, "date": dateProtoTable,
	}
	if len(want) != len(tables) {
		t.Errorf("node records %d prototypes; the engine models %d", len(want), len(tables))
	}
	for kind, got := range tables {
		w := want[kind]
		if len(w) == 0 {
			t.Errorf("%s.prototype: not recorded", kind)
		}
		if len(w) != len(got) {
			t.Errorf("%s.prototype: %d members, node has %d", kind, len(got), len(w))
		}
		for name, f := range w {
			g, ok := got[name]
			switch {
			case !ok:
				t.Errorf("%s.prototype.%s: missing", kind, name)
			case f == nil:
				if g.Fn != "" {
					t.Errorf("%s.prototype.%s: a function %q, node has none", kind, name, g.Fn)
				}
			case g.Fn != f.Name || g.Arity != f.Length:
				t.Errorf("%s.prototype.%s: function %q length %d, node %q length %d", kind, name, g.Fn, g.Arity, f.Name, f.Length)
			}
		}
	}
	// Inherited answers every method of Object.prototype, with its length.
	for name, f := range want["object"] {
		v, ok := Inherited(name)
		switch {
		case f == nil:
			if ok {
				t.Errorf("Inherited(%q) answers a function", name)
			}
		case !ok || !Member(v, "length").IsNum() || Member(v, "length").NumValue() != float64(f.Length):
			t.Errorf("Inherited(%q): %v, length %v, want %d", name, ok, Member(v, "length"), f.Length)
		}
	}
}

// TestPrototypeFunctionsAreShared: a member read twice is one function, and
// the names of one function (trimLeft, trimStart) are the same function.
func TestPrototypeFunctionsAreShared(t *testing.T) {
	s, arr := Str("a"), ArrOf(Int(1))
	for _, c := range []struct {
		a, b Value
		same bool
	}{
		{Member(arr, "fill"), Member(arr, "fill"), true},
		{Member(s, "trimLeft"), Member(s, "trimStart"), true},
		{Member(s, "trimRight"), Member(s, "trimEnd"), true},
		{Member(s, "trimLeft"), Member(s, "trimEnd"), false},
		{Member(Int(1), "toString"), Member(s, "toString"), false},
		{Member(arr, "hasOwnProperty"), Member(Obj(ObjectOf("a", Int(1))), "hasOwnProperty"), true},
		{Member(Timestamp(0), "toGMTString"), Member(Timestamp(0), "toUTCString"), true},
	} {
		if SameRef(c.a, c.b) != c.same {
			t.Errorf("%v and %v: same function %v, want %v", c.a, c.b, !c.same, c.same)
		}
	}
	if got := Member(Member(s, "trimLeft"), "name").AsString(); got != "trimStart" {
		t.Errorf("trimLeft.name = %q, want trimStart", got)
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
