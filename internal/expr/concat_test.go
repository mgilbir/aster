package expr

import "testing"

func TestConcatChain(t *testing.T) {
	for src, want := range map[string]string{
		`1 + 2 + "a" + 3 + 4`:                  "3a34",
		`"a" + 1 + null + undefined + [1,2]`:   "a1nullundefined1,2",
		`"a" + {} + true + 1.5 + "z"`:          "a[object Object]true1.5z",
		`1 + 2 + 3 + 4`:                        "10",
		`null + null + "x"`:                    "0x",
		`"" + "b" + "" + "c"`:                  "bc",
		`("a" + "b") + ("c" + "d") + ("e")`:    "abcde",
		`"a" + (1 + 2) + (3 + "b") + 4 + 5`:    "a33b45",
		`toString(1) + 2 + toString(3) + "!!"`: "123!!",
	} {
		v, err := evalBudget(t, src, nil)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := v.AsString(); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
	// Each concatenation is charged the length of its result, as when the
	// additions were nested closures.
	b := NewStringBudget(1 << 20)
	if _, err := evalBudget(t, "pad('x', 300) + pad('y', 300) + pad('z', 300)", b); err != nil {
		t.Fatal(err)
	}
	if got := int64(1<<20) - b.left; got != 600+900 {
		t.Errorf("charged %d bytes, want 1500", got)
	}
	// A chain that outgrows the budget fails at the operand that does.
	b = NewStringBudget(700)
	if _, err := evalBudget(t, "pad('x', 300) + pad('y', 300) + pad('z', 300)", b); err == nil {
		t.Error("want the budget exceeded")
	}
}
