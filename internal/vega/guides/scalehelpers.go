package guides

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// maxTicks bounds the number of ticks, labels or legend entries one guide may
// produce; a specification can otherwise request billions (tickCount: 1e9).
const maxTicks = 100000

// Env is what the tick and entry generators need from the runtime.
type Env struct {
	// Locale supplies number and time formats; nil selects format.DefaultLocale.
	Locale *format.Locale
	// Warn receives upstream's dataflow warnings (may be nil).
	Warn func(msg string)
}

func (e *Env) warn(msg string) {
	if e != nil && e.Warn != nil {
		e.Warn(msg)
	}
}

func (e *Env) locale() *format.Locale {
	if e == nil || e.Locale == nil {
		return format.DefaultLocale()
	}
	return e.Locale
}

// numFunc adapts a number formatter to the jsval-typed label functions; the
// argument is coerced as d3-format does (`+value`).
func numFunc(f func(float64) string, err error) (func(jsval.Value) string, error) {
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) string { return f(jsval.ToNumber(v)) }, nil
}

func timeFunc(f format.TimeFormatter, err error) (func(jsval.Value) string, error) {
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) string { return f(jsval.ToNumber(v)) }, nil
}

// specString reads a number specifier for formatFloat: null and undefined
// mean "none" (which formatFloat turns into ",").
func specString(v jsval.Value) string {
	if v.IsNullish() {
		return ""
	}
	return v.AsString()
}

// spanSpec reads a number specifier for formatSpan, which substitutes ",f"
// for null and undefined but keeps an empty string as a specifier of its own.
func spanSpec(v jsval.Value) string {
	if v.IsNullish() {
		return ",f"
	}
	return v.AsString()
}

func typeOf(s scale.Scale) string { return s.Type() }

func binsOf(s scale.Scale) ([]float64, bool) {
	if t, ok := s.(scale.Typed); ok {
		if b := t.Bins(); b != nil {
			return b, true
		}
	}
	return nil, false
}

func numsToValues(f []float64) []jsval.Value {
	out := make([]jsval.Value, len(f))
	for i, x := range f {
		out[i] = jsval.Num(x)
	}
	return out
}

func peekValue(v []jsval.Value) jsval.Value {
	if len(v) == 0 {
		return jsval.Undefined
	}
	return v[len(v)-1]
}

// jsMin and jsMax are Math.min and Math.max over coerced numbers: NaN wins.
func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}

func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

// TickCount is vega-scale's tickCount: it turns a count specifier (a number,
// an interval name, or {interval, step}) into the count or interval the scale's
// ticks method takes, honouring a minimum step between ticks.
func (e *Env) TickCount(s scale.Scale, count jsval.Value, minStep jsval.Value) (scale.TickCount, error) {
	var tc scale.TickCount
	typ := typeOf(s)
	if count.IsNum() {
		n := count.NumValue()
		if bins, ok := binsOf(s); ok {
			n = jsMax(n, float64(len(bins)))
		}
		if !minStep.IsNullish() {
			domain := s.Domain()
			d0 := jsval.ToNumber(domain0(domain))
			d1 := jsval.ToNumber(peekValue(domain))
			lo, hi := jsMin(d0, d1), jsMax(d0, d1)
			span := (hi - lo) / jsval.ToNumber(minStep)
			if span == 0 || math.IsNaN(span) {
				span = 1
			}
			n = jsMin(n, math.Floor(span)+1)
			_, hasBins := binsOf(s)
			if !hasBins && !scale.IsLogarithmic(typ) && !scale.IsTemporal(typ) && lo < hi {
				// d3 tick steps grow monotonically as the count shrinks
				ms := jsval.ToNumber(minStep)
				for n > 1 && scale.TickStep(lo, hi, n) < ms {
					n--
				}
			}
		}
		// The bound applies after the minimum-step clamp, which can reduce an
		// absurd count to a handful of ticks.
		if math.Abs(n) > maxTicks {
			return tc, fmt.Errorf("tick count %v exceeds the limit of %d", n, maxTicks)
		}
		return scale.Count(n), nil
	}

	var name string
	var step float64
	switch {
	case count.IsObj():
		// {interval, step}: `step` applies to the named interval
		name = count.Get("interval").AsString()
		if s := count.Get("step"); s.IsTruthy() {
			step = jsval.ToNumber(s)
		}
	case count.IsStr():
		name = count.StrValue()
	default:
		return tc, nil // unspecified: the scale's default
	}
	if typ != scale.TypeTime && typ != scale.TypeUTC {
		return tc, errors.New("Only time and utc scales accept interval strings.")
	}
	zone := e.locale().Local
	if typ == scale.TypeUTC {
		zone = format.UTC
	}
	iv, ok := format.IntervalFor(zone, name)
	if !ok {
		return tc, fmt.Errorf("unknown time interval %q", name)
	}
	if step != 0 {
		if iv, ok = iv.Every(step); !ok {
			return tc, fmt.Errorf("invalid step %v for interval %q", step, name)
		}
	}
	return scale.IntervalCount(iv), nil
}

