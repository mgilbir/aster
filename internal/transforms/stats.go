package transforms

import (
	"context"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Rand is a source of uniform random numbers in [0, 1), Math.random's shape.
// vega-statistics reads a single replaceable global; here every consumer takes
// one explicitly so results are reproducible when the runtime seeds it.
type Rand func() float64

// LCG is vega-statistics' seeded generator (glibc constants). Its arithmetic
// is done in float64 exactly as upstream's JavaScript does, including the
// rounding of the product when it exceeds 2^53, so seeded sequences match.
func LCG(seed float64) Rand {
	return func() float64 {
		// The explicit conversion stops the compiler fusing the multiply-add,
		// which would change the rounding.
		seed = math.Mod(float64(1103515245*seed)+12345, 2147483647)
		return seed / 2147483647
	}
}

// Numbers is vega-statistics' numbers(): the values of the tuples under f
// coerced with unary plus, skipping null/undefined, "" and NaN. Each tuple
// read is a step of work charged to ctx's ledger, which polls ctx.
func Numbers(ctx context.Context, data []jsval.Value, f Accessor) ([]float64, error) {
	m := budget.NewMeter(ctx)
	out := make([]float64, 0, len(data))
	for _, t := range data {
		if err := m.Work(1); err != nil {
			return nil, err
		}
		v := f(t)
		if v.IsNullish() || (v.IsStr() && v.StrValue() == "") {
			continue
		}
		if f := jsval.ToNumber(v); f >= f {
			out = append(out, f)
		}
	}
	return out, nil
}

// QuantileSorted is d3.quantileSorted (R-7 interpolation). ok is false for an
// empty slice or NaN p.
func QuantileSorted(sorted []float64, p float64) (float64, bool) {
	n := len(sorted)
	if n == 0 || math.IsNaN(p) {
		return 0, false
	}
	if p <= 0 || n < 2 {
		return sorted[0], true
	}
	if p >= 1 {
		return sorted[n-1], true
	}
	i := float64(float64(n-1) * p)
	i0 := int(math.Floor(i))
	v0, v1 := sorted[i0], sorted[i0+1]
	return v0 + float64((v1-v0)*(i-float64(i0))), true
}

// Quantiles is vega-statistics' quantiles(array, p, f): d3's quantileSorted
// of the valid numbers for each p, undefined when there are none (or p is
// NaN), and NaN where the interpolation makes it (two infinities).
func Quantiles(ctx context.Context, data []jsval.Value, p []float64, f Accessor) ([]jsval.Value, error) {
	vals, err := Numbers(ctx, data, f)
	if err != nil {
		return nil, err
	}
	slices.Sort(vals)
	out := make([]jsval.Value, len(p))
	for i, q := range p {
		if v, ok := QuantileSorted(vals, q); ok {
			out[i] = jsval.Num(v)
		}
	}
	return out, nil
}

// Quartiles is vega-statistics' quartiles: the 25th, 50th and 75th percentiles.
func Quartiles(ctx context.Context, data []jsval.Value, f Accessor) ([3]jsval.Value, error) {
	q, err := Quantiles(ctx, data, []float64{0.25, 0.5, 0.75}, f)
	if err != nil {
		return [3]jsval.Value{}, err
	}
	return [3]jsval.Value{q[0], q[1], q[2]}, nil
}

// BootstrapCI is vega-statistics' bootstrapCI: a percentile bootstrap
// confidence interval of the mean using `samples` resamples. ok is false when
// the input is empty or no resample has a finite mean (upstream: undefined).
// A resample costs as much as the data is long, a step of work a value
// charged to ctx's ledger, which polls ctx.
func BootstrapCI(ctx context.Context, data []jsval.Value, samples int, alpha float64, f Accessor, rand Rand) (lo, hi float64, ok bool, err error) {
	if len(data) == 0 {
		return 0, 0, false, nil
	}
	values, err := Numbers(ctx, data, f)
	if err != nil {
		return 0, 0, false, err
	}
	n := len(values)
	m := budget.NewMeter(ctx)
	mu := make([]float64, 0, samples)
	for j := 0; j < samples; j++ {
		if err := m.Work(int64(n + 1)); err != nil {
			return 0, 0, false, err
		}
		a := 0.0
		for i := 0; i < n; i++ {
			a += values[int(rand()*float64(n))]
		}
		if m := a / float64(n); !math.IsNaN(m) {
			mu = append(mu, m)
		}
	}
	slices.Sort(mu)
	lo, ok1 := QuantileSorted(mu, alpha/2)
	hi, ok2 := QuantileSorted(mu, 1-alpha/2)
	return lo, hi, ok1 && ok2, nil
}

// DefaultRand is used where a transform needs randomness and the caller gave
// none. It matches upstream's Math.random default: unseeded.
var DefaultRand Rand = rand.Float64
