package scale

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// C25 (audit 2026-10-18): an ordinal domain interns its values as d3's
// InternMap does, in O(1) each, those held by reference (arrays, objects)
// included: setting a domain of n distinct arrays and looking each up once
// compares with O(n) entries in all, not O(n²). Counted at n and 2n.
func TestOrdinalInternsByReferenceInConstantTime(t *testing.T) {
	probes := func(n int) int64 {
		d := make([]jsval.Value, n)
		for i := range d {
			d[i] = jsval.ArrOf(jsval.Int(i))
		}
		o := NewOrdinal()
		o.SetRange([]jsval.Value{jsval.Str("a"), jsval.Str("b")})
		o.SetDomain(d)
		for _, v := range d {
			o.Apply(v)
		}
		return o.probes
	}
	p1, p2 := probes(2000), probes(4000)
	if p2 > 3*p1 || p1 > 10*2000 {
		t.Errorf("interning 2000 arrays compared with %d entries, 4000 with %d: not linear", p1, p2)
	}
}

// Identity is InternMap's: an array by reference, a Date by its time value,
// -0 as 0, NaN as NaN.
func TestOrdinalInternIdentity(t *testing.T) {
	a, b := jsval.ArrOf(jsval.Num(1)), jsval.ArrOf(jsval.Num(1))
	o := NewOrdinal()
	o.SetRange([]jsval.Value{jsval.Str("r0"), jsval.Str("r1"), jsval.Str("r2"), jsval.Str("r3"), jsval.Str("r4")})
	o.SetDomain([]jsval.Value{a, b, jsval.Timestamp(5), jsval.Num(math.Copysign(0, -1)), jsval.Num(math.NaN())})
	if n := len(o.Domain()); n != 5 {
		t.Fatalf("domain of %d values, want 5", n)
	}
	for _, c := range []struct {
		v    jsval.Value
		want string
	}{
		{a, "r0"}, {b, "r1"}, {jsval.Num(5), "r2"}, {jsval.Timestamp(5), "r2"},
		{jsval.Num(0), "r3"}, {jsval.Num(math.NaN()), "r4"},
	} {
		if got := o.Apply(c.v).StrValue(); got != c.want {
			t.Errorf("scale(%v) = %q, want %q", c.v, got, c.want)
		}
	}
	if got := o.Apply(jsval.ArrOf(jsval.Num(1))); !got.IsStr() || len(o.Domain()) != 6 {
		t.Errorf("a third array of the same element is a new domain value (got %v, domain %d)", got, len(o.Domain()))
	}
}
