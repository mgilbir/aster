package format

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// maxCachedFormats bounds each memo table. Specifiers can come from data (a
// format string per row), so the caches of vega-format's memoize() must not
// grow without limit; past the bound formats are simply rebuilt.
const maxCachedFormats = 1024

// Locale is vega-format's locale: a number locale and a time locale, with
// the memoised formatter factories Vega's expression functions and guide
// labels use. It is safe for concurrent use.
type Locale struct {
	Number *NumberLocale
	Time   *TimeLocale
	// Local is the calendar of the timeXxx functions (UTC unless configured).
	Local Zone

	mu      sync.Mutex
	numFmt  map[string]*NumberFormat
	timeFmt map[timeKey]*TimeFormat
	parsers map[timeKey]*TimeParser
}

type timeKey struct {
	spec string
	utc  bool
}

// NewLocale is vega-format's locale(numberSpec, timeSpec). An undefined or
// null spec selects the default locale for that half; local is the zone of
// the time functions that are not their utc variants.
func NewLocale(numberSpec, timeSpec jsval.Value, local Zone) (*Locale, error) {
	l := &Locale{Number: DefaultNumberLocale(), Time: DefaultTimeLocale(), Local: local}
	if numberSpec.IsTruthy() {
		n, err := NumberLocaleFromValue(numberSpec)
		if err != nil {
			return nil, err
		}
		l.Number = n
	}
	if timeSpec.IsTruthy() {
		t, err := TimeLocaleFromValue(timeSpec)
		if err != nil {
			return nil, err
		}
		l.Time = t
	}
	return l, nil
}

// DefaultLocale is the en-US locale with UTC as the local zone.
func DefaultLocale() *Locale {
	return &Locale{Number: DefaultNumberLocale(), Time: DefaultTimeLocale(), Local: UTC}
}

// NumberFormat is locale.format(spec): the d3 format for a specifier,
// memoised.
func (l *Locale) NumberFormat(spec string) (*NumberFormat, error) {
	l.mu.Lock()
	f, ok := l.numFmt[spec]
	l.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := l.Number.Format(spec)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.numFmt == nil || len(l.numFmt) >= maxCachedFormats {
		l.numFmt = make(map[string]*NumberFormat)
	}
	l.numFmt[spec] = f
	l.mu.Unlock()
	return f, nil
}

func (l *Locale) numberFormatSpec(s Specifier) (*NumberFormat, error) {
	return l.NumberFormat(s.String())
}

// FormatFloat is locale.formatFloat(spec): a number format that, when the
// specifier has no precision, shows up to 12 significant digits and trims
// insignificant trailing zeros. An empty specifier means ",".
func (l *Locale) FormatFloat(spec string) (func(float64) string, error) {
	if spec == "" {
		spec = ","
	}
	s, err := ParseSpecifier(spec)
	if err != nil {
		return nil, err
	}
	if s.HasPrecision() {
		f, err := l.numberFormatSpec(s)
		if err != nil {
			return nil, err
		}
		return f.Format, nil
	}
	s.Precision = 12
	switch s.Type {
	case "%":
		s.Precision -= 2
	case "e":
		s.Precision--
	}
	f, err := l.numberFormatSpec(s)
	if err != nil {
		return nil, err
	}
	one, err := l.NumberFormat(".1f")
	if err != nil {
		return nil, err
	}
	// format('.1f')(1)[1]: the second UTF-16 unit of "1.0" in this locale.
	units := utf16.Encode([]rune(one.Format(1)))
	decimal := ""
	if len(units) > 1 {
		decimal = string(utf16.Decode(units[1:2]))
	}
	return func(x float64) string { return trimZeroes(f.Format(x), decimal) }, nil
}

// trimZeroes is vega-format's trimZeroes: strip trailing zeros after the
// decimal character, keeping any exponent or suffix.
func trimZeroes(str, decimal string) string {
	dec := strings.Index(str, decimal)
	if dec < 0 || decimal == "" {
		return str
	}
	r := []rune(str)
	decR := len([]rune(str[:dec]))
	idx := rightmostDigit(r, decR)
	end := ""
	if idx >= 0 && idx < len(r) {
		end = string(r[idx:])
	}
	if idx < 0 {
		// rightmostDigit found nothing (undefined upstream): --idx is NaN and
		// str.slice(0, NaN) is "".
		return ""
	}
	for idx--; idx > decR; idx-- {
		if r[idx] != '0' {
			idx++
			break
		}
	}
	return string(r[:max(idx, 0)]) + end
}

