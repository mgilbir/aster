package scale

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// Legend types (vega-scale's legend-types).
const (
	SymbolLegend   = "symbol"
	DiscreteLegend = "discrete"
	GradientLegend = "gradient"
)

// Labels is the list of values a legend or axis labels, with the optional upper
// bound vega-scale hangs on the array as `values.max` (the end of the last bin).
type Labels struct {
	Values []jsval.Value
	Max    float64
	HasMax bool
}

// thresholdValues returns the label values of a discretizing scale: the
// thresholds preceded by -Infinity, with +Infinity as the maximum.
func thresholdValues(th []jsval.Value) Labels {
	vals := make([]jsval.Value, 0, len(th)+1)
	vals = append(vals, jsval.Num(math.Inf(-1)))
	vals = append(vals, th...)
	return Labels{Values: vals, Max: math.Inf(1), HasMax: true}
}

// scaleThresholds is the `scale[symbols[type]]()` call: quantiles for quantile,
// thresholds for quantize, the domain for threshold. ok is false for any other
// scale.
func scaleThresholds(s Scale) ([]jsval.Value, bool) {
	switch x := s.(type) {
	case *Quantile:
		return numsToValues(x.Quantiles()), true
	case *Quantize:
		return numsToValues(x.Thresholds()), true
	case *Threshold:
		return x.Domain(), true
	}
	return nil, false
}

// hasSymbols reports the discretizing types whose legends label their
// thresholds (vega-scale's `symbols` table).
func hasSymbols(typ string) bool {
	return typ == TypeQuantile || typ == TypeQuantize || typ == TypeThreshold
}

// LabelValues is vega-scale's labelValues: the values to label for a scale -
// bin edges, the visible log ticks, discretizing thresholds, or tick values.
func LabelValues(s Scale, count TickCount) Labels {
	if bins := binsOf(s); bins != nil {
		vals := numsToValues(bins[:max(len(bins)-1, 0)])
		l := Labels{Values: vals}
		if len(bins) > 0 {
			l.Max, l.HasMax = bins[len(bins)-1], true
		}
		return l
	}
	if s.Type() == TypeLog {
		return Labels{Values: TickLogValues(s, count)}
	}
	if hasSymbols(s.Type()) {
		th, _ := scaleThresholds(s)
		return thresholdValues(th)
	}
	return Labels{Values: TickValues(s, count)}
}

// thresholdFormat is vega-scale's thresholdFormat: a number format sized to the
// smallest gap between a quantile/quantize scale's break points.
func thresholdFormat(loc *format.Locale, s Scale, specifier jsval.Value) (Format, error) {
	var vals []float64
	switch x := s.(type) {
	case *Quantile:
		vals = x.Quantiles()
	case *Quantize:
		vals = valuesToNums(x.Domain()) // quantize uses its [x0, x1] domain
	}
	n := len(vals)
	var d float64
	switch {
	case n > 1:
		d = vals[1] - vals[0]
	case n == 1:
		d = vals[0]
	default:
		d = math.NaN()
	}
	for i := 1; i < n; i++ {
		d = math.Min(d, vals[i]-vals[i-1])
	}
	spec, err := spanSpecifier(specifier)
	if err != nil {
		return nil, err
	}
	// tickCount = 3 ticks times 10 for increased resolution
	f, err := loc.FormatSpan(0, d, 3*10, spec)
	if err != nil {
		return nil, err
	}
	return strFormat(f), nil
}

// LabelFormatter formats one label. index and labels give its position in the
// list being formatted (range formats look at the next value and at the
// list's Max). ok is false where upstream returns null (no label).
type LabelFormatter func(value jsval.Value, index int, labels Labels) (text jsval.Value, ok bool)

// LabelFormat is vega-scale's labelFormat(locale, scale, count, type,
// specifier, formatType, noSkip). legendType is SymbolLegend, DiscreteLegend or
// "" (axes and gradient legends).
func LabelFormat(loc *format.Locale, s Scale, count TickCount, legendType string, specifier jsval.Value, formatType string, noSkip bool) (LabelFormatter, error) {
	var f Format
	var err error
	if (s.Type() == TypeQuantile || s.Type() == TypeQuantize) && formatType != TypeTime && formatType != TypeUTC {
		f, err = thresholdFormat(loc, s, specifier)
	} else {
		f, err = TickFormat(loc, s, count, specifier, formatType, noSkip)
	}
	if err != nil {
		return nil, err
	}
	isRange := hasSymbols(s.Type()) || binsOf(s) != nil
	switch {
	case legendType == SymbolLegend && isRange:
		return formatRange(f), nil
	case legendType == DiscreteLegend:
		return func(v jsval.Value, i int, _ Labels) (jsval.Value, bool) {
			if i == 0 {
				return jsval.Null, false
			}
			return f(v), true
		}, nil
	}
	return func(v jsval.Value, _ int, _ Labels) (jsval.Value, bool) { return f(v), true }, nil
}

