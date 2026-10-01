package format

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of upstream's own tests (see internal/upstream) for the number and time formatting this
// package ports: d3-format, d3-time, d3-time-format.

// zoneOf is the local calendar upstream's tests ran in.
func zoneOf(t *testing.T, r *upstream.Replay) Zone {
	t.Helper()
	loc, err := time.LoadLocation(r.File.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	return Local(loc)
}

// fnResult is the answer for a call that returns a function (or null): the recorded function marker
// when the engine also has one, null otherwise.
func fnResult(c *upstream.Call, ok bool) any {
	if !ok {
		return nil
	}
	if _, isFn := upstream.IsFunction(c.Result); isFn {
		return c.Result
	}
	return "function"
}

func TestUpstreamD3Format(t *testing.T) {
	r := upstream.Start(t, "d3-format")
	loc := DefaultNumberLocale()
	enUS := loc
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		switch c.Fn {
		case "formatDefaultLocale", "formatLocale":
			l, err := NumberLocaleFromValue(upstream.ToValue(c.Arg(0)))
			if err != nil {
				r.Skip("locale definitions the engine rejects")
				continue
			}
			if c.Fn == "formatDefaultLocale" {
				loc = l
			}
			r.Skip("constructions (a locale object is the answer)")
		case "formatLocale()", "formatDefaultLocale()":
			// formatLocale(definition).format(specifier)(value), and formatPrefix likewise
			l, err := NumberLocaleFromValue(upstream.ToValue(c.ConstructedWith[0]))
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			via := c.ViaSteps()
			switch {
			case len(via) == 0 && (c.Method == "format" || c.Method == "formatPrefix"):
				spec, ok := c.Arg(0).(string)
				if !ok {
					r.Skip("non-string specifier")
					continue
				}
				var err error
				if c.Method == "format" {
					_, err = l.Format(spec)
				} else {
					_, err = l.FormatPrefix(spec, upstream.Number(c.Arg(1)))
				}
				r.Check(c, fnResult(c, err == nil), err != nil)
			case len(via) == 1 && c.Method == "" && len(c.Args) == 1 && len(via[0].Args) >= 1:
				spec, ok := via[0].Args[0].(string)
				if !ok {
					r.Skip("non-string specifier")
					continue
				}
				if via[0].Method == "format" {
					nf, err := l.Format(spec)
					if err != nil {
						r.Check(c, nil, true)
						continue
					}
					r.Check(c, nf.FormatValue(upstream.ToValue(c.Args[0])), false)
				} else if via[0].Method == "formatPrefix" && len(via[0].Args) == 2 {
					pf, err := l.FormatPrefix(spec, upstream.Number(via[0].Args[1]))
					if err != nil {
						r.Check(c, nil, true)
						continue
					}
					r.Check(c, pf(upstream.Number(c.Args[0])), false)
				} else {
					r.Skip("unmapped locale step")
				}
			default:
				r.Skip("locale methods of another shape")
			}
		case "format":
			spec, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			_, err := loc.Format(spec)
			r.Check(c, fnResult(c, err == nil), err != nil)
		case "formatSpecifier()":
			// a FormatSpecifier a test went on changing, then asked for its string
			spec, ok := argAtCall(c.ConstructedWith, 0).(string)
			if !ok || c.Method != "toString" {
				r.Skip("specifier questions of another shape")
				continue
			}
			s, err := ParseSpecifier(spec)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			changed := true
			for _, step := range c.ChainSteps() {
				field, isSet := strings.CutPrefix(step.Method, "set:")
				if !isSet || !setSpecifierField(&s, field, step.Args[0]) {
					changed = false
					break
				}
			}
			if !changed {
				r.Skip("specifier changes the adapter does not map")
				continue
			}
			r.Check(c, s.String(), false)
		case "format()", "formatPrefix()":
			spec, ok := c.ConstructedWith[0].(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			var f func(jsval.Value) string
			if c.Fn == "format()" {
				nf, err := loc.Format(spec)
				if err != nil {
					r.Skip("format rejected (checked at construction)")
					continue
				}
				f = nf.FormatValue
			} else {
				v, _ := upstream.Num(c.ConstructedWith[1])
				pf, err := loc.FormatPrefix(spec, v)
				if err != nil {
					r.Skip("format rejected (checked at construction)")
					continue
				}
				f = func(x jsval.Value) string { return pf(jsval.ToNumber(x)) }
			}
			r.Check(c, f(upstream.ToValue(c.Arg(0))), false)
		case "formatPrefix":
			spec, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			v, _ := upstream.Num(c.Arg(1))
			_, err := loc.FormatPrefix(spec, v)
			r.Check(c, fnResult(c, err == nil), err != nil)
		case "formatSpecifier":
			spec, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			s, err := ParseSpecifier(spec)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			opt := func(x float64) any {
				if math.IsNaN(x) {
					return upstream.Undefined()
				}
				return upstream.Enc(x)
			}
			r.Check(c, map[string]any{
				"$class": "FormatSpecifier",
				"fill":   s.Fill, "align": string(s.Align), "sign": string(s.Sign), "symbol": s.Symbol,
				"zero": s.Zero, "width": opt(s.Width), "comma": s.Comma, "precision": opt(s.Precision),
				"trim": s.Trim, "type": s.Type,
			}, false)
		case "precisionFixed", "precisionPrefix", "precisionRound":
			a, _ := upstream.Num(c.Arg(0))
			b, _ := upstream.Num(c.Arg(1))
			var got float64
			switch c.Fn {
			case "precisionFixed":
				got = PrecisionFixed(a)
			case "precisionPrefix":
				got = PrecisionPrefix(a, b)
			default:
				got = PrecisionRound(a, b)
			}
			r.Check(c, upstream.Enc(got), false)
		default:
			r.Skip("unmapped " + c.Fn)
		}
	}
	_ = enUS
	r.Done(900)
}

// intervalOf resolves an exported d3-time interval name (timeDay, utcMonday, unixDay, ...).
func intervalOf(name string, local Zone) (Interval, bool) {
	z := UTC
	switch {
	case strings.HasPrefix(name, "time"):
		z = local
		name = name[len("time"):]
	case strings.HasPrefix(name, "utc"):
		name = name[len("utc"):]
	case name == "unixDay":
		return UTC.UnixDay(), true
	default:
		return Interval{}, false
	}
	switch name {
	case "Millisecond":
		return z.Millisecond(), true
	case "Second":
		return z.Second(), true
	case "Minute":
		return z.Minute(), true
	case "Hour":
		return z.Hour(), true
	case "Day":
		return z.Day(), true
	case "Week", "Sunday":
		return z.Week(0), true
	case "Monday":
		return z.Week(1), true
	case "Tuesday":
		return z.Week(2), true
	case "Wednesday":
		return z.Week(3), true
	case "Thursday":
		return z.Week(4), true
	case "Friday":
		return z.Week(5), true
	case "Saturday":
		return z.Week(6), true
	case "Month":
		return z.Month(), true
	case "Year":
		return z.Year(), true
	}
	return Interval{}, false
}

// intervalFromFunction resolves a recorded interval argument through its origin: an export, or the
// result of `<export>.every(k)`.
func intervalFromFunction(v any, local Zone) (Interval, bool) {
	origin, ok := upstream.IsFunction(v)
	if !ok || origin == nil {
		return Interval{}, false
	}
	if name, ok := origin["export"].(string); ok {
		return intervalOf(name, local)
	}
	from, _ := origin["from"].(string)
	if base, ok := strings.CutSuffix(from, ".every"); ok {
		iv, ok := intervalOf(base, local)
		if !ok {
			return Interval{}, false
		}
		args, _ := origin["args"].([]any)
		if len(args) != 1 {
			return Interval{}, false
		}
		k, ok := upstream.Num(args[0])
		if !ok {
			return Interval{}, false
		}
		return iv.Every(k)
	}
	return Interval{}, false
}

func TestUpstreamD3Time(t *testing.T) {
	r := upstream.Start(t, "d3-time")
	local := zoneOf(t, r)
	ctx := context.Background()
	date := func(v any) float64 {
		if d, ok := upstream.Date(v); ok {
			return d
		}
		if f, ok := upstream.Num(v); ok {
			return f
		}
		if v == nil {
			return 0
		}
		return math.NaN()
	}
	// d3 reads an absent step as 1; null counts as absent (step == null).
	step := func(v any) float64 {
		if v == nil || upstream.IsUndefined(v) {
			return 1
		}
		return jsval.ToNumber(upstream.ToValue(v))
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		if c.Fn == "timeTicks" || c.Fn == "utcTicks" {
			z := local
			if c.Fn == "utcTicks" {
				z = UTC
			}
			start, stop := date(c.Arg(0)), date(c.Arg(1))
			var got []float64
			var err error
			if n, isNum := upstream.Num(c.Arg(2)); isNum {
				got, err = z.Ticks(ctx, start, stop, n)
			} else if iv, ok := intervalFromFunction(c.Arg(2), local); ok {
				got, err = TicksWith(ctx, iv, start, stop)
			} else {
				r.Skip("tick interval the recording cannot identify")
				continue
			}
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, dates(got), false)
			continue
		}
		name, method, hasMethod := strings.Cut(c.Fn, ".")
		if strings.HasSuffix(c.Fn, ".every()") {
			// timeDay.every(k) is an interval of its own, asked like any other
			name, method, hasMethod = strings.TrimSuffix(c.Fn, ".every()"), c.Method, c.Method != ""
		}
		iv, ok := intervalOf(name, local)
		if !ok {
			r.Skip("custom intervals (timeInterval)")
			continue
		}
		if strings.HasSuffix(c.Fn, ".every()") {
			k := jsval.ToNumber(upstream.ToValue(c.ConstructedWith[0]))
			iv, ok = iv.Every(k)
			if !ok {
				r.Check(c, nil, true)
				continue
			}
		}
		if !hasMethod {
			// timeDay(date) is floor; with no argument, the pinned clock.
			t0 := date(c.Arg(0))
			if len(c.Args) == 0 {
				t0 = 1767225600000
			}
			r.Check(c, upstream.EncDate(iv.Floor(t0)), false)
			continue
		}
		switch method {
		case "floor":
			r.Check(c, upstream.EncDate(iv.Floor(date(c.Arg(0)))), false)
		case "ceil":
			r.Check(c, upstream.EncDate(iv.Ceil(date(c.Arg(0)))), false)
		case "round":
			r.Check(c, upstream.EncDate(iv.Round(date(c.Arg(0)))), false)
		case "offset":
			r.Check(c, upstream.EncDate(iv.Offset(date(c.Arg(0)), step(c.Arg(1)))), false)
		case "range":
			got, err := iv.Range(ctx, date(c.Arg(0)), date(c.Arg(1)), step(c.Arg(2)))
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, dates(got), false)
		case "count":
			n, ok := iv.Count(date(c.Arg(0)), date(c.Arg(1)))
			if !ok {
				r.Skip("count of a filtered interval")
				continue
			}
			r.Check(c, upstream.Enc(n), false)
		case "every":
			_, ok := iv.Every(jsval.ToNumber(upstream.ToValue(c.Arg(0))))
			r.Check(c, fnResult(c, ok), false)
		default:
			r.Skip("unmapped method " + method)
		}
	}
	r.Done(900)
}

