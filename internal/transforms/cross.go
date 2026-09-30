package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// CrossParams configures Cross.
type CrossParams struct {
	// Filter, when non-nil, keeps a pair if it returns true. It receives the
	// candidate tuple {a, b}.
	Filter func(jsval.Value) bool
	// As names the left and right fields; empty entries default to "a", "b".
	As [2]string
}

// Cross is the self cross-product: every ordered pair (left, right) of input
// tuples, left-major, as new tuples {a: left, b: right} that pass Filter. The
// context is polled while pairs are examined, and more than MaxGroupCells
// output tuples is an error.
func Cross(ctx context.Context, data []jsval.Value, p CrossParams) ([]jsval.Value, error) {
	a, b := p.As[0], p.As[1]
	if a == "" {
		a = "a"
	}
	if b == "" {
		b = "b"
	}
	if p.Filter == nil && len(data) > 0 && len(data) > MaxGroupCells/len(data) {
		return nil, limitErr("cross tuples", len(data)*len(data), MaxGroupCells)
	}
	if p.Filter == nil {
		if err := reserveOut(ctx, int(min(budget.Mul(int64(len(data)), int64(len(data))), math.MaxInt32)), len(data)); err != nil {
			return nil, err
		}
	}
	room := budget.From(ctx).RowsLeft()
	var out []jsval.Value
	n := len(data)
	work := 0
	var t *jsval.Object
	for i := 0; i < n; i++ {
		left := data[i]
		t = jsval.NewObject(2)
		t.Set(a, left)
		for j := 0; j < n; j++ {
			if err := poll(ctx, work); err != nil {
				return nil, err
			}
			work++
			// Like upstream, one scratch tuple is reused until a pair passes.
			t.Set(b, data[j])
			if p.Filter == nil || p.Filter(jsval.Obj(t)) {
				if len(out) >= MaxGroupCells {
					return nil, limitErr("cross tuples", len(out)+1, MaxGroupCells)
				}
				if int64(len(out)) >= room {
					return nil, reserveOut(ctx, len(out)+1, 0)
				}
				out = append(out, jsval.Obj(t))
				t = jsval.NewObject(2)
				t.Set(a, left)
			}
		}
	}
	return out, nil
}
