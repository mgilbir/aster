package aster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/mgilbir/aster/internal/geo"
)

// Option configures a Converter.
type Option func(*config)

type fontEntry struct {
	family string
	data   []byte
	axes   map[string]float64 // WithFontInstance's point in the design space
	face   int                // WithFontFace's face of a collection; -1 for every face
}

// fontPalette is a family's colour palette, WithFontPalette's.
type fontPalette struct {
	family string
	index  int
}

type config struct {
	loader                 Loader
	theme                  string
	memoryLimit            uint64
	pixelWork              uint64 // WithPixelWorkLimit's; 0: the default
	timeout                time.Duration
	textMeasure            bool
	vegaLiteVersion        string // human-readable, e.g. "6.4"
	systemFonts            bool
	fonts                  []fontEntry
	palettes               []fontPalette // WithFontPalette, in order
	fallbacks              []string      // WithFontFallback's families
	fallbackSet            bool          // WithFontFallback was given
	defaultFontFamily      string
	defaultSerifFamily     string
	defaultMonospaceFamily string
	timezone               string
	harfBuzzText           bool
	pangoText              int              // 0: unrounded advances; 1 and 2: Pango's (see WithPangoTextForTest)
	now                    func() time.Time // the clock for now(); nil is the system clock (tests pin it)
	fontFailForTest        bool             // the tests' WithFontSetFailureForTest
	projections            geo.Registry     // WithProjection's types, by lower-case name
	// errs are the options' own mistakes, which New reports.
	errs []error
}

// fontsGiven reports whether any option changes the fonts from the embedded
// ones, so that New loads them (and fails for one that does not load).
func (c *config) fontsGiven() bool {
	return len(c.fonts) > 0 || c.fallbackSet || c.systemFonts || len(c.palettes) > 0 ||
		c.defaultFontFamily != "" || c.defaultSerifFamily != "" || c.defaultMonospaceFamily != ""
}

func defaultConfig() *config {
	return &config{
		loader:      DenyLoader{},
		timeout:     30 * time.Second,
		textMeasure: true,
	}
}

// WithLoader sets the Loader that decides which data, images and links a
// specification may use, and fetches them. The default, and what a nil l
// selects, is DenyLoader, which refuses all of them. A nil pointer in l, such
// as a (*FileLoader)(nil) from a NewFileLoader whose error was dropped, makes
// New return an error.
func WithLoader(l Loader) Option {
	return func(c *config) {
		c.loader = l
	}
}

// WithTheme sets a Vega theme configuration (JSON string) applied to all
// renders. The config is passed to the Vega-Lite compiler as well as the Vega
// runtime, so compile-time keys (background, view.continuousWidth/Height, …)
// take effect; VegaLiteToVega output therefore also reflects the theme. A
// theme that is not a JSON object makes New return an error.
func WithTheme(theme string) Option {
	return func(c *config) {
		c.theme = theme
	}
}

// WithMemoryLimit sets the memory budgets of a single render from one
// number of bytes. Go has no per-call heap to cap, so the limit is converted
// into budgets on what a specification can make the engine hold, each charged
// before the allocation it pays for: data rows (512 bytes each), scene items
// (1,536), loaded bytes (half the limit), the SVG and expression strings (a
// quarter), the glyphs of a shaped run (a quarter), the rasterizer's canvas,
// and the specification itself (a sixteenth: it costs about nineteen times
// its size once parsed and compiled, and an inline geometry is all
// specification and no rows). docs/limits.md lists every budget, its default
// and its formula.
//
// The budgets replace the defaults: a limit above the defaults' scale raises
// them (1 GiB allows 2M rows and 512 MiB of loaded data, against 1M and
// 64 MiB by default). Zero, the default, keeps the defaults. A render past a
// budget fails with an error wrapping ErrLimit.
func WithMemoryLimit(bytes uint64) Option {
	return func(c *config) {
		c.memoryLimit = bytes
	}
}

