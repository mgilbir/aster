package vegalite

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestSsetIsAJavaScriptSet: members are told apart by SameValueZero, as a
// Set tells them apart, and kept as added, in insertion order: a value and
// its text are two members, as are two arrays of the same text; NaN is one
// member, and -0 is 0. Past ssetScan members the index answers the same.
func TestSsetIsAJavaScriptSet(t *testing.T) {
	for _, n := range []int{0, ssetScan + 1} {
		s := newSset()
		for i := range n {
			s.add(string(rune('A' + i)))
		}
		a1, a2 := jsval.ArrOf(jsval.Str("a")), jsval.ArrOf(jsval.Str("a"))
		for _, v := range []Value{jsval.Str("a"), a1, a2, a1, jsval.Str("a"), jsval.Num(math.NaN()), jsval.Num(math.NaN()),
			jsval.Num(0), jsval.Num(math.Copysign(0, -1)), jsval.Str("0"), jsval.Null, jsval.Str("null")} {
			s.addValue(v)
		}
		if got, want := s.size(), n+8; got != want {
			t.Fatalf("n=%d: size %d, want %d (a, [a], [a]', NaN, 0, \"0\", null, \"null\")", n, got, want)
		}
		vs := s.values()[n:]
		if !vs[0].IsStr() || !jsval.SameRef(vs[1], a1) || !jsval.SameRef(vs[2], a2) {
			t.Errorf("n=%d: members not kept as added, in order: %v", n, vs)
		}
		if !s.has("a") || s.hasValue(jsval.ArrOf(jsval.Str("a"))) || !s.hasValue(a2) {
			t.Errorf(`n=%d: has("a")=%v, has(new ["a"])=%v, has(a2)=%v`, n, s.has("a"), s.hasValue(jsval.ArrOf(jsval.Str("a"))), s.hasValue(a2))
		}
		t2 := ssetOfValues([]Value{a1})
		if hasIntersection(newSset("a"), t2) || !hasIntersection(s, t2) || setEqual(newSset("a"), t2) {
			t.Errorf("n=%d: \"a\" and [\"a\"] intersect or are equal sets", n)
		}
		if c := s.clone(); !setEqual(c, s) || !jsval.SameRef(c.values()[n+1], a1) {
			t.Errorf("n=%d: clone differs", n)
		}
	}
}
