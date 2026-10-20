package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// DistSpec describes a probability distribution the way a Density
// specification does. Unset (nil) numeric parameters take upstream's
// defaults: normal and lognormal mean 0 and stdev 1, uniform min 0 and max 1.
type DistSpec struct {
	// Kind is one of "normal", "lognormal", "uniform", "kde", "mixture".
	Kind string

	Mean, Stdev, Min, Max *float64

	// Field, From and Bandwidth configure a kde. The sample points are Field
	// applied to From, or to the transform's input tuples when From is nil.
	// A zero Bandwidth is estimated from the data.
	Field     Field
	From      []jsval.Value
	Bandwidth float64
	// BandwidthGiven reports a truthy bandwidth, used as it is (NaN for a
	// value that is no number) rather than estimated.
	BandwidthGiven bool
	// FieldNull reports a kde whose field is null (a falsy field), which
	// upstream maps the sample data with: a TypeError.
	FieldNull bool

	// Distributions and Weights configure a mixture. A NaN weight counts as
	// 1, like a missing one.
	Distributions []DistSpec
	Weights       []float64
}

// maxMixtureDepth bounds nested mixtures in an untrusted specification.
const maxMixtureDepth = 32

// NewDistribution instantiates spec. source supplies the input tuples for
// kde distributions without an explicit From.
func NewDistribution(spec DistSpec, source []jsval.Value) (Distribution, error) {
	return newDistribution(spec, source, 0)
}

func newDistribution(spec DistSpec, source []jsval.Value, depth int) (Distribution, error) {
	if depth > maxMixtureDepth {
		return nil, fmt.Errorf("%w: distribution nesting exceeds %d", ErrLimit, maxMixtureDepth)
	}
	val := func(p *float64, def float64) float64 {
		if p == nil {
			return def
		}
		return *p
	}
	switch spec.Kind {
	// The parameters are as the distribution's setters keep them (mean
	// and min `_ || 0`, stdev and max `_ == null ? 1 : _`, applied by the
	// caller to the specification's values): a truthy value that is no
	// number stays NaN.
	case "normal":
		return &Normal{Mean: val(spec.Mean, 0), Stdev: val(spec.Stdev, 1), s: normalSampler{next: math.NaN()}}, nil
	case "lognormal":
		return &LogNormal{Mean: val(spec.Mean, 0), Stdev: val(spec.Stdev, 1), s: normalSampler{next: math.NaN()}}, nil
	case "uniform":
		return &Uniform{Min: val(spec.Min, 0), Max: val(spec.Max, 1)}, nil
	case "kde":
		if spec.FieldNull {
			// d.data((def.from || data()).map(def.field)) of a null field
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "null is not a function"}
		}
		k := NewKernelDensity(kdeSupport(spec, source), spec.Bandwidth)
		if spec.BandwidthGiven {
			// bandwidth(_): a truthy value is kept, NaN or not
			k.Bandwidth = spec.Bandwidth
		}
		return k, nil
	case "mixture":
		ds := make([]Distribution, len(spec.Distributions))
		for i, s := range spec.Distributions {
			d, err := newDistribution(s, source, depth+1)
			if err != nil {
				return nil, err
			}
			ds[i] = d
		}
		return NewMixture(ds, spec.Weights), nil
	}
	return nil, fmt.Errorf("Unknown distribution function: %s", spec.Kind)
}

func kdeSupport(spec DistSpec, source []jsval.Value) []jsval.Value {
	if spec.Field.IsNil() {
		return nil
	}
	src := spec.From
	if src == nil {
		src = source
	}
	out := make([]jsval.Value, len(src))
	for i, t := range src {
		out[i] = spec.Field.Get(t)
	}
	return out
}

// DensityParams configures Density.
type DensityParams struct {
	Distribution DistSpec
	// Method is "pdf" (default) or "cdf".
	Method string
	// Extent is the sampling domain; nil is allowed only for a kde, whose
	// data extent is used.
	Extent *[2]float64
	// ExtentValues, when not nil, is `_.extent` (when truthy) read as
	// extent[0] and extent[1], which replaces Extent; StepsValues, when not
	// nil, the step bounds as values, which replace Steps, MinSteps and
	// MaxSteps (sampleCurveJS).
	ExtentValues *[2]jsval.Value
	StepsValues  *StepsSpec
	// Steps overrides both MinSteps and MaxSteps when non-zero.
	Steps    float64
	MinSteps float64 // default 25
	MaxSteps float64 // default 200
	// As are the output fields; empty strings mean "value" and "density".
	As [2]string
	// AsExact reports that As are the names as the runtime resolved them
	// (`_.as || defaults`, then each entry's text): an empty one is the
	// empty name, not the default.
	AsExact bool
}

