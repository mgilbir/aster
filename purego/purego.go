// Package purego converts Vega and Vega-Lite visualization specs to SVG, PNG
// and vector PDF with an engine written entirely in Go: no JavaScript
// runtime and no WebAssembly. Its API mirrors the root aster package call for
// call, so the two engines can be swapped and compared.
//
// Basic usage:
//
//	c, err := purego.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer c.Close()
//
//	svg, err := c.VegaLiteToSVG(specJSON)
//	png, err := c.VegaLiteToPNG(specJSON)
//	pdf, err := c.VegaLiteToPDF(specJSON)
package purego

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/mgilbir/aster/internal/fontsubset"
	"github.com/mgilbir/aster/internal/loader"
	"github.com/mgilbir/aster/internal/pngopt"
	"github.com/mgilbir/aster/internal/svgpdf"
	"github.com/mgilbir/aster/internal/textmeasure"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/raster"
	"github.com/mgilbir/aster/purego/internal/svg"
	"github.com/mgilbir/aster/purego/internal/text"
	"github.com/mgilbir/aster/purego/internal/transforms"
	"github.com/mgilbir/aster/purego/internal/vega"
	"github.com/mgilbir/aster/purego/internal/vegalite"
)

// Loader controls how external resources (data files, remote URLs) are
// fetched. It is the same type as aster.Loader, so loaders can be shared
// between the two engines.
type Loader = loader.Loader

// The loader implementations are shared with the root aster package.
type (
	// DenyLoader denies all resource loading. This is the default.
	DenyLoader = loader.DenyLoader
	// HTTPLoader allows loading resources over HTTP and HTTPS.
	HTTPLoader = loader.HTTPLoader
	// FileLoader serves files from a base directory on disk. It accepts
	// relative paths and rejects absolute URLs and path traversal.
	FileLoader = loader.FileLoader
	// StaticLoader returns a JSON-serialized payload for every Load call.
	StaticLoader = loader.StaticLoader
	// FallbackLoader routes requests to multiple child loaders in order.
	FallbackLoader = loader.FallbackLoader
)

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
var NewHTTPLoader = loader.NewHTTPLoader

// NewFileLoader creates a FileLoader confined to dir.
var NewFileLoader = loader.NewFileLoader

// NewFallbackLoader creates a FallbackLoader from the given children.
var NewFallbackLoader = loader.NewFallbackLoader

// Converter renders Vega/Vega-Lite specs to SVG, PNG and PDF. A Converter is
// not safe for concurrent use; create one per goroutine.
type Converter struct {
	cfg      *config
	location *time.Location
	theme    jsval.Value // parsed WithTheme config; Undefined when none
	closed   bool

	// Layout text measurement, built on first use.
	measurerOnce sync.Once
	measurer     *text.Measurer
	measurerErr  error

	// PNG text is shaped by one shaper per converter, built on first use.
	shaperOnce sync.Once
	shaper     raster.Shaper
	shaperErr  error

	// PDF output shapes text with the same measurer the root package uses,
	// built on first use.
	pdfOnce     sync.Once
	pdfMeasurer *textmeasure.Measurer
	pdfErr      error
}

// errConverterClosed is returned by every rendering method after Close.
var errConverterClosed = errors.New("purego: converter is closed")

// supportedVersions are the Vega-Lite versions this engine compiles.
var supportedVersions = []VersionInfo{
	{Key: "vl6_4", VegaVersion: "6.4.0", VegaLiteVersion: "6.4.3"},
}

// VersionInfo describes an available Vega-Lite version set.
type VersionInfo struct {
	Key             string // version set key, e.g. "vl6_4"
	VegaVersion     string // Vega version whose behaviour the engine follows
	VegaLiteVersion string // Vega-Lite version the compiler follows
}

// AvailableVersions reports the Vega-Lite versions this engine supports.
func AvailableVersions() ([]VersionInfo, error) {
	return append([]VersionInfo(nil), supportedVersions...), nil
}

