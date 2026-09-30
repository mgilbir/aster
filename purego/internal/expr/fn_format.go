package expr

import (
	"context"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

var backgroundContext = context.Background()

// The formatting functions forward to the view's locale (Scope.Locale), as
// vega-functions' `locale[method](spec)(value)` wrappers do; a null value
// short-circuits to the string "null".

func init() {
	fn("format", func(s *Scope, args []jsval.Value) jsval.Value {
		out, err := s.locale().FormatValue(arg(args, 0), arg(args, 1))
		if err != nil {
			throw("Error", "%v", err)
		}
		return jsval.Str(out)
	})
	timeFmt := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			v := arg(args, 0)
			if v.IsTimestamp() || v.IsNum() || v.IsNull() {
				out, err := s.locale().TimeFormatValue(v, arg(args, 1), utc)
				if err != nil {
					throw("Error", "%v", err)
				}
				return jsval.Str(out)
			}
			// A non-date is converted with +value first (strings and arrays
			// through Number()).
			out, err := s.locale().TimeFormatValue(jsval.Num(s.num(v)), arg(args, 1), utc)
			if err != nil {
				throw("Error", "%v", err)
			}
			return jsval.Str(out)
		}
	}
	fn("timeFormat", timeFmt(false))
	fn("utcFormat", timeFmt(true))
	fn("timeParse", func(s *Scope, args []jsval.Value) jsval.Value {
		return s.locale().TimeParseValue(strArg(s, arg(args, 0)), arg(args, 1), false)
	})
	fn("utcParse", func(s *Scope, args []jsval.Value) jsval.Value {
		return s.locale().TimeParseValue(strArg(s, arg(args, 0)), arg(args, 1), true)
	})

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
