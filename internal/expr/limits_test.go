package expr

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func evalBudget(t *testing.T, src string, b *StringBudget) (jsval.Value, error) {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	s := NewScope(nil)
	s.Strings = b
	return p.Eval(s)
}

func TestStringGrowthIsBounded(t *testing.T) {
	for _, src := range []string{
		"pad('x', 16000000) + pad('x', 16000000)",
		"join([pad('x', 9000000), pad('x', 9000000)], '')",
		"replace(pad('x', 4000000), regexp('x', 'g'), pad('y', 2000))",
	} {
		if _, err := evalBudget(t, src, nil); err == nil || !strings.Contains(err.Error(), "string length") {
			t.Errorf("%s: want a RangeError, got %v", src, err)
		}
	}
}

func TestStringBudgetIsPerRender(t *testing.T) {
	b := NewStringBudget(3 << 20)
	if _, err := evalBudget(t, "pad('x', 2000000)", b); err != nil {
		t.Fatal(err)
	}
	if _, err := evalBudget(t, "pad('x', 2000000)", b); err == nil {
		t.Fatal("second 2 MB string must exceed the 3 MB budget")
	}
	// Small strings are free.
	for i := 0; i < 10000; i++ {
		if _, err := evalBudget(t, "'abc' + 'def'", b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSequenceIsCharged(t *testing.T) {
	b := NewStringBudget(1 << 20)
	if _, err := evalBudget(t, "sequence(100000)", b); err == nil {
		t.Fatal("a 100k-element array (2.4 MB) must exceed a 1 MB budget")
	}
	if _, err := evalBudget(t, "sequence(100)", b); err != nil {
		t.Fatal(err)
	}
}
