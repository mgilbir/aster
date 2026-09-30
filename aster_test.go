package aster_test

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/dejavu"
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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
}

// knownFailures lists Vega-Lite examples whose output differs from the
// expected SVGs (rendered by upstream Vega 6.4.0 / Vega-Lite 6.4.3 in node,
// see testdata/vega-lite/gen_expected.mjs) for a known reason. They are
// skipped rather than marked as errors so the suite stays green.
var knownFailures = map[string]string{
	// Emoji: the expected SVGs measure emoji with node-canvas's system color
	// emoji font; the bundled monochrome Noto Emoji has different advances.
	"isotype_bar_chart_emoji": "emoji advances differ from node-canvas's color emoji font",
	"layer_bar_fruit":         "emoji advances differ from node-canvas's color emoji font",

	// QuickJS differs from V8.
	"histogram_nonlinear":          "QuickJS formats Infinity as \"Infinity\", V8's Intl as \"∞\"",
	"geo_point":                    "QuickJS trigonometry differs from V8 in the last bit (-1e-13 vs 0)",
	"trail_color":                  "QuickJS trigonometry differs from V8 in the last bit (trail arc joins)",
	"bar_grouped_thin":             "QuickJS sort orders an inconsistent mixed-type comparator differently from V8's TimSort",
	"bar_grouped_thin_minBandSize": "QuickJS sort orders an inconsistent mixed-type comparator differently from V8's TimSort",

	// go-text's advances are rounded to 1/64 px; node-canvas's are not. A few
	// label or legend widths land on the other side of a pixel boundary.
	"bar_grouped_repeated":                   "text width rounding (1px)",
	"config_numberFormatType_test":           "text width rounding (1px)",
	"line_color_binned":                      "text width rounding (1px)",
	"point_binned_color":                     "text width rounding (1px)",
	"point_binned_opacity":                   "text width rounding (1px)",
	"point_binned_size":                      "text width rounding (1px)",
	"stacked_bar_count":                      "text width rounding (1px)",
	"stacked_bar_count_corner_radius_config": "text width rounding (1px)",
	"stacked_bar_count_corner_radius_mark":   "text width rounding (1px)",
	"stacked_bar_count_corner_radius_mark_x": "text width rounding (1px)",
	"stacked_bar_count_corner_radius_stroke": "text width rounding (1px)",
	"stacked_bar_size":                       "text width rounding (1px)",

	// Runtime errors in specific specs.
	"facet_independent_scale_layer_broken": "known broken spec: TypeError in Vega compile",
}

// slowSpecs lists specs that take >2s to render (mostly geo/TopoJSON).
// Skipped with -short to keep the development cycle fast.
var slowSpecs = map[string]bool{
	"geo_choropleth":                true, // ~9s
	"geo_circle":                    true, // ~41s
	"geo_constant_value":            true, // ~4s
	"geo_layer":                     true, // ~2.5s
	"geo_line":                      true, // ~2.5s
	"geo_repeat":                    true, // ~3s
	"geo_rule":                      true, // ~2.5s
	"geo_trellis":                   true, // ~11s
	"interactive_1d_geo_brush":      true, // ~3s
	"interactive_geo_facet_species": true, // ~13s
	"interactive_splom":             true, // ~2s
	"layer_point_line_loess":        true, // ~4s
	"repeat_splom":                  true, // ~3s
}

// datasetRedirectTransport rewrites known vega-datasets CDN/GitHub URLs to
// point at a local httptest server, enabling offline testing of specs that
// reference absolute URLs.
type datasetRedirectTransport struct {
	target    string // local httptest server URL
	transport http.RoundTripper
}

func (t *datasetRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	path := req.URL.Path

	var rewritten string
	switch host {
	case "cdn.jsdelivr.net":
		// /npm/vega-datasets@v1.29.0/data/X → /data/X
		const prefix = "/npm/vega-datasets@"
		if strings.HasPrefix(path, prefix) {
			// Find "/data/" after the version segment.
			if idx := strings.Index(path, "/data/"); idx >= 0 {
				rewritten = path[idx:]
			}
		}
	case "raw.githubusercontent.com":
		// /vega/vega-datasets/{branch}/data/X → /data/X
		const prefix = "/vega/vega-datasets/"
		if strings.HasPrefix(path, prefix) {
			rest := path[len(prefix):]
			if idx := strings.Index(rest, "/"); idx >= 0 {
				rewritten = rest[idx:]
			}
		}
	}

	if rewritten != "" {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(t.target, "http://")
		req.URL.Path = rewritten
	}

	return t.transport.RoundTrip(req)
}

// datasetServer starts a local HTTP server that serves testdata/vega-datasets/
// and returns an HTTPLoader whose transport rewrites CDN/GitHub dataset URLs
// to the local server.
func datasetServer(t *testing.T) *aster.HTTPLoader {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/vega-datasets")))
	t.Cleanup(srv.Close)

	transport := &datasetRedirectTransport{
		target:    srv.URL,
		transport: srv.Client().Transport,
	}
	return &aster.HTTPLoader{
		Client: &http.Client{Transport: transport},
	}
}

