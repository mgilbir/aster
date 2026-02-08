package aster

import (
	"strings"
	"time"
)

// Option configures a Converter.
type Option func(*config)

type config struct {
	loader          Loader
	theme           string
	memoryLimit     uint64
	timeout         time.Duration
	textMeasure     bool
	vegaLiteVersion string // version set key, e.g. "vl6_4"
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
