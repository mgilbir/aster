package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
	case "normal":
		return NewNormal(val(spec.Mean, 0), val(spec.Stdev, 1)), nil
	case "lognormal":
		return NewLogNormal(val(spec.Mean, 0), val(spec.Stdev, 1)), nil
	case "uniform":
		return NewUniform(val(spec.Min, 0), val(spec.Max, 1)), nil
	case "kde":
		return NewKernelDensity(kdeSupport(spec, source), spec.Bandwidth), nil
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
	// Steps overrides both MinSteps and MaxSteps when non-zero.
	Steps    float64
	MinSteps float64 // default 25
	MaxSteps float64 // default 200
	// As are the output fields; empty strings mean "value" and "density".
	As [2]string
}

// Density samples a distribution's pdf or cdf into (value, density) tuples,
// adaptively refined where the curve bends (SampleCurve). source supplies
// input tuples for a kde without explicit data. The output tuples are new.
func Density(ctx context.Context, source []jsval.Value, p DensityParams) ([]jsval.Value, error) {
	dist, err := NewDistribution(p.Distribution, source)
	if err != nil {
		return nil, err
	}
	minsteps, maxsteps := stepBounds(p.Steps, p.MinSteps, p.MaxSteps)
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
	var domain [2]float64
	valid := true
	if p.Extent != nil {
		domain = *p.Extent
	} else {
		k, ok := dist.(*KernelDensity)
		if !ok {
			return nil, fmt.Errorf("Missing density extent parameter.")
		}
		domain, valid = valuesExtent(k.Data())
	}
	return sampleTuples(ctx, f, domain, valid, minsteps, maxsteps, nil, nil, p.As, 1)
}

// valuesExtent is vega-util's extent over raw values, read as numbers.
// valid is false when there is no valid value: upstream's extent is then
// [undefined, undefined] and the curve's end points carry an undefined x.
// (When the extreme values are strings upstream also leaks them, unconverted,
// as the x of the end points; here they are numbers.)
func valuesExtent(vs []jsval.Value) (e [2]float64, valid bool) {
	lo, hi, ok := Extent(len(vs), func(i int) jsval.Value { return vs[i] })
	if !ok || lo.IsUndefined() {
		return [2]float64{math.NaN(), math.NaN()}, false
	}
	return [2]float64{jsval.ToNumber(lo), jsval.ToNumber(hi)}, true
}

// sampleTuples runs SampleCurve and builds tuples: group-by names first, then
// the value and density fields (density scaled by scale).
func sampleTuples(ctx context.Context, f func(float64) float64, domain [2]float64, validDomain bool, minsteps, maxsteps float64,
	names []string, dims []jsval.Value, as [2]string, scale float64) ([]jsval.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pts, err := SampleCurve(f, domain, minsteps, maxsteps)
	if err != nil {
		return nil, err
	}
	a0, a1 := as[0], as[1]
	if a0 == "" {
		a0 = "value"
	}
	if a1 == "" {
		a1 = "density"
	}
	out := make([]jsval.Value, len(pts))
	for i, pt := range pts {
		o := jsval.NewObject(len(names) + 2)
		for j, n := range names {
			o.Set(n, dims[j])
		}
		if !validDomain && (i == 0 || i == len(pts)-1) {
			o.Set(a0, jsval.Undefined)
		} else {
			o.Set(a0, jsval.Num(pt[0]))
		}
		o.Set(a1, jsval.Num(pt[1]*scale))
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
