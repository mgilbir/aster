package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

// BinConfig configures Bin (vega-statistics' bin()). Zero values mean
// "unset", which upstream treats identically (a falsy maxbins, base, step,
// minstep or span is ignored).
type BinConfig struct {
	Extent [2]float64
	// MaxBins is the maximum number of bins; 0 means 20.
	MaxBins float64
	// Base is the number base for step-size search; 0 means 10.
	Base float64
	// Step, when non-zero, is used as is.
	Step float64
	// Steps, when non-nil, restricts the step to one of the listed sizes (an
	// explicit empty list yields a NaN step, as upstream's undefined does).
	Steps []float64
	// MinStep is the smallest step considered.
	MinStep float64
	// Divide lists the factors tried to refine the step; nil means [5, 2].
	Divide []float64
	// Span overrides the extent's span when non-zero.
	Span float64
	// NoNice disables rounding start and stop to multiples of step.
	NoNice bool
}

// BinResult is the outcome of Bin.
type BinResult struct {
	Start, Stop, Step float64
}

// maxStepSearch bounds the `while too many bins, step *= base` search. With
// a base above 1 and a step above 0 the search ends after a few rounds (the
// first step is within a factor base² of span/maxbins), so the bound is a
// backstop no input upstream finishes reaches; with a base below 1 or a
// step of 0 it never ends upstream, and is refused at once.
const maxStepSearch = 4096

