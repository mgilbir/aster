package transforms

import (
	"context"
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Limits on work a specification can request. They are far above anything a
// real chart needs; exceeding one is an error, not a truncation.
const (
	// MaxGroupCells bounds the cells aggregate's cross product may create.
	MaxGroupCells = 4_000_000
	// MaxGroupDims bounds the group-by fields of an aggregate with cross: its
	// cell enumeration recurses once per field, and fields with a single value
	// add no cells, so MaxGroupCells does not bound them.
	MaxGroupDims = 256
	// MaxSequence bounds the length of a sequence transform.
	MaxSequence = 10_000_000
	// MaxBins bounds the number of bins (and bin steps) a bin transform
	// searches or produces.
	MaxBins = 1_000_000
	// MaxSteps bounds sampled curve points (density, regression).
	MaxSteps = 1_000_000
	// MaxPivotWork bounds rows x distinct values of a pivot.
	MaxPivotWork = 20_000_000
)

// ErrLimit is wrapped by errors reporting an exceeded work limit.
var ErrLimit = budget.ErrLimit

// limitErr reports a count n over limit. n is the number the specification
// asked for, printed as JavaScript prints it (1e+300, Infinity), never a
// clamped or wrapped int.
func limitErr(what string, n float64, limit int) error {
	return fmt.Errorf("%w: %s %s exceeds %d", ErrLimit, what, jsval.JSNumberString(n), limit)
}

// ctxCheckMask sets how often loops poll the context: every 4096 iterations.
const ctxCheckMask = 4095

// poll reports the context error on every 4096th iteration i.
func poll(ctx context.Context, i int) error {
	if i&ctxCheckMask == 0 {
		return ctx.Err()
	}
	return nil
}

// meteredAccessor is f with each call a step of work charged to ctx's
// ledger, which polls ctx: for a helper that reads every row through f and
// meters nothing itself (a fit's passes over its points). Once ctx is done
// f is no longer called and the rows read as undefined, which every such
// helper skips quickly; err reports the context's error, to return when the
// helper is over.
func meteredAccessor(ctx context.Context, f Accessor) (get Accessor, err func() error) {
	m := budget.NewMeter(ctx)
	var done error
	get = func(v jsval.Value) jsval.Value {
		if done != nil {
			return jsval.Undefined
		}
		if done = m.Work(1); done != nil {
			return jsval.Undefined
		}
		return f(v)
	}
	return get, func() error { return done }
}

// dimValues reads the group-by values of t.
func dimValues(fields []Field, t jsval.Value) []jsval.Value {
	out := make([]jsval.Value, len(fields))
	for i, f := range fields {
		out[i] = f.Get(t)
	}
	return out
}

// reserveRows fails, before anything is allocated, when the n rows a
// transform is about to make would exceed the render's row budget. The rows
// it passes on are held by the operators that made them, and those its
// previous run made are being replaced, so only the new ones count.
func reserveRows(ctx context.Context, n int) error {
	return budget.Reserve(ctx, int64(n))
}