func dates(ts []float64) []any {
	out := make([]any, len(ts))
	for i, t := range ts {
		out[i] = upstream.EncDate(t)
	}
	return out
}

func TestUpstreamD3TimeFormat(t *testing.T) {
	r := upstream.Start(t, "d3-time-format")
	local := zoneOf(t, r)
	loc := DefaultTimeLocale()
	timeOf := func(v any) float64 {
		if d, ok := upstream.Date(v); ok {
			return d
		}
		return jsval.ToNumber(upstream.ToValue(v))
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		var z Zone
		switch c.Fn {
		case "isoFormat":
			s, ok := ISOFormat(timeOf(c.Arg(0)))
			if !ok {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, s, false)
			continue
		case "isoParse":
			s, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string input")
				continue
			}
			tm, ok := loc.Parse("%Y-%m-%dT%H:%M:%S.%LZ", UTC).Parse(s)
			r.Check(c, parsedDate(tm, ok), false)
			continue
		case "timeFormatDefaultLocale", "timeFormatLocale":
			l, err := TimeLocaleFromValue(upstream.ToValue(c.Arg(0)))
			if err != nil {
				r.Skip("locale definitions the engine rejects")
				continue
			}
			if c.Fn == "timeFormatDefaultLocale" {
				loc = l
			}
			r.Skip("constructions (a locale object is the answer)")
			continue
		case "timeFormatLocale()", "timeFormatDefaultLocale()":
			// timeFormatLocale(definition).format(specifier)(date), and the utc and parse kin
			l, err := TimeLocaleFromValue(upstream.ToValue(c.ConstructedWith[0]))
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			via := c.ViaSteps()
			zoneFor := func(m string) Zone {
				if strings.HasPrefix(m, "utc") {
					return UTC
				}
				return local
			}
			switch {
			case len(via) == 0 && c.Method != "" && len(c.Args) == 1:
				if _, ok := c.Args[0].(string); !ok {
					r.Skip("non-string specifier")
					continue
				}
				r.Check(c, fnResult(c, true), false)
			case len(via) == 1 && c.Method == "" && len(c.Args) == 1 && len(via[0].Args) == 1:
				spec, ok := via[0].Args[0].(string)
				if !ok {
					r.Skip("non-string specifier")
					continue
				}
				z := zoneFor(via[0].Method)
				switch via[0].Method {
				case "format", "utcFormat":
					r.Check(c, l.Format(spec, z).FormatValue(upstream.ToValue(c.Args[0])), false)
				case "parse", "utcParse":
					s, ok := c.Args[0].(string)
					if !ok {
						r.Skip("non-string input")
						continue
					}
					tm, ok := l.Parse(spec, z).Parse(s)
					r.Check(c, parsedDate(tm, ok), false)
				default:
					r.Skip("unmapped locale step")
				}
			default:
				r.Skip("locale methods of another shape")
			}
			continue
		case "timeFormat", "utcFormat", "utcParse", "timeParse":
			spec, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			z = local
			if strings.HasPrefix(c.Fn, "utc") {
				z = UTC
			}
			if strings.HasSuffix(c.Fn, "Format") {
				_ = loc.Format(spec, z)
			} else {
				_ = loc.Parse(spec, z)
			}
			r.Check(c, fnResult(c, true), false)
			continue
		case "timeFormat()", "utcFormat()", "utcParse()", "timeParse()":
			spec, ok := c.ConstructedWith[0].(string)
			if !ok {
				r.Skip("non-string specifier")
				continue
			}
			z = local
			if strings.HasPrefix(c.Fn, "utc") {
				z = UTC
			}
			if strings.HasSuffix(c.Fn, "Format()") {
				r.Check(c, loc.Format(spec, z).FormatValue(upstream.ToValue(c.Arg(0))), false)
				continue
			}
			s, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("non-string input")
				continue
			}
			tm, ok := loc.Parse(spec, z).Parse(s)
			r.Check(c, parsedDate(tm, ok), false)
		default:
			r.Skip("unmapped " + c.Fn)
		}
	}
	r.Done(550)
}

func parsedDate(t float64, ok bool) any {
	if !ok {
		return nil
	}
	return upstream.EncDate(t)
}

// setSpecifierField is `specifier[field] = v` on d3's FormatSpecifier.
func setSpecifierField(s *Specifier, field string, v any) bool {
	val := upstream.ToValue(v)
	switch field {
	case "fill":
		s.Fill = val.AsString()
	case "align":
		s.Align = val.AsString()[0]
	case "sign":
		s.Sign = val.AsString()[0]
	case "symbol":
		s.Symbol = val.AsString()
	case "zero":
		s.Zero = val.IsTruthy()
	case "width":
		s.Width = upstream.Number(v)
	case "comma":
		s.Comma = val.IsTruthy()
	case "precision":
		s.Precision = upstream.Number(v)
	case "trim":
		s.Trim = val.IsTruthy()
	case "type":
		s.Type = val.AsString()
	default:
		return false
	}
	return true
}