// New creates a new Converter with the given options.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if v := cfg.vegaLiteVersion; v != "" {
		key := "vl" + strings.ReplaceAll(v, ".", "_")
		found := false
		var names []string
		for _, s := range supportedVersions {
			names = append(names, strings.ReplaceAll(strings.TrimPrefix(s.Key, "vl"), "_", "."))
			if s.Key == key {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("purego: unsupported Vega-Lite version %q (available: %s)", v, strings.Join(names, ", "))
		}
	}
	loc := time.UTC
	if tz := cfg.timezone; tz != "" && tz != "UTC" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("purego: unsupported timezone %q: %w", tz, err)
		}
		loc = l
	}
	if cfg.loader == nil {
		cfg.loader = DenyLoader{}
	}
	c := &Converter{cfg: cfg, location: loc}
	if cfg.theme != "" {
		theme, err := jsval.ParseJSONString(cfg.theme)
		if err != nil {
			return nil, fmt.Errorf("purego: invalid theme config: %w", err)
		}
		if !theme.IsObj() {
			return nil, errors.New("purego: invalid theme config: not a JSON object")
		}
		c.theme = theme
	}
	return c, nil
}

// Close releases all resources held by the Converter. It is safe to call
// multiple times; after Close every rendering method returns an error.
func (c *Converter) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	if closer, ok := c.cfg.loader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// VegaToSVG renders a Vega spec (JSON) to an SVG string.
func (c *Converter) VegaToSVG(spec []byte) (string, error) {
	if c.closed {
		return "", errConverterClosed
	}
	v, err := jsval.ParseJSON(spec)
	if err != nil {
		return "", fmt.Errorf("purego: parsing Vega spec: %w", err)
	}
	return c.renderSVG(v)
}

// VegaLiteToSVG renders a Vega-Lite spec (JSON) to an SVG string.
func (c *Converter) VegaLiteToSVG(spec []byte) (string, error) {
	if c.closed {
		return "", errConverterClosed
	}
	vg, err := c.compileVegaLite(spec)
	if err != nil {
		return "", err
	}
	return c.renderSVG(vg)
}

// renderSVG runs a Vega spec and serializes the resulting scenegraph. The
// whole operation is bounded by the converter's timeout.
func (c *Converter) renderSVG(spec jsval.Value) (out string, err error) {
	defer func() {
		// Every layer returns errors for bad input; a panic here is a bug,
		// but it must not take the host process down.
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("purego: internal error: %v", r)
		}
	}()
	m, err := c.measurerInit()
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	if c.cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.timeout)
		defer cancel()
	}
	opts := vega.Options{
		Loader:   c.cfg.loader,
		Location: c.location,
		Config:   c.theme,
		Limits:   c.limits(),
		// Seeded per render, like the root engine, so sample, jitter and
		// bootstrap confidence intervals are reproducible. 123456789 is the
		// seed Vega-Lite's own example renders use (vg2svg --seed).
		Random: transforms.LCG(randomSeed),
	}
	if m != nil {
		opts.TextMeasurer = m
	}
	res, err := vega.Render(ctx, spec, opts)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("purego: render timed out after %v: %w", c.cfg.timeout, err)
		}
		return "", fmt.Errorf("purego: rendering Vega: %w", err)
	}
	so := svg.Options{
		Width:  res.Width,
		Height: res.Height,
		Origin: res.Origin,
	}
	if res.HasBackground {
		so.Background = res.Background
	}
	if m != nil {
		so.Measurer = m
	}
	// Vega sanitizes every href through the view's loader; the Loader decides
	// which links a chart may carry, and a rejected URL renders no link.
	so.Href = func(uri string) ([]svg.HrefAttr, bool) {
		href, err := c.cfg.loader.Sanitize(ctx, uri)
		if err != nil {
			return nil, false
		}
		return []svg.HrefAttr{{Name: "xlink:href", Value: href}}, true
	}
	return svg.Render(ctx, res.Scenegraph, so)
}

// randomSeed seeds Vega's random() and every transform that samples.
const randomSeed = 123456789

// limits turns WithMemoryLimit into the engine's work bounds. The engine has
// no separate heap to cap, so the byte budget is converted into the number of
// rows and scene items a render may create, at a conservative per-object cost.
func (c *Converter) limits() vega.Limits {
	const bytesPerRow, bytesPerItem = 256, 512
	var l vega.Limits
	if n := c.cfg.memoryLimit; n > 0 {
		l.MaxRows = int(max(n/bytesPerRow, 1000))
		l.MaxItems = int(max(n/bytesPerItem, 1000))
	}
	return l
}

