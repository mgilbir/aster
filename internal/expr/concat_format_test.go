package expr

import "testing"

// A format() operand is appended to the chain without a string of its own; the
// result, the errors and the budget charge must be those of the plain calls.
func TestConcatChainFormat(t *testing.T) {
	for _, args := range []string{
		`1.5, ""`, `1234.5678, ",.2f"`, `null, ""`, `undefined, ".3s"`, `"12", "d"`, `-0.000001234, ""`,
		`1e21, ""`, `0.1 + 0.2, ""`, `NaN, ""`, `[1], "x"`, `5, "bad spec zz"`, `5, ".2%"`, `12345, "$,.0f"`, `7, "c"`,
		`true, ""`, `5`,
	} {
		fused := `"a" + format(` + args + `) + "b" + format(` + args + `)`
		plain := `"a" + toString(format(` + args + `)) + "b" + toString(format(` + args + `))`
		v1, e1 := evalBudget(t, fused, nil)
		v2, e2 := evalBudget(t, plain, nil)
		if (e1 == nil) != (e2 == nil) || (e1 != nil && e1.Error() != e2.Error()) {
			t.Errorf("%s: error %v, plain %v", args, e1, e2)
			continue
		}
		if e1 == nil && v1.AsString() != v2.AsString() {
			t.Errorf("%s: %q, plain %q", args, v1.AsString(), v2.AsString())
		}
	}
	// format first in the chain, and after a number: the generic path.
	for src, want := range map[string]string{
		`format(1.5, "") + "x" + format(2, "")`: "1.5x2",
		`1 + 2 + format(3, "") + format(4, "")`: "334",
	} {
		if v, err := evalBudget(t, src, nil); err != nil || v.AsString() != want {
			t.Errorf("%s = %v, %v; want %q", src, v, err, want)
		}
	}
	// The charge is that of the nested additions: 601 and 602 bytes, the two
	// results over the free 512.
	b := NewStringBudget(1 << 20)
	if _, err := evalBudget(t, "pad('x', 300) + format(1, '') + pad('y', 300) + format(2, '')", b); err != nil {
		t.Fatal(err)
	}
	if got := int64(1<<20) - b.left; got != 601+602 {
		t.Errorf("charged %d bytes, want %d", got, 601+602)
	}
}
