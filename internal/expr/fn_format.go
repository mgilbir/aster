package expr

import (
	"context"
	"math"
	"unsafe"

	"github.com/mgilbir/aster/internal/jsval"
)

var backgroundContext = context.Background()

// The formatting functions forward to the view's locale (Scope.Locale), as
// vega-functions' `locale[method](spec)(value)` wrappers do; a null value
// short-circuits to the string "null".

func init() {
	fn("format", func(s *Scope, args []jsval.Value) jsval.Value {
		// d3 reads the specifier with String(); a number format writes at
		// most its width in fill characters (format.MaxWidth) besides the
		// number, a few MB with a locale's numerals, so it is charged once
		// made.
		spec := s.specifier(jsval.Str(s.str(arg(args, 1))))
		var buf [64]byte // a label fits: the string is made once, at its length
		out, err := s.locale().AppendFormatValue(s.walker(), buf[:0], arg(args, 0), spec)
		if err != nil {
			failFormat(err)
		}
		return jsval.Str(s.keepFormatted(out, true))
	})
	timeFmt := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			v := arg(args, 0)
			if !(v.IsTimestamp() || v.IsNum() || v.IsNull()) {
				// A non-date is converted with +value first (strings and
				// arrays through Number()).
				v = jsval.Num(s.num(v))
			}
			// A time format writes as much as its specifier asks (each %c
			// a locale pattern), so it stops at the room the string budget
			// has left.
			var buf [64]byte
			out, ok, err := s.locale().AppendTimeFormatValue(s.walker(), buf[:0], v, s.specifier(arg(args, 1)), utc, s.formatRoom())
			if err != nil {
				failFormat(err)
			}
			return jsval.Str(s.keepFormatted(out, ok))
		}
	}
	fn("timeFormat", timeFmt(false))
	fn("utcFormat", timeFmt(true))
	parse := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			in := strArg(s, arg(args, 0))
			if in.IsStr() {
				s.workBytes(len(in.StrValue())) // a pass over the input
			}
			out, err := s.locale().TimeParseValue(s.walker(), in, s.specifier(jsval.Str(s.str(arg(args, 1)))), utc)
			if err != nil {
				failFormat(err)
			}
			return out
		}
	}
	fn("timeParse", parse(false))
	fn("utcParse", parse(true))

	// monthFormat(month) and friends format a fixed year-2000 date through
	// timeFormat; a non-integer argument gives "".
	named := func(spec string, date func(s *Scope, v jsval.Value) (mo, d float64, ok bool)) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			mo, d, ok := date(s, arg(args, 0))
			if !ok {
				return jsval.Str("")
			}
			l := s.locale()
			t := l.Local.Date(2000, mo, d, 0, 0, 0, 0)
			return jsval.Str(l.TimeFormat(spec).Format(t))
		}
	}
	isInt := func(v jsval.Value) bool {
		return v.IsNum() && v.NumValue() == math.Trunc(v.NumValue()) && !math.IsInf(v.NumValue(), 0)
	}
	month := func(s *Scope, v jsval.Value) (float64, float64, bool) { return v.NumValue(), 1, isInt(v) }
	// dayFormat(day) formats date 2+day of January 2000, computed with +.
	day := func(s *Scope, v jsval.Value) (float64, float64, bool) {
		d := s.add(jsval.Num(2), v)
		return 0, d.NumValue(), isInt(d)
	}
	fn("monthFormat", named("%B", month))
	fn("monthAbbrevFormat", named("%b", month))
	fn("dayFormat", named("%A", day))
	fn("dayAbbrevFormat", named("%a", day))
}

// strArg keeps a string as is and converts null to itself; anything else is
// left for the parser's own String() conversion.
func strArg(s *Scope, v jsval.Value) jsval.Value {
	if v.IsStr() || v.IsNull() {
		return v
	}
	return jsval.Str(s.str(v))
}

// specifier charges what reading a format specifier costs: it is compiled
// (into steps about its size, %c expanded), and an invalid one is quoted in
// the error, so a long one is charged as a string of its length, and its
// pass as work. A multi-format object is left as it is.
func (s *Scope) specifier(v jsval.Value) jsval.Value {
	if v.IsStr() {
		n := len(v.StrValue())
		s.chargeBytes(n)
		s.workBytes(n)
	}
	return v
}

// formatRoom is how many bytes a formatted string may take: what the string
// budget has left, and no more bytes than a string of MaxStringLength code
// units can hold, plus the smallString bytes a string has free.
func (s *Scope) formatRoom() int {
	return int(min(s.Strings.room(), 3*MaxStringLength) + smallString)
}

// keepFormatted checks and charges a string a formatter made, which complete
// is false for when the formatter stopped at formatRoom, and returns it as a
// string of its own (b is the caller's buffer, often on its stack).
func (s *Scope) keepFormatted(b []byte, complete bool) string {
	if !complete {
		if s.Strings.room() < 3*MaxStringLength {
			exceeded("the expression string budget of this render is used up")
		}
		exceeded("a formatted string of more than %d bytes exceeds the limit of %d UTF-16 code units", 3*MaxStringLength, MaxStringLength)
	}
	s.checkString(unsafe.String(unsafe.SliceData(b), len(b)))
	s.chargeBytes(len(b)) // before the string is made
	s.workBytes(len(b))
	return string(b)
}
