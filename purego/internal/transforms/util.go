package transforms

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Limits on work a specification can request. They are far above anything a
// real chart needs; exceeding one is an error, not a truncation.
const (
	// MaxGroupCells bounds the cells aggregate's cross product may create.
	MaxGroupCells = 4_000_000
	// MaxSequence bounds the length of a sequence transform.
	MaxSequence = 10_000_000
	// MaxBins bounds the number of bins (and bin steps) a bin transform
	// searches or produces.
	MaxBins = 1_000_000
	// MaxSteps bounds sampled curve points (density, regression).
	MaxSteps = 1_000_000
)

// ErrLimit is wrapped by errors reporting an exceeded work limit.
var ErrLimit = errors.New("transforms: limit exceeded")

func limitErr(what string, n, limit int) error {
	return fmt.Errorf("%w: %s %d exceeds %d", ErrLimit, what, n, limit)
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

// dimValues reads the group-by values of t.
func dimValues(fields []Field, t jsval.Value) []jsval.Value {
	out := make([]jsval.Value, len(fields))
	for i, f := range fields {
		out[i] = f.Get(t)
	}
	return out
}
