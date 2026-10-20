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
	// ProbValues, when not nil, are the probabilities as given, which the
	// output carries (Probs are them as numbers); ProbsNotArray reports
	// truthy probabilities that are no array, whose p.map is a TypeError
	// for the first group.
	ProbValues    []jsval.Value
	ProbsNotArray bool
	// Step is the probability step (0: 0.01; NaN stays NaN, no
	// probability), used when Probs is nil.
	Step float64
	// StepExact reports a Step the caller resolved (`_.step || 0.01` of a
	// truthy value that is 0 as a number stays 0).
	StepExact bool
	// As are the output fields; empty strings mean "prob" and "value".
	As [2]string
	// AsExact reports that As are the names as the runtime resolved them
	// (`_.as || defaults`, then each entry's text): an empty one is the
	// empty name, not the default.
	AsExact bool
}

// quantileEpsilon keeps the last generated probability strictly below 1.
const quantileEpsilon = 1e-14

// Quantile generates sample quantile values per group. Output tuples are new:
// group-by fields, then probability and value. A group without valid numbers
// gets an undefined value.
func Quantile(ctx context.Context, source []jsval.Value, p QuantileParams) ([]jsval.Value, error) {
	if p.GroupBy == nil && p.Field.Uncallable { // partition: data.map(field)
		return nil, &jsval.Thrown{Name: "TypeError", Msg: "object is not a function"}
	}
	as0, as1 := p.As[0], p.As[1]
	if as0 == "" && !p.AsExact {
		as0 = "prob"
	}
	if as1 == "" && !p.AsExact {
		as1 = "value"
	}
	probs := p.Probs
	if probs == nil {
		step := p.Step
		if step == 0 && !p.StepExact {
			step = 0.01
		}
		// d3.range(step/2, 1 - EPSILON, step)
		start, stop := step/2, 1-quantileEpsilon
		// d3.range: n = Math.max(0, Math.ceil((stop - start) / step)) | 0,
		// so NaN is no probability at all. Past MaxSteps is refused.
		n := math.Max(0, math.Ceil((stop-start)/step))
		if math.IsNaN(n) || math.IsInf(n, 0) { // NaN | 0 and Infinity | 0 are 0
			n = 0
		}
		if n > MaxSteps {
			return nil, limitErr("quantile probabilities", n, MaxSteps)
		}
		probs = make([]float64, int(n))
		for i := range probs {
			probs[i] = start + float64(float64(i)*step)
		}
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Key()
	}
	groups, err := Partition(keysOf(ctx), source, p.GroupBy)
	if err != nil {
		return nil, err
	}
	if err := reserveRows(ctx, int(min(budget.Mul(int64(len(groups)), int64(len(probs))), math.MaxInt32))); err != nil {
		return nil, err
	}
	var out []jsval.Value
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.ProbsNotArray {
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "p.map is not a function"}
		}
		q, err := Quantiles(ctx, g.Tuples, probs, p.Field.Get)
		if err != nil {
			return nil, err
		}
		for i, pr := range probs {
			o := jsval.NewObject(len(names) + 2)
			for j, n := range names {
				o.Set(n, g.Dims[j])
			}
			if p.ProbValues != nil {
				o.Set(as0, p.ProbValues[i])
			} else {
				o.Set(as0, jsval.Num(pr))
			}
			o.Set(as1, q[i]) // undefined for no values, NaN as d3 computes it
			out = append(out, jsval.Obj(o))
		}
	}
	return out, nil
}
