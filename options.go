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
	loader            Loader
	theme             string
	memoryLimit       uint64
	timeout           time.Duration
	textMeasure       bool
	vegaLiteVersion   string // version set key, e.g. "vl6_4"
	systemFonts       bool
	fonts             []fontEntry
	defaultFontFamily string
	timezone          string
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

// WithTheme sets a Vega theme configuration (JSON string) applied to all renders.
func WithTheme(theme string) Option {
	return func(c *config) {
		c.theme = theme
	}
}

// WithMemoryLimit sets the maximum memory (in bytes) for the QuickJS runtime.
// Zero means no limit.
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
// internal version set keys (e.g. "vl5_8", "vl6_4").
// The default is "6.4".
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
// resolving "sans-serif" and other generic CSS font families. Defaults to
// "Liberation Sans" (the embedded font). Use this with WithFont to switch
// the primary font used for text measurement.
func WithDefaultFontFamily(family string) Option {
	return func(c *config) {
		c.defaultFontFamily = family
	}
}

// WithTimezone sets the timezone for JavaScript Date operations.
// Defaults to "UTC" for deterministic output. Currently only "UTC" is
// supported; other values are passed through but have no effect unless
// the QuickJS WASM runtime supports them.
func WithTimezone(tz string) Option {
	return func(c *config) {
		c.timezone = tz
	}
}
