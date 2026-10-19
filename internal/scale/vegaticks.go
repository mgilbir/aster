package scale

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// ErrIntervalString is returned when an interval string is given to a scale
// that is not a time or utc scale.
var ErrIntervalString = errors.New("Only time and utc scales accept interval strings.")

// MaxTicks bounds the ticks, labels and legend entries one guide may list
// (TickValues, LabelValues and the minimum-step reduction of TickCountFor): a
// specification can otherwise ask for billions (tickCount: 1e9). Past it the
// helpers fail with an error wrapping budget.ErrLimit.
const MaxTicks = 100000

// Format is a label formatter: it maps a value (number, date, string, ...) to
// its text. Like vega-scale's formatters it answers a jsval.Value because a
// formatter may return an array of lines or null.
type Format func(v jsval.Value) jsval.Value

func strFormat(f func(float64) string) Format {
	return func(v jsval.Value) jsval.Value { return jsval.Str(f(jsval.ToNumber(v))) }
}

// defaultFormatter is vega-scale's `String(value)`, mapping over arrays,
// with the call's Walker w.
func defaultFormatter(w jsval.Walker) Format {
	return func(v jsval.Value) jsval.Value {
		if v.IsArr() {
			items := v.Items()
			w.Work(int64(len(items)))
			out := make([]jsval.Value, len(items))
			for i, it := range items {
				out[i] = jsval.Str(w.String(it))
			}
			return jsval.Arr(out)
		}
		return jsval.Str(w.String(v))
	}
}

// TickCountFor is vega-scale's tickCount(scale, count, minStep): it resolves a
// specification's `count` (a number, an interval name, or {interval, step}) to
// the argument scale.ticks expects. A numeric count is raised to the number of
// bins and, when minStep is not null or undefined, capped by the domain span
// divided by minStep and lowered until the d3 tick step is not below minStep.
// Interval names of a time scale use the scale's own calendar.
//
// Any other count passes through as upstream's does: a boolean is the number
// it reads as wherever the count is used, and anything else is unspecified.
func TickCountFor(s Scale, count, minStep jsval.Value) (TickCount, error) {
	switch {
	case count.IsNum():
		n := count.NumValue()
		bins := binsOf(s)
		if bins != nil {
			n = math.Max(n, float64(len(bins))) // Math.max: NaN wins, as in Go
		}
		if !minStep.IsNullish() {
			var err error
			if n, err = minStepCount(s, n, bins != nil, jsval.ToNumber(minStep)); err != nil {
				return TickCount{}, err
			}
		}
		return Count(n), nil
	case count.IsBool():
		return Count(jsval.ToNumber(count)), nil
	case count.IsNull():
		return TickCount{Null: true}, nil
	}
	step := jsval.Undefined
	if count.IsObj() || count.IsArr() {
		step = count.Get("step")
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
			return TickCount{}, ErrIntervalString
		}
		iv, ok := format.IntervalFor(z, count.StrValue())
		if !step.IsTruthy() {
			if !ok {
				// vega-time answers undefined for an unknown unit: the
				// scale's default count.
				return TickCount{}, nil
			}
			return IntervalCount(iv), nil
		}
		// `if (step) count = count.every(step)`: on undefined, or on an
		// interval with no every of its own (the quarter is already
		// month.every(3)), that call throws.
		switch {
		case !ok:
			return TickCount{}, errors.New("Cannot read properties of undefined (reading 'every')")
		case !iv.HasEvery():
			return TickCount{}, errors.New("count.every is not a function")
		}
		e, ok := iv.Every(jsval.ToNumber(step))
		if !ok {
			return TickCount{}, nil // every() answers null: the default count
		}
		return IntervalCount(e), nil
	}
	switch {
	case count.IsNum():
		return Count(count.NumValue()), nil // {interval: 5}
	case count.IsBool():
		return Count(jsval.ToNumber(count)), nil
	case count.IsNull():
		return TickCount{Null: true}, nil // {interval: null}
	}
	return TickCount{}, nil
}