// measurerInit builds the text measurer used for layout on first use; it
// returns nil when text measurement is disabled, which selects Vega's own
// width estimate.
func (c *Converter) measurerInit() (*text.Measurer, error) {
	if !c.cfg.textMeasure {
		return nil, nil
	}
	c.measurerOnce.Do(func() {
		var opts []text.Option
		if c.cfg.systemFonts {
			opts = append(opts, text.WithSystemFonts())
		}
		for _, f := range c.cfg.fonts {
			opts = append(opts, text.WithFont(f.family, f.data))
		}
		if f := c.cfg.defaultFontFamily; f != "" {
			opts = append(opts, text.WithDefaultFontFamily(f))
		}
		if f := c.cfg.defaultSerifFamily; f != "" {
			opts = append(opts, text.WithDefaultSerifFamily(f))
		}
		if f := c.cfg.defaultMonospaceFamily; f != "" {
			opts = append(opts, text.WithDefaultMonospaceFamily(f))
		}
		c.measurer, c.measurerErr = text.New(opts...)
		if c.measurerErr != nil {
			c.measurerErr = fmt.Errorf("purego: initializing text measurer: %w", c.measurerErr)
		}
	})
	return c.measurer, c.measurerErr
}

// VegaLiteToVega compiles a Vega-Lite spec (JSON) to a full Vega spec (JSON).
func (c *Converter) VegaLiteToVega(spec []byte) ([]byte, error) {
	if c.closed {
		return nil, errConverterClosed
	}
	vg, err := c.compileVegaLite(spec)
	if err != nil {
		return nil, err
	}
	return jsval.AppendJSON(nil, vg), nil
}

// compileVegaLite parses a Vega-Lite spec and compiles it to Vega with the
// converter's theme and time zone.
func (c *Converter) compileVegaLite(spec []byte) (jsval.Value, error) {
	v, err := jsval.ParseJSON(spec)
	if err != nil {
		return jsval.Undefined, fmt.Errorf("purego: parsing Vega-Lite spec: %w", err)
	}
	vg, err := vegalite.Compile(v, vegalite.Options{Config: c.theme, Location: c.location})
	if err != nil {
		return jsval.Undefined, fmt.Errorf("purego: compiling Vega-Lite: %w", err)
	}
	return vg, nil
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

// SVGToPNG rasterizes an SVG string to a PNG image.
func (c *Converter) SVGToPNG(svg string, opts ...PNGOption) ([]byte, error) {
	if c.closed {
		return nil, errConverterClosed
	}
	cfg := defaultPNGConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if !(cfg.scale > 0) || math.IsInf(cfg.scale, 1) {
		return nil, fmt.Errorf("purego: invalid PNG scale %v (must be a positive, finite number)", cfg.scale)
	}
	out, err := c.rasterize(svg, cfg.scale)
	if err != nil {
		return nil, err
	}
	switch {
	case cfg.quantizeColors > 0:
		out = pngopt.QuantizeOrRecode(out, cfg.quantizeColors)
	case cfg.recode:
		out = pngopt.Recode(out)
	}
	return out, nil
}

func (c *Converter) rasterize(svg string, scale float64) ([]byte, error) {
	c.shaperOnce.Do(func() {
		fonts := make([]raster.FontData, len(c.cfg.fonts))
		for i, f := range c.cfg.fonts {
			fonts[i] = raster.FontData{Family: f.family, Data: f.data}
		}
		c.shaper, c.shaperErr = raster.NewShaper(fonts...)
		if c.shaperErr != nil {
			c.shaperErr = fmt.Errorf("purego: initializing PNG text shaper: %w", c.shaperErr)
		}
	})
	if c.shaperErr != nil {
		return nil, c.shaperErr
	}
	out, err := raster.RenderPNG([]byte(svg), raster.Options{Scale: scale, Shaper: c.shaper})
	if err != nil {
		return nil, fmt.Errorf("purego: rendering PNG: %w", err)
	}
	return out, nil
}

// PDFTextMode selects how text is represented in PDF output.
type PDFTextMode int

const (
	// PDFTextEmbed (the default) emits real PDF text with subset TrueType
	// fonts embedded. Text whose font cannot be embedded falls back to glyph
	// outlines automatically.
	PDFTextEmbed PDFTextMode = iota
	// PDFTextNamed emits PDF text referencing fonts by name only; the
	// consuming pipeline must embed the same font files. See the root
	// package's PDFTextNamed.
	PDFTextNamed
	// PDFTextOutlines converts every glyph to filled path outlines.
	PDFTextOutlines
)

// PDFOption configures a single PDF render operation.
type PDFOption func(*pdfConfig)

type pdfConfig struct {
	text PDFTextMode
}

// WithPDFText selects how text is represented in the PDF; see the
// PDFTextMode constants. The default is PDFTextEmbed.
func WithPDFText(mode PDFTextMode) PDFOption {
	return func(c *pdfConfig) {
		c.text = mode
	}
}

// FontUsage reports the source bytes and referenced glyph IDs of one face in
// a rendered PDF. It is the same type as aster.FontUsage.
type FontUsage = svgpdf.FontUsage

// VegaToPDF renders a Vega spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	out, _, err := c.VegaToPDFUsage(spec, opts...)
	return out, err
}

