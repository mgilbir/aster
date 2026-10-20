package format

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// A locale whose dateTime is many copies of %c refers to itself: d3 would
// recurse until its stack overflowed, so a format of it throws a RangeError
// at once, and it is compiled in time linear in its text (each pattern
// once), not b^depth.
func TestLocaleExpansionIsBounded(t *testing.T) {
	names := func(n int) []string { return strings.Split(strings.TrimSuffix(strings.Repeat("x,", n), ","), ",") }
	def := TimeLocaleDef{DateTime: "%Y" + strings.Repeat("%c", 40), Date: "%x", Time: "%X", Periods: []string{"a", "b"},
		Days: names(7), ShortDays: names(7), Months: names(12), ShortMonths: names(12)}
	start := time.Now()
	l, err := NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	var th *jsval.Thrown
	if _, err := l.Format("%c", Zone{}).AppendFormatErr(nil, 0); !errors.As(err, &th) || th.Name != "RangeError" {
		t.Errorf("format of a self-expanding pattern: %v; want d3's RangeError", err)
	}
	// d3's parse stops before %c once the input is used up: no recursion.
	if _, ok, err := l.Parse("%c", Zone{}).ParseErr("2020"); ok || err != nil {
		t.Errorf("parse = %v, %v; want null", ok, err)
	}
	// A pattern that refers to itself after matching a character recurses
	// once per character: the recursion is bounded (RangeError), as V8's
	// stack bounds d3's.
	def.DateTime = "a%c"
	l, err = NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Parse("%c", Zone{}).ParseErr(strings.Repeat("a", 100000)); !errors.As(err, &th) || th.Name != "RangeError" {
		t.Errorf("parse of a self-recursive pattern: %v; want RangeError", err)
	}
	if _, ok, err := l.Parse("%c", Zone{}).ParseErr(strings.Repeat("a", 100)); ok || err != nil {
		t.Errorf("parse of 100 levels = %v, %v; want null (the input ends)", ok, err)
	}
	d := time.Since(start)
	// CI baseline 0.0002 s (Heavy gates timings run); limit 2x, floor 1 s: slower is a finding to investigate.
	testutil.Timing(t, "self-expanding locale compile", d, testutil.Scaled(1*time.Second))
	if d > testutil.Scaled(1*time.Second) {
		t.Errorf("compiling a self-expanding locale took %v", d)
	}
}
