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
	vegaLiteVersion        string // version set key, e.g. "vl6_4"
	systemFonts            bool
	fonts                  []fontEntry
	defaultFontFamily      string
	defaultMonospaceFamily string
	timezone               string
}

func defaultConfig() *config {
	return &config{
		loader:      DenyLoader{},
		timeout:     30 * time.Second,
		textMeasure: true,
		// vegaLiteVersion left empty; runtime reads default from versions.json
	}
}

// WithLoader sets the resource loader used by Vega for external data.
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

// WithMemoryLimit sets the maximum memory (in bytes) for the QuickJS runtime.
// Zero means no limit. WASM linear memory is 32-bit addressable, so values
// above 4 GiB are clamped to 4 GiB.
func WithMemoryLimit(bytes uint64) Option {
	return func(c *config) {
		c.memoryLimit = bytes
	}
}

// WithTimeout sets the maximum duration for a single render operation.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		c.timeout = d
	}
}

// WithTextMeasurement controls whether Go-side text measurement is enabled.
// When enabled, text widths are computed using go-text/typesetting for accurate
// layout. When disabled, Vega's default estimation is used.
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
		// Map "5.8" → "vl5_8", "6.4" → "vl6_4", etc.
		key := "vl" + strings.ReplaceAll(v, ".", "_")
		c.vegaLiteVersion = key
	}
}

// WithSystemFonts enables scanning of system-installed fonts for text
// measurement. System fonts supplement the always-present embedded Liberation Sans.
func WithSystemFonts() Option {
	return func(c *config) {
		c.systemFonts = true
	}
}

// WithFont registers a custom TTF font with the given family name for text
// measurement. Custom fonts take priority over system and embedded fonts.
// Multiple calls append additional fonts; later fonts take higher priority.
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

// WithDefaultMonospaceFamily sets the font family name used to resolve the
// generic "monospace" CSS family for PNG rasterization. Defaults to
// "Liberation Mono" (the embedded font). Register the matching TTF with
// WithFont.
func WithDefaultMonospaceFamily(family string) Option {
	return func(c *config) {
		c.defaultMonospaceFamily = family
	}
}

// WithTimezone sets the timezone for JavaScript Date operations.
// Defaults to "UTC" for deterministic output. Only "UTC" is currently
// supported (the QuickJS WASM runtime has no timezone database); New returns
// an error for any other value.
func WithTimezone(tz string) Option {
	return func(c *config) {
		c.timezone = tz
	}
}

// PNGOption configures a single PNG render operation.
type PNGOption func(*pngConfig)

type pngConfig struct {
	scale          float64
	recode         bool
	quantizeColors int
}

func defaultPNGConfig() *pngConfig {
	return &pngConfig{
		scale: 1.0,
	}
}

// WithScale sets the scale factor for PNG rendering. A scale of 2.0 produces
// an image with twice the dimensions. Default is 1.0.
func WithScale(scale float64) PNGOption {
	return func(c *pngConfig) {
		c.scale = scale
	}
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
	return func(c *pngConfig) {
		c.recode = true
	}
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
	return func(c *pngConfig) {
		// Clamp here, not just in the quantizer: the render path gates on
		// quantizeColors > 0, so an unclamped non-positive value would
		// silently disable quantization instead of honoring the documented
		// 2..256 contract.
		if maxColors < 2 {
			maxColors = 2
		}
		if maxColors > 256 {
			maxColors = 256
		}
		c.quantizeColors = maxColors
	}
}