// rightmostDigit finds where the number's digits end: at an exponent marker,
// else after the last digit past the decimal point; -1 when there is none.
func rightmostDigit(r []rune, dec int) int {
	for i := len(r) - 1; i > 0; i-- {
		if r[i] == 'e' {
			return i
		}
	}
	for i := len(r) - 1; i > dec; i-- {
		if r[i] >= '0' && r[i] <= '9' {
			return i + 1
		}
	}
	return -1
}

// FormatSpan is locale.formatSpan(start, stop, count, specifier): a format
// whose precision suits the tick step of the span, as scale tick labels use.
// A caller holding an undefined or null specifier passes ",f", which is
// what d3 substitutes; the empty specifier is a specifier of its own.
func (l *Locale) FormatSpan(start, stop, count float64, specifier string) (func(float64) string, error) {
	s, err := ParseSpecifier(specifier)
	if err != nil {
		return nil, err
	}
	step := TickStep(start, stop, count)
	value := math.Max(math.Abs(start), math.Abs(stop))
	if !s.HasPrecision() {
		switch s.Type {
		case "s":
			if p := PrecisionPrefix(step, value); !math.IsNaN(p) {
				s.Precision = p
			}
			return l.Number.FormatPrefixSpecifier(reparse(s), value)
		case "", "e", "g", "p", "r":
			if p := PrecisionRound(step, value); !math.IsNaN(p) {
				if s.Type == "e" {
					p--
				}
				s.Precision = p
			}
		case "f", "%":
			if p := PrecisionFixed(step); !math.IsNaN(p) {
				if s.Type == "%" {
					p -= 2
				}
				s.Precision = p
			}
		}
	}
	f, err := l.numberFormatSpec(s)
	if err != nil {
		return nil, err
	}
	return f.Format, nil
}

// reparse round-trips a specifier through its canonical text, which is what
// d3 does when a FormatSpecifier object is handed to format or formatPrefix.
func reparse(s Specifier) Specifier {
	r, err := ParseSpecifier(s.String())
	if err != nil {
		return s
	}
	return r
}

// TimeFormat is locale.timeFormat for a string specifier (memoised).
func (l *Locale) TimeFormat(spec string) *TimeFormat { return l.timeFormat(spec, false) }

// UTCFormat is locale.utcFormat for a string specifier (memoised).
func (l *Locale) UTCFormat(spec string) *TimeFormat { return l.timeFormat(spec, true) }

func (l *Locale) timeFormat(spec string, utc bool) *TimeFormat {
	key := timeKey{spec, utc}
	l.mu.Lock()
	f, ok := l.timeFmt[key]
	l.mu.Unlock()
	if ok {
		return f
	}
	z := l.Local
	if utc {
		z = UTC
	}
	f = l.Time.Format(spec, z)
	l.mu.Lock()
	if l.timeFmt == nil || len(l.timeFmt) >= maxCachedFormats {
		l.timeFmt = make(map[timeKey]*TimeFormat)
	}
	l.timeFmt[key] = f
	l.mu.Unlock()
	return f
}

// TimeParse is locale.timeParse (memoised).
func (l *Locale) TimeParse(spec string) *TimeParser { return l.timeParser(spec, false) }

// UTCParse is locale.utcParse (memoised).
func (l *Locale) UTCParse(spec string) *TimeParser { return l.timeParser(spec, true) }

func (l *Locale) timeParser(spec string, utc bool) *TimeParser {
	key := timeKey{spec, utc}
	l.mu.Lock()
	p, ok := l.parsers[key]
	l.mu.Unlock()
	if ok {
		return p
	}
	z := l.Local
	if utc {
		z = UTC
	}
	p = l.Time.Parse(spec, z)
	l.mu.Lock()
	if l.parsers == nil || len(l.parsers) >= maxCachedFormats {
		l.parsers = make(map[timeKey]*TimeParser)
	}
	l.parsers[key] = p
	l.mu.Unlock()
	return p
}

// TimeFormatter is a function from an instant to its label.
type TimeFormatter func(t float64) string