// Density samples a distribution's pdf or cdf into (value, density) tuples,
// adaptively refined where the curve bends (SampleCurve). source supplies
// input tuples for a kde without explicit data. The output tuples are new.
func Density(ctx context.Context, source []jsval.Value, p DensityParams) ([]jsval.Value, error) {
	dist, err := NewDistribution(p.Distribution, source)
	if err != nil {
		return nil, err
	}
	steps := stepsSpecOf(p.Steps, p.MinSteps, p.MaxSteps)
	if p.StepsValues != nil {
		steps = *p.StepsValues
	}
	method := p.Method
	if method == "" {
		method = "pdf"
	}
	var f func(float64) float64
	switch method {
	case "pdf":
		f = dist.PDF
	case "cdf":
		f = dist.CDF
	default:
		return nil, fmt.Errorf("Invalid density method: %s", method)
	}
	var lo, hi jsval.Value
	switch {
	case p.ExtentValues != nil:
		lo, hi = p.ExtentValues[0], p.ExtentValues[1]
	case p.Extent != nil:
		lo, hi = jsval.Num(p.Extent[0]), jsval.Num(p.Extent[1])
	default:
		k, ok := dist.(*KernelDensity)
		if !ok {
			return nil, fmt.Errorf("Missing density extent parameter.")
		}
		// extent(dist.data()): the data's least and greatest values as
		// they are
		lo, hi = rawExtent(jsval.WalkerFor(ctx), k.Data())
	}
	// The samples are new tuples: reserve them before they are drawn.
	if err := reserveRows(ctx, maxSamplesOf(steps)); err != nil {
		return nil, err
	}
	return sampleTuples(ctx, f, lo, hi, steps, nil, nil, p.As, p.AsExact, 1)
}

// maxSamples is the most points SampleCurve returns for these step bounds:
// it only halves an interval at least twice its resolution (span /
// maxsteps) wide, so no interval is narrower and there are at most maxsteps
// of them; past MaxSteps it fails. The bounds are spec numbers, compared in
// floating point before they become an int.
func maxSamples(minsteps, maxsteps float64) int {
	n := math.Max(minsteps, maxsteps)
	switch {
	case !(n > 0):
		return 1
	case n > MaxSteps:
		return MaxSteps + 1
	}
	return int(math.Ceil(n)) + 1
}

// sampleTuples runs sampleCurveJS over [lo, hi] and builds tuples: group-by
// names first, then the value and density fields (density scaled by scale).
func sampleTuples(ctx context.Context, f func(float64) float64, lo, hi jsval.Value, steps StepsSpec,
	names []string, dims []jsval.Value, as [2]string, exact bool, scale float64) ([]jsval.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// f can cost a pass over the data (kde), so poll as it is sampled; a
	// cancelled run yields NaN samples until SampleCurve's own budget ends.
	var cerr error
	calls := 0
	pf := f
	f = func(x float64) float64 {
		if calls++; calls&15 == 0 && cerr == nil {
			cerr = ctx.Err()
		}
		if cerr != nil {
			return math.NaN()
		}
		return pf(x)
	}
	pts, err := sampleCurveJS(jsval.WalkerFor(ctx), f, lo, hi, steps)
	if cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, err
	}
	a0, a1 := as[0], as[1]
	if a0 == "" && !exact {
		a0 = "value"
	}
	if a1 == "" && !exact {
		a1 = "density"
	}
	out := make([]jsval.Value, len(pts))
	for i, pt := range pts {
		o := jsval.NewObject(len(names) + 2)
		for j, n := range names {
			o.Set(n, dims[j])
		}
		o.Set(a0, pt.x)
		o.Set(a1, jsval.Num(pt.y*scale))
		out[i] = jsval.Obj(o)
	}
	return out, nil
}

// stepBounds is `steps || minsteps || 25` and `steps || maxsteps || 200`.
func stepBounds(steps, minsteps, maxsteps float64) (float64, float64) {
	orDefault := func(x, def float64) float64 {
		if x == 0 || math.IsNaN(x) {
			return def
		}
		return x
	}
	if steps != 0 && !math.IsNaN(steps) {
		return steps, steps
	}
	return orDefault(minsteps, 25), orDefault(maxsteps, 200)
}