func domain0(d []jsval.Value) jsval.Value {
	if len(d) == 0 {
		return jsval.Undefined
	}
	return d[0]
}

// ValidTicks is vega-scale's validTicks: keep the candidate ticks that fall
// inside the scale range, ordered along the range (vega#2579), and thin them
// by halving while there are more than count.
func ValidTicks(s scale.Scale, ticks []jsval.Value, count scale.TickCount) []jsval.Value {
	rng := s.Range()
	lo := jsval.ToNumber(domain0(rng))
	hi := jsval.ToNumber(peekValue(rng))
	descending := false
	if lo > hi {
		lo, hi = hi, lo
		descending = true
	}
	lo, hi = math.Floor(lo), math.Ceil(hi)

	type pair struct {
		v jsval.Value
		p float64
	}
	pairs := make([]pair, 0, len(ticks))
	for _, v := range ticks {
		p := jsval.ToNumber(s.Apply(v))
		if lo <= p && p <= hi {
			pairs = append(pairs, pair{v, p})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if descending {
			return pairs[i].p > pairs[j].p
		}
		return pairs[i].p < pairs[j].p
	})
	out := make([]jsval.Value, len(pairs))
	for i, p := range pairs {
		out[i] = p.v
	}

	if count.HasN && count.N > 0 && len(out) > 1 {
		endpoints := []jsval.Value{out[0], out[len(out)-1]}
		for float64(len(out)) > count.N && len(out) >= 3 {
			kept := out[:0:0]
			for i, v := range out {
				if i%2 == 0 {
					kept = append(kept, v)
				}
			}
			out = kept
		}
		if len(out) < 3 {
			out = endpoints
		}
	}
	return out
}

// TickValues is vega-scale's tickValues: binned scales use their bins, scales
// with a ticks method use it, and everything else lists its domain.
func TickValues(s scale.Scale, count scale.TickCount) ([]jsval.Value, error) {
	if bins, ok := binsOf(s); ok {
		return ValidTicks(s, numsToValues(bins), count), nil
	}
	if t, ok := s.(scale.Ticker); ok {
		ticks := t.Ticks(count)
		if len(ticks) > maxTicks {
			return nil, fmt.Errorf("scale produced %d ticks, more than the limit of %d", len(ticks), maxTicks)
		}
		out := make([]jsval.Value, len(ticks))
		temporal := scale.IsTemporal(typeOf(s))
		for i, x := range ticks {
			if temporal {
				out[i] = jsval.Timestamp(x)
			} else {
				out[i] = jsval.Num(x)
			}
		}
		return out, nil
	}
	d := s.Domain()
	if len(d) > maxTicks {
		return nil, fmt.Errorf("scale domain of %d values exceeds the tick limit of %d", len(d), maxTicks)
	}
	return d, nil
}

// tickLog is vega-scale's tickLog: d3-scale's log tick label filter. With
// values true it returns the filtered ticks, otherwise the predicate.
func tickLog(s scale.Scale, count scale.TickCount, values bool) (test func(float64) bool, out []jsval.Value, err error) {
	ticks, err := TickValues(s, count)
	if err != nil {
		return nil, nil, err
	}
	base := 10.0
	if b, ok := scale.Get(s, "base"); ok {
		base = jsval.ToNumber(b)
	}
	logb := jsmath.Log(base)
	n := float64(len(ticks))
	c := 10.0
	if count.HasN {
		c = count.N
	}
	k := jsMax(1, base*c/n)

	test = func(d float64) bool {
		i := d / jsmath.Pow(base, jsRound(jsmath.Log(d)/logb))
		if float64(i*base) < base-0.5 {
			i *= base
		}
		return i <= k
	}
	if values {
		for _, t := range ticks {
			if test(jsval.ToNumber(t)) {
				out = append(out, t)
			}
		}
	}
	return test, out, nil
}

