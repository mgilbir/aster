package scale

import (
	"errors"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// ErrIntervalString is returned when an interval string is given to a scale
// that is not a time or utc scale.
var ErrIntervalString = errors.New("Only time and utc scales accept interval strings.")

// Format is a label formatter: it maps a value (number, date, string, ...) to
// its text. Like vega-scale's formatters it answers a jsval.Value because a
// formatter may return an array of lines or null.
type Format func(v jsval.Value) jsval.Value

func strFormat(f func(float64) string) Format {
	return func(v jsval.Value) jsval.Value { return jsval.Str(f(jsval.ToNumber(v))) }
}

// defaultFormatter is vega-scale's `String(value)`, mapping over arrays.
func defaultFormatter(v jsval.Value) jsval.Value {
	if v.IsArr() {
		items := v.Items()
		out := make([]jsval.Value, len(items))
		for i, it := range items {
			out[i] = jsval.Str(it.AsString())
		}
		return jsval.Arr(out)
	}
	return jsval.Str(v.AsString())
}

// TickCountFor is vega-scale's tickCount(scale, count, minStep): it resolves a
// specification's `count` (a number, an interval name, or {interval, step}) to
// the argument scale.ticks expects. A numeric count is capped by the number of
// bins and, when minStep is given, by the domain span divided by minStep and by
// the largest count whose d3 tick step is not below minStep. The zone selects
// the calendar for interval names of time scales.
func TickCountFor(s Scale, count jsval.Value, minStep *float64) (TickCount, error) {
	var out TickCount
	bins := binsOf(s)
	step := 0.0
	if count.IsNum() {
		n := count.NumValue()
		if bins != nil {
			n = math.Max(n, float64(len(bins)))
		}
		if minStep != nil {
			d := s.Domain()
			if len(d) > 0 {
				d0, dn := jsval.ToNumber(d[0]), jsval.ToNumber(d[len(d)-1])
				lo, hi := math.Min(d0, dn), math.Max(d0, dn)
				span := (hi - lo) / *minStep
				if span == 0 || span != span {
					span = 1
				}
				n = math.Min(n, math.Floor(span)+1)
				if bins == nil && !IsLogarithmic(s.Type()) && !IsTemporal(s.Type()) && lo < hi {
					// d3 tick steps grow monotonically as the count shrinks
					for n > 1 && TickStep(lo, hi, n) < *minStep {
						n--
					}
				}
			}
		}
		return Count(n), nil
	}
	if count.IsObj() || count.IsArr() {
		if sv := count.Get("step"); sv.IsTruthy() {
			step = jsval.ToNumber(sv)
		}
		count = count.Get("interval")
	}
	if count.IsStr() {
		var z format.Zone
		switch s.Type() {
		case TypeTime:
			z = zoneOf(s)
		case TypeUTC:
			z = format.UTC
		default:
			return out, ErrIntervalString
		}
		iv, ok := format.IntervalFor(z, count.StrValue())
		if !ok {
			// vega-time answers undefined for an unknown unit, so the ticks
			// fall back to the default count - unless a step was given, in
			// which case upstream calls undefined.every() and throws.
			if step != 0 {
				return out, errors.New("Unrecognized interval: " + count.StrValue())
			}
			return out, nil
		}
		if step != 0 {
			if e, ok := iv.Every(step); ok {
				iv = e
			} else {
				return out, nil
			}
		}
		return IntervalCount(iv), nil
	}
	if count.IsNum() {
		return Count(count.NumValue()), nil
	}
	return out, nil
}

func zoneOf(s Scale) format.Zone {
	if t, ok := s.(*Time); ok {
		return t.zone
	}
	return LocalZone
}

func binsOf(s Scale) []float64 {
	if t, ok := s.(Typed); ok {
		return t.Bins()
	}
	return nil
}

// ValidTicks is vega-scale's validTicks: it keeps the tick values whose scaled
// position lies within the scale range, orders them along the range, and -
// when count is a positive number - thins them (dropping every other tick)
// until at most count remain, always keeping the end points.
func ValidTicks(s Scale, ticks []jsval.Value, count TickCount) []jsval.Value {
	rng := s.Range()
	var lo, hi float64
	if len(rng) > 0 {
		lo, hi = jsval.ToNumber(rng[0]), jsval.ToNumber(rng[len(rng)-1])
	} else {
		lo, hi = math.NaN(), math.NaN()
	}
	descending := false
	if lo > hi {
		lo, hi = hi, lo
		descending = true
	}
	lo, hi = math.Floor(lo), math.Ceil(hi)

	type tv struct {
		v jsval.Value
		p float64
	}
	kept := make([]tv, 0, len(ticks))
	for _, v := range ticks {
		p := jsval.ToNumber(s.Apply(v))
		if lo <= p && p <= hi {
			kept = append(kept, tv{v, p})
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if descending {
			return kept[j].p < kept[i].p
		}
		return kept[i].p < kept[j].p
	})
	out := make([]jsval.Value, len(kept))
	for i, k := range kept {
		out[i] = k.v
	}
	if count.HasN && count.N > 0 && len(out) > 1 {
		endpoints := []jsval.Value{out[0], out[len(out)-1]}
		for float64(len(out)) > count.N && len(out) >= 3 {
			thinned := out[:0:0]
			for i, v := range out {
				if i%2 == 0 {
					thinned = append(thinned, v)
				}
			}
			out = thinned
		}
		if len(out) < 3 {
			out = endpoints
		}
	}
	return out
}

// TickValues is vega-scale's tickValues: the bins (validated) for a binned
// scale, else scale.ticks(count), else the whole domain.
func TickValues(s Scale, count TickCount) []jsval.Value {
	if bins := binsOf(s); bins != nil {
		return ValidTicks(s, numsToValues(bins), count)
	}
	if t, ok := s.(Ticker); ok {
		ticks := t.Ticks(count)
		if IsTemporal(s.Type()) {
			return numsToTimestamps(ticks)
		}
		return numsToValues(ticks)
	}
	return s.Domain()
}

// logKeep is vega-scale's tickLog test: whether a log-scale label is dense
// enough to show (d3's loggish tickFormat criterion). count is the requested
// tick count (0 when unspecified).
func logKeep(s Scale, count TickCount) func(d float64) bool {
	// An unspecified count reads as 0 here (upstream sees null, which is 0 in
	// arithmetic; undefined would give NaN and hide every label).
	n := 0.0
	if count.HasN {
		n = count.N
	}
	ticks := TickValues(s, count)
	base, _ := Get(s, "base")
	b := base.NumValue()
	logb := jsmath.Log(b)
	k := math.Max(1, b*n/float64(len(ticks)))
	return func(d float64) bool {
		i := d / jsPow(b, jsRound(jsmath.Log(d)/logb))
		if i*b < b-0.5 {
			i *= b
		}
		return i <= k
	}
}

// TickLogValues is vega-scale's tickLog(scale, count, true): the log ticks that
// pass the density test.
func TickLogValues(s Scale, count TickCount) []jsval.Value {
	test := logKeep(s, count)
	ticks := TickValues(s, count)
	out := ticks[:0:0]
	for _, t := range ticks {
		if test(t.NumValue()) {
			out = append(out, t)
		}
	}
	return out
}

// TickFormat is vega-scale's tickFormat(locale, scale, count, specifier,
// formatType, noSkip): the formatter for a scale's tick labels. specifier is a
// d3 number/time format string, or (for time scales) a multi-format object.
// formatType, when "time" or "utc", forces a time format regardless of the
// scale type. Log scales hide labels that are too dense unless noSkip is set or
// the scale is binned.
func TickFormat(loc *format.Locale, s Scale, count TickCount, specifier jsval.Value, formatType string, noSkip bool) (Format, error) {
	typ := s.Type()
	switch {
	case typ == TypeTime || formatType == TypeTime:
		return timeFormatOf(loc, specifier, false)
	case typ == TypeUTC || formatType == TypeUTC:
		return timeFormatOf(loc, specifier, true)
	case IsLogarithmic(typ):
		spec, err := numberSpecifier(specifier)
		if err != nil {
			return nil, err
		}
		varfmt, err := loc.FormatFloat(spec)
		if err != nil {
			return nil, err
		}
		if noSkip || binsOf(s) != nil {
			return strFormat(varfmt), nil
		}
		test := logKeep(s, count)
		return func(v jsval.Value) jsval.Value {
			if x := jsval.ToNumber(v); test(x) {
				return jsval.Str(varfmt(x))
			}
			return jsval.Str("")
		}, nil
	}
	if tf, ok := s.(TickFormatter); ok && tf.HasTickFormat() {
		// if a d3 scale has tickFormat, it must be continuous
		d := s.Domain()
		spec, err := spanSpecifier(specifier)
		if err != nil {
			return nil, err
		}
		n := math.NaN()
		if count.HasN {
			n = count.N
		}
		d0, dn := math.NaN(), math.NaN()
		if len(d) > 0 {
			d0, dn = jsval.ToNumber(d[0]), jsval.ToNumber(d[len(d)-1])
		}
		f, err := loc.FormatSpan(d0, dn, n, spec)
		if err != nil {
			return nil, err
		}
		return strFormat(f), nil
	}
	if specifier.IsTruthy() {
		spec, err := numberSpecifier(specifier)
		if err != nil {
			return nil, err
		}
		nf, err := loc.NumberFormat(spec)
		if err != nil {
			return nil, err
		}
		return strFormat(nf.Format), nil
	}
	return defaultFormatter, nil
}

// spanSpecifier is numberSpecifier for locale.formatSpan, which defaults a null
// or undefined specifier to ",f" but honours an explicit empty string as the
// blank specifier.
func spanSpecifier(v jsval.Value) (string, error) {
	if v.IsNullish() {
		return ",f", nil
	}
	return numberSpecifier(v)
}

// numberSpecifier reads a number format specifier: undefined and null mean the
// default ("" here). d3's formatSpecifier matches the specifier with a regular
// expression's exec, which converts it to a string first, so a number such as
// 42 is the specifier "42" (a width); whether that text is a legal specifier
// is up to the parser.
func numberSpecifier(v jsval.Value) (string, error) {
	if v.IsNullish() {
		return "", nil
	}
	return v.AsString(), nil
}

func timeFormatOf(loc *format.Locale, specifier jsval.Value, utc bool) (Format, error) {
	f, err := loc.TimeFormatSpec(specifier, utc)
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) jsval.Value {
		return jsval.Str(f(dateNumber(v)))
	}, nil
}
