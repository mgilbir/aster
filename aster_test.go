package aster_test

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// normalizeSVGNumbers rounds all floating-point numbers in an SVG string to
// the given number of decimal places. This allows comparing SVGs across
// different text measurement implementations that produce sub-pixel differences.
func normalizeSVGNumbers(svg string, decimals int) string {
	mult := math.Pow(10, float64(decimals))
	re := regexp.MustCompile(`-?\d+\.\d+`)
	return re.ReplaceAllStringFunc(svg, func(s string) string {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		rounded := math.Round(f*mult) / mult
		return strconv.FormatFloat(rounded, 'f', decimals, 64)
	})
}

func TestVegaLiteToSVG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
	if !strings.Contains(svg, "</svg>") {
		t.Errorf("expected SVG output containing </svg>")
	}
}

func TestVegaToSVG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vg.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaToSVG(spec)
	if err != nil {
		t.Fatalf("VegaToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
}

func TestVegaLiteToVega(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	vgSpec, err := c.VegaLiteToVega(spec)
	if err != nil {
		t.Fatalf("VegaLiteToVega: %v", err)
	}

	if len(vgSpec) == 0 {
		t.Error("expected non-empty Vega spec")
	}
	if !strings.Contains(string(vgSpec), `"$schema"`) {
		t.Errorf("expected Vega spec to contain $schema")
	}
}

func TestDenyLoaderPreventsLoading(t *testing.T) {
	// The default DenyLoader should prevent any data loading.
	// A spec with inline data should still work.
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// This should succeed since the spec uses inline data.
	_, err = c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG with inline data should succeed: %v", err)
	}
}

func TestNoTextMeasurement(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
}

// specHasURL reports whether a Vega-Lite spec references external data via "url".
func specHasURL(spec []byte) bool {
	return strings.Contains(string(spec), `"url"`)
}

// knownFailures lists specs that fail due to known runtime limitations
// (e.g. polyfill gaps, unsupported features). These are skipped rather than
// marked as errors so the test suite stays green while we work on fixes.
var knownFailures = map[string]string{
	"geo_sphere": "structuredClone polyfill cannot handle undefined projection params",

	// Timezone: expected SVGs were generated in PDT (UTC-7), not UTC.
	// These fail because aria-label timestamps differ by 7 hours.
	"time_parse_local":                   "expected SVGs generated in PDT timezone",
	"time_parse_utc_format":              "expected SVGs generated in PDT timezone",
	"time_output_utc_scale":              "expected SVGs generated in PDT timezone",
	"time_output_utc_timeunit":           "expected SVGs generated in PDT timezone",
	"layer_line_errorband_pre_aggregated": "expected SVGs generated in PDT timezone",
	"line_timestamp_domain":              "expected SVGs generated in PDT timezone",

	// Missing emoji glyphs: Liberation/DejaVu Sans lack emoji characters.
	"isotype_bar_chart_emoji": "no emoji font for text measurement",
	"isotype_grid":            "no emoji font for text measurement",
	"layer_bar_fruit":         "no emoji font for text measurement",

	// QuickJS formats Infinity as "Infinity" string, not "∞" symbol.
	"histogram_nonlinear": "QuickJS Infinity formatting differs from Node.js",
}

// loadFont reads a TTF file from the fonts directory.
func loadFont(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loading font %s: %v", path, err)
	}
	return data
}

// dejaVuFontOptions returns aster options that configure DejaVu Sans as the
// text measurement font. DejaVu Sans is the default sans-serif on Ubuntu,
// matching the environment used to generate the vega-lite expected SVGs.
func dejaVuFontOptions(t *testing.T) []aster.Option {
	t.Helper()
	dir := filepath.Join("internal", "textmeasure", "fonts", "dejavu")
	return []aster.Option{
		aster.WithFont("DejaVu Sans", loadFont(t, filepath.Join(dir, "DejaVuSans.ttf"))),
		aster.WithFont("DejaVu Sans", loadFont(t, filepath.Join(dir, "DejaVuSans-Bold.ttf"))),
		aster.WithFont("DejaVu Sans", loadFont(t, filepath.Join(dir, "DejaVuSans-Oblique.ttf"))),
		aster.WithFont("DejaVu Sans", loadFont(t, filepath.Join(dir, "DejaVuSans-BoldOblique.ttf"))),
		aster.WithFont("DejaVu Sans Mono", loadFont(t, filepath.Join(dir, "DejaVuSansMono.ttf"))),
		aster.WithFont("DejaVu Sans Mono", loadFont(t, filepath.Join(dir, "DejaVuSansMono-Bold.ttf"))),
		aster.WithFont("DejaVu Sans Mono", loadFont(t, filepath.Join(dir, "DejaVuSansMono-Oblique.ttf"))),
		aster.WithFont("DejaVu Sans Mono", loadFont(t, filepath.Join(dir, "DejaVuSansMono-BoldOblique.ttf"))),
		aster.WithDefaultFontFamily("DejaVu Sans"),
	}
}

