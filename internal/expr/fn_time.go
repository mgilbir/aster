package expr

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// dateOf is `new Date(v)` with a single argument: a Date is copied, anything
// else goes through ToPrimitive; a string is parsed with Date.parse and the
// rest becomes a time value.
func (s *Scope) dateOf(v jsval.Value) float64 {
	switch v.Kind() {
	case jsval.KindTimestamp:
		return v.NumValue()
	case jsval.KindNum:
		return format.TimeClip(v.NumValue())
	}
	p := s.primitive(v, false)
	if p.IsStr() {
		return format.ParseDate(p.StrValue(), s.zone())
	}
	return format.TimeClip(s.num(p))
}

// dateFromFields is `new Date(y, m, ...)` and Date.UTC(y, m, ...): the fields
// after the month default to (1, 0, 0, 0, 0) and a year of 0 to 99 means 1900
// to 1999.
func (s *Scope) dateFromFields(args []jsval.Value, utc bool) float64 {
	if len(args) > 7 {
		args = args[:7]
	}
	var f [7]float64
	for i, a := range args {
		f[i] = s.num(a)
	}
	if utc {
		return format.UTCArgs(f[:len(args)]...)
	}
	return s.zone().DatetimeArgs(f[:len(args)]...)
}

// dateFn defines a date getter: upstream generates `new Date(a).getMonth()`
// from the first argument alone, so the others are never evaluated.
func dateFn(name string, f builtinFn) { def(name, &funcDef{min: 1, argc: 1, fn: f}) }

func init() {
	getters := map[string]func(f format.Fields) int{
		"date":         func(f format.Fields) int { return f.Day },
		"day":          func(f format.Fields) int { return f.Weekday },
		"year":         func(f format.Fields) int { return f.Year },
		"month":        func(f format.Fields) int { return f.Month },
		"hours":        func(f format.Fields) int { return f.Hour },
		"minutes":      func(f format.Fields) int { return f.Minute },
		"seconds":      func(f format.Fields) int { return f.Second },
		"milliseconds": func(f format.Fields) int { return f.Millisecond },
	}
	for name, get := range getters {
		get := get
		field := func(z format.Zone, t float64) jsval.Value {
			if !format.Valid(t) {
				return jsval.Num(math.NaN())
			}
			return jsval.Int(get(z.Fields(t)))
		}
		dateFn(name, func(s *Scope, args []jsval.Value) jsval.Value { return field(s.zone(), s.dateOf(args[0])) })
		dateFn("utc"+name, func(s *Scope, args []jsval.Value) jsval.Value { return field(format.UTC, s.dateOf(args[0])) })
	}
	dateFn("time", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Num(s.dateOf(args[0])) })
	dateFn("timezoneoffset", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.zone().TimezoneOffset(s.dateOf(args[0])))
	})
	fn("now", func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Num(s.nowMs()) })
	fn("utc", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.dateFromFields(args, true))
	})
	fn("datetime", func(s *Scope, args []jsval.Value) jsval.Value {
		switch len(args) {
		case 0:
			return jsval.Timestamp(format.TimeClip(s.nowMs()))
		case 1:
			return jsval.Timestamp(s.dateOf(args[0]))
		}
		return jsval.Timestamp(s.dateFromFields(args, false))
	})
	// quarter is `1 + ~~(new Date(d).getMonth() / 3)`, so an Invalid Date
	// (month NaN, ~~NaN = 0) is quarter 1.
	quarter := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			t := s.dateOf(arg(args, 0))
			z := s.zone()
			if utc {
				z = format.UTC
			}
			if !format.Valid(t) {
				return jsval.Num(1)
			}
			return jsval.Int(1 + z.Fields(t).Month/3)
		}
	}
	fn("quarter", quarter(false))
	fn("utcquarter", quarter(true))
	fn("week", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.zone().WeekOfYear(s.dateOf(arg(args, 0))))
	})
	fn("utcweek", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(format.UTC.WeekOfYear(s.dateOf(arg(args, 0))))
	})
	fn("isoweek", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.zone().ISOWeekOfYear(s.dateOf(arg(args, 0))))
	})
	fn("utcisoweek", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(format.UTC.ISOWeekOfYear(s.dateOf(arg(args, 0))))
	})
	fn("dayofyear", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.zone().DayOfYear(s.dateOf(arg(args, 0))))
	})
	fn("utcdayofyear", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(format.UTC.DayOfYear(s.dateOf(arg(args, 0))))
	})

	// timeOffset(unit, date, step) and timeSequence(unit, start, stop, step) are
	// d3 interval methods; an unknown unit gives undefined.
	offset := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			z := s.zone()
			if utc {
				z = format.UTC
			}
			var step float64
			stepV := arg(args, 2)
			if stepV.IsNullish() {
				step = 1
			} else {
				step = s.num(stepV)
			}
			ctx := s.Context
			if ctx == nil {
				ctx = backgroundContext
			}
			t, ok, err := format.OffsetUnit(ctx, z, s.str(arg(args, 0)), s.num(arg(args, 1)), math.Floor(step))
			if err != nil {
				panic(&cancelled{err}) // a quarter offset's walk stopped with the call
			}
			if !ok {
				return jsval.Undefined
			}
			return jsval.Timestamp(t)
		}
	}
	fn("timeOffset", offset(false))
	fn("utcOffset", offset(true))
	sequence := func(utc bool) builtinFn {
		return func(s *Scope, args []jsval.Value) jsval.Value {
			z := s.zone()
			if utc {
				z = format.UTC
			}
			step := 1.0
			if sv := arg(args, 3); !sv.IsNullish() {
				step = math.Floor(s.num(sv))
			}
			ctx := s.Context
			if ctx == nil {
				ctx = backgroundContext
			}
			// Each date is charged as the array grows, before the next is
			// made (C106): a sequence the string budget cannot hold stops
			// there rather than after millions of dates are built.
			b := itemBuilder{s: s}
			ok, err := format.SequenceUnitEach(ctx, z, s.str(arg(args, 0)), s.num(arg(args, 1)), s.num(arg(args, 2)), step,
				func(t float64) { b.push(jsval.Timestamp(t)) })
			switch {
			case errors.Is(err, budget.ErrLimit):
				// MaxRangeLen is the engine's bound: it ends the render (C43).
				panic(&limitPanic{err})
			case err != nil:
				panic(&cancelled{err})
			case !ok:
				return jsval.Undefined
			}
			return jsval.Arr(b.items)
		}
	}
	fn("timeSequence", sequence(false))
	fn("utcSequence", sequence(true))
	fn("timeUnitSpecifier", func(s *Scope, args []jsval.Value) jsval.Value {
		units := arg(args, 0)
		s.chargeItems(units.Len())
		s.work(units.Len())
		names := make([]string, 0, units.Len())
		for _, u := range units.Items() {
			names = append(names, s.str(u))
		}
		spec, err := format.TimeUnitSpecifier(names, format.UnitSpecifiersFromValue(s.walker(), arg(args, 1)))
		if err != nil {
			failFormat(err)
		}
		return jsval.Str(spec)
	})
}