// VegaLiteToPDF renders a Vega-Lite spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaLiteToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	out, _, err := c.VegaLiteToPDFUsage(spec, opts...)
	return out, err
}

// SVGToPDF converts an SVG string (as produced by the Vega SVG renderer) to a
// single-page vector PDF. Only the SVG subset that Vega emits is supported.
func (c *Converter) SVGToPDF(svg string, opts ...PDFOption) ([]byte, error) {
	out, _, err := c.SVGToPDFUsage(svg, opts...)
	return out, err
}

// SVGToPDFUsage is SVGToPDF plus the per-face glyph usage of the produced PDF.
func (c *Converter) SVGToPDFUsage(svg string, opts ...PDFOption) ([]byte, []FontUsage, error) {
	if c.closed {
		return nil, nil, errConverterClosed
	}
	cfg := &pdfConfig{text: PDFTextEmbed}
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
		return nil, nil, fmt.Errorf("purego: unknown PDF text mode %d", cfg.text)
	}
	m, err := c.pdfMeasurerInit()
	if err != nil {
		return nil, nil, err
	}
	out, uses, err := svgpdf.ConvertWithUsage(svg, m, svgpdf.Options{Text: mode})
	if err != nil {
		return nil, nil, fmt.Errorf("purego: rendering PDF: %w", err)
	}
	return out, uses, nil
}

// VegaToPDFUsage renders a Vega spec to a vector PDF and reports its per-face
// glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		return nil, nil, err
	}
	return c.SVGToPDFUsage(svg, opts...)
}

// VegaLiteToPDFUsage renders a Vega-Lite spec to a vector PDF and reports its
// per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaLiteToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		return nil, nil, err
	}
	return c.SVGToPDFUsage(svg, opts...)
}

func (c *Converter) pdfMeasurerInit() (*textmeasure.Measurer, error) {
	c.pdfOnce.Do(func() {
		var opts []textmeasure.MeasurerOption
		if c.cfg.systemFonts {
			opts = append(opts, textmeasure.WithSystemFonts())
		}
		for _, f := range c.cfg.fonts {
			opts = append(opts, textmeasure.WithFont(f.family, f.data))
		}
		if f := c.cfg.defaultFontFamily; f != "" {
			opts = append(opts, textmeasure.WithDefaultFontFamily(f))
		}
		if f := c.cfg.defaultSerifFamily; f != "" {
			opts = append(opts, textmeasure.WithDefaultSerifFamily(f))
		}
		if f := c.cfg.defaultMonospaceFamily; f != "" {
			opts = append(opts, textmeasure.WithDefaultMonospaceFamily(f))
		}
		c.pdfMeasurer, c.pdfErr = textmeasure.New(opts...)
		if c.pdfErr != nil {
			c.pdfErr = fmt.Errorf("purego: initializing PDF text shaper: %w", c.pdfErr)
		}
	})
	return c.pdfMeasurer, c.pdfErr
}

// SubsetFont builds a TrueType subset of source containing gids, preserving
// the source's original glyph numbering; see aster.SubsetFont.
func SubsetFont(source []byte, gids []uint16) (subset []byte, postScriptName string, err error) {
	f, err := fontsubset.Parse(source)
	if err != nil {
		return nil, "", fmt.Errorf("purego: parse font: %w", err)
	}
	name := svgpdf.PostScriptNameOrFallback(f.PostScriptName())
	if !f.CanSubset() {
		return nil, name, fmt.Errorf("purego: font %q has no TrueType outlines to subset", name)
	}
	set := make(map[uint16]bool, len(gids))
	for _, g := range gids {
		set[g] = true
	}
	sub, err := f.Subset(set)
	if err != nil {
		return nil, name, fmt.Errorf("purego: subset font: %w", err)
	}
	return sub, name, nil
}
