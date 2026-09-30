package transforms

import (
	"context"
	"github.com/mgilbir/aster/internal/budget"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// QuantileParams configures the Quantile transform.
type QuantileParams struct {
	GroupBy []Field
	Field   Field
	// Probs are the probabilities to sample; nil means every step starting at
	// Step/2 and below 1.
	Probs []float64
	// Step is the probability step (default 0.01), used when Probs is nil.
	Step float64
	// As are the output fields; empty strings mean "prob" and "value".
	As [2]string
}

// quantileEpsilon keeps the last generated probability strictly below 1.
const quantileEpsilon = 1e-14

// Quantile generates sample quantile values per group. Output tuples are new:
// group-by fields, then probability and value. A group without valid numbers
// gets an undefined value.
func Quantile(ctx context.Context, source []jsval.Value, p QuantileParams) ([]jsval.Value, error) {
	as0, as1 := p.As[0], p.As[1]
	if as0 == "" {
		as0 = "prob"
	}
	if as1 == "" {
		as1 = "value"
	}
	probs := p.Probs
	if probs == nil {
		step := p.Step
		if step == 0 || math.IsNaN(step) {
			step = 0.01
		}
		// d3.range(step/2, 1 - EPSILON, step)
		start, stop := step/2, 1-quantileEpsilon
		n := math.Max(0, math.Ceil((stop-start)/step))
		if math.IsNaN(n) || n > MaxSteps {
			return nil, limitErr("quantile probabilities", int(min(n, math.MaxInt32)), MaxSteps)
		}
		probs = make([]float64, int(n))
		for i := range probs {
			probs[i] = start + float64(float64(i)*step)
		}
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Name
	}
	groups := Partition(source, p.GroupBy)
	if err := reserveOut(ctx, int(min(budget.Mul(int64(len(groups)), int64(len(probs))), math.MaxInt32)), len(source)); err != nil {
		return nil, err
	}
	var out []jsval.Value
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q := Quantiles(g.Tuples, probs, p.Field.Get)
		for i, pr := range probs {
			o := jsval.NewObject(len(names) + 2)
			for j, n := range names {
				o.Set(n, g.Dims[j])
			}
			o.Set(as0, jsval.Num(pr))
			if math.IsNaN(q[i]) {
				o.Set(as1, jsval.Undefined)
			} else {
				o.Set(as1, jsval.Num(q[i]))
			}
			out = append(out, jsval.Obj(o))
		}
	}
	return out, nil
}
