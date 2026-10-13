package aster

import (
	"context"
	"errors"
	"strings"
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

// The parse of a specification's JSON polls the call's context (ADR 0010):
// a call whose context has ended stops in that parse, not after it, however
// large the specification. Counted in polls, not timed: the context ends
// before the call starts, and the error names the stage that saw it.
func TestSpecParsePolls(t *testing.T) {
	values := "[" + strings.Repeat("0,", 200_000) + "0]"
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		stage string
		run   func() error
	}{
		{"parsing Vega spec", func() error {
			_, err := c.VegaToSVG([]byte(`{"data":[{"name":"t","values":`+values+`}]}`), WithContext(ctx))
			return err
		}},
		{"parsing Vega-Lite spec", func() error {
			_, err := c.VegaLiteToSVG([]byte(`{"data":{"values":`+values+`},"mark":"point"}`), WithContext(ctx))
			return err
		}},
	} {
		err := tc.run()
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), tc.stage) {
			t.Errorf("a call whose context had ended returned %v; want the context's error, in %s", err, tc.stage)
		}
	}
}