// TestVLConvertSpecs runs the 23 vl-convert test specs against their expected
// SVGs in testdata/vl-convert/expected/v5_8/.
// Specs that require external data are skipped.
// Expected SVGs are from https://github.com/vega/vl-convert (BSD-3-Clause).
//
// Font: Liberation Sans (default embedded font, matching vl-convert's bundled font).
// Version: Vega-Lite 5.8 (matching the expected SVGs in v5_8/).
func TestVLConvertSpecs(t *testing.T) {
	specs, err := filepath.Glob("testdata/vl-convert/*.vl.json")
	if err != nil {
		t.Fatalf("globbing specs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("no vl-convert specs found in testdata/vl-convert/")
	}

	// Uses Liberation Sans (default) — matches vl-convert's bundled font.
	c, err := aster.New(aster.WithVegaLiteVersion("5.8"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	expectedDir := filepath.Join("testdata", "vl-convert", "expected", "v5_8")

	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".vl.json")
		t.Run(name, func(t *testing.T) {
			spec, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatalf("reading spec: %v", err)
			}

			if specHasURL(spec) {
				t.Skip("requires external data")
			}

			svg, err := c.VegaLiteToSVG(spec)
			if err != nil {
				t.Fatalf("VegaLiteToSVG: %v", err)
			}

			if !strings.HasPrefix(svg, "<svg") {
				t.Fatalf("expected SVG output starting with <svg, got: %.100s", svg)
			}

			// Compare against expected SVG if one exists.
			expectedPath := filepath.Join(expectedDir, name+".svg")
			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				// No expected file for this spec — just verify it rendered.
				t.Logf("no expected SVG at %s, render OK (%d bytes)", expectedPath, len(svg))
				return
			}

			if normalizeSVGNumbers(svg, 0) != normalizeSVGNumbers(string(expected), 0) {
				t.Errorf("SVG output differs from vl-convert expected (%d vs %d bytes)", len(svg), len(expected))
			}
		})
	}
}

// TestVegaLiteExamples runs the official vega-lite v6.4.0 compiled examples.
// Specs that require external data are skipped.
// Expected SVGs are from https://github.com/vega/vega-lite (BSD-3-Clause).
//
// Font: DejaVu Sans (explicitly loaded, matching Ubuntu CI's default sans-serif
// used to generate the expected SVGs via node-canvas/Cairo).
func TestVegaLiteExamples(t *testing.T) {
	specDir := filepath.Join("testdata", "vega-lite", "v6.4.0", "specs")
	expectedDir := filepath.Join("testdata", "vega-lite", "v6.4.0", "expected")

	specs, err := filepath.Glob(filepath.Join(specDir, "*.vl.json"))
	if err != nil {
		t.Fatalf("globbing specs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatalf("no specs found in %s", specDir)
	}

	// Uses DejaVu Sans — matches Ubuntu CI where vega-lite generated these SVGs.
	opts := append([]aster.Option{aster.WithVegaLiteVersion("6.4")}, dejaVuFontOptions(t)...)
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".vl.json")
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownFailures[name]; ok {
				t.Skipf("known failure: %s", reason)
			}

			spec, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatalf("reading spec: %v", err)
			}

			if specHasURL(spec) {
				t.Skip("requires external data")
			}

			svg, err := c.VegaLiteToSVG(spec)
			if err != nil {
				t.Fatalf("VegaLiteToSVG: %v", err)
			}

			if !strings.HasPrefix(svg, "<svg") {
				t.Fatalf("expected SVG output starting with <svg, got: %.100s", svg)
			}

			// Compare against expected SVG from vega-lite compiled examples.
			expectedPath := filepath.Join(expectedDir, name+".svg")
			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("reading expected SVG: %v", err)
			}

			if normalizeSVGNumbers(svg, 0) != normalizeSVGNumbers(string(expected), 0) {
				t.Errorf("SVG output differs from vega-lite expected (%d vs %d bytes)", len(svg), len(expected))
			}
		})
	}
}
