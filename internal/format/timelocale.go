package format

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgilbir/aster/internal/budget"

	"github.com/mgilbir/aster/internal/jsval"
)

// TimeLocaleDef is a d3-time-format locale definition (Vega's
// config.locale.time). Every member is required, as in d3.
type TimeLocaleDef struct {
	DateTime    string   // format of %c
	Date        string   // format of %x
	Time        string   // format of %X
	Periods     []string // AM, PM
	Days        []string // Sunday first
	ShortDays   []string
	Months      []string
	ShortMonths []string
}

// TimeLocale compiles time format and parse specifiers for one locale. It is
// immutable and safe for concurrent use.
type TimeLocale struct {
	def TimeLocaleDef
	// Lower-cased name -> index of the LAST equal name, as d3's
	// new Map(names.map((n, i) => [n.toLowerCase(), i])) builds it.
	periodLookup, weekdayLookup, shortWeekdayLookup, monthLookup, shortMonthLookup map[string]int
}

var defaultTimeLocale = mustTimeLocale(TimeLocaleDef{
	DateTime:    "%x, %X",
	Date:        "%-m/%-d/%Y",
	Time:        "%-I:%M:%S %p",
	Periods:     []string{"AM", "PM"},
	Days:        []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	ShortDays:   []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
	Months:      []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	ShortMonths: []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
})

func mustTimeLocale(d TimeLocaleDef) *TimeLocale {
	l, err := NewTimeLocale(d)
	if err != nil {
		panic(err)
	}
	return l
}

// DefaultTimeLocale is d3-time-format's built-in en-US locale.
func DefaultTimeLocale() *TimeLocale { return defaultTimeLocale }

// A time locale's patterns are copied into every label that uses %c, %x or
// %X, and expand into each other, so a specification's config.locale could
// make each label megabytes long; its names are tried in turn by every parse.
// The members are bounded where the locale is built, far above any real
// locale: in d3's locale files the longest pattern is 23 bytes, the longest
// name 23 bytes, and a list holds at most 12 names.
const (
	// MaxLocalePattern bounds dateTime, date and time, in bytes.
	MaxLocalePattern = 256
	// MaxLocaleName bounds each period, day and month name, in bytes.
	MaxLocaleName = 128
	// MaxLocaleNames bounds the entries of each list of names.
	MaxLocaleNames = 64
	// MaxLocaleExpansion bounds the bytes %c, %x or %X can write once their
	// patterns are expanded into each other.
	MaxLocaleExpansion = 4096
)

// NewTimeLocale is d3.timeFormatLocale. It fails, with an error wrapping
// budget.ErrLimit, for a member past the bounds above.
func NewTimeLocale(def TimeLocaleDef) (*TimeLocale, error) {
	if def.Periods == nil || def.Days == nil || def.ShortDays == nil || def.Months == nil || def.ShortMonths == nil {
		return nil, errors.New("time locale needs periods, days, shortDays, months and shortMonths")
	}
	if err := def.check(); err != nil {
		return nil, err
	}
	l := &TimeLocale{def: def}
	l.periodLookup = lookupOf(def.Periods)
	l.weekdayLookup = lookupOf(def.Days)
	l.shortWeekdayLookup = lookupOf(def.ShortDays)
	l.monthLookup = lookupOf(def.Months)
	l.shortMonthLookup = lookupOf(def.ShortMonths)
	for _, d := range [...]string{"%c", "%x", "%X"} {
		if n := l.maxOutput(l.compileFormat(nil, d, 0, new(int))); n > MaxLocaleExpansion {
			return nil, fmt.Errorf("%w: time locale %s expands to up to %d bytes, more than %d", budget.ErrLimit, d, n, MaxLocaleExpansion)
		}
	}
	return l, nil
}