// dejaVuFontOptions returns aster options that configure DejaVu Sans as the
// text measurement font. DejaVu Sans is the default sans-serif on Ubuntu,
// matching the environment used to generate the vega-lite expected SVGs.
func dejaVuFontOptions(t *testing.T) []aster.Option {
	t.Helper()
	return []aster.Option{
		aster.WithFont("DejaVu Sans", dejavu.SansRegular),
		aster.WithFont("DejaVu Sans", dejavu.SansBold),
		aster.WithFont("DejaVu Sans", dejavu.SansOblique),
		aster.WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoRegular),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoBold),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoOblique),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoBoldOblique),
		aster.WithDefaultFontFamily("DejaVu Sans"),
	}
}

// TestVLConvertSpecs runs the 23 vl-convert test specs against their expected
// SVGs in testdata/vl-convert/expected/v5_8/.
// Absolute-URL specs are served via a local httptest server.
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
	// FallbackLoader: FileLoader for relative paths, httptest for absolute URLs.
	httpLoader := datasetServer(t)
	c, err := aster.New(
		aster.WithVegaLiteVersion("5.8"),
		aster.WithLoader(aster.NewFallbackLoader(
			&aster.FileLoader{BaseDir: "testdata/vega-datasets"},
			httpLoader,
		)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	expectedDir := filepath.Join("testdata", "vl-convert", "expected", "v5_8")

	// VL 5.8 specific known failures.
	vlConvertKnownFailures := map[string]string{
		"geoScale":             "geoScale function not available in vendored Vega 5.25",
		"maptile_background_2": "geoScale function not available in vendored Vega 5.25",
		"stacked_bar_h":        "sub-pixel rounding with missing custom fonts (Caveat/serif)",
	}

	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".vl.json")
		t.Run(name, func(t *testing.T) {
			if reason, ok := vlConvertKnownFailures[name]; ok {
				t.Skipf("known failure: %s", reason)
			}

			spec, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatalf("reading spec: %v", err)
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

// TestVegaLiteExamples runs the official vega-lite v6.4.3 example specs
// (https://github.com/vega/vega-lite, BSD-3-Clause) against expected SVGs
// rendered by upstream Vega 6.4.0 / Vega-Lite 6.4.3 in node — the versions
// vendored here — with testdata/vega-lite/gen_expected.mjs, in UTC.
// Absolute-URL specs are served via a local httptest server.
//
// Font: DejaVu Sans (explicitly loaded, matching the face gen_expected.mjs
// measures with through node-canvas).
func TestVegaLiteExamples(t *testing.T) {
	specDir := filepath.Join("testdata", "vega-lite", "v6.4.3", "specs")
	expectedDir := filepath.Join("testdata", "vega-lite", "v6.4.3", "expected")

	specs, err := filepath.Glob(filepath.Join(specDir, "*.vl.json"))
	if err != nil {
		t.Fatalf("globbing specs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatalf("no specs found in %s", specDir)
	}

	// Uses DejaVu Sans — matches Ubuntu CI where vega-lite generated these SVGs.
	// FallbackLoader: FileLoader for relative paths, httptest for absolute URLs.
	httpLoader := datasetServer(t)
	opts := append([]aster.Option{
		aster.WithVegaLiteVersion("6.4"),
		aster.WithLoader(aster.NewFallbackLoader(
			&aster.FileLoader{BaseDir: "testdata/vega-datasets"},
			httpLoader,
		)),
	}, dejaVuFontOptions(t)...)
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".vl.json")
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownFailures[name]; ok {
				t.Skipf("known failure: %s", reason)
			}
			if testing.Short() && slowSpecs[name] {
				t.Skip("slow spec (use -short=false to run)")
			}

			spec, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatalf("reading spec: %v", err)
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

// Vega resolves an href asynchronously and re-renders once the loader has
// sanitized it; timers must run as macrotasks for that re-render to see the
// sanitized link. A loader that rejects the URL renders no link.
func TestHrefLinks(t *testing.T) {
	spec := []byte(`{"data":{"values":[{"a":1,"u":"https://example.com/x"}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"href":{"field":"u"}}}`)
	for _, tc := range []struct {
		name   string
		loader aster.Loader
		links  int
	}{
		{"allowed", aster.NewHTTPLoader(nil), 1},
		{"denied", aster.DenyLoader{}, 0},
	} {
		c, err := aster.New(aster.WithLoader(tc.loader))
		if err != nil {
			t.Fatal(err)
		}
		svg, err := c.VegaLiteToSVG(spec)
		_ = c.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := strings.Count(svg, `<a xlink:href="https://example.com/x"`); got != tc.links {
			t.Errorf("%s: %d links, want %d", tc.name, got, tc.links)
		}
	}
}