// WithPixelWorkLimit sets the rasterizer's work budget for PNG output: the
// pixel operations one render may make at scale 1 (pixels filled and
// stroked, edge crossings, points flattened, layers composited, masks,
// pattern tiles, and every pass a filter makes over its region). The default
// is 128 Mi; the heaviest chart of the corpora spends less than a fifth of
// it. The budget follows the render's scale as the default does: ops times
// the scale squared above scale 1 (a render at scale 2 has four times the
// pixels to draw, and four times the budget), and at least 16 operations per
// output pixel, so that one value fits every scale. SVG input with filters
// over large regions (a blur costs about 49 operations a pixel) may need
// more; a service rendering untrusted SVG may want less. The label
// transform's bitmaps and a PDF's images of colour glyphs, drawn at their
// own pixels, have it at scale 1. A render past it fails with an error
// wrapping ErrLimit. Zero is refused by New: the budget cannot be turned
// off, only raised (a value past what an int holds saturates).
func WithPixelWorkLimit(ops uint64) Option {
	return func(c *config) {
		if ops == 0 {
			c.errs = append(c.errs, errors.New("aster: WithPixelWorkLimit(0): the pixel work budget must be positive (it cannot be turned off; pass a large value to raise it)"))
			return
		}
		c.pixelWork = ops
	}
}

// WithTimeout sets the maximum duration of one call: every stage of it
// together, text shaping included. The default is 30 s; zero or negative
// means no timeout. A call past it fails with an error wrapping
// context.DeadlineExceeded that names the stage.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		c.timeout = d
	}
}

// WithTextMeasurement controls whether font-based text measurement is enabled
// (the default). When enabled, text widths are computed by shaping with the
// configured fonts for accurate layout. When disabled, Vega's default
// estimation is used, and the wordcloud transform, which needs to measure
// words, fails as it does upstream without a canvas.
func WithTextMeasurement(enabled bool) Option {
	return func(c *config) {
		c.textMeasure = enabled
	}
}

// WithVegaLiteVersion sets the Vega-Lite compiler: "6.4" or "6.4.3"
// (Vega-Lite 6.4.3, the default), "5.8" or "5.8.0"; a leading "v" is
// accepted. A specification's
// $schema does not choose it. An unknown version makes New return an error
// listing the available ones; AvailableVersions lists them too.
func WithVegaLiteVersion(v string) Option {
	return func(c *config) {
		c.vegaLiteVersion = strings.TrimPrefix(v, "v")
	}
}

// WithSystemFonts also makes the fonts installed on the machine available,
// for layout, PNG and PDF alike. A system font is used only when a
// specification names its family, or WithFontFallback lists it; never as a
// fallback by default: some system fonts' licences forbid drawing them into
// distributed documents, and output should not depend on the machine.
func WithSystemFonts() Option {
	return func(c *config) {
		c.systemFonts = true
	}
}

// WithFont registers a TrueType, OpenType or WOFF font under the given
// family name, for layout, PNG and PDF alike; its weight and style are read
// from the font. A font collection (.ttc, .otc) registers every face of it,
// each with its own weight and style; WithFontFace registers one.
//
// A family you register faces for is made of your faces alone, as a CSS
// @font-face family is: an embedded or system font of the same name
// ("Liberation Sans", "Arial") is no longer that family, so registering one
// replaces it. Among the family's faces, CSS font-weight and font-style choose
// the best fit, and of faces that fit equally the one registered last wins.
// Characters the family lacks are drawn from the fallback chain: by default
// your registered fonts, the most recently registered first, then the
// embedded ones; WithFontFallback sets it.
//
// A font that does not load makes New return an error. The bytes are
// copied: the caller may reuse ttf once WithFont returns.
//
// A colour font (COLR, sbix, CBDT) draws its glyphs in colour in PNG and PDF
// output. Registered with any family name, it is also what draws the
// characters a spec's fonts lack, such as emoji, before the bundled
// monochrome Noto Emoji.
func WithFont(family string, ttf []byte) Option {
	ttf = bytes.Clone(ttf)
	return func(c *config) {
		c.fonts = append(c.fonts, fontEntry{family: family, data: ttf, face: -1})
	}
}

