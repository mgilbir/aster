package purego

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
// loaded data, parsed rows and generated scene items.
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

// WithTextMeasurement controls whether font-based text measurement is enabled.
// When enabled, text widths are computed by shaping with the configured fonts
// for accurate layout. When disabled, Vega's default estimation is used.
func WithTextMeasurement(enabled bool) Option {
	return func(c *config) {
		c.textMeasure = enabled
	}
}

// WithVegaLiteVersion sets the Vega-Lite version to use, e.g. "6.4" (the
// default). An unknown version makes New return an error listing the
// available versions; see AvailableVersions to discover them.
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
// resolving the generic "sans-serif" CSS family, for both text measurement
// and PNG rasterization. Defaults to "Liberation Sans" (embedded).
func WithDefaultFontFamily(family string) Option {
	return func(c *config) {
		c.defaultFontFamily = family
	}
}

// WithDefaultSerifFamily sets the font family name used to resolve the generic
// "serif" CSS family. Defaults to "Liberation Serif" (embedded).
func WithDefaultSerifFamily(family string) Option {
	return func(c *config) {
		c.defaultSerifFamily = family
	}
}

// WithDefaultMonospaceFamily sets the font family name used to resolve the
// generic "monospace" CSS family. Defaults to "Liberation Mono" (embedded).
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

// PNGOption configures a single PNG render operation.
type PNGOption func(*pngConfig)

type pngConfig struct {
	scale          float64
	recode         bool
	quantizeColors int
}

func defaultPNGConfig() *pngConfig {
	return &pngConfig{scale: 1.0}
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
// unchanged.
func WithRecodePNG() PNGOption {
	return func(c *pngConfig) {
		c.recode = true
	}
}

// WithQuantizePNG lossily quantizes the rendered PNG to at most maxColors
// colors (clamped to 2..256) and encodes it 8-bit indexed, falling back to the
// lossless WithRecodePNG behaviour when the quality guard cannot be met. See
// the root package's WithQuantizePNG for details.
func WithQuantizePNG(maxColors int) PNGOption {
	return func(c *pngConfig) {
		c.quantizeColors = min(max(maxColors, 2), 256)
	}
}
