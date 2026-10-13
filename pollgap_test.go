package aster

import (
	"context"
	"errors"
	"testing"
)

// pollsCtx ends after its Err has been polled n times: a deterministic way to
// end a call partway, which no machine's speed changes.
type pollsCtx struct {
	context.Context
	n int
}

func (c *pollsCtx) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

// The harness's big-body Loader polls its context as it builds the body, so a
// call ending partway is not held until 32 MiB exist: a context that ends at
// the third poll stops it with the body unbuilt. Counted in polls, not timed;
// a fraction of wall-clock time between polls failed on a loaded runner.
func TestBigLoaderStopsPartway(t *testing.T) {
	for _, kind := range []string{"bigcsv", "bigjson"} {
		l := &bigLoader{kind: kind, mb: 32}
		b, err := l.Load(&pollsCtx{Context: context.Background(), n: 2}, "x")
		if !errors.Is(err, context.Canceled) || b != nil {
			t.Errorf("%s: %d bytes, %v; want it stopped by the context", kind, len(b), err)
		}
	}
}