// binJSRound is Math.round: halves round toward +Infinity.
func binJSRound(x float64) float64 {
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// Bin computes a nice bin start, stop and step for an extent, as
// vega-statistics' bin() does.
func Bin(c BinConfig) (BinResult, error) {
	maxb := c.MaxBins
	if maxb == 0 || math.IsNaN(maxb) {
		maxb = 20
	}
	if maxb > MaxBins {
		return BinResult{}, limitErr("maxbins", maxb, MaxBins)
	}
	base := c.Base
	if base == 0 || math.IsNaN(base) {
		base = 10
	}
	// Any base is binned with as upstream bins with it: a base of 1, or
	// one below 0, gives a NaN step (every value bins to NaN), Infinity a
	// step of 1, and a base between 0 and 1 a step found as for any other
	// unless its search for fewer bins would never end (ADR 0011).
	logb := jsmath.Log(base)
	div := c.Divide
	if div == nil {
		div = []float64{5, 2}
	}
	lo, hi := c.Extent[0], c.Extent[1]

	span := c.Span
	if span == 0 || math.IsNaN(span) {
		span = hi - lo
		if span == 0 || math.IsNaN(span) {
			span = math.Abs(lo)
			if span == 0 || math.IsNaN(span) {
				span = 1
			}
		}
	}

	var step float64
	switch {
	case c.Step != 0 && !math.IsNaN(c.Step):
		step = c.Step
	case c.Steps != nil:
		v := span / maxb
		i := 0
		for i < len(c.Steps) && c.Steps[i] < v {
			i++
		}
		if k := max(0, i-1); k < len(c.Steps) {
			step = c.Steps[k]
		} else {
			step = math.NaN()
		}
	default:
		level := math.Ceil(jsmath.Log(maxb) / logb)
		minstep := c.MinStep
		step = math.Max(minstep, jsmath.Pow(base, binJSRound(jsmath.Log(span)/logb)-level))
		for n := 0; math.Ceil(span/step) > maxb; n++ {
			// step *= base never lowers the bin count for a base of 1 or
			// less, nor from a step of 0: upstream loops forever.
			if n > maxStepSearch || !(base > 1) || step == 0 {
				return BinResult{}, errBinSearch(maxb, base)
			}
			step *= base
		}
		for _, d := range div {
			v := step / d
			if v >= minstep && span/v <= maxb {
				step = v
			}
		}
	}

	v := jsmath.Log(step)
	precision := 0.0
	if !(v >= 0) {
		precision = binToInt32(-v/logb) + 1
	}
	eps := jsmath.Pow(base, -precision-1)
	if !c.NoNice {
		v = float64(math.Floor(lo/step+eps) * step)
		if lo < v {
			lo = v - step
		} else {
			lo = v
		}
		hi = math.Ceil(hi/step) * step
	}
	stop := hi
	if hi == lo {
		stop = lo + step
	}
	return BinResult{Start: lo, Stop: stop, Step: step}, nil
}

// errBinSearch is the step search upstream never ends: step *= base never
// lowers the bin count for a base of 1 or less, nor from a step of 0.
func errBinSearch(maxb, base float64) error {
	return fmt.Errorf("bin: %w: no step size gives at most %s bins with base %s: the search for one does not end",
		ErrLimit, jsval.JSNumberString(maxb), jsval.JSNumberString(base))
}

// binEpsilon offsets floating point error in bin assignment (vega#1737).
const binEpsilon = 1e-14

// BinParams configures the Bin transform.
type BinParams struct {
	Field Field
	// Config carries extent (required), maxbins, base, divide, span, step,
	// steps, minstep and nice.
	Config BinConfig
	// Spec, when not nil, replaces Config and Anchor: the parameters as the
	// specification gave them, computed with as JavaScript does (BinJS).
	Spec        *BinSpec
	AnchorValue *jsval.Value
	// NoInterval writes only the lower bound, not the upper one.
	NoInterval bool
	// Anchor shifts the bin boundaries so that one falls on it; nil for none.
	Anchor *float64
	// Name names the binning accessor; empty means "bin_" + field name.
	Name string
	// As are the output fields; empty strings mean "bin0" and "bin1".
	As [2]string
	// AsExact reports that As are the names as the runtime resolved them
	// (`_.as || defaults`, then each entry's text): an empty one is the
	// empty name, not the default.
	AsExact bool
}

// Binner maps values to the lower bound of their bin. Start, Stop and Step
// are the runtime's `bins` signal, as numbers; StartV, StopV and StepV are
// them as upstream holds them, which are other values (strings) only when
// the specification's own are kept (see BinJS), and then the bins are
// computed with JavaScript's operators. Those values live behind a pointer,
// nil for the numeric Binner of every real chart, so that one costs what it
// did before them.
type Binner struct {
	Start, Stop, Step float64
	js                *binValues
	asExact           bool // BinParams.AsExact
	// Name is the accessor name ("bin_x" unless overridden).
	Name  string
	field Field
}

// binValues is a Binner's boundaries as values, and the walker its
// JavaScript operators use.
type binValues struct {
	start, stop, step jsval.Value
	w                 jsval.Walker
}

// StartV is the bins' start as upstream holds it.
func (b *Binner) StartV() jsval.Value {
	if b.js != nil {
		return b.js.start
	}
	return jsval.Num(b.Start)
}

// StopV is the bins' stop as upstream holds it.
func (b *Binner) StopV() jsval.Value {
	if b.js != nil {
		return b.js.stop
	}
	return jsval.Num(b.Stop)
}

// StepV is the bins' step as upstream holds it.
func (b *Binner) StepV() jsval.Value {
	if b.js != nil {
		return b.js.step
	}
	return jsval.Num(b.Step)
}

// NewBinner resolves the bin boundaries for p.
func NewBinner(ctx context.Context, p BinParams) (*Binner, error) {
	return newBinner(jsval.WalkerFor(ctx), p)
}

func newBinner(w jsval.Walker, p BinParams) (*Binner, error) {
	name := p.Name
	if name == "" {
		name = "bin_" + p.Field.Key()
	}
	if p.Spec != nil {
		start, stop, step, err := BinJS(w, *p.Spec)
		if err != nil {
			return nil, err
		}
		start, stop = binBounds(w, start, stop, step, p.AnchorValue)
		b := &Binner{Name: name, field: p.Field}
		b.Start, b.Stop, b.Step = num(start), num(stop), num(step)
		if !(start.IsNum() && stop.IsNum() && step.IsNum()) {
			b.js = &binValues{start: start, stop: stop, step: step, w: w}
		}
		return b, nil
	}
	bins, err := Bin(p.Config)
	if err != nil {
		return nil, err
	}
	step := bins.Step
	start := bins.Start
	stop := float64(math.Ceil((bins.Stop-start)/step)*step) + start
	if p.Anchor != nil {
		a := *p.Anchor
		d := a - (start + float64(step*math.Floor((a-start)/step)))
		start += d
		stop += d
	}
	return &Binner{Start: start, Stop: stop, Step: step, Name: name, field: p.Field}, nil
}

// Of bins a numeric value: NaN stays NaN, values outside [Start, Stop] map to
// -Inf / +Inf, and the last bin is closed on the right.
func (b *Binner) Of(v float64) float64 {
	switch {
	case v < b.Start:
		return math.Inf(-1)
	case v > b.Stop:
		return math.Inf(1)
	}
	v = math.Max(b.Start, math.Min(v, b.Stop-b.Step))
	return b.Start + float64(b.Step*math.Floor(binEpsilon+(v-b.Start)/b.Step))
}

// Value bins a tuple field the way the transform's accessor does: null for
// null or empty input, else the bin's lower bound (as a number, possibly
// infinite or NaN).
func (b *Binner) Value(t jsval.Value) jsval.Value {
	x := b.field.Get(t)
	if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
		return jsval.Null
	}
	if b.js == nil {
		return jsval.Num(b.Of(jsval.ToNumber(x)))
	}
	// v < start ? -Infinity : v > stop ? +Infinity : (v = Math.max(start,
	// Math.min(v, stop - step)), start + step * Math.floor(EPSILON + (v -
	// start) / step))
	v := jsval.Num(jsval.ToNumber(x))
	switch {
	case Less(b.js.w, v, b.js.start):
		return jsval.Num(math.Inf(-1))
	case Greater(b.js.w, v, b.js.stop):
		return jsval.Num(math.Inf(1))
	}
	f := math.Max(b.Start, math.Min(v.NumValue(), b.Stop-b.Step))
	return jsAdd(b.js.w, b.js.start, jsval.Num(b.Step*math.Floor(binEpsilon+(f-b.Start)/b.Step)))
}

