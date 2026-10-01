package format

import (
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

// fmtOp is one step of a compiled format: literal text, or a directive with
// its padding modifier.
type fmtOp struct {
	dir byte   // directive letter, or 0 for a literal
	pad byte   // fill for numeric directives: '0', ' ' or 0 (no padding)
	lit string // literal text (dir == 0)
}

// TimeFormat is a compiled d3 time format function (timeFormat or utcFormat).
type TimeFormat struct {
	loc  *TimeLocale
	zone Zone
	spec string
	ops  []fmtOp
}

// Format is d3.timeFormat(specifier) in the given zone (UTC for utcFormat).
func (l *TimeLocale) Format(specifier string, z Zone) *TimeFormat {
	f := &TimeFormat{loc: l, zone: z, spec: specifier}
	f.ops = l.compileFormat(nil, specifier, 0, new(int))
	return f
}

// compileFormat translates a specifier into ops, expanding %c, %x and %X
// in place (they ignore padding modifiers).
func (l *TimeLocale) compileFormat(ops []fmtOp, spec string, depth int, work *int) []fmtOp {
	lit := 0 // start of pending literal
	flush := func(end int) {
		if end > lit {
			ops = append(ops, fmtOp{lit: spec[lit:end]})
		}
	}
	for i := 0; i < len(spec); {
		if spec[i] != '%' {
			i++
			continue
		}
		flush(i)
		i++
		var pad byte
		havePad := false
		if i < len(spec) {
			switch spec[i] {
			case '-':
				pad, havePad = 0, true
				i++
			case '_':
				pad, havePad = ' ', true
				i++
			case '0':
				pad, havePad = '0', true
				i++
			}
		}
		if i >= len(spec) { // a trailing "%" or "%-" prints nothing
			lit = i
			break
		}
		r, w := utf8.DecodeRuneInString(spec[i:])
		c := spec[i]
		i += w
		lit = i
		if w > 1 || r == utf8.RuneError {
			ops = append(ops, fmtOp{lit: spec[i-w : i]}) // unknown directive: just the character
			continue
		}
		if !havePad {
			pad = '0'
			if c == 'e' {
				pad = ' '
			}
		}
		switch c {
		case 'c', 'x', 'X':
			if *work++; depth < maxLocaleNesting && *work <= maxLocaleExpansions {
				sub := l.def.DateTime
				if c == 'x' {
					sub = l.def.Date
				} else if c == 'X' {
					sub = l.def.Time
				}
				ops = l.compileFormat(ops, sub, depth+1, work)
			}
		case 'a', 'A', 'b', 'B', 'd', 'e', 'f', 'g', 'G', 'H', 'I', 'j', 'L', 'm', 'M', 'p', 'q', 'Q', 's', 'S',
			'u', 'U', 'V', 'w', 'W', 'y', 'Y', 'Z':
			ops = append(ops, fmtOp{dir: c, pad: pad})
		case '%':
			ops = append(ops, fmtOp{lit: "%"})
		default:
			ops = append(ops, fmtOp{lit: string(rune(c))}) // unknown directive: the letter without "%"
		}
	}
	flush(len(spec))
	return ops
}

// String is the specifier text.
func (f *TimeFormat) String() string { return f.spec }

// Format formats the instant t. An Invalid Date formats as d3 does (numeric
// directives print "NaN", names print nothing).
func (f *TimeFormat) Format(t float64) string {
	var buf [64]byte
	return string(f.AppendFormat(buf[:0], t))
}

// FormatValue formats a dynamic value the way d3's format function does:
// anything that is not a Date is converted with +value first.
func (f *TimeFormat) FormatValue(v jsval.Value) string { return f.Format(jsval.ToNumber(v)) }

// dayInfo caches the calendar arithmetic several directives share.
type dayInfo struct {
	days    int64 // local day number (days since 1970-01-01)
	jan1    int64 // day number of 1 January of the year
	weekday int
}

func weekdayOfDay(days int64) int { return int((days%7 + 11) % 7) }

// floorToWeekday is the latest day at or before days that falls on weekday.
func floorToWeekday(days int64, weekday int) int64 {
	return days - int64((weekdayOfDay(days)+7-weekday)%7)
}

// AppendFormat appends the formatted instant to dst.
func (f *TimeFormat) AppendFormat(dst []byte, t float64) []byte {
	valid := Valid(t)
	var fl Fields
	var di dayInfo
	if valid {
		fl = f.zone.Fields(t)
		di.days = daysFromCivil(int64(fl.Year), fl.Month+1) + int64(fl.Day) - 1
		di.jan1 = daysFromCivil(int64(fl.Year), 1)
		di.weekday = fl.Weekday
	}
	l := f.loc
	for _, op := range f.ops {
		if op.dir == 0 {
			dst = append(dst, op.lit...)
			continue
		}
		if !valid {
			dst = f.appendInvalid(dst, op)
			continue
		}
		switch op.dir {
		case 'a':
			dst = append(dst, nameAt(l.def.ShortDays, fl.Weekday)...)
		case 'A':
			dst = append(dst, nameAt(l.def.Days, fl.Weekday)...)
		case 'b':
			dst = append(dst, nameAt(l.def.ShortMonths, fl.Month)...)
		case 'B':
			dst = append(dst, nameAt(l.def.Months, fl.Month)...)
		case 'd', 'e':
			dst = appendPad(dst, fl.Day, op.pad, 2)
		case 'f':
			dst = appendPad(dst, fl.Millisecond, op.pad, 3)
			dst = append(dst, "000"...)
		case 'g', 'G':
			if !isoValid(di) {
				width := 4
				if op.dir == 'g' {
					width = 2
				}
				dst = appendNaN(dst, op.pad, width)
				break
			}
			y := isoYear(di)
			if op.dir == 'g' {
				dst = appendPad(dst, y%100, op.pad, 2)
			} else {
				dst = appendPad(dst, y%10000, op.pad, 4)
			}
		case 'H':
			dst = appendPad(dst, fl.Hour, op.pad, 2)
		case 'I':
			h := fl.Hour % 12
			if h == 0 {
				h = 12
			}
			dst = appendPad(dst, h, op.pad, 2)
		case 'j':
			if !yearStartValid(di.jan1) {
				dst = appendNaN(dst, op.pad, 3)
				break
			}
			dst = appendPad(dst, int(di.days-di.jan1)+1, op.pad, 3)
		case 'L':
			dst = appendPad(dst, fl.Millisecond, op.pad, 3)
		case 'm':
			dst = appendPad(dst, fl.Month+1, op.pad, 2)
		case 'M':
			dst = appendPad(dst, fl.Minute, op.pad, 2)
		case 'p':
			i := 0
			if fl.Hour >= 12 {
				i = 1
			}
			dst = append(dst, nameAt(l.def.Periods, i)...)
		case 'q':
			dst = strconv.AppendInt(dst, int64(1+fl.Month/3), 10)
		case 'Q':
			dst = jsval.AppendJSNumber(dst, t)
		case 's':
			dst = jsval.AppendJSNumber(dst, math.Floor(t/1000))
		case 'S':
			dst = appendPad(dst, fl.Second, op.pad, 2)
		case 'u':
			wd := fl.Weekday
			if wd == 0 {
				wd = 7
			}
			dst = strconv.AppendInt(dst, int64(wd), 10)
		case 'U':
			if !yearStartValid(di.jan1) {
				dst = appendNaN(dst, op.pad, 2)
				break
			}
			dst = appendPad(dst, weekNumber(di, 0), op.pad, 2)
		case 'V':
			if !isoValid(di) {
				dst = appendNaN(dst, op.pad, 2)
				break
			}
			dst = appendPad(dst, isoWeek(di), op.pad, 2)
		case 'w':
			dst = strconv.AppendInt(dst, int64(fl.Weekday), 10)
		case 'W':
			if !yearStartValid(di.jan1) {
				dst = appendNaN(dst, op.pad, 2)
				break
			}
			dst = appendPad(dst, weekNumber(di, 1), op.pad, 2)
		case 'y':
			dst = appendPad(dst, fl.Year%100, op.pad, 2)
		case 'Y':
			dst = appendPad(dst, fl.Year%10000, op.pad, 4)
		case 'Z':
			dst = f.appendZone(dst, t)
		}
	}
	return dst
}

// weekNumber is %U (weekStart 0, Sunday) or %W (1, Monday): the number of
// week boundaries between the day before 1 January and the date.
func weekNumber(di dayInfo, weekStart int) int {
	return int((floorToWeekday(di.days, weekStart) - floorToWeekday(di.jan1-1, weekStart)) / 7)
}

// isoThursday is the Thursday of the date's ISO week as d3 computes it:
// floor to Thursday from Thursday to Sunday, ceil to Thursday from Monday
// to Wednesday.
func isoThursday(di dayInfo) int64 {
	wd := di.weekday
	if wd >= 4 || wd == 0 {
		return floorToWeekday(di.days, 4)
	}
	return floorToWeekday(di.days, 4) + 7
}

func isoYear(di dayInfo) int {
	y, _, _ := civilFromDays(isoThursday(di))
	return int(y)
}

func isoWeek(di dayInfo) int {
	th := isoThursday(di)
	y, _, _ := civilFromDays(th)
	jan1 := daysFromCivil(y, 1)
	n := (th - floorToWeekday(jan1, 4)) / 7
	if weekdayOfDay(jan1) == 4 {
		n++
	}
	return int(n)
}

// Near the ends of the Date range, the start of the year (or the ISO week's
// Thursday) that %j, %U, %W, %V, %g and %G floor to can fall outside it; d3
// then computes with an Invalid Date and prints NaN.
func yearStartValid(jan1 int64) bool { return jan1 >= -100000000 }

func isoValid(di dayInfo) bool {
	th := isoThursday(di)
	if th < -100000000 || th > 100000000 {
		return false
	}
	y, _, _ := civilFromDays(th)
	return yearStartValid(daysFromCivil(y, 1))
}

func appendNaN(dst []byte, fill byte, width int) []byte {
	if width > 3 && fill != 0 {
		dst = append(dst, fill)
	}
	return append(dst, "NaN"...)
}

func (f *TimeFormat) appendZone(dst []byte, t float64) []byte {
	if f.zone.IsUTC() {
		return append(dst, "+0000"...)
	}
	z := f.zone.TimezoneOffset(t)
	sign := byte('-')
	if !(z > 0) {
		z = -z
		sign = '+'
	}
	dst = append(dst, sign)
	dst = appendPadFloat(dst, math.Trunc(z/60), '0', 2)
	return appendPadFloat(dst, math.Mod(z, 60), '0', 2)
}

// appendInvalid formats a directive for an Invalid Date. d3 pads NaN like any
// number; names come out as undefined, which Array.join turns into "".
func (f *TimeFormat) appendInvalid(dst []byte, op fmtOp) []byte {
	switch op.dir {
	case 'a', 'A', 'b', 'B':
		return dst
	case 'f':
		return append(dst, "NaN000"...)
	case 'I':
		return append(dst, "12"...) // NaN % 12 || 12
	case 'p':
		return append(dst, nameAt(f.loc.def.Periods, 0)...)
	case 'q':
		return append(dst, '1')
	case 'Z':
		if f.zone.IsUTC() {
			return append(dst, "+0000"...)
		}
		return append(dst, "+00NaN"...)
	case 'Y', 'G':
		// pad(NaN, fill, 4): "NaN" is only three characters long.
		if op.pad != 0 {
			dst = append(dst, op.pad)
		}
	}
	return append(dst, "NaN"...)
}

// appendPad is d3's pad(value, fill, width) for integers: a sign, then fill
// characters up to width digits.
func appendPad(dst []byte, v int, fill byte, width int) []byte {
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var tmp [24]byte
	digits := strconv.AppendInt(tmp[:0], int64(v), 10)
	if fill != 0 {
		for n := len(digits); n < width; n++ {
			dst = append(dst, fill)
		}
	}
	return append(dst, digits...)
}

func appendPadFloat(dst []byte, v float64, fill byte, width int) []byte {
	if math.IsNaN(v) {
		return append(dst, "NaN"...)
	}
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var tmp [32]byte
	digits := jsval.AppendJSNumber(tmp[:0], v)
	if fill != 0 {
		for n := len(digits); n < width; n++ {
			dst = append(dst, fill)
		}
	}
	return append(dst, digits...)
}

// ISOFormat is Date.prototype.toISOString: "YYYY-MM-DDTHH:mm:ss.sssZ", with
// a signed six-digit year outside 0..9999. ok is false for an Invalid Date
// (where JavaScript throws a RangeError).
func ISOFormat(t float64) (s string, ok bool) {
	if !Valid(t) {
		return "", false
	}
	fl := UTC.Fields(t)
	var b []byte
	switch {
	case fl.Year >= 0 && fl.Year <= 9999:
		b = appendPad(b, fl.Year, '0', 4)
	case fl.Year < 0:
		b = append(b, '-')
		b = appendPad(b, -fl.Year, '0', 6)
	default:
		b = append(b, '+')
		b = appendPad(b, fl.Year, '0', 6)
	}
	b = append(b, '-')
	b = appendPad(b, fl.Month+1, '0', 2)
	b = append(b, '-')
	b = appendPad(b, fl.Day, '0', 2)
	b = append(b, 'T')
	b = appendPad(b, fl.Hour, '0', 2)
	b = append(b, ':')
	b = appendPad(b, fl.Minute, '0', 2)
	b = append(b, ':')
	b = appendPad(b, fl.Second, '0', 2)
	b = append(b, '.')
	b = appendPad(b, fl.Millisecond, '0', 3)
	b = append(b, 'Z')
	return string(b), true
}
