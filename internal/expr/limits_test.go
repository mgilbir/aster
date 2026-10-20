package expr

import (
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
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

// MaxStringLength is the engine's bound, far below V8's: reaching it ends the
// render with ErrLimit, which no expression can catch (C43). A string past
// V8's own limit is the RangeError upstream throws too.
func TestStringGrowthIsBounded(t *testing.T) {
	for _, src := range []string{
		"pad('x', 16000000) + pad('x', 16000000)",
		"pad('x', 16000000) + pad('x', 16000000) + 'y'",
		"join([pad('x', 9000000), pad('x', 9000000)], '')",
		"'' + [pad('x', 9000000), pad('x', 9000000)]",
		"replace(pad('', 4000000, 'x'), regexp('x', 'g'), pad('y', 2000))",
		"replace(pad('', 20000, 'x') + 'y', 'y', pad('', 2000, '$`'))",
		"pad('x', 20000000)",
		"pad('é', 6000000) + pad('é', 6000000) + pad('é', 6000000)",
		"upper(pad('', 9000000, 'ß'))",
	} {
		if _, err := evalBudget(t, src, nil); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: want ErrLimit, got %v", src, err)
		}
	}
	for _, src := range []string{"pad('x', 1e12)", "pad('', 1e9, 'ab')"} {
		var e *Error
		if _, err := evalBudget(t, src, nil); !errors.As(err, &e) || e.Name != "RangeError" {
			t.Errorf("%s: want the RangeError V8 throws, got %v", src, err)
		}
	}
	// The limit counts UTF-16 code units, not bytes (C97): 6M two-byte
	// characters are 12 MB but 6M units.
	if v, err := evalBudget(t, "length(pad('é', 6000000) + pad('é', 6000000))", nil); err != nil || v.NumValue() != 12e6 {
		t.Errorf("12M two-byte characters: %v, %v", v, err)
	}
	// A regular expression replacement is as long as it really is, not
	// refused by an estimate.
	if v, err := evalBudget(t, "length(replace(pad('x', 4000000), regexp('x', 'g'), pad('y', 2000)))", nil); err != nil || v.NumValue() != 4001999 {
		t.Errorf("one match: %v, %v", v, err)
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

// String(array) and a chain of + are charged for what they build, once: a
// result the budget holds must not fail because every prefix was charged
// again (C44), and it must cost what join costs.
func TestStringOfArrayChargedOnce(t *testing.T) {
	items := make([]jsval.Value, 5000)
	for i := range items {
		items[i] = jsval.Str("abcdefghijklmnopqrst")
	}
	for _, src := range []string{"'' + datum", "join(datum, ',')", "toString(datum)", "'x' + datum + 'y' + datum"} {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(nil)
		s.Datum = jsval.Arr(items)
		s.Strings = NewStringBudget(1 << 20)
		if _, err := p.Eval(s); err != nil {
			t.Errorf("%s: %v", src, err)
		}
		if used := int64(1<<20) - s.Strings.left; used > 4*2*105_000 {
			t.Errorf("%s: charged %d bytes for about %d", src, used, 105_000)
		}
	}
}

// timeSequence charges each date it adds to its array before it makes the
// next (C106): a sequence past the string budget stops when the budget is
// spent, having allocated about what it was allowed, where it once built
// every date first (32 MB for four million) and charged the array after.
func TestTimeSequenceChargedAsItGrows(t *testing.T) {
	const budgetBytes = 1 << 20
	for _, src := range []string{
		"timeSequence('milliseconds', 0, 4e6)",
		"utcSequence('milliseconds', 0, 4e6)",
	} {
		var err error
		got := allocated(func() { _, err = evalBudget(t, src, NewStringBudget(budgetBytes)) })
		if !errors.Is(err, budget.ErrLimit) {
			t.Fatalf("%s: err = %v, want ErrLimit", src, err)
		}
		// The array is charged as it doubles (at most twice its length) and
		// the copy it grows from is garbage: a few times the budget.
		if !testutil.RaceEnabled && got > 4*budgetBytes {
			t.Errorf("%s under a %d-byte budget allocated %d bytes", src, budgetBytes, got)
		}
	}
}
