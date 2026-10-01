package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// FoldParams configures Fold.
type FoldParams struct {
	Fields []Field
	// As is the [key, value] output pair; empty entries default to "key" and
	// "value".
	As [2]string
}

// Fold turns each tuple into one tuple per field: a copy of the tuple with the
// field's name under As[0] and its value under As[1]. Output is grouped by
// input tuple, in field order.
func Fold(ctx context.Context, data []jsval.Value, p FoldParams) ([]jsval.Value, error) {
	k, v := p.As[0], p.As[1]
	if k == "" {
		k = "key"
	}
	if v == "" {
		v = "value"
	}
	if err := reserveOut(ctx, int(min(budget.Mul(int64(len(data)), int64(len(p.Fields))), math.MaxInt32)), len(data)); err != nil {
		return nil, err
	}
	if err := reserveFoldWeight(ctx, data, len(p.Fields)); err != nil {
		return nil, err
	}
	out := make([]jsval.Value, 0, len(data)*len(p.Fields))
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		for _, f := range p.Fields {
			d := shallowCopyTuple(t)
			d.Set(k, jsval.Str(f.Name))
			d.Set(v, f.Apply(t))
			out = append(out, jsval.Obj(d))
		}
	}
	return out, nil
}

// reserveFoldWeight checks, before the copies exist, that the rows weigh what
// the budget allows: a copy of a tuple holds the tuple's fields and two more,
// and the row count alone does not see a tuple of many fields copied once per
// folded field.
func reserveFoldWeight(ctx context.Context, data []jsval.Value, copies int) error {
	b := budget.From(ctx)
	if b == nil || b.RowBytes <= 0 {
		return nil
	}
	var in, out int64
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return err
		}
		c := jsval.RowCost(t)
		in += max(c-b.RowBytes, 0)
		out += budget.Mul(int64(copies), max(c+2*jsval.RowField-b.RowBytes, 0))
	}
	return b.ReserveWeight(0, out-in)
}
