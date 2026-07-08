// Package aster converts Vega and Vega-Lite visualization specs to SVG, PNG
// and vector PDF. It embeds Vega/Vega-Lite inside QuickJS (via WASM) for a
// pure-Go, CGO-free solution.
//
// Basic usage:
//
//	c, err := aster.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer c.Close()
//
//	svg, err := c.VegaLiteToSVG(specJSON)
//	png, err := c.VegaLiteToPNG(specJSON)
//	pdf, err := c.VegaLiteToPDF(specJSON)
package aster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"

	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/runtime"
	"github.com/mgilbir/aster/internal/svgpdf"
	"github.com/mgilbir/aster/internal/textmeasure"
)

// Converter renders Vega/Vega-Lite specs to SVG, PNG and PDF.
type Converter struct {
	rt       *runtime.Runtime
	measurer *textmeasure.Measurer
	fonts    fontPlan // shared by text measurement and PNG rasterization
	loader   Loader   // stashed for Close()
	closed   bool     // set by Close; every entry point checks it

	pngOnce     sync.Once
	pngRenderer *resvg.Renderer
	pngErr      error

	// PDF output shapes text with a Measurer. Normally the converter's own
	// measurer is reused; when text measurement was disabled via
	// WithTextMeasurement(false), one is created lazily for PDF use only.
	pdfMeasurerOnce sync.Once
	pdfMeasurer     *textmeasure.Measurer
	pdfMeasurerErr  error
}

// errConverterClosed is returned by every rendering method after Close.
var errConverterClosed = errors.New("aster: converter is closed")

// New creates a new Converter with the given options.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	// The QuickJS WASM runtime has no timezone database; only UTC (via a Date
	// polyfill) is implemented. Failing here beats silently rendering with an
	// unexpected timezone.
	if cfg.timezone != "" && cfg.timezone != "UTC" {
		return nil, fmt.Errorf("aster: unsupported timezone %q (only \"UTC\" is supported)", cfg.timezone)
	}

	// Both pipelines are configured from a single font plan so SVG layout and
	// PNG rasterization can't disagree about fonts or generic-family mappings.
	plan := newFontPlan(cfg)

	var measurer *textmeasure.Measurer
	var tm runtime.TextMeasurer
	if cfg.textMeasure {
		var err error
		measurer, err = textmeasure.New(plan.measurerOptions()...)
		if err != nil {
			return nil, fmt.Errorf("aster: initializing text measurer: %w", err)
		}
		tm = measurer
	}

	rtCfg := runtime.Config{
		Loader:       cfg.loader,
		TextMeasurer: tm,
		Theme:        cfg.theme,
		MemoryLimit:  cfg.memoryLimit,
		Timeout:      cfg.timeout,
		Version:      cfg.vegaLiteVersion,
		Timezone:     cfg.timezone,
	}

	rt, err := runtime.New(rtCfg)
	if err != nil {
		// runtime.New already namespaces its errors ("aster/runtime: ...");
		// don't double-prefix.
		return nil, err
	}

	return &Converter{
		rt:       rt,
		measurer: measurer,
		fonts:    plan,
		loader:   cfg.loader,
	}, nil
}

// VersionInfo describes an available Vega-Lite version set.
type VersionInfo struct {
	Key             string // internal key accepted by the runtime, e.g. "vl6_4"
	VegaVersion     string // resolved Vega runtime version, e.g. "6.2.0"
	VegaLiteVersion string // Vega-Lite version, e.g. "6.4.0"
}