// WithFontFallback sets the families that draw, in order, the characters a
// specification's fonts lack (emoji, other scripts), replacing the default
// chain: every font registered with WithFont and its kin, the most recently
// registered first, then the embedded ones, Noto Emoji last. Each family's
// best fit for the weight and style is tried first. With no families there
// is no fallback, and such characters draw as the font's .notdef glyph.
//
// A family may be one you registered, an embedded one ("Liberation Sans",
// "Noto Emoji"), or a system one with WithSystemFonts: naming a system family
// here is how it becomes a fallback, which it never is by default (check its
// licence before distributing what it draws). A family with no font makes New
// return an error. The family WithDefaultFontFamily names is always tried before
// the chain, as the generic sans-serif.
func WithFontFallback(families ...string) Option {
	return func(c *config) {
		c.fallbacks, c.fallbackSet = append([]string{}, families...), true
	}
}

// WithFontFace registers one face of a font collection (.ttc, .otc), by
// its index from 0, under family, as WithFont registers a font. WithFont
// registers every face of a collection, which CSS font-weight and
// font-style choose among; this is for a collection whose faces are not
// one family's weights and styles. A negative index makes New return an
// error; the bytes are copied, as WithFont's are.
func WithFontFace(family string, ttc []byte, index int) Option {
	ttc = bytes.Clone(ttc)
	return func(c *config) {
		if index < 0 {
			c.errs = append(c.errs, fmt.Errorf("aster: WithFontFace(%q): face index %d is negative", family, index))
			return
		}
		c.fonts = append(c.fonts, fontEntry{family: family, data: ttc, face: index})
	}
}

// WithFontPalette draws the colour glyphs (COLR, coloured by CPAL) of
// family's fonts with their palette index, from 0, rather than the first:
// a font's dark-mode palette, say. It applies to every font of the family,
// given with WithFont, found with WithSystemFonts, or embedded; a palette a
// font does not have is its first. A later call for a family wins.
func WithFontPalette(family string, index int) Option {
	return func(c *config) {
		c.palettes = append(c.palettes, fontPalette{family, index})
	}
}

// WithFontInstance registers a variable font with the given family name at
// one point of its design space, as WithFont registers a font: axes are by
// tag, {"wght": 650, "wdth": 75} say. The instance's outlines, metrics and
// colour glyphs (COLR's variable paints) are drawn and measured; an axis not
// named stays at its default, one outside its range is clamped to it, and
// one the font does not have makes New return an error, as a font that does
// not load does. A variable font given with WithFont is drawn at its default
// instance. Register the same font
// more than once, under one family at different weights, to have CSS
// font-weight choose among instances. Of a collection, its first face is
// the one registered. The bytes are copied, as WithFont's are.
func WithFontInstance(family string, ttf []byte, axes map[string]float64) Option {
	ttf, axes = bytes.Clone(ttf), maps.Clone(axes)
	return func(c *config) {
		c.fonts = append(c.fonts, fontEntry{family: family, data: ttf, axes: axes})
	}
}

// WithDefaultFontFamily sets the family the generic "sans-serif" CSS family
// resolves to, which is Vega's default font, for layout, PNG and PDF alike.
// The default is "Liberation Sans" (embedded, metrically compatible with
// Arial). Use it with WithFont to change the font most text is set in. A
// family with no font (registered, embedded, or with WithSystemFonts
// installed) makes New return an error.
func WithDefaultFontFamily(family string) Option {
	return func(c *config) {
		c.defaultFontFamily = family
	}
}

// WithDefaultSerifFamily sets the family the generic "serif" CSS family
// resolves to, for layout, PNG and PDF alike. The default is "Liberation
// Serif" (embedded, metrically compatible with Times New Roman). Register the
// font with WithFont; a family with no font makes New return an error.
func WithDefaultSerifFamily(family string) Option {
	return func(c *config) {
		c.defaultSerifFamily = family
	}
}

// WithDefaultMonospaceFamily sets the family the generic "monospace" CSS
// family resolves to, for layout, PNG and PDF alike. The default is
// "Liberation Mono" (embedded). Register the font with WithFont; a family
// with no font makes New return an error.
func WithDefaultMonospaceFamily(family string) Option {
	return func(c *config) {
		c.defaultMonospaceFamily = family
	}
}