// minStepCount is the minimum-step half of tickCount for a numeric count n:
//
//	count = Math.min(count, Math.floor((hi - lo) / minStep || 1) + 1);
//	if (!bins && !log && !temporal && lo < hi)
//	  while (count > 1 && tickStep(lo, hi, count) < minStep) --count;
//
// The loop is linear in the count, which a specification chooses (1e9 and
// more), so it is replaced by one with the same result (ADR 0011). d3's tick
// step grows as the count shrinks while the count is at least 2 (below 2,
// tickSpec may double the count, and there is at most one candidate there),
// so the first count at least 2 that leaves the loop is found by bisection.
// A loop that would stop above MaxTicks fails instead, as would the guide;
// a count so large that --count cannot change it is a loop upstream never
// leaves, and fails too.
func minStepCount(s Scale, n float64, binned bool, minStep float64) (float64, error) {
	d := s.Domain()
	d0, dn := math.NaN(), math.NaN()
	if len(d) > 0 {
		d0, dn = jsval.ToNumber(d[0]), jsval.ToNumber(d[len(d)-1])
	}
	lo, hi := math.Min(d0, dn), math.Max(d0, dn)
	span := (hi - lo) / minStep
	if span == 0 || math.IsNaN(span) {
		span = 1
	}
	n = math.Min(n, math.Floor(span)+1)
	if binned || IsLogarithmic(s.Type()) || IsTemporal(s.Type()) || !(lo < hi) {
		return n, nil
	}
	return reduceForMinStep(n, func(c float64) bool { return TickStep(lo, hi, c) < minStep })
}

// reduceForMinStep is `while (count > 1 && below(count)) --count` for a
// below that is true for large counts and false for small ones (the tick
// step is monotone in the count from 2 up), without visiting every count.
func reduceForMinStep(n float64, below func(float64) bool) (float64, error) {
	if !(n > 1) || !below(n) {
		return n, nil
	}
	if n > MaxTicks+1 {
		if n-1 == n {
			return 0, fmt.Errorf("%w: tick count %v with a minimum step cannot be reduced", budget.ErrLimit, n)
		}
		// The counts from n down to n2 all stay in the loop if n2 does:
		// skip them. Subtracting whole numbers is exact below 2^53.
		n2 := n - math.Ceil(n-(MaxTicks+1))
		if !below(n2) {
			return 0, fmt.Errorf("%w: tick count %v exceeds the limit of %d", budget.ErrLimit, n2, MaxTicks)
		}
		n = n2
	}
	// The loop's candidates are n-k for k = 1, 2, ...; those of at least 2
	// are k <= kA. Find the first that leaves the loop.
	kA := max(math.Floor(n-2), 0)
	a, b := 1.0, kA+1
	for a < b {
		mid := math.Floor((a + b) / 2)
		if below(n - mid) {
			a = mid + 1
		} else {
			b = mid
		}
	}
	if a <= kA {
		return n - a, nil
	}
	c := n - (kA + 1) // below 2: the last candidate, or the loop's end
	if c > 1 && below(c) {
		c--
	}
	return c, nil
}

func zoneOf(s Scale) format.Zone {
	if t, ok := s.(*Time); ok {
		return t.zone
	}
	return format.UTC // only *Time is a "time" scale
}

func binsOf(s Scale) []float64 {
	if t, ok := s.(Typed); ok {
		return t.Bins()
	}
	return nil
}

