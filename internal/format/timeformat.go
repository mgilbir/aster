package format

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

// fmtOp is one step of a format: literal text, or a directive with its
// padding modifier. A %c, %x or %X is a directive that formats the locale's
// pattern (ref).
type fmtOp struct {
	dir byte   // directive letter, or 0 for a literal
	pad byte   // fill for numeric directives: '0', ' ' or 0 (no padding)
	lit string // literal text (dir == 0)
}

func (op fmtOp) ref() bool { return op.dir == 'c' || op.dir == 'x' || op.dir == 'X' }

// TimeFormat is a d3 time format function (timeFormat or utcFormat). It
// keeps its specifier and reads it step by step each time it formats, as
// d3's newFormat does: a specifier of a million directives costs its own
// bytes, not a compiled copy many times their size, and a %c refers to the
// locale's pattern rather than copying it.
type TimeFormat struct {
	loc  *TimeLocale
	zone Zone
	spec string
	ops  []fmtOp // the compiled steps; nil for a specifier past maxCompiledSpec
	// maxOut bounds the bytes one date formats to; thrown is the exception
	// every call throws (a %c, %x or %X whose pattern d3 cannot format).
	maxOut int
	thrown *jsval.Thrown
}

// Format is d3.timeFormat(specifier) in the given zone (UTC for utcFormat).
func (l *TimeLocale) Format(specifier string, z Zone) *TimeFormat {
	f := &TimeFormat{loc: l, zone: z, spec: specifier}
	for i := 0; i < len(specifier); {
		var op fmtOp
		op, i = nextFmtOp(specifier, i)
		if op.ref() && f.thrown == nil {
			f.thrown = l.pat[patternIndex(op.dir)].thrown
		}
	}
	f.maxOut = l.maxOutput(specifier)
	if len(specifier) <= maxCompiledSpec {
		f.ops = compileFormat(specifier)
	}
	return f
}

// compileFormat is the steps of spec, read once.
func compileFormat(spec string) []fmtOp {
	ops := make([]fmtOp, 0, 2*strings.Count(spec, "%")+1)
	for i := 0; i < len(spec); {
		var op fmtOp
		op, i = nextFmtOp(spec, i)
		ops = append(ops, op)
	}
	return ops
}

// nextFmtOp reads the step of spec that starts at i (i < len(spec)) and
// returns where the next starts. A literal runs to the next "%"; %c, %x and
// %X ignore padding modifiers.
func nextFmtOp(spec string, i int) (fmtOp, int) {
	if spec[i] != '%' {
		j := strings.IndexByte(spec[i:], '%')
		if j < 0 {
			return fmtOp{lit: spec[i:]}, len(spec)
		}
		return fmtOp{lit: spec[i : i+j]}, i + j
	}
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
		return fmtOp{}, i
	}
	r, w := utf8.DecodeRuneInString(spec[i:])
	c := spec[i]
	i += w
	if w > 1 || r == utf8.RuneError {
		return fmtOp{lit: spec[i-w : i]}, i // unknown directive: just the character
	}
	if !havePad {
		pad = '0'
		if c == 'e' {
			pad = ' '
		}
	}
	switch c {
	case 'a', 'A', 'b', 'B', 'c', 'd', 'e', 'f', 'g', 'G', 'H', 'I', 'j', 'L', 'm', 'M', 'p', 'q', 'Q', 's', 'S',
		'u', 'U', 'V', 'w', 'W', 'x', 'X', 'y', 'Y', 'Z':
		return fmtOp{dir: c, pad: pad}, i
	case '%':
		return fmtOp{lit: "%"}, i
	}
	return fmtOp{lit: spec[i-1 : i]}, i // unknown directive: the letter without "%"
}

// String is the specifier text.
func (f *TimeFormat) String() string { return f.spec }

// Format formats the instant t. An Invalid Date formats as d3 does (numeric
// directives print "NaN", names print nothing). It panics with the
// *jsval.Thrown d3 throws for a format that uses a locale pattern d3 cannot
// format (see Err); AppendFormatErr returns it instead.
func (f *TimeFormat) Format(t float64) string {
	var buf [64]byte
	return string(f.AppendFormat(buf[:0], t))
}

// FormatValue formats a dynamic value the way d3's format function does:
// anything that is not a Date is converted with +value first.
func (f *TimeFormat) FormatValue(v jsval.Value) string { return f.Format(jsval.ToNumber(v)) }