// WithHarfBuzzTextMetrics measures text the way HarfBuzz reports advances:
// the font size rounded up to whole pixels and each glyph advance rounded to
// 1/64 px. By default advances are unrounded at the exact font size, as
// browsers and node-canvas measure, which is what upstream Vega lays charts
// out with; this option restores the rounded model earlier versions of this
// package used, for output that is byte-stable with them.
func WithHarfBuzzTextMetrics() Option {
	return func(c *config) {
		c.harfBuzzText = true
	}
}

// WithTimezone sets the timezone used for local-time operations (time scales,
// timeFormat, date parsing without a zone). Defaults to "UTC" for
// deterministic output. Any IANA zone name known to the Go runtime is
// accepted; New returns an error for an unknown one, and for "Local": the
// host's zone would make the output depend on the machine that renders it,
// so name the zone instead.
func WithTimezone(tz string) Option {
	return func(c *config) {
		c.timezone = tz
	}
}

// PNGOption configures a single PNG render operation: WithScale,
// WithRecodePNG, WithQuantizePNG, WithContext, WithErrorHandler, and
// WithSignal for the Vega and Vega-Lite methods.
type PNGOption interface{ applyPNG(*pngConfig) }

type pngOption func(*pngConfig)

func (f pngOption) applyPNG(c *pngConfig) { f(c) }

// RenderOption configures a single VegaToSVG, VegaLiteToSVG or
// VegaLiteToVega call: WithContext, WithSignal and WithErrorHandler
// (VegaLiteToVega renders nothing, so it ignores WithSignal).
type RenderOption interface{ applyRender(*renderConfig) }

// renderConfig is what configures one call, whatever its output.
type renderConfig struct {
	ctx     context.Context // WithContext's; nil for none
	signals []SignalOption
	handler func(error) error // WithErrorHandler's
}

// SignalOption sets a top-level signal of the specification before the chart
// is rendered; see WithSignal. It is a RenderOption, a PNGOption and a
// PDFOption.
type SignalOption struct {
	name  string
	value any
}

// WithSignal sets the top-level signal name to value, as Vega's
// view.signal(name, value) does, and the chart is rendered as it then stands:
// a Vega-Lite variable parameter (one a bound input sets) is a signal of the
// same name. A selection's state is held in its store dataset as well, which
// a signal write does not change. value is anything encoding/json encodes, which the signal then
// holds as the JSON value (a json.RawMessage is used as it is); a time.Time
// becomes its RFC 3339 string, so a date is better set as milliseconds since
// the epoch. Several signals are set in the order given, each propagated
// before the next is set.
//
// A name the specification does not define fails the render, as it does
// upstream. The SVG-input methods (SVGToPNG, SVGToPDF) render no
// specification and ignore it.
func WithSignal(name string, value any) SignalOption {
	return SignalOption{name: name, value: value}
}

func (o SignalOption) applyRender(c *renderConfig) { c.signals = append(c.signals, o) }
func (o SignalOption) applyPNG(c *pngConfig)       { o.applyRender(&c.render) }
func (o SignalOption) applyPDF(c *pdfConfig)       { o.applyRender(&c.render) }

// ErrorHandlerOption sets the error handler of one call; see
// WithErrorHandler. It is a RenderOption, a PNGOption and a PDFOption.
type ErrorHandlerOption struct {
	handler func(error) error
}

