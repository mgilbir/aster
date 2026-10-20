package format

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// TimeLocaleDef is a d3-time-format locale definition (Vega's
// config.locale.time). The lists of names are required, as in d3, which
// maps over them when the locale is made; the patterns of %c, %x and %X are
// used only when those directives format or parse, as d3 uses them
// (PatternKinds).
type TimeLocaleDef struct {
	DateTime string // format of %c
	Date     string // format of %x
	Time     string // format of %X
	// PatternKinds says what DateTime, Date and Time (in this order) hold;
	// the zero value is a string.
	PatternKinds [3]PatternKind
	Periods      []string // AM, PM
	Days         []string // Sunday first
	ShortDays    []string
	Months       []string
	ShortMonths  []string
}

// PatternKind is what a time locale's dateTime, date or time member holds,
// as d3's newFormat and parseSpecifier use it: they read its length, and
// charCodeAt and slice of it, only when %c, %x or %X formats or parses.
type PatternKind uint8

const (
	// PatternString is a string: the pattern.
	PatternString PatternKind = iota
	// PatternEmpty is an empty array: a format writes "" (slice and join
	// work on it), a parse matches nothing.
	PatternEmpty
	// PatternNoLength has no positive length (a number, a boolean, an
	// object whose length is missing or not above 0): a format throws (it
	// has no slice), a parse matches nothing.
	PatternNoLength
	// PatternThrows is null or undefined, an array with elements, or an
	// object whose length is above 0: a format and a parse that reach it
	// throw.
	PatternThrows
)

// patternKindOf is the kind of a locale member read as d3 reads it.
func patternKindOf(v jsval.Value) PatternKind {
	switch {
	case v.IsStr():
		return PatternString
	case v.IsNullish():
		return PatternThrows
	case v.IsArr():
		if v.Len() == 0 {
			return PatternEmpty
		}
		return PatternThrows
	case v.IsObj():
		// `i < n` with n = specifier.length: the relational comparison
		// converts the length to a number.
		if jsval.ToNumber(v.Get("length")) > 0 {
			return PatternThrows
		}
	}
	return PatternNoLength
}

// TimeLocale compiles time format and parse specifiers for one locale. It is
// immutable and safe for concurrent use.
type TimeLocale struct {
	def TimeLocaleDef
	// pat are the patterns of %c, %x and %X, compiled once: a specifier
	// refers to them, as d3's formats.c refers to its own function.
	pat [3]localePattern
	// halves: a name or pattern holds a lone surrogate, which may pair with
	// its neighbour in a formatted date.
	halves bool
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
	l.compilePatterns()
	for _, names := range [][]string{{def.DateTime, def.Date, def.Time}, def.Periods, def.Days, def.ShortDays, def.Months, def.ShortMonths} {
		for _, n := range names {
			l.halves = l.halves || strings.IndexByte(n, 0xED) >= 0
		}
	}
	for k, d := range [...]string{"%c", "%x", "%X"} {
		if p := &l.pat[k]; p.thrown == nil && p.maxOut > MaxLocaleExpansion {
			return nil, fmt.Errorf("%w: time locale %s expands to up to %d bytes, more than %d", budget.ErrLimit, d, p.maxOut, MaxLocaleExpansion)
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

// maxOutput bounds the bytes a specifier formats any date to.
func (l *TimeLocale) maxOutput(spec string) int {
	longest := func(names []string) int {
		m := 0
		for _, n := range names {
			m = max(m, len(n))
		}
		return m
	}
	n := 0
	for i := 0; i < len(spec); {
		var op fmtOp
		op, i = nextFmtOp(spec, i)
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
		case 'c', 'x', 'X':
			n += l.pat[patternIndex(op.dir)].maxOut
		default:
			n += 24 // a number: at most a sign and 17 digits, or "NaN000"
		}
	}
	return n
}

func lookupOf(names []string) map[string]int {
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[jsval.JSLower(n)] = i
	}
	return m
}

// TimeLocaleFromValue reads a locale definition object as found in a Vega
// configuration, as d3-time-format reads it: the lists of names must be
// arrays of strings (d3 maps over them with String.prototype.replace and
// toLowerCase, which throw otherwise), the patterns may be anything, and are
// read when they are used (PatternKind).
func TimeLocaleFromValue(v jsval.Value) (*TimeLocale, error) {
	if !v.IsObj() {
		return nil, errors.New("time locale must be an object")
	}
	var def TimeLocaleDef
	for k, p := range [...]struct {
		key string
		dst *string
	}{{"dateTime", &def.DateTime}, {"date", &def.Date}, {"time", &def.Time}} {
		m := v.Get(p.key)
		if def.PatternKinds[k] = patternKindOf(m); def.PatternKinds[k] == PatternString {
			*p.dst = m.StrValue()
		}
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

// localePattern is one of %c, %x and %X compiled: its steps (a %c, %x or
// %X in it refers to that pattern), the most bytes it writes, and the
// exception d3 throws when it formats, if it does: one of PatternNoLength
// or PatternThrows, a pattern that uses %c, %x or %X in a cycle (d3
// recurses until its stack overflows), or one that uses a pattern that
// throws.
type localePattern struct {
	kind   PatternKind
	text   string  // the pattern
	fmt    []fmtOp // its compiled steps (a pattern is at most MaxLocalePattern bytes)
	parse  []parseOp
	maxOut int
	thrown *jsval.Thrown
	state  uint8 // compilePatterns' walk: 0 not seen, 1 in progress, 2 done
}

var patternNames = [3]string{"dateTime", "date", "time"}

// patternIndex is the pattern of directive c ('c', 'x' or 'X').
func patternIndex(c byte) int {
	switch c {
	case 'x':
		return 1
	case 'X':
		return 2
	}
	return 0
}

func errStackOverflow() *jsval.Thrown {
	return &jsval.Thrown{Name: "RangeError", Msg: "Maximum call stack size exceeded"}
}

// compilePatterns compiles the three patterns and resolves, depth first,
// what each writes at most and whether formatting it throws. Each pattern
// is visited once, so the work is linear in the patterns' length however
// they refer to each other.
func (l *TimeLocale) compilePatterns() {
	texts := [3]string{l.def.DateTime, l.def.Date, l.def.Time}
	for k := range l.pat {
		p := &l.pat[k]
		p.kind = l.def.PatternKinds[k]
		if p.kind == PatternString {
			p.text = texts[k]
			p.fmt = compileFormat(texts[k])
			p.parse = compileParse(nil, texts[k])
		}
	}
	for k := range l.pat {
		l.resolve(k)
	}
}

func (l *TimeLocale) resolve(k int) {
	p := &l.pat[k]
	if p.state == 2 {
		return
	}
	p.state = 1
	switch p.kind {
	case PatternNoLength:
		p.thrown = &jsval.Thrown{Name: "TypeError", Msg: "the time locale's " + patternNames[k] + " has no slice method (it is not a string)"}
	case PatternThrows:
		p.thrown = &jsval.Thrown{Name: "TypeError", Msg: "the time locale's " + patternNames[k] + " is not a string"}
	}
	for i := 0; i < len(p.text); {
		var op fmtOp
		op, i = nextFmtOp(p.text, i)
		if !op.ref() {
			continue
		}
		sub := &l.pat[patternIndex(op.dir)]
		switch sub.state {
		case 1:
			p.thrown = errStackOverflow()
		case 0:
			l.resolve(patternIndex(op.dir))
		}
		if p.thrown == nil && sub.thrown != nil {
			p.thrown = sub.thrown
		}
	}
	if p.thrown == nil {
		p.maxOut = l.maxOutput(p.text)
	}
	p.state = 2
}
