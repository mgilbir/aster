package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/purego/internal/budget"
	"github.com/mgilbir/aster/purego/internal/jsval"
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