// AppendFormat appends the formatted instant to dst; it panics as Format
// does.
func (f *TimeFormat) AppendFormat(dst []byte, t float64) []byte {
	out, err := f.AppendFormatErr(dst, t)
	if err != nil {
		panic(err)
	}
	return out
}

// AppendFormatErr appends the formatted instant to dst, or returns the
// exception d3 throws (Err).
func (f *TimeFormat) AppendFormatErr(dst []byte, t float64) ([]byte, error) {
	out, _, err := f.appendMax(dst, t, math.MaxInt)
	return out, err
}

// Err is the *jsval.Thrown d3 throws whenever the format is called: it uses
// a %c, %x or %X whose locale pattern is not one d3 can format (missing, not
// a string, or using itself in a cycle). Nil for every other format.
func (f *TimeFormat) Err() error {
	if f.thrown != nil {
		return f.thrown
	}
	return nil
}

// MaxOutput bounds the bytes the format writes for any date.
func (f *TimeFormat) MaxOutput() int { return f.maxOut }

// fmtDate is the date a format writes: TimeClip'd, as d3's
// `new Date(+date)` makes it, and its calendar fields in the zone.
type fmtDate struct {
	t     float64
	valid bool
	fl    Fields
	// iso is dISO(date), the Thursday of its ISO week, which %g, %G and
	// %V each use, computed by the first of them (hasISO).
	iso    float64
	hasISO bool
}

func (d *fmtDate) isoThursday(z Zone) float64 {
	if !d.hasISO {
		d.iso, d.hasISO = z.isoThursday(d.t, d.fl.Weekday), true
	}
	return d.iso
}

// appendMax is AppendFormatErr that stops, with ok false, once dst passes
// max bytes: before a literal (of the specifier, as long as it is) would, or
// after a directive did (one writes a name of at most MaxLocaleName bytes,
// or a number).
func (f *TimeFormat) appendMax(dst []byte, t float64, max int) (out []byte, ok bool, err error) {
	if f.thrown != nil {
		return dst, false, f.thrown
	}
	d := fmtDate{t: timeClip(t)}
	if d.valid = Valid(d.t); d.valid {
		d.fl = f.zone.Fields(d.t)
	}
	start := len(dst)
	dst, ok = f.appendOps(dst, f.spec, f.ops, &d, max)
	if f.loc.halves {
		// Names that end and begin with half a character join into one,
		// as Array.prototype.join joins them.
		dst = jsval.PairSurrogates(dst, start)
	}
	return dst, ok, nil
}

// appendOps writes the steps of spec; a %c, %x or %X writes its locale
// pattern's. That recursion is at most three deep: a pattern that refers to
// itself, through the others or not, throws (TimeLocale.compilePatterns),
// and a format that uses one never runs. A specifier of up to
// maxCompiledSpec bytes, and every locale pattern, is read from its compiled
// steps (ops); a longer one is read step by step from its text, so it costs
// no compiled copy many times its size.
func (f *TimeFormat) appendOps(dst []byte, spec string, ops []fmtOp, d *fmtDate, max int) ([]byte, bool) {
	ok := true
	if ops != nil || spec == "" {
		for _, op := range ops {
			if dst, ok = f.appendOp(dst, op, d, max); !ok {
				break
			}
		}
		return dst, ok
	}
	for i := 0; i < len(spec) && ok; {
		var op fmtOp
		op, i = nextFmtOp(spec, i)
		dst, ok = f.appendOp(dst, op, d, max)
	}
	return dst, ok
}

// maxCompiledSpec is the longest specifier compiled to steps: they take
// fmtOpBytes each, up to one for each byte, so a longer one, which only a
// specification written to be long has, is read from its text instead.
const maxCompiledSpec = 1024

