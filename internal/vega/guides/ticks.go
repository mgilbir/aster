package guides

import (
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// AxisTicksInput are the resolved parameters of vega-encode's AxisTicks
// operator.
type AxisTicksInput struct {
	Scale scale.Scale
	// Extra adds an extra tick pegged to the first value; it closes the
	// last bin of axes for binned domains.
	Extra bool
	// Count is the tick count (number), a calendar interval name or
	// {interval, step}; null/undefined means "unspecified".
	Count jsval.Value
	// Values are the exact tick values (an array), or undefined.
	Values  jsval.Value
	MinStep jsval.Value
	// FormatType ("number", "time", "utc" or "") and FormatSpecifier select
	// the label format. Format, if set, replaces both.
	FormatType      string
	FormatSpecifier jsval.Value
	Format          func(jsval.Value) string
}

// AxisTicks generates the tick datums of an axis: {index, tickIndex, value,
// label} with index the tick position normalised to [0, 1], plus, when
// requested, a closing `extra` tick {index: -1, tickIndex: -1, extra:
// {value}, label: ""}. Datums are fresh objects.
func (e *Env) AxisTicks(in AxisTicksInput) ([]jsval.Value, error) {
	if in.Scale == nil {
		return nil, errors.New("AxisTicks: missing scale")
	}
	var values []jsval.Value
	hasValues := in.Values.IsTruthy() // an array (even an empty one) is truthy
	if hasValues {
		if !in.Values.IsArr() {
			return nil, errors.New("AxisTicks: values must be an array")
		}
		values = in.Values.Items()
	}
	// a missing count defaults to the number of explicit values, else 10
	tally := in.Count
	if tally.IsNullish() {
		if hasValues {
			tally = jsval.Int(len(values))
		} else {
			tally = jsval.Int(10)
		}
	}
	count, err := e.TickCount(in.Scale, tally, in.MinStep)
	if err != nil {
		return nil, err
	}
	var format func(jsval.Value) jsval.Value
	if in.Format != nil {
		custom := in.Format
		format = func(v jsval.Value) jsval.Value { return jsval.Str(custom(v)) }
	} else if format, err = e.tickFormatValue(in.Scale, count, in.FormatSpecifier, in.FormatType, hasValues); err != nil {
		return nil, err
	}
	if hasValues {
		values = ValidTicks(in.Scale, values, count)
	} else if values, err = TickValues(in.Scale, count); err != nil {
		return nil, err
	}

	denom := float64(len(values) - 1)
	if len(values) <= 1 { // `values.length - 1 || 1`
		denom = 1
	}
	ticks := make([]jsval.Value, 0, len(values)+1)
	for i, v := range values {
		ticks = append(ticks, objv(
			"index", float64(i)/denom,
			"tickIndex", i,
			"value", v,
			"label", format(v),
		))
	}
	if in.Extra && len(ticks) > 0 {
		ticks = append(ticks, objv(
			"index", -1,
			"tickIndex", -1,
			"extra", objv("value", ticks[0].Get("value")),
			"label", "",
		))
	}
	return ticks, nil
}

// LegendEntriesInput are the resolved parameters of vega-encode's
// LegendEntries operator.
type LegendEntriesInput struct {
	// Type is "symbol" (default), "gradient" or "discrete".
	Type  string
	Scale scale.Scale
	// Count is the approximate number of entries (default 5).
	Count jsval.Value
	// Limit caps the entries of a symbol legend (0/NaN: unlimited).
	Limit jsval.Value
	// Values are exact entry values, or undefined.
	Values  jsval.Value
	MinStep jsval.Value
	// FormatType and FormatSpecifier select the label format.
	FormatType      string
	FormatSpecifier jsval.Value
	// Size computes the symbol extent of an entry (the SizeExpr of the
	// legend plan, evaluated with the value as `datum`); nil selects the
	// constant SizeConst (default 8).
	Size      func(value jsval.Value) jsval.Value
	SizeConst jsval.Value
}

// LegendEntries generates the datums of a legend. Symbol legends yield
// {index, label, value, offset, size}; gradients {index, label, value, perc};
// discrete gradients {index, label, value, perc, perc2}. A symbol legend that
// exceeds its limit ends with an "… N entries" placeholder entry.
func (e *Env) LegendEntries(in LegendEntriesInput) ([]jsval.Value, error) {
	if in.Scale == nil {
		return nil, errors.New("LegendEntries: missing scale")
	}
	typ := in.Type
	if typ == "" {
		typ = symbolLegend
	}
	s := in.Scale
	limit := jsval.ToNumber(in.Limit)
	countArg := in.Count
	if countArg.IsNullish() {
		countArg = jsval.Int(5)
	}
	count, err := e.TickCount(s, countArg, in.MinStep)
	if err != nil {
		return nil, err
	}
	hasValues := in.Values.IsTruthy()
	lskip := hasValues || typ == symbolLegend
	format, err := e.LabelFormat(s, count, typ, in.FormatSpecifier, in.FormatType, lskip)
	if err != nil {
		return nil, err
	}
	var set LabelValueSet
	if hasValues {
		if !in.Values.IsArr() {
			return nil, errors.New("LegendEntries: values must be an array")
		}
		set = LabelValueSet{Values: in.Values.Items(), Max: jsval.Undefined}
	} else if set, err = LabelValues(s, count); err != nil {
		return nil, err
	}
	values := set.Values
	if len(values) > maxTicks {
		return nil, fmt.Errorf("%w: legend of %d entries exceeds the limit of %d", budget.ErrLimit, len(values), maxTicks)
	}

	switch typ {
	case symbolLegend:
		return e.symbolEntries(in, s, format, set, limit)
	case gradientLegend:
		return gradientEntries(s, format, set, hasValues), nil
	}
	return discreteEntries(s, format, set), nil
}

func (e *Env) symbolEntries(in LegendEntriesInput, s scale.Scale, format LabelFormatFunc, set LabelValueSet, limit float64) ([]jsval.Value, error) {
	values := set.Values
	items := values
	max := set.Max // slicing drops the `max` property of the array
	ellipsis := false
	if limit != 0 && !math.IsNaN(limit) && float64(len(values)) > limit {
		e.warn("Symbol legend count exceeds limit, filtering items.")
		items = values[:sliceEnd(len(values), limit-1)]
		max = jsval.Undefined
		ellipsis = true
	}

	size := in.Size
	var offset float64
	if size != nil {
		// if the first value maps to size zero, remove it from the list (vega#717)
		// items[0] of no items is undefined, and the scale is applied to it all
		// the same (an implicit ordinal domain grows by it).
		if !in.Values.IsTruthy() {
			first := jsval.Undefined
			if len(items) > 0 {
				first = items[0]
			}
			if r := s.Apply(first); r.IsNum() && r.NumValue() == 0 && len(items) > 0 {
				items = items[1:]
				max = jsval.Undefined
			}
		}
		// compute the size offset for legend entries
		for _, v := range items {
			offset = jsMax(offset, jsval.ToNumber(size(v)))
		}
	} else {
		c := in.SizeConst
		offset = 8
		if c.IsTruthy() {
			offset = jsval.ToNumber(c)
		}
		size = func(jsval.Value) jsval.Value { return jsval.Num(offset) }
	}

	out := make([]jsval.Value, 0, len(items)+1)
	for i, v := range items {
		out = append(out, objv(
			"index", i,
			"label", format(v, i, items, max),
			"value", v,
			"offset", offset,
			"size", size(v),
		))
	}
	if ellipsis {
		last := jsval.Undefined
		if len(out) < len(values) {
			last = values[len(out)]
		}
		out = append(out, objv(
			"index", len(out),
			"label", fmt.Sprintf("…%d entries", len(values)-len(out)),
			"value", last,
			"offset", offset,
			"size", size(last),
		))
	}
	return out, nil
}

func gradientEntries(s scale.Scale, format LabelFormatFunc, set LabelValueSet, hasValues bool) []jsval.Value {
	values := set.Values
	domain := s.Domain()
	d0, dn := domain0(domain), peekValue(domain)
	fraction := scale.ScaleFraction(s, jsval.ToNumber(d0), jsval.ToNumber(dn))

	// if automatic label generation produces 2 or fewer values, use the
	// domain end points instead (vega#1364); dates compare by identity
	if len(values) < 3 && !hasValues && !sameEndpoint(domain) {
		values = []jsval.Value{d0, dn}
		set.Max = jsval.Undefined
	}
	out := make([]jsval.Value, len(values))
	for i, v := range values {
		out[i] = objv(
			"index", i,
			"label", format(v, i, values, set.Max),
			"value", v,
			"perc", fraction(v),
		)
	}
	return out
}

// sameEndpoint is `domain[0] === peek(domain)`.
func sameEndpoint(domain []jsval.Value) bool {
	if len(domain) < 2 {
		return true
	}
	a, b := domain[0], domain[len(domain)-1]
	if a.IsTimestamp() || b.IsTimestamp() {
		return false // distinct Date objects are never identical
	}
	return jsval.SameRef(a, b)
}

func discreteEntries(s scale.Scale, format LabelFormatFunc, set LabelValueSet) []jsval.Value {
	values := set.Values
	last := len(values) - 1
	fraction := LabelFraction(s)
	out := make([]jsval.Value, len(values))
	for i, v := range values {
		perc := jsval.Value(jsval.Int(0))
		if i > 0 {
			perc = jsval.Num(fraction(v))
		}
		perc2 := jsval.Value(jsval.Int(1))
		if i != last {
			perc2 = jsval.Num(fraction(values[i+1]))
		}
		out[i] = objv(
			"index", i,
			"label", format(v, i, values, set.Max),
			"value", v,
			"perc", perc,
			"perc2", perc2,
		)
	}
	return out
}

// sliceEnd is where Array.prototype.slice(0, end) stops: end is truncated
// toward zero, and a negative end counts back from the length (so a negative
// symbolLimit keeps all but the last few entries).
func sliceEnd(length int, end float64) int {
	if math.IsNaN(end) {
		return 0
	}
	e := math.Trunc(end)
	if e < 0 {
		return int(math.Max(float64(length)+e, 0))
	}
	return int(math.Min(e, float64(length)))
}