// jsRound is Math.round.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// TickFormat is vega-scale's tickFormat: the label formatter for axis ticks
// and continuous legends.
func (e *Env) TickFormat(s scale.Scale, count scale.TickCount, specifier jsval.Value, formatType string, noSkip bool) (func(jsval.Value) string, error) {
	typ := typeOf(s)
	loc := e.locale()
	switch {
	case typ == scale.TypeTime || formatType == scale.TypeTime:
		return timeFunc(loc.TimeFormatSpec(specifier, false))
	case typ == scale.TypeUTC || formatType == scale.TypeUTC:
		return timeFunc(loc.TimeFormatSpec(specifier, true))
	case scale.IsLogarithmic(typ):
		varfmt, err := numFunc(loc.FormatFloat(specString(specifier)))
		if err != nil {
			return nil, err
		}
		if _, hasBins := binsOf(s); noSkip || hasBins {
			return varfmt, nil
		}
		test, _, err := tickLog(s, count, false)
		if err != nil {
			return nil, err
		}
		return func(v jsval.Value) string {
			if test(jsval.ToNumber(v)) {
				return varfmt(v)
			}
			return ""
		}, nil
	}
	if t, ok := s.(scale.TickFormatter); ok && t.HasTickFormat() {
		// if a d3 scale has tickFormat, it must be continuous
		d := s.Domain()
		c := float64(scale.DefaultTickCount)
		if count.HasN {
			c = count.N
		}
		return numFunc(loc.FormatSpan(jsval.ToNumber(domain0(d)), jsval.ToNumber(peekValue(d)), c, spanSpec(specifier)))
	}
	if specifier.IsTruthy() {
		f, err := loc.NumberFormat(specifier.AsString())
		if err != nil {
			return nil, err
		}
		return f.FormatValue, nil
	}
	return func(v jsval.Value) string { return v.AsString() }, nil
}

// usesDefaultFormatter reports whether TickFormat falls through to
// vega-scale's defaultFormatter: no time, logarithmic or continuous
// formatting applies and there is no specifier.
func usesDefaultFormatter(s scale.Scale, specifier jsval.Value, formatType string) bool {
	typ := typeOf(s)
	switch {
	case typ == scale.TypeTime || formatType == scale.TypeTime,
		typ == scale.TypeUTC || formatType == scale.TypeUTC,
		scale.IsLogarithmic(typ):
		return false
	}
	if t, ok := s.(scale.TickFormatter); ok && t.HasTickFormat() {
		return false
	}
	return !specifier.IsTruthy()
}

// defaultFormatter is vega-scale's: an array value keeps its shape, each
// element converted with String, so a label of several lines stays one;
// anything else is String(value).
func defaultFormatter(v jsval.Value) jsval.Value {
	if !v.IsArr() {
		return jsval.Str(v.AsString())
	}
	items := v.Items()
	out := make([]jsval.Value, len(items))
	for i, it := range items {
		out[i] = jsval.Str(it.AsString())
	}
	return jsval.Arr(out)
}

// tickFormatValue is TickFormat for label text, which may be an array of
// lines when the default formatter applies.
func (e *Env) tickFormatValue(s scale.Scale, count scale.TickCount, specifier jsval.Value, formatType string, noSkip bool) (func(jsval.Value) jsval.Value, error) {
	if usesDefaultFormatter(s, specifier, formatType) {
		return defaultFormatter, nil
	}
	f, err := e.TickFormat(s, count, specifier, formatType, noSkip)
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) jsval.Value { return jsval.Str(f(v)) }, nil
}

// LabelValueSet is the value list of a legend plus the `max` property that
// vega-scale hangs on the array (the upper end of the last bin).
type LabelValueSet struct {
	Values []jsval.Value
	Max    jsval.Value // undefined when the array carries no max
}

// scaleThresholds returns the threshold list vega-scale's `symbols` table
// reads from a discretizing scale, and whether the scale is one.
func scaleThresholds(s scale.Scale) ([]jsval.Value, bool) {
	switch t := s.(type) {
	case *scale.Quantile:
		return numsToValues(t.Quantiles()), true
	case *scale.Quantize:
		return numsToValues(t.Thresholds()), true
	case *scale.Threshold:
		return t.Domain(), true
	}
	return nil, false
}

// LabelValues is vega-scale's labelValues: the values a legend lists for a
// scale.
func LabelValues(s scale.Scale, count scale.TickCount) (LabelValueSet, error) {
	if bins, ok := binsOf(s); ok {
		if len(bins) == 0 {
			return LabelValueSet{Max: jsval.Undefined}, nil
		}
		return LabelValueSet{Values: numsToValues(bins[:len(bins)-1]), Max: jsval.Num(bins[len(bins)-1])}, nil
	}
	if typeOf(s) == scale.TypeLog {
		_, vals, err := tickLog(s, count, true)
		return LabelValueSet{Values: vals}, err
	}
	if th, ok := scaleThresholds(s); ok {
		vals := append([]jsval.Value{jsval.Num(math.Inf(-1))}, th...)
		return LabelValueSet{Values: vals, Max: jsval.Num(math.Inf(1))}, nil
	}
	vals, err := TickValues(s, count)
	return LabelValueSet{Values: vals}, err
}

