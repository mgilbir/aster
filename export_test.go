package aster

import (
	"context"
	"fmt"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/svgpdf"
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
// Like the comparisons that use it, it renders as upstream does: past the
// errors upstream logs.
func (c *Converter) VegaToSVGAfterSignalWritesForTest(spec []byte, writes [][2]string) (string, error) {
	release, ok := c.enter()
	if !ok {
		return "", ErrClosed
	}
	defer release()
	ctx, cancel := c.opContext(context.Background())
	defer cancel()
	var ws []vega.SignalWrite
	for _, w := range writes {
		v, err := jsval.ParseJSON([]byte(w[1]))
		if err != nil {
			return "", fmt.Errorf("signal %s: %w", w[0], err)
		}
		ws = append(ws, vega.SignalWrite{Name: w[0], Value: v})
	}
	reportFrom(ctx).handler = func(error) error { return nil }
	return c.vegaSVG(context.WithValue(ctx, signalWritesKey{}, ws), spec)
}

// StagesForTest renders spec to SVG, then that SVG to PNG and to PDF, and
// returns the time each stage took: json, compile (Vega-Lite), parse,
// dataflow, text (measuring, inside dataflow), svg, png and pdf.
func (c *Converter) StagesForTest(spec []byte, lite bool) (map[string]time.Duration, error) {
	release, ok := c.enter()
	if !ok {
		return nil, ErrClosed
	}
	defer release()
	ctx, cancel := c.opContext(context.Background())
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
	if _, _, err := c.svgToPDF(ctx, svg, nil, false, true); err != nil {
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
	m, shaper, err := c.fontSet()
	if err != nil {
		return nil, err
	}
	if !c.cfg.textMeasure {
		m = nil
	}
	return func() (err error) {
		release, ok := c.enter()
		if !ok {
			return ErrClosed
		}
		defer release()
		ctx, cancel := c.opContext(context.Background())
		defer cancel()
		defer recoverInto(&err)
		opts := vega.Options{
			Loader:   c.cfg.loader,
			Location: c.location,
			Now:      c.cfg.now,
			Config:   c.theme,
			Limits:   c.lim.vega,
			Random:   transforms.LCG(randomSeed),
			Shaper:   shaper,
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

// LimitsForTest is what each row of docs/limits.md's budget table is, in
// bytes or counts, for WithMemoryLimit(n); 0 for "none". It reads the bounds
// a call passes on, as the call sites read them, not their defaults: a bound
// the doc promises but no call applies must fail the doc test (C40).
func LimitsForTest(n uint64) map[string]int64 {
	c := deriveLimits(n, 0)
	l := c.vega
	r := c.raster.WithDefaults()
	return map[string]int64{
		"data rows":              int64(l.MaxRows),
		"scene items":            int64(l.MaxItems),
		"facet cells (subflows)": int64(l.MaxSubflows),
		"dataflow operators":     int64(l.MaxOperators),
		"loaded bytes (data and images of one call, together)":                  l.MaxLoadBytes,
		"parsed JSON (spec and data)":                                           l.MaxParseBytes,
		"expression strings and arrays":                                         l.MaxStringBytes,
		"bitmaps the heatmap transform paints":                                  l.MaxCanvasBytes,
		"SVG output":                                                            int64(c.svgBytes),
		"specification size":                                                    int64(c.specBytes),
		"glyphs of one shaped run of text":                                      c.shaping.RunBytes,
		"rasterizer canvas memory (PNG, and the `label` transform's drawing)":   int64(r.MaxCanvasBytes),
		"rasterizer pixels per canvas (PNG)":                                    int64(r.MaxPixels),
		"rasterizer path points held at once (PNG)":                             int64(r.MaxPathSegments),
		"pixels of one image (SVG, PNG and PDF output)":                         int64((&Converter{lim: c}).imagePixels()),
		"memory charged to the call's ledger":                                   c.ledger.MaxBytes,
		"Vega-Lite views":                                                       int64(c.vlViews),
		"SVG a chart's PDF is drawn from (`VegaToPDF`, `VegaLiteToPDF`), bytes": int64(c.pdfChart.MaxInputBytes),
		"SVG a chart's PDF is drawn from, elements":                             int64(c.pdfChart.MaxElements),
		"SVG a chart's PDF is drawn from, path segments":                        int64(c.pdfChart.MaxPathSegments),
		"SVG a chart's PDF is drawn from, text bytes":                           int64(c.pdfChart.MaxTextBytes),
		"SVG input to `SVGToPDF`, bytes":                                        int64(c.pdfInput.MaxInputBytes),
		"SVG input to `SVGToPDF`, elements":                                     int64(c.pdfInput.MaxElements),
		"SVG input to `SVGToPDF`, path segments":                                int64(c.pdfInput.MaxPathSegments),
		"SVG input to `SVGToPDF`, text bytes":                                   int64(c.pdfInput.MaxTextBytes),
	}
}

// WithFontSetFailureForTest makes the Converter's fonts fail to load.
func WithFontSetFailureForTest() Option {
	return func(c *config) { c.fontFailForTest = true }
}

// FixedLimitsForTest are the root package's own fixed bounds of
// docs/limits.md's fixed-bounds table, by name.
var FixedLimitsForTest = map[string]int64{
	"maxImageBytes": maxImageBytes, "maxImagePixels": maxImagePixels,
	"pixelOps": int64(deriveLimits(0, 0).pixelOps), "pixelOpsPerPixel": int64(deriveLimits(0, 0).pixelOpsPerPixel),
}

// PDFLimitsForTest are the PDF writer's bounds for a chart's SVG and for
// SVG input under WithMemoryLimit(n), and the SVG output budget.
func PDFLimitsForTest(n uint64) (chart, input svgpdf.Limits, svgBytes int) {
	c := deriveLimits(n, 0)
	return c.pdfLimits(true), c.pdfLimits(false), c.svgBytes
}
