package aster

import (
	"strings"
	"time"
)

// Option configures a Converter.
type Option func(*config)

type fontEntry struct {
	family string
	data   []byte
}

type config struct {
	loader                 Loader
	theme                  string
	memoryLimit            uint64
	timeout                time.Duration
	textMeasure            bool
	vegaLiteVersion        string // human-readable, e.g. "6.4"
	systemFonts            bool
	fonts                  []fontEntry
	defaultFontFamily      string
	defaultSerifFamily     string
	defaultMonospaceFamily string
	timezone               string
	harfBuzzText           bool
	pangoText              int              // 0: unrounded advances; 1 and 2: Pango's (see WithPangoTextForTest)
	now                    func() time.Time // the clock for now(); nil is the system clock (tests pin it)
}

func defaultConfig() *config {
	return &config{
		loader:      DenyLoader{},
		timeout:     30 * time.Second,
		textMeasure: true,
	}
}

// WithLoader sets the resource loader used for external data.
// By default, all loading is denied (DenyLoader).
func WithLoader(l Loader) Option {
	return func(c *config) {
		c.loader = l
	}
}

// WithTheme sets a Vega theme configuration (JSON string) applied to all
// renders. The config is passed to the Vega-Lite compiler as well as the Vega
// runtime, so compile-time keys (background, view.continuousWidth/Height, …)
// take effect; VegaLiteToVega output therefore also reflects the theme.
func WithTheme(theme string) Option {
	return func(c *config) {
		c.theme = theme
	}
}

// WithMemoryLimit bounds the memory a single render may use, in bytes. Zero
// means no limit. The pure-Go engine has no separate heap to cap, so the limit
// is enforced as a budget on what a specification can make the engine hold:
// loaded data, parsed rows, generated scene items and the glyphs of each long
// run of text shaped (a quarter of the limit, at least 1 MiB). The specification is
// bounded too, to a sixteenth of the limit (at least 1 MiB): it costs about
// nineteen times its size once parsed and compiled, and an inline geometry is
// all specification and no rows. A larger one is refused with an error wrapping
// ErrLimit, whichever entry point it came in by.
func WithMemoryLimit(bytes uint64) Option {
	return func(c *config) {
		c.memoryLimit = bytes
	}
}

// WithTimeout sets the maximum duration for a single render operation: every
// stage of it together, text shaping included.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		c.timeout = d
	}
}

// WithTextMeasurement controls whether font-based text measurement is enabled.
// When enabled, text widths are computed by shaping with the configured fonts
// for accurate layout. When disabled, Vega's default estimation is used.
func WithTextMeasurement(enabled bool) Option {
	return func(c *config) {
		c.textMeasure = enabled
	}
}

// WithVegaLiteVersion sets the Vega-Lite version to use.
// Accepts human-readable versions like "5.8", "6.4" which are mapped to
// internal version set keys (e.g. "vl5_8", "vl6_4"). The default is "6.4".
// An unknown version makes New return an error listing the available
// versions; see AvailableVersions to discover them programmatically.
func WithVegaLiteVersion(v string) Option {
	return func(c *config) {
		c.vegaLiteVersion = strings.TrimPrefix(v, "v")
	}
}

// WithSystemFonts enables scanning of system-installed fonts for text
// measurement. System fonts supplement the always-present embedded fonts.
func WithSystemFonts() Option {
	return func(c *config) {
		c.systemFonts = true
	}
}

// WithFont registers a custom TTF font with the given family name. Custom
// fonts take priority over system and embedded fonts. Multiple calls append
// additional fonts; later fonts take higher priority.
func WithFont(family string, ttf []byte) Option {
	return func(c *config) {
		c.fonts = append(c.fonts, fontEntry{family: family, data: ttf})
	}
}

// WithDefaultFontFamily sets the font family name used as the fallback when
// resolving the generic "sans-serif" CSS family. It applies to both text
// measurement (SVG layout) and PNG rasterization. Defaults to "Liberation
// Sans" (the embedded font). Use this with WithFont to switch the primary
// font used across both pipelines.
func WithDefaultFontFamily(family string) Option {
	return func(c *config) {
		c.defaultFontFamily = family
	}
}

// WithDefaultSerifFamily sets the font family name used to resolve the generic
// "serif" CSS family. It applies to both text measurement (SVG layout) and PNG
// rasterization. Defaults to "Liberation Serif" (the embedded font, metrically
// compatible with Times New Roman). Register the matching TTF with WithFont.
func WithDefaultSerifFamily(family string) Option {
	return func(c *config) {
		c.defaultSerifFamily = family
	}
}

// WithDefaultMonospaceFamily sets the font family name used to resolve the
// generic "monospace" CSS family. It applies to both text measurement (SVG
// layout) and PNG rasterization. Defaults to "Liberation Mono" (the embedded
// font). Register the matching TTF with WithFont.
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
// accepted; New returns an error for an unknown one.
func WithTimezone(tz string) Option {
	return func(c *config) {
		c.timezone = tz
	}
}

// PNGOption configures a single PNG render operation: WithScale,
// WithRecodePNG, WithQuantizePNG, and WithSignal for the Vega and Vega-Lite
// methods.
type PNGOption interface{ applyPNG(*pngConfig) }

type pngOption func(*pngConfig)

func (f pngOption) applyPNG(c *pngConfig) { f(c) }

// RenderOption configures a single VegaToSVG or VegaLiteToSVG call. WithSignal
// is the one there is.
type RenderOption interface{ applyRender(*renderConfig) }

// renderConfig is what configures the Vega render of a call, whatever its
// output.
type renderConfig struct {
	signals []SignalOption
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
// re-compress the pixel stream and therefore pay per decoded byte. Costs one
// extra decode/encode round trip (tens of milliseconds for chart-sized
// images).
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
// the decoded volume 4x versus RGBA. Costs one decode/encode round trip plus
// the quantization pass: roughly tens of milliseconds for chart-sized images,
// up to ~150ms at double-scale renders. When both quantize and recode are
// requested, quantization applies. Falls back to the lossless WithRecodePNG
// behaviour — logging at debug level via log/slog — whenever quantization
// cannot maintain the output within the quality guard or cannot keep the
// encoded size in check, so enabling it is always safe.
func WithQuantizePNG(maxColors int) PNGOption {
	return pngOption(func(c *pngConfig) {
		c.quantizeColors = min(max(maxColors, 2), 256)
	})
}