// LabelFormatFunc formats legend entry i of values; a null result means "no
// label" (the first entry of a discrete gradient).
type LabelFormatFunc func(value jsval.Value, index int, all []jsval.Value, max jsval.Value) jsval.Value

// LabelFormat is vega-scale's labelFormat.
func (e *Env) LabelFormat(s scale.Scale, count scale.TickCount, legendType string, specifier jsval.Value, formatType string, noSkip bool) (LabelFormatFunc, error) {
	typ := typeOf(s)
	var format func(jsval.Value) string
	var err error
	if (typ == scale.TypeQuantile || typ == scale.TypeQuantize) && formatType != scale.TypeTime && formatType != scale.TypeUTC {
		format, err = e.thresholdFormat(s, specifier)
	} else {
		format, err = e.TickFormat(s, count, specifier, formatType, noSkip)
	}
	if err != nil {
		return nil, err
	}
	// Plain and discrete labels keep array values whole (defaultFormatter).
	label := func(v jsval.Value) jsval.Value { return jsval.Str(format(v)) }
	if !(typ == scale.TypeQuantile || typ == scale.TypeQuantize) && usesDefaultFormatter(s, specifier, formatType) {
		label = defaultFormatter
	}

	_, symbolsType := scaleThresholds(s)
	_, hasBins := binsOf(s)
	discreteRange := symbolsType || hasBins

	switch {
	case legendType == symbolLegend && discreteRange:
		return func(v jsval.Value, i int, all []jsval.Value, max jsval.Value) jsval.Value {
			// the upper limit is the next value, else the array's max, else +Infinity
			limit := jsval.Num(math.Inf(1))
			if !max.IsNullish() {
				limit = max
			}
			if i+1 < len(all) && !all[i+1].IsNullish() {
				limit = all[i+1]
			}
			lo, hi := formatFinite(v, format), formatFinite(limit, format)
			switch {
			case lo.IsTruthy() && hi.IsTruthy():
				return jsval.Str(lo.StrValue() + " – " + hi.StrValue())
			case hi.IsTruthy():
				return jsval.Str("< " + hi.StrValue())
			}
			return jsval.Str("≥ " + lo.AsString())
		}, nil
	case legendType == discreteLegend:
		return func(v jsval.Value, i int, _ []jsval.Value, _ jsval.Value) jsval.Value {
			if i == 0 {
				return jsval.Null
			}
			return label(v)
		}, nil
	}
	return func(v jsval.Value, _ int, _ []jsval.Value, _ jsval.Value) jsval.Value {
		return label(v)
	}, nil
}

// formatFinite formats finite numbers and answers null for anything else
// (Number.isFinite is false for non-numbers).
func formatFinite(v jsval.Value, format func(jsval.Value) string) jsval.Value {
	if v.IsNum() && !math.IsInf(v.NumValue(), 0) && !math.IsNaN(v.NumValue()) {
		return jsval.Str(format(v))
	}
	return jsval.Null
}

// thresholdFormat is vega-scale's thresholdFormat: quantile and quantize
// labels get the precision of the smallest gap between thresholds.
func (e *Env) thresholdFormat(s scale.Scale, specifier jsval.Value) (func(jsval.Value) string, error) {
	var vals []jsval.Value
	if q, ok := s.(*scale.Quantile); ok {
		vals = numsToValues(q.Quantiles())
	} else {
		vals = s.Domain() // quantize: its [x0, x1] domain
	}
	n := len(vals)
	if n == 0 {
		return numFunc(e.locale().FormatSpan(0, math.NaN(), 30, spanSpec(specifier)))
	}
	d := jsval.ToNumber(vals[0])
	if n > 1 {
		d = jsval.ToNumber(vals[1]) - jsval.ToNumber(vals[0])
	}
	for i := 1; i < n; i++ {
		d = jsMin(d, jsval.ToNumber(vals[i])-jsval.ToNumber(vals[i-1]))
	}
	// tickCount = 3 ticks times 10 for increased resolution
	return numFunc(e.locale().FormatSpan(0, d, 3*10, spanSpec(specifier)))
}

// LabelFraction is vega-scale's labelFraction: value to [0, 1] along the
// (extended, for threshold scales) domain.
func LabelFraction(s scale.Scale) func(jsval.Value) float64 {
	domain := s.Domain()
	count := len(domain) - 1
	lo := jsval.ToNumber(domain0(domain))
	hi := jsval.ToNumber(peekValue(domain))
	span := hi - lo
	if typeOf(s) == scale.TypeThreshold {
		adjust := 0.1
		if count > 0 {
			adjust = span / float64(count)
		}
		lo -= adjust
		hi += adjust
		span = hi - lo
	}
	return func(v jsval.Value) float64 { return (jsval.ToNumber(v) - lo) / span }
}