func (d TimeLocaleDef) check() error {
	for _, p := range [...]struct {
		name, v string
	}{{"dateTime", d.DateTime}, {"date", d.Date}, {"time", d.Time}} {
		if len(p.v) > MaxLocalePattern {
			return fmt.Errorf("%w: time locale %s of %d bytes exceeds %d", budget.ErrLimit, p.name, len(p.v), MaxLocalePattern)
		}
	}
	for _, l := range [...]struct {
		name  string
		names []string
	}{{"periods", d.Periods}, {"days", d.Days}, {"shortDays", d.ShortDays}, {"months", d.Months}, {"shortMonths", d.ShortMonths}} {
		if len(l.names) > MaxLocaleNames {
			return fmt.Errorf("%w: time locale %s has %d names, more than %d", budget.ErrLimit, l.name, len(l.names), MaxLocaleNames)
		}
		for _, n := range l.names {
			if len(n) > MaxLocaleName {
				return fmt.Errorf("%w: a time locale name in %s of %d bytes exceeds %d", budget.ErrLimit, l.name, len(n), MaxLocaleName)
			}
		}
	}
	return nil
}

// maxOutput bounds the bytes a compiled format writes for any date.
func (l *TimeLocale) maxOutput(ops []fmtOp) int {
	longest := func(names []string) int {
		m := 0
		for _, n := range names {
			m = max(m, len(n))
		}
		return m
	}
	n := 0
	for _, op := range ops {
		switch op.dir {
		case 0:
			n += len(op.lit)
		case 'a':
			n += longest(l.def.ShortDays)
		case 'A':
			n += longest(l.def.Days)
		case 'b':
			n += longest(l.def.ShortMonths)
		case 'B':
			n += longest(l.def.Months)
		case 'p':
			n += longest(l.def.Periods)
		default:
			n += 24 // a number: at most a sign and 17 digits, or "NaN000"
		}
	}
	return n
}

func lookupOf(names []string) map[string]int {
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[strings.ToLower(n)] = i
	}
	return m
}

// TimeLocaleFromValue reads a locale definition object as found in a Vega
// configuration.
func TimeLocaleFromValue(v jsval.Value) (*TimeLocale, error) {
	if !v.IsObj() {
		return nil, errors.New("time locale must be an object")
	}
	var def TimeLocaleDef
	str := func(key string, dst *string) error {
		m := v.Get(key)
		if !m.IsStr() {
			return fmt.Errorf("time locale %q must be a string", key)
		}
		*dst = m.StrValue()
		return nil
	}
	list := func(key string, dst *[]string) error {
		m := v.Get(key)
		if !m.IsArr() {
			return fmt.Errorf("time locale %q must be an array of strings", key)
		}
		out := make([]string, 0, m.Len())
		for _, it := range m.Items() {
			if !it.IsStr() {
				return fmt.Errorf("time locale %q must be an array of strings", key)
			}
			out = append(out, it.StrValue())
		}
		*dst = out
		return nil
	}
	for _, e := range []error{
		str("dateTime", &def.DateTime), str("date", &def.Date), str("time", &def.Time),
		list("periods", &def.Periods), list("days", &def.Days), list("shortDays", &def.ShortDays),
		list("months", &def.Months), list("shortMonths", &def.ShortMonths),
	} {
		if e != nil {
			return nil, e
		}
	}
	return NewTimeLocale(def)
}

func nameAt(names []string, i int) string {
	if i < 0 || i >= len(names) {
		return "" // Array.prototype.join renders undefined as ""
	}
	return names[i]
}

// maxLocaleNesting bounds how deep %c, %x and %X may expand into each other.
// d3 recurses without limit (a cyclic locale overflows the stack); beyond
// this depth the directive expands to nothing.
const maxLocaleNesting = 8

// maxLocaleExpansions bounds how many %c, %x and %X one specifier expands in
// all. The nesting bound alone leaves b^8 work for a locale whose dateTime
// holds b copies of %c, which a specification can supply (config.locale).
const maxLocaleExpansions = 1 << 12
