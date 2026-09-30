package transforms

import (
	"context"
	"slices"

	"github.com/mgilbir/aster/internal/jsval"
)

// Collect is vega-transforms' collect: it returns the tuples sorted by cmp.
// The sort is stable, as Array.prototype.sort is, so tuples that compare equal
// keep their input order. A nil comparator returns a copy in input order. The
// input slice is not modified.
func Collect(ctx context.Context, data []jsval.Value, cmp Comparator) ([]jsval.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := slices.Clone(data)
	if cmp != nil {
		SortTuples(out, cmp)
	}
	return out, ctx.Err()
}
