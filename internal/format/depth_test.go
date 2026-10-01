package format

import (
	"strings"
	"testing"
	"time"
)

// A locale whose dateTime is many copies of %c expands b^maxLocaleNesting
// directives without a bound on the total: it must finish at once.
func TestLocaleExpansionIsBounded(t *testing.T) {
	names := func(n int) []string { return strings.Split(strings.TrimSuffix(strings.Repeat("x,", n), ","), ",") }
	def := TimeLocaleDef{DateTime: "%Y" + strings.Repeat("%c", 40), Date: "%x", Time: "%X", Periods: []string{"a", "b"},
		Days: names(7), ShortDays: names(7), Months: names(12), ShortMonths: names(12)}
	l, err := NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	f := l.Format("%c", Zone{})
	_ = f.Format(0)
	p := l.Parse("%c", Zone{})
	_, _ = p.Parse("2020")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("compiling a self-expanding locale took %v", d)
	}
}
