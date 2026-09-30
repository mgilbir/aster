package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

func init() {
	fn("isArray", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsArr()) })
	fn("isBoolean", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsBool()) })
	fn("isDate", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsTimestamp()) })
	fn("isDefined", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(!arg(args, 0).IsUndefined()) })
	fn("isNumber", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsNum()) })
	fn("isObject", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(isObjectLike(arg(args, 0))) })
	fn("isRegExp", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsPattern()) })
	fn("isString", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(arg(args, 0).IsStr()) })
	// isValid is `_ != null && _ === _`: not null, undefined or NaN.
	fn("isValid", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		return jsval.Bool(!v.IsNullish() && !(v.IsNum() && math.IsNaN(v.NumValue())))
	})
	fn("isTuple", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.tuples == nil {
			return jsval.False
		}
		return jsval.Bool(s.tuples.IsTuple(arg(args, 0)))
	})

	// toBoolean is vega-util's: null/undefined/"" give null, "false" and "0"
	// and falsy values give false, everything else true.
	fn("toBoolean", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		if v.IsNullish() || v.IsStr() && v.StrValue() == "" {
			return jsval.Null
		}
		if !v.IsTruthy() || v.IsStr() && (v.StrValue() == "false" || v.StrValue() == "0") {
			return jsval.False
		}
		return jsval.True
	})
	// toDate returns a Date unchanged, a number unchanged (not a Date!), and
	// Date.parse(v), a number, for anything else.
	fn("toDate", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		switch {
		case v.IsNullish() || v.IsStr() && v.StrValue() == "":
			return jsval.Null
		case v.IsNum() || v.IsTimestamp():
			return v
		}
		return jsval.Num(format.ParseDate(s.str(v), s.zone()))
	})
	fn("toNumber", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		if v.IsNullish() || v.IsStr() && v.StrValue() == "" {
			return jsval.Null
		}
		return jsval.Num(s.num(v))
	})
	fn("toString", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		if v.IsNullish() || v.IsStr() && v.StrValue() == "" {
			return jsval.Null
		}
		return jsval.Str(s.str(s.primitive(v, false)))
	})
}