// TimeFormatSpec is locale.timeFormat(spec) / locale.utcFormat(spec) for a
// dynamic specifier: a string is an ordinary format; anything else must be a
// time multi-format object (see TimeMultiFormat).
func (l *Locale) TimeFormatSpec(spec jsval.Value, utc bool) (TimeFormatter, error) {
	if spec.IsStr() {
		return l.timeFormat(spec.StrValue(), utc).Format, nil
	}
	return l.TimeMultiFormat(spec, utc)
}

// TimeMultiFormat is vega-format's timeMultiFormat: the default d3 "multi
// scale" time format, in which the label is the coarsest unit a tick sits on
// (a month start reads "March", midnight "Sun 05", and so on). spec may
// override the format of each unit ("milliseconds", "seconds", "minutes",
// "hours", "date", "day", "week", "month", "quarter", "year"); null or
// undefined mean the defaults, other non-objects are an error.
func (l *Locale) TimeMultiFormat(spec jsval.Value, utc bool) (TimeFormatter, error) {
	if spec.IsNullish() || !spec.IsTruthy() {
		spec = jsval.Obj(nil)
	}
	if !spec.IsObj() && !spec.IsArr() {
		return nil, fmt.Errorf("Invalid time multi-format specifier: %s", spec.AsString())
	}
	z := l.Local
	if utc {
		z = UTC
	}
	pick := func(keys []string, def string) *TimeFormat {
		for _, k := range keys {
			if v := spec.Get(k); v.IsTruthy() {
				return l.timeFormat(v.AsString(), utc)
			}
		}
		return l.timeFormat(def, utc)
	}
	second, minute, hour := z.Second(), z.Minute(), z.Hour()
	day, week, month, year := z.Day(), z.Week(0), z.Month(), z.Year()
	quarter, _ := z.Month().Every(3)
	fL := pick([]string{"milliseconds"}, ".%L")
	fS := pick([]string{"seconds"}, ":%S")
	fM := pick([]string{"minutes"}, "%I:%M")
	fH := pick([]string{"hours"}, "%I %p")
	fd := pick([]string{"date", "day"}, "%a %d")
	fw := pick([]string{"week"}, "%b %d")
	fm := pick([]string{"month"}, "%B")
	fq := pick([]string{"quarter"}, "%B")
	fy := pick([]string{"year"}, "%Y")
	return func(t float64) string {
		var f *TimeFormat
		switch {
		case second.Floor(t) < t:
			f = fL
		case minute.Floor(t) < t:
			f = fS
		case hour.Floor(t) < t:
			f = fM
		case day.Floor(t) < t:
			f = fH
		case month.Floor(t) < t:
			if week.Floor(t) < t {
				f = fd
			} else {
				f = fw
			}
		case year.Floor(t) < t:
			if quarter.Floor(t) < t {
				f = fm
			} else {
				f = fq
			}
		default:
			f = fy
		}
		return f.Format(t)
	}, nil
}

// FormatValue is the `format(value, spec)` expression function: "null" for
// null, else the number format applied to +value.
func (l *Locale) FormatValue(v, spec jsval.Value) (string, error) {
	if v.Kind() == jsval.KindNull {
		return "null", nil
	}
	f, err := l.NumberFormat(spec.AsString())
	if err != nil {
		return "", err
	}
	return f.FormatValue(v), nil
}

// TimeFormatValue is the `timeFormat(value, spec)` / `utcFormat(value, spec)`
// expression functions: "null" for null, else the time format of the date
// (a non-date is first converted with +value).
func (l *Locale) TimeFormatValue(v, spec jsval.Value, utc bool) (string, error) {
	if v.Kind() == jsval.KindNull {
		return "null", nil
	}
	f, err := l.TimeFormatSpec(spec, utc)
	if err != nil {
		return "", err
	}
	return f(jsval.ToNumber(v)), nil
}

// TimeParseValue is the `timeParse(string, spec)` / `utcParse(string, spec)`
// expression functions: the parsed date as a Timestamp, or Null where d3
// returns null. A null input gives the string "null" as in Vega's wrapper.
func (l *Locale) TimeParseValue(v, spec jsval.Value, utc bool) jsval.Value {
	if v.Kind() == jsval.KindNull {
		return jsval.Str("null")
	}
	t, ok := l.timeParser(spec.AsString(), utc).Parse(v.AsString())
	if !ok {
		return jsval.Null
	}
	return jsval.Timestamp(t)
}