// ValidTicks is vega-scale's validTicks: it keeps the tick values whose scaled
// position lies within the scale range, orders them along the range (vega
// #2579), and - when count is a positive number - thins them (dropping every
// other tick) until at most count remain, always keeping the end points.
func ValidTicks(s Scale, ticks []jsval.Value, count TickCount) []jsval.Value {
	rng := s.Range()
	lo, hi := math.NaN(), math.NaN()
	if len(rng) > 0 {
		lo, hi = jsval.ToNumber(rng[0]), jsval.ToNumber(rng[len(rng)-1])
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
// scale, else scale.ticks(count), else the whole domain. More than MaxTicks
// values is an error wrapping budget.ErrLimit; ctx bounds a calendar walk.
func TickValues(ctx context.Context, s Scale, count TickCount) ([]jsval.Value, error) {
	if bins := binsOf(s); bins != nil {
		if len(bins) > MaxTicks {
			return nil, errTooMany("bins", len(bins))
		}
		return ValidTicks(s, numsToValues(bins), count), nil
	}
	if t, ok := s.(Ticker); ok {
		var ticks []float64
		var err error
		if b, ok := s.(boundedTicker); ok {
			// refused before more than MaxTicks are walked or kept
			ticks, err = b.ticksN(ctx, count, MaxTicks)
		} else {
			ticks, err = t.Ticks(ctx, count)
		}
		if err != nil {
			return nil, err
		}
		if len(ticks) > MaxTicks {
			return nil, errTooMany("ticks", len(ticks))
		}
		if IsTemporal(s.Type()) {
			return numsToTimestamps(ticks), nil
		}
		return numsToValues(ticks), nil
	}
	d := s.Domain()
	if len(d) > MaxTicks {
		return nil, errTooMany("domain values", len(d))
	}
	return d, nil
}

// boundedTicker is a Ticker that can stop at a number of ticks (every scale
// of this package that has ticks).
type boundedTicker interface {
	ticksN(ctx context.Context, count TickCount, max int) ([]float64, error)
}

func errTooMany(what string, n int) error {
	return fmt.Errorf("%w: the scale gives %d %s to label, more than the limit of %d", budget.ErrLimit, n, what, MaxTicks)
}

// tickLog is vega-scale's tickLog(scale, count, values): d3-scale's log label
// filter, keeping a tick when it is a dense enough multiple of a power of the
// base. The test is returned, and with values the ticks that pass it. A null
// count reads as 0 here; an undefined one makes k NaN, which hides every
// label.
func tickLog(ctx context.Context, s Scale, count TickCount, values bool) (test func(float64) bool, out []jsval.Value, err error) {
	ticks, err := TickValues(ctx, s, count)
	if err != nil {
		return nil, nil, err
	}
	base := 10.0
	if b, ok := Get(s, "base"); ok {
		base = jsval.ToNumber(b)
	}
	logb := jsmath.Log(base)
	k := math.Max(1, float64(base*count.number())/float64(len(ticks)))
	test = func(d float64) bool {
		i := d / jsPow(base, jsRound(jsmath.Log(d)/logb))
		if float64(i*base) < base-0.5 {
			i *= base
		}
		return i <= k
	}
	if values {
		out = ticks[:0:0]
		for _, t := range ticks {
			if test(jsval.ToNumber(t)) {
				out = append(out, t)
			}
		}
	}
	return test, out, nil
}

// TickFormat is vega-scale's tickFormat(locale, scale, count, specifier,
// formatType, noSkip): the formatter for a scale's tick labels. specifier is a
// d3 number/time format string, or (for time scales) a multi-format object.
// formatType, when "time" or "utc", forces a time format regardless of the
// scale type. Log scales hide labels that are too dense unless noSkip is set or
// the scale is binned; finding them lists the ticks, which ctx bounds.
func TickFormat(ctx context.Context, loc *format.Locale, s Scale, count TickCount, specifier jsval.Value, formatType string, noSkip bool) (Format, error) {
	typ := s.Type()
	w := jsval.WalkerFor(ctx)
	switch {
	case typ == TypeTime || formatType == TypeTime:
		return timeFormatOf(w, loc, specifier, false)
	case typ == TypeUTC || formatType == TypeUTC:
		return timeFormatOf(w, loc, specifier, true)
	case IsLogarithmic(typ):
		spec := "" // formatFloat(spec || ',')
		if specifier.IsTruthy() {
			spec = w.String(specifier)
		}
		varfmt, err := loc.FormatFloat(spec)
		if err != nil {
			return nil, err
		}
		if noSkip || binsOf(s) != nil {
			return strFormat(varfmt), nil
		}
		test, _, err := tickLog(ctx, s, count, false)
		if err != nil {
			return nil, err
		}
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
		n := count.number()
		d0, dn := math.NaN(), math.NaN()
		if len(d) > 0 {
			d0, dn = jsval.ToNumber(d[0]), jsval.ToNumber(d[len(d)-1])
		}
		f, err := loc.FormatSpan(d0, dn, n, spanSpecifier(w, specifier))
		if err != nil {
			return nil, err
		}
		return strFormat(f), nil
	}
	if specifier.IsTruthy() {
		nf, err := loc.NumberFormat(w.String(specifier))
		if err != nil {
			return nil, err
		}
		// d3's format reads +value, except "c", which prints the value.
		return func(v jsval.Value) jsval.Value { return jsval.Str(nf.FormatValue(w, v)) }, nil
	}
	return defaultFormatter(w), nil
}

// spanSpecifier is the specifier of locale.formatSpan, which substitutes ",f"
// for null and undefined but keeps an empty string as a specifier of its own.
// d3's formatSpecifier matches the specifier with a regular expression's
// exec, which converts it to a string first, so a number such as 42 is the
// specifier "42" (a width).
func spanSpecifier(w jsval.Walker, v jsval.Value) string {
	if v.IsNullish() {
		return ",f"
	}
	return w.String(v)
}

func timeFormatOf(w jsval.Walker, loc *format.Locale, specifier jsval.Value, utc bool) (Format, error) {
	f, err := loc.TimeFormatSpec(w, specifier, utc)
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) jsval.Value {
		return jsval.Str(f(dateNumber(v)))
	}, nil
}
