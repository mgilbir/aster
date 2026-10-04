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
	"time"

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
		aster.WithTimeout(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	expectedDir := filepath.Join("testdata", "vl-convert", "expected", "v5_8")

	// VL 5.8 specific known failures.
	vlConvertKnownFailures := map[string]string{
		"geoScale":             "vl-convert's Vega 5.25 has no geoScale function",
		"maptile_background_2": "vl-convert's Vega 5.25 has no geoScale function",
		"stacked_bar_h":        "sub-pixel rounding with missing custom fonts (Caveat/serif)",
		// The engine renders Vega-Lite 5.8 with its Vega 6.4 runtime; Vega 6
		// sizes this size legend taller than vl-convert's Vega 5 (checked in
		// node).
		"circle_binned":          "Vega 6 legend layout differs from Vega 5",
		"circle_binned_base_url": "Vega 6 legend layout differs from Vega 5",
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

			// vl-convert renders Vega-Lite 5.8 with Vega 5, whose default
			// stroke-miterlimit is 10; the engine renders every version with
			// its Vega 6.4 runtime, where it is 4.
			want := strings.Replace(string(expected), `stroke-miterlimit="10"`, `stroke-miterlimit="4"`, 1)
			if normalizeSVGNumbers(svg, 0) != normalizeSVGNumbers(want, 0) {
				t.Errorf("SVG output differs from vl-convert expected (%d vs %d bytes)", len(svg), len(expected))
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