// AvailableVersions reports the Vega-Lite version sets bundled in this build,
// sorted by key. Pass a VegaLiteVersion (e.g. "6.4") to WithVegaLiteVersion.
func AvailableVersions() ([]VersionInfo, error) {
	m, err := runtime.AvailableVersions()
	if err != nil {
		return nil, err
	}
	out := make([]VersionInfo, 0, len(m))
	for k, v := range m {
		out = append(out, VersionInfo{Key: k, VegaVersion: v.VegaVersion, VegaLiteVersion: v.VegaLiteVersion})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Close releases all resources held by the Converter. It is safe to call
// multiple times; after Close every rendering method returns an error.
func (c *Converter) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	var firstErr error
	if c.pngRenderer != nil {
		if err := c.pngRenderer.Close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.rt != nil {
		if err := c.rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if closer, ok := c.loader.(io.Closer); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// VegaToSVG renders a Vega spec (JSON) to an SVG string.
func (c *Converter) VegaToSVG(spec []byte) (string, error) {
	if c.closed {
		return "", errConverterClosed
	}
	return c.rt.VegaToSVG(string(spec))
}

// VegaLiteToSVG renders a Vega-Lite spec (JSON) to an SVG string.
func (c *Converter) VegaLiteToSVG(spec []byte) (string, error) {
	if c.closed {
		return "", errConverterClosed
	}
	return c.rt.VegaLiteToSVG(string(spec))
}

// VegaLiteToVega compiles a Vega-Lite spec (JSON) to a full Vega spec (JSON).
func (c *Converter) VegaLiteToVega(spec []byte) ([]byte, error) {
	if c.closed {
		return nil, errConverterClosed
	}
	result, err := c.rt.VegaLiteToVega(string(spec))
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// VegaToPNG renders a Vega spec (JSON) to a PNG image.
func (c *Converter) VegaToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPNG(svg, opts...)
}

// VegaLiteToPNG renders a Vega-Lite spec (JSON) to a PNG image.
func (c *Converter) VegaLiteToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPNG(svg, opts...)
}

// SVGToPNG converts an SVG string to a PNG image using resvg.
func (c *Converter) SVGToPNG(svg string, opts ...PNGOption) ([]byte, error) {
	if c.closed {
		// Without this guard a post-Close call would lazily instantiate a
		// fresh PNG renderer that nothing would ever release.
		return nil, errConverterClosed
	}
	cfg := defaultPNGConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	if !(cfg.scale > 0) || math.IsInf(cfg.scale, 1) {
		return nil, fmt.Errorf("aster: invalid PNG scale %v (must be a positive, finite number)", cfg.scale)
	}

	r, err := c.pngRendererInit()
	if err != nil {
		return nil, err
	}

	out, err := r.Render(context.Background(), []byte(svg), cfg.scale)
	if err != nil {
		return nil, err
	}
	switch {
	case cfg.quantizeColors > 0:
		out = quantizeOrRecodePNG(out, cfg.quantizeColors)
	case cfg.recode:
		out = recodePNG(out)
	}
	return out, nil
}

// PDFTextMode selects how text is represented in PDF output.
type PDFTextMode int

const (
	// PDFTextEmbed (the default) emits real PDF text with subset TrueType
	// fonts embedded: only the glyphs a chart uses ship, once, in the font
	// program, and each occurrence costs two bytes. Output is self-contained
	// and text is selectable and searchable. Text whose font cannot be
	// embedded (CFF/OTF outlines, unloadable system fonts) falls back to
	// glyph outlines automatically.
	PDFTextEmbed PDFTextMode = iota

	// PDFTextNamed emits the same PDF text structure without embedding the
	// font program: fonts are referenced by name only, so the output is as
	// small as it gets. Glyphs are addressed by the IDs of the exact font
	// file used at generation time — the consuming pipeline must embed that
	// same font file when assembling the final document, or viewers will
	// substitute a different font and may draw the wrong glyphs. Use this
	// when generating many charts whose fonts are embedded once at assembly
	// time.
	PDFTextNamed

	// PDFTextOutlines converts every glyph occurrence to filled path
	// outlines. No fonts are referenced or embedded at all; output is much
	// larger and text is not selectable, but nothing can go wrong with font
	// handling downstream.
	PDFTextOutlines
)

// PDFOption configures a single PDF render operation.
type PDFOption func(*pdfConfig)

type pdfConfig struct {
	text PDFTextMode
}

func defaultPDFConfig() *pdfConfig {
	return &pdfConfig{text: PDFTextEmbed}
}

// WithPDFText selects how text is represented in the PDF; see the
// PDFTextMode constants. The default is PDFTextEmbed.
func WithPDFText(mode PDFTextMode) PDFOption {
	return func(c *pdfConfig) {
		c.text = mode
	}
}

// VegaToPDF renders a Vega spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	if c.closed {
		return nil, errConverterClosed
	}
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPDF(svg, opts...)
}

// VegaLiteToPDF renders a Vega-Lite spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaLiteToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	if c.closed {
		return nil, errConverterClosed
	}
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPDF(svg, opts...)
}

