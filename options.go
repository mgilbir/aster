package aster

import "time"

// Option configures a Converter.
type Option func(*config)

type config struct {
	loader      Loader
	theme       string
	memoryLimit uint64
	timeout     time.Duration
	textMeasure bool
}

func defaultConfig() *config {
	return &config{
		loader:      DenyLoader{},
		timeout:     30 * time.Second,
		textMeasure: true,
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