// formatRange labels a bin as "lo – hi", "< hi" (no lower bound) or "≥ lo".
func formatRange(f Format) LabelFormatter {
	return func(value jsval.Value, index int, labels Labels) (jsval.Value, bool) {
		var limit jsval.Value
		switch {
		case index+1 < len(labels.Values) && !labels.Values[index+1].IsNullish():
			limit = labels.Values[index+1]
		case labels.HasMax:
			limit = jsval.Num(labels.Max)
		default:
			limit = jsval.Num(math.Inf(1))
		}
		lo, hi := formatFinite(value, f), formatFinite(limit, f)
		switch {
		case lo != "" && hi != "":
			return jsval.Str(lo + " – " + hi), true
		case hi != "":
			return jsval.Str("< " + hi), true
		}
		return jsval.Str("≥ " + lo), true
	}
}

// formatFinite is `Number.isFinite(value) ? format(value) : null`, with null and
// an empty string both reading as "no label" (they are equally falsy upstream).
func formatFinite(v jsval.Value, f Format) string {
	if !v.IsNum() || math.IsInf(v.NumValue(), 0) || math.IsNaN(v.NumValue()) {
		return ""
	}
	return f(v).AsString()
}

// LabelFraction is vega-scale's labelFraction: the position of a value within
// the scale's domain as a fraction in [0, 1] (a threshold scale's domain is
// widened by one step at each end so its first and last bins have width).
func LabelFraction(s Scale) func(value float64) float64 {
	d := s.Domain()
	count := float64(len(d) - 1)
	lo, hi := math.NaN(), math.NaN()
	if len(d) > 0 {
		lo, hi = jsval.ToNumber(d[0]), jsval.ToNumber(d[len(d)-1])
	}
	span := hi - lo
	if s.Type() == TypeThreshold {
		adjust := 0.1
		if count != 0 {
			adjust = span / count
		}
		lo -= adjust
		hi += adjust
		span = hi - lo
	}
	return func(v float64) float64 { return (v - lo) / span }
}

// captionFormat is caption.js's `format`: the formatter used to describe a
// scale's domain in text. Abbreviated time specifiers are spelled out
// (%a -> %A, %b -> %B) to read better for screen readers, and time scales with
// no specifier get a full date-time format.
func captionFormat(loc *format.Locale, s Scale, specifier jsval.Value, formatType string) (Format, error) {
	typ := formatType
	if typ == "" {
		typ = s.Type()
	}
	if specifier.IsStr() && IsTemporal(typ) {
		specifier = jsval.Str(strings.ReplaceAll(strings.ReplaceAll(specifier.StrValue(), "%a", "%A"), "%b", "%B"))
	}
	switch {
	case !specifier.IsTruthy() && typ == TypeTime:
		return timeFormatOf(loc, jsval.Str("%A, %d %B %Y, %X"), false)
	case !specifier.IsTruthy() && typ == TypeUTC:
		return timeFormatOf(loc, jsval.Str("%A, %d %B %Y, %X UTC"), true)
	}
	lf, err := LabelFormat(loc, s, Count(5), "", specifier, formatType, true)
	if err != nil {
		return nil, err
	}
	return func(v jsval.Value) jsval.Value {
		out, _ := lf(v, 0, Labels{})
		return out
	}, nil
}

// CaptionOptions are the options of vega-scale's domainCaption.
type CaptionOptions struct {
	// MaxLen is the number of domain values listed before eliding (min 3,
	// default 7).
	MaxLen float64
	// Format is a d3 format specifier or time multi-format (optional).
	Format jsval.Value
	// FormatType forces "time", "utc" or "number" formatting (optional).
	FormatType string
}

// DomainCaption is vega-scale's domainCaption: a one-sentence description of a
// scale's domain for accessibility ("5 values: a, b, ...", "values from 0 to
// 10", "3 boundaries: ...").
func DomainCaption(loc *format.Locale, s Scale, opt CaptionOptions) (string, error) {
	maxlen := 7.0
	if opt.MaxLen != 0 && opt.MaxLen == opt.MaxLen {
		maxlen = opt.MaxLen
	}
	maxlen = math.Max(3, maxlen)
	fmtv, err := captionFormat(loc, s, opt.Format, opt.FormatType)
	if err != nil {
		return "", err
	}
	str := func(v jsval.Value) string { return fmtv(v).AsString() }
	join := func(vals []jsval.Value) string {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = str(v)
		}
		return strings.Join(parts, ", ")
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	switch {
	case IsDiscretizing(s.Type()):
		v := LabelValues(s, TickCount{}).Values
		if len(v) > 0 {
			v = v[1:]
		}
		return itoa(len(v)) + " boundar" + plural(len(v), "y", "ies") + ": " + join(v), nil
	case IsDiscrete(s.Type()):
		d := s.Domain()
		n := len(d)
		var v string
		if float64(n) > maxlen {
			head := d[:int(maxlen)-2]
			v = join(head) + ", ending with " + str(d[n-1])
		} else {
			v = join(d)
		}
		return itoa(n) + " value" + plural(n, "", "s") + ": " + v, nil
	}
	d := s.Domain()
	var first, last jsval.Value
	if len(d) > 0 {
		first, last = d[0], d[len(d)-1]
	}
	return "values from " + str(first) + " to " + str(last), nil
}

func itoa(n int) string { return jsval.JSNumberString(float64(n)) }