// upper is `start + step * (1 + (v - start) / step)`, the bin's upper bound.
func (b *Binner) upper(v jsval.Value) jsval.Value {
	if b.js == nil && v.IsNum() {
		f := v.NumValue()
		return jsval.Num(b.Start + float64(b.Step*(1+(f-b.Start)/b.Step)))
	}
	if b.js == nil {
		return jsval.Num(b.Start + float64(b.Step*(1+(num(v)-b.Start)/b.Step)))
	}
	return jsAdd(b.js.w, b.js.start, jsval.Num(b.Step*(1+(num(v)-b.Start)/b.Step)))
}

// BinTuples assigns each tuple its bin bounds (fields As[0], As[1], default bin0 and
// bin1) in place and returns the Binner.
func BinTuples(ctx context.Context, data []jsval.Value, p BinParams) (*Binner, error) {
	b, err := newBinner(jsval.WalkerFor(ctx), p)
	if err != nil {
		return nil, err
	}
	b.asExact = p.AsExact
	return b, b.Apply(ctx, data, p.NoInterval, p.As)
}

// Apply writes bin bounds into the tuples.
func (b *Binner) Apply(ctx context.Context, data []jsval.Value, noInterval bool, as [2]string) error {
	b0, b1 := as[0], as[1]
	if b0 == "" && !b.asExact {
		b0 = "bin0"
	}
	if b1 == "" && !b.asExact {
		b1 = "bin1"
	}
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return err
		}
		v := b.Value(t)
		o := jsval.FieldsFor(t, b0)
		o.Set(b0, v)
		if noInterval {
			continue
		}
		if v.IsNull() {
			o.Set(b1, jsval.Null)
			continue
		}
		// The convoluted form agrees better with upstream in floating point
		// (vega#830); infinite values propagate through it (vega#2227).
		o.Set(b1, b.upper(v))
	}
	return nil
}

// binToInt32 is JavaScript's ~~x.
func binToInt32(x float64) float64 { return float64(jsmath.ToInt32(x)) }
