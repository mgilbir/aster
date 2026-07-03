package aster_test

import (
	"os"
	"testing"

	"github.com/mgilbir/aster"
)

// moviesSpec is a heavier Vega-Lite spec: it loads ~3200 rows from the
// vendored vega-datasets and does binning + aggregation inside QuickJS.
const moviesSpec = `{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "data": {"url": "data/movies.json"},
  "mark": "circle",
  "encoding": {
    "x": {"bin": {"maxbins": 10}, "field": "IMDB Rating"},
    "y": {"bin": {"maxbins": 10}, "field": "Rotten Tomatoes Rating"},
    "size": {"aggregate": "count"}
  }
}`

func benchSpec(b *testing.B, path string) []byte {
	b.Helper()
	spec, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("reading spec %s: %v", path, err)
	}
	return spec
}

func benchConverter(b *testing.B, opts ...aster.Option) *aster.Converter {
	b.Helper()
	c, err := aster.New(opts...)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(func() { _ = c.Close() })
	return c
}

// BenchmarkNew measures converter startup: compiling and instantiating the
// QuickJS WASM runtime and loading the Vega/Vega-Lite bundles.
func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c, err := aster.New()
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		if err := c.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

// BenchmarkVegaLiteToSVG measures rendering a small inline-data Vega-Lite
// spec to SVG (QuickJS/wazero execution path).
func BenchmarkVegaLiteToSVG(b *testing.B) {
	spec := benchSpec(b, "testdata/bar-chart.vl.json")
	c := benchConverter(b)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.VegaLiteToSVG(spec); err != nil {
			b.Fatalf("VegaLiteToSVG: %v", err)
		}
	}
}

// BenchmarkVegaToSVG measures rendering a small inline-data Vega spec to SVG.
func BenchmarkVegaToSVG(b *testing.B) {
	spec := benchSpec(b, "testdata/bar-chart.vg.json")
	c := benchConverter(b)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.VegaToSVG(spec); err != nil {
			b.Fatalf("VegaToSVG: %v", err)
		}
	}
}

// BenchmarkVegaLiteToVega measures compiling Vega-Lite to Vega (pure
// QuickJS work, no scenegraph rendering).
func BenchmarkVegaLiteToVega(b *testing.B) {
	spec := benchSpec(b, "testdata/bar-chart.vl.json")
	c := benchConverter(b)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.VegaLiteToVega(spec); err != nil {
			b.Fatalf("VegaLiteToVega: %v", err)
		}
	}
}

// BenchmarkVegaLiteToSVGMovies measures a heavier render: ~3200 rows loaded
// through the Loader, binned and aggregated inside QuickJS.
func BenchmarkVegaLiteToSVGMovies(b *testing.B) {
	c := benchConverter(b, aster.WithLoader(&aster.FileLoader{BaseDir: "testdata/vega-datasets"}))

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.VegaLiteToSVG([]byte(moviesSpec)); err != nil {
			b.Fatalf("VegaLiteToSVG: %v", err)
		}
	}
}

// BenchmarkSVGToPNG measures the resvg WASM rendering path in isolation.
// The renderer is warmed up before the loop so one-time module
// instantiation and font loading are not measured.
func BenchmarkSVGToPNG(b *testing.B) {
	spec := benchSpec(b, "testdata/bar-chart.vl.json")
	c := benchConverter(b)

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		b.Fatalf("VegaLiteToSVG: %v", err)
	}
	if _, err := c.SVGToPNG(svg); err != nil {
		b.Fatalf("SVGToPNG warm-up: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.SVGToPNG(svg); err != nil {
			b.Fatalf("SVGToPNG: %v", err)
		}
	}
}

// BenchmarkVegaLiteToPNG measures the full end-to-end pipeline:
// QuickJS render to SVG followed by resvg render to PNG.
func BenchmarkVegaLiteToPNG(b *testing.B) {
	spec := benchSpec(b, "testdata/bar-chart.vl.json")
	c := benchConverter(b)

	if _, err := c.VegaLiteToPNG(spec); err != nil {
		b.Fatalf("VegaLiteToPNG warm-up: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.VegaLiteToPNG(spec); err != nil {
			b.Fatalf("VegaLiteToPNG: %v", err)
		}
	}
}
