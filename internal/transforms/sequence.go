package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// SequenceParams configures Sequence.
type SequenceParams struct {
	Start, Stop float64
	// Step defaults to 1 when zero or NaN (upstream: `_.step || 1`).
	Step float64
	// As is the output field (default "data").
	As string
}

// Sequence generates tuples {as: start + i*step} for i in [0, ceil((stop-start)/step)),
// d3.range semantics: nothing when the count is not positive or not finite.
// More than MaxSequence values is an error.
func Sequence(ctx context.Context, p SequenceParams) ([]jsval.Value, error) {
	step := p.Step
	if step == 0 || math.IsNaN(step) {
		step = 1
	}
	as := p.As
	if as == "" {
		as = "data"
	}
	c := math.Ceil((p.Stop - p.Start) / step)
	if math.IsNaN(c) || math.IsInf(c, 0) || c <= 0 {
		return nil, nil
	}
	if c > MaxSequence {
		return nil, limitErr("sequence length", int(min(c, math.MaxInt32)), MaxSequence)
	}
	n := int(c)
	if err := reserveOut(ctx, n, 0); err != nil {
		return nil, err
	}
	out := make([]jsval.Value, n)
	for i := 0; i < n; i++ {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		o := jsval.NewObject(1)
		o.Set(as, jsval.Num(p.Start+float64(float64(i)*step)))
		out[i] = jsval.Obj(o)
	}
	return out, nil
}
