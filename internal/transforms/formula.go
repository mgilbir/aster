package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// FormulaParams configures Formula.
type FormulaParams struct {
	// Expr computes the value of the new field from a tuple.
	Expr Accessor
	// As is the field written on each tuple.
	As string
}

// Formula writes Expr(t) to field As of every tuple, in place, and returns the
// input slice. Upstream's initonly flag only matters when a dataflow re-runs,
// so a one-shot call has no use for it. Values that are not objects cannot
// carry fields and are left untouched.
func Formula(ctx context.Context, data []jsval.Value, p FormulaParams) ([]jsval.Value, error) {
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		// `t[as] = expr(t)`: the value first, then the write, which
		// throws into a primitive tuple.
		v := p.Expr(t)
		jsval.FieldsFor(t, p.As).Set(p.As, v)
	}
	return data, nil
}