// appendOp writes one step.
func (f *TimeFormat) appendOp(dst []byte, op fmtOp, d *fmtDate, max int) ([]byte, bool) {
	l := f.loc
	fl := &d.fl
	if len(dst)+len(op.lit) > max || len(dst) > max {
		return dst, false // a directive writes a name or a number: dst is at most that past max
	}
	if op.dir == 0 {
		return append(dst, op.lit...), true
	}
	if op.ref() {
		p := &l.pat[patternIndex(op.dir)]
		return f.appendOps(dst, p.text, p.fmt, d, max)
	}
	if !d.valid {
		return f.appendInvalid(dst, op), true
	}
	{
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
		case 'g':
			dst = appendPadNum(dst, jsRem(f.zone.yearOf(d.isoThursday(f.zone)), 100), op.pad, 2)
		case 'G':
			dst = appendPadNum(dst, jsRem(f.zone.yearOf(d.isoThursday(f.zone)), 10000), op.pad, 4)
		case 'H':
			dst = appendPad(dst, fl.Hour, op.pad, 2)
		case 'I':
			h := fl.Hour % 12
			if h == 0 {
				h = 12
			}
			dst = appendPad(dst, h, op.pad, 2)
		case 'j':
			dst = appendPadNum(dst, 1+f.zone.count(kDay, 0, f.zone.Year().Floor(d.t), d.t), op.pad, 3)
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
			dst = jsval.AppendJSNumber(dst, d.t)
		case 's':
			dst = jsval.AppendJSNumber(dst, math.Floor(d.t/1000))
		case 'S':
			dst = appendPad(dst, fl.Second, op.pad, 2)
		case 'u':
			wd := fl.Weekday
			if wd == 0 {
				wd = 7
			}
			dst = strconv.AppendInt(dst, int64(wd), 10)
		case 'U':
			dst = appendPadNum(dst, f.zone.count(kWeek, 0, f.zone.Year().Floor(d.t)-1, d.t), op.pad, 2)
		case 'V':
			dst = appendPadNum(dst, f.zone.isoWeekNumber(d.isoThursday(f.zone)), op.pad, 2)
		case 'w':
			dst = strconv.AppendInt(dst, int64(fl.Weekday), 10)
		case 'W':
			dst = appendPadNum(dst, f.zone.count(kWeek, 1, f.zone.Year().Floor(d.t)-1, d.t), op.pad, 2)
		case 'y':
			dst = appendPad(dst, fl.Year%100, op.pad, 2)
		case 'Y':
			dst = appendPad(dst, fl.Year%10000, op.pad, 4)
		case 'Z':
			dst = f.appendZone(dst, d.t)
		}
	}
	return dst, len(dst) <= max
}

// The day and week numbers are d3's, through the same intervals d3 counts
// with (C120 of the 2026-10-18 audit): an interval's count corrects a span
// by the change of the zone's offset truncated to whole minutes, as
// getTimezoneOffset is, so in a year whose offset changed by seconds (a zone
// leaving local mean time) a count is one less than the civil difference,
// in d3 and here alike.

// count is the d3 interval of kind (a day, or a week starting on weekday)
// counted from a to b.
func (z Zone) count(kind unitKind, weekday int, a, b float64) float64 {
	iv := Interval{z: z, kind: kind, weekday: weekday}
	n, _ := iv.Count(a, b)
	return n
}

// isoThursday is d3-time-format's dISO(d): the Thursday of the date's ISO
// week, timeThursday(d) from Thursday to Sunday, timeThursday.ceil(d) from
// Monday to Wednesday.
func (z Zone) isoThursday(t float64, weekday int) float64 {
	th := z.Week(4)
	if weekday >= 4 || weekday == 0 {
		return th.Floor(t)
	}
	return th.Ceil(t)
}

// isoWeekNumber is %V: timeThursday.count(timeYear(d), d) +
// (timeYear(d).getDay() === 4) of d = dISO(date).
func (z Zone) isoWeekNumber(d float64) float64 {
	y := z.Year().Floor(d)
	n := z.count(kWeek, 4, y, d)
	if Valid(y) && z.Fields(y).Weekday == 4 {
		n++
	}
	return n
}

// yearOf is getFullYear: NaN for an Invalid Date.
func (z Zone) yearOf(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	return float64(z.Fields(t).Year)
}

// jsRem is JavaScript's %: the sign of the dividend, NaN for NaN.
func jsRem(a, b float64) float64 { return math.Mod(a, b) }

// appendPadNum is d3's pad(value, fill, width) for a count that may be NaN.
func appendPadNum(dst []byte, v float64, fill byte, width int) []byte {
	if math.IsNaN(v) {
		return appendNaN(dst, fill, width)
	}
	return appendPad(dst, int(v), fill, width)
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
	if t = timeClip(t); !Valid(t) {
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
