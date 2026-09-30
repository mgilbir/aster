package transforms

import (
	"context"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Filter keeps the tuples for which pred is true, in input order.
func Filter(ctx context.Context, data []jsval.Value, pred func(jsval.Value) bool) ([]jsval.Value, error) {
	out := make([]jsval.Value, 0, len(data))
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		if pred(t) {
			out = append(out, t)
		}
	}
	return out, nil
}