// SVGToPDF converts an SVG string (as produced by the Vega SVG renderer) to
// a single-page vector PDF suitable for direct embedding in LaTeX documents
// via \includegraphics. Text handling is controlled by WithPDFText: by
// default the fonts a chart uses are subset and embedded, so the output is
// self-contained and text is selectable.
//
// Only the SVG subset that Vega emits is supported. Unsupported constructs
// (gradients, images, embedded CSS, ...) return a descriptive error rather
// than a silently incomplete chart; callers can fall back to SVGToPNG.
func (c *Converter) SVGToPDF(svg string, opts ...PDFOption) ([]byte, error) {
	if c.closed {
		// Without this guard a post-Close call would lazily build a fresh PDF
		// measurer (matching SVGToPNG's closed-converter contract).
		return nil, errConverterClosed
	}
	cfg := defaultPDFConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	var mode svgpdf.TextMode
	switch cfg.text {
	case PDFTextEmbed:
		mode = svgpdf.TextEmbed
	case PDFTextNamed:
		mode = svgpdf.TextNamed
	case PDFTextOutlines:
		mode = svgpdf.TextOutlines
	default:
		return nil, fmt.Errorf("aster: unknown PDF text mode %d", cfg.text)
	}

	m, err := c.pdfMeasurerInit()
	if err != nil {
		return nil, err
	}
	out, err := svgpdf.Convert(svg, m, svgpdf.Options{Text: mode})
	if err != nil {
		return nil, fmt.Errorf("aster: rendering PDF: %w", err)
	}
	return out, nil
}

// pdfMeasurerInit returns the text measurer used to shape text for PDF
// output. It reuses the converter's measurer when text measurement is
// enabled (the default) and otherwise builds one on first use from the same
// fontPlan, so PDF glyph outlines come from the font the SVG was laid out
// against.
func (c *Converter) pdfMeasurerInit() (*textmeasure.Measurer, error) {
	if c.measurer != nil {
		return c.measurer, nil
	}
	c.pdfMeasurerOnce.Do(func() {
		c.pdfMeasurer, c.pdfMeasurerErr = textmeasure.New(c.fonts.measurerOptions()...)
		if c.pdfMeasurerErr != nil {
			c.pdfMeasurerErr = fmt.Errorf("aster: initializing PDF text shaper: %w", c.pdfMeasurerErr)
		}
	})
	return c.pdfMeasurer, c.pdfMeasurerErr
}

// pngRendererInit lazily initializes the PNG renderer on first use. It draws
// its fonts and generic-family mapping from the same fontPlan that configured
// text measurement, so PNG glyphs are rasterized with the font the SVG layout
// was measured against.
func (c *Converter) pngRendererInit() (*resvg.Renderer, error) {
	c.pngOnce.Do(func() {
		fonts, families := c.fonts.resvgFonts()
		c.pngRenderer, c.pngErr = resvg.New(context.Background(), fonts, families)
		if c.pngErr != nil {
			c.pngErr = fmt.Errorf("aster: initializing PNG renderer: %w", c.pngErr)
		}
	})
	return c.pngRenderer, c.pngErr
}
