package aster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// A bound or the call's end that stops a stage from deep inside it, as a
// *budget.Stop panic (text shaping inside the SVG writer, the rasterizer, the
// PDF writer), is that stage's error as one returned would be: its message
// names the stage, and the timeout when the call's WithTimeout ended it, as
// errors.md says (C58: it was "aster: context deadline exceeded").
func TestStopPanicNamesTheStage(t *testing.T) {
	c, err := New(WithTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := []byte(`{"width":20,"height":20,"marks":[{"type":"rect","encode":{"enter":{"width":{"value":5},"height":{"value":5}}}}]}`)
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		t.Fatal(err)
	}
	limit := fmt.Errorf("%w: a test bound", ErrLimit)
	for _, tc := range []struct {
		hook, stage string
		run         func(ctx context.Context) error
	}{
		{"svg", "writing SVG", func(ctx context.Context) error { _, err := c.vegaSVG(ctx, spec); return err }},
		{"png", "rendering PNG", func(ctx context.Context) error { _, err := c.svgToPNG(ctx, svg, defaultPNGConfig()); return err }},
		{"pdf", "rendering PDF", func(ctx context.Context) error {
			_, _, err := c.svgToPDF(ctx, svg, 0, false, false)
			return err
		}},
	} {
		for _, stop := range []error{limit, context.DeadlineExceeded} {
			release, ok := c.enter()
			if !ok {
				t.Fatal("closed")
			}
			ctx, cancel := c.opContext(context.Background())
			ctx = context.WithValue(ctx, stagesKey{}, func(s string, _ time.Duration) {
				if s == tc.hook {
					panic(&budget.Stop{Err: stop})
				}
			})
			err := tc.run(ctx)
			cancel()
			release()
			if !errors.Is(err, stop) || !strings.Contains(err.Error(), tc.stage) {
				t.Errorf("%s stopped by %v: %v, want it wrapped and the stage named (%s)", tc.hook, stop, err, tc.stage)
			}
		}
	}
}