// WithErrorHandler makes one call tell h of every error upstream Vega would
// log and carry on past, and of every warning, and lets h decide whether the
// call goes on, as go/types' Config.Error does for a type checker.
//
// Without a handler, the first such error fails the call (wrapped in the
// error it returns) and warnings are dropped. With one, h is called with each
// in turn: return nil and the call goes on as upstream does (the data set
// stays empty, the image undrawn, the operator's value unchanged); return an
// error and the call fails with it. h is told of:
//
//   - a *LoadError: data or an image that could not be loaded or read;
//   - an error an expression or an operator raised while the chart was
//     evaluated (a TypeError, a RangeError, an invalid parameter);
//   - a Warning, which never fails the call unless h returns an error for it.
//
// Warnings include the Vega-Lite compiler's, with upstream's text; they also
// reach h from VegaLiteToVega.
//
// Errors that do not depend on h always fail the call: an invalid
// specification, an exceeded limit (ErrLimit), a timeout
// (context.DeadlineExceeded), Close (context.Canceled).
//
// h is called on the goroutine that made the call, never concurrently, and
// before the call returns. Data and evaluation errors reach it as they arise;
// image failures once the stage that fetched or decoded them is over, once
// per image URL of the call, under the URL the input names. h may call the
// Converter's Close (the call then fails with context.Canceled) or start
// another call on it.
//
// To render what can be rendered, as a browser does, and log the rest:
//
//	svg, err := c.VegaLiteToSVG(spec, aster.WithErrorHandler(func(err error) error {
//		log.Print(err)
//		return nil
//	}))
func WithErrorHandler(h func(err error) error) ErrorHandlerOption {
	return ErrorHandlerOption{handler: h}
}

func (o ErrorHandlerOption) applyRender(c *renderConfig) { c.handler = o.handler }
func (o ErrorHandlerOption) applyPNG(c *pngConfig)       { o.applyRender(&c.render) }
func (o ErrorHandlerOption) applyPDF(c *pdfConfig)       { o.applyRender(&c.render) }

type pngConfig struct {
	scale          float64
	recode         bool
	quantizeColors int
	render         renderConfig
}

func defaultPNGConfig() *pngConfig {
	return &pngConfig{scale: 1.0}
}

// WithScale sets the scale factor for PNG rendering. A scale of 2.0 produces
// an image with twice the dimensions. Default is 1.0.
func WithScale(scale float64) PNGOption {
	return pngOption(func(c *pngConfig) {
		c.scale = scale
	})
}

// WithRecodePNG losslessly re-encodes the rendered PNG into its cheapest
// equivalent color format: 8-bit indexed when the image has at most 256
// distinct colors, 24-bit truecolor when it is fully opaque. Pixels are
// unchanged; typical charts shrink several-fold. Worth enabling when the PNG
// is embedded into documents (PDF, office formats), whose writers decode and
// re-compress the pixel stream and therefore pay per decoded byte. Costs a
// scan of the rendered image and a second encoding, proportional to its
// pixels, within the call's WithTimeout and WithMemoryLimit.
func WithRecodePNG() PNGOption {
	return pngOption(func(c *pngConfig) {
		c.recode = true
	})
}

// WithQuantizePNG lossily quantizes the rendered PNG to at most maxColors
// colors (clamped to 2..256) and encodes it 8-bit indexed: a weighted
// median-cut palette with Floyd-Steinberg dithering. Resolution and layout
// are untouched; popular colors — a chart's flat areas — normally earn their
// own palette slots and map exactly, while antialiased edge pixels shift
// slightly (a quality guard bounds the deviation). Compared to WithRecodePNG
// this also covers images with more than 256 distinct colors — the common
// case for antialiased chart renders — shrinking them several-fold and, in
// consumers that decode the pixel stream (PDF and office embedders), cutting
// the decoded volume 4x versus RGBA. Costs the quantization pass over the
// rendered image and a second encoding, proportional to its pixels and its
// distinct colors; it runs within the call's WithTimeout and WithMemoryLimit
// like the rest of the call, and a call that runs out of either fails with
// their error. When both quantize and recode are requested, quantization
// applies. Falls back to the lossless WithRecodePNG behaviour — logging at
// debug level via log/slog — whenever quantization cannot maintain the
// output within the quality guard or cannot keep the encoded size in check,
// and, without trying, for an image of more than 65,536 distinct colors
// (photographic or dense-gradient content, which a palette cannot keep
// within the guard): the output is then lossless.
func WithQuantizePNG(maxColors int) PNGOption {
	return pngOption(func(c *pngConfig) {
		c.quantizeColors = min(max(maxColors, 2), 256)
	})
}
