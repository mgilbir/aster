package aster

import (
	"context"
	"fmt"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/vega"
)

// WithClockForTest pins the clock now() and datetime() read, so a render
// that draws the current time can be compared with the node oracle, which
// pins the same instant.
func WithClockForTest(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}

// VegaToSVGAfterSignalWritesForTest renders a Vega spec, writes each signal
// (name, JSON value) as View.signal does, re-running after each, and returns
// the chart after the last write.
func (c *Converter) VegaToSVGAfterSignalWritesForTest(spec []byte, writes [][2]string) (string, error) {
	release, ok := c.enter()
	if !ok {
		return "", errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	var ws []vega.SignalWrite
	for _, w := range writes {
		v, err := jsval.ParseJSON([]byte(w[1]))
		if err != nil {
			return "", fmt.Errorf("signal %s: %w", w[0], err)
		}
		ws = append(ws, vega.SignalWrite{Name: w[0], Value: v})
	}
	return c.vegaSVG(context.WithValue(ctx, signalWritesKey{}, ws), spec)
}
