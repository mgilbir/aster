package aster

import (
	"context"
	"fmt"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/text"
	"github.com/mgilbir/aster/internal/transforms"
	"github.com/mgilbir/aster/internal/transforms/wordcloud"
	"github.com/mgilbir/aster/internal/vega"
)

// WithClockForTest pins the clock now() and datetime() read, so a render
// that draws the current time can be compared with the node oracle, which
// pins the same instant.
func WithClockForTest(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}

// WithPangoTextForTest measures text the way the oracle's node-canvas does:
// every glyph advance a whole number of 1/1024 px, rounded to nearest as
// HarfBuzz 3 and later do (Pango 1.57 on macOS), or floored as HarfBuzz 2 does
// (the Pango 1.48 node-canvas bundles on Linux) when floor is set. The widths
// then agree with the oracle's to the last bit, not only to a tolerance, so a
// layout that rounds a width up cannot land on the other side of an integer.
func WithPangoTextForTest(floor bool) Option {
	return func(c *config) {
		c.pangoText = 1
		if floor {
			c.pangoText = 2
		}
	}
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

// StagesForTest renders spec to SVG, then that SVG to PNG and to PDF, and
// returns the time each stage took: json, compile (Vega-Lite), parse,
// dataflow, text (measuring, inside dataflow), svg, png and pdf.
func (c *Converter) StagesForTest(spec []byte, lite bool) (map[string]time.Duration, error) {
	release, ok := c.enter()
	if !ok {
		return nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	stages := map[string]time.Duration{}
	ctx = context.WithValue(ctx, stagesKey{}, func(s string, d time.Duration) { stages[s] += d })
	var svg string
	var err error
	if lite {
		svg, err = c.vegaLiteSVG(ctx, spec)
	} else {
		svg, err = c.vegaSVG(ctx, spec)
	}
	if err != nil {
		return nil, err
	}
	if _, err := c.svgToPNG(ctx, svg, nil); err != nil {
		return nil, err
	}
	if _, _, err := c.svgToPDF(ctx, svg, nil); err != nil {
		return nil, err
	}
	return stages, nil
}

// RenderPreparedForTest compiles spec once and returns a function that runs
// the dataflow (vega.Render: the Vega spec to a scenegraph) with the options
// a render passes, without the SVG, PNG and PDF writers, so a profile of the
// function is a profile of the dataflow stage.
func (c *Converter) RenderPreparedForTest(spec []byte, lite bool) (func() error, error) {
	ctx := context.Background()
	var vg jsval.Value
	var err error
	if lite {
		vg, err = c.compileVegaLite(ctx, spec)
	} else {
		vg, err = jsval.ParseJSON(spec)
	}
	if err != nil {
		return nil, err
	}
	m, err := c.measurerInit()
	if err != nil {
		return nil, err
	}
	return func() (err error) {
		release, ok := c.enter()
		if !ok {
			return errConverterClosed
		}
		defer release()
		ctx, cancel := c.opContext()
		defer cancel()
		defer recoverInto(&err)
		opts := vega.Options{
			Loader:   c.cfg.loader,
			Location: c.location,
			Now:      c.cfg.now,
			Config:   c.theme,
			Limits:   c.limits(),
			Random:   transforms.LCG(randomSeed),
			Shaper:   lazyShaper{c},
		}
		if m != nil {
			opts.TextMeasurer = text.NewCanvasContext(m)
			opts.WordcloudText = wordcloud.NewCanvasRenderer(m)
		}
		_, err = vega.Render(ctx, vg, opts)
		return err
	}, nil
}

// RaceSlowdownForTest is raceSlowdown, for the external tests' time limits.
const RaceSlowdownForTest = raceSlowdown
