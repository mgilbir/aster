package format

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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

// NewTimeLocale is d3.timeFormatLocale.
func NewTimeLocale(def TimeLocaleDef) (*TimeLocale, error) {
	if def.Periods == nil || def.Days == nil || def.ShortDays == nil || def.Months == nil || def.ShortMonths == nil {
		return nil, errors.New("time locale needs periods, days, shortDays, months and shortMonths")
	}
	l := &TimeLocale{def: def}
	l.periodLookup = lookupOf(def.Periods)
	l.weekdayLookup = lookupOf(def.Days)
	l.shortWeekdayLookup = lookupOf(def.ShortDays)
	l.monthLookup = lookupOf(def.Months)
	l.shortMonthLookup = lookupOf(def.ShortMonths)
	return l, nil
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
