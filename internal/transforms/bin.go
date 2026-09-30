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

// maxStepSearch bounds the `while too many bins, step *= base` search, which
// does not terminate for a base of 1 or a zero step.
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
		return BinResult{}, limitErr("maxbins", int(maxb), MaxBins)
	}
	base := c.Base
	if base == 0 || math.IsNaN(base) {
		base = 10
	}
	if base <= 1 || math.IsInf(base, 1) {
		// Upstream computes a NaN step here (log(1) is 0) and bins everything
		// into NaN, or, for a base below one, shrinks the step forever while
		// searching. Neither is a chart; refuse it.
		return BinResult{}, fmt.Errorf("bin: base must be greater than 1, got %g", base)
	}
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
			if n > maxStepSearch || base <= 1 || step == 0 {
				return BinResult{}, fmt.Errorf("bin: no step size satisfies maxbins %g with base %g", maxb, base)
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

// binEpsilon offsets floating point error in bin assignment (vega#1737).
const binEpsilon = 1e-14

// BinParams configures the Bin transform.
type BinParams struct {
	Field Field
	// Config carries extent (required), maxbins, base, divide, span, step,
	// steps, minstep and nice.
	Config BinConfig
	// NoInterval writes only the lower bound, not the upper one.
	NoInterval bool
	// Anchor shifts the bin boundaries so that one falls on it; nil for none.
	Anchor *float64
	// Name names the binning accessor; empty means "bin_" + field name.
	Name string
	// As are the output fields; empty strings mean "bin0" and "bin1".
	As [2]string
}

// Binner maps values to the lower bound of their bin. Start, Stop and Step
// are the runtime's `bins` signal.
type Binner struct {
	Start, Stop, Step float64
	// Name is the accessor name ("bin_x" unless overridden).
	Name  string
	field Field
}

// NewBinner resolves the bin boundaries for p.
func NewBinner(p BinParams) (*Binner, error) {
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
	name := p.Name
	if name == "" {
		name = "bin_" + p.Field.Name
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
	return jsval.Num(b.Of(jsval.ToNumber(x)))
}

// BinTuples assigns each tuple its bin bounds (fields As[0], As[1], default bin0 and
// bin1) in place and returns the Binner.
func BinTuples(ctx context.Context, data []jsval.Value, p BinParams) (*Binner, error) {
	b, err := NewBinner(p)
	if err != nil {
		return nil, err
	}
	return b, b.Apply(ctx, data, p.NoInterval, p.As)
}

// Apply writes bin bounds into the tuples.
func (b *Binner) Apply(ctx context.Context, data []jsval.Value, noInterval bool, as [2]string) error {
	b0, b1 := as[0], as[1]
	if b0 == "" {
		b0 = "bin0"
	}
	if b1 == "" {
		b1 = "bin1"
	}
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return err
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		v := b.Value(t)
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
		f := v.NumValue()
		o.Set(b1, jsval.Num(b.Start+float64(b.Step*(1+(f-b.Start)/b.Step))))
	}
	return nil
}

// binToInt32 is JavaScript's ~~x: truncation with 32-bit wraparound, NaN and
// infinities giving 0.
func binToInt32(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	x = math.Mod(math.Trunc(x), 4294967296)
	if x >= 2147483648 {
		x -= 4294967296
	} else if x < -2147483648 {
		x += 4294967296
	}
	return x
}
