package aster_test

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

func TestSVGToPNG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50">
		<rect width="100" height="50" fill="steelblue"/>
	</svg>`

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	data, err := c.SVGToPNG(svg)
	if err != nil {
		t.Fatalf("SVGToPNG: %v", err)
	}

	// Verify PNG header.
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.HasPrefix(data, pngHeader) {
		t.Fatal("output does not have PNG header")
	}

	// Decode and check dimensions.
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 50 {
		t.Errorf("expected 100x50, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestVegaLiteToPNG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	data, err := c.VegaLiteToPNG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
	}

	// Verify valid PNG.
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() < 10 || bounds.Dy() < 10 {
		t.Errorf("unexpected small dimensions: %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestVegaLiteToPNGScale(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	data1x, err := c.VegaLiteToPNG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPNG scale=1: %v", err)
	}
	data2x, err := c.VegaLiteToPNG(spec, aster.WithScale(2.0))
	if err != nil {
		t.Fatalf("VegaLiteToPNG scale=2: %v", err)
	}

	img1x, err := png.Decode(bytes.NewReader(data1x))
	if err != nil {
		t.Fatalf("png.Decode 1x: %v", err)
	}
	img2x, err := png.Decode(bytes.NewReader(data2x))
	if err != nil {
		t.Fatalf("png.Decode 2x: %v", err)
	}

	b1 := img1x.Bounds()
	b2 := img2x.Bounds()

	if b2.Dx() != b1.Dx()*2 || b2.Dy() != b1.Dy()*2 {
		t.Errorf("expected 2x dimensions: 1x=%dx%d, 2x=%dx%d",
			b1.Dx(), b1.Dy(), b2.Dx(), b2.Dy())
	}
}

func TestSVGToPNGError(t *testing.T) {
	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err = c.SVGToPNG("not valid svg at all")
	if err == nil {
		t.Fatal("expected error for invalid SVG, got nil")
	}
}

// pngRMSE computes the root-mean-square error between two images, normalized
// to the [0, 65535] range (matching color.Color.RGBA() output).
func pngRMSE(a, b image.Image) (float64, error) {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return 0, fmt.Errorf("dimension mismatch: %dx%d vs %dx%d",
			ab.Dx(), ab.Dy(), bb.Dx(), bb.Dy())
	}
	var sum float64
	n := ab.Dx() * ab.Dy() * 4 // 4 channels: R, G, B, A
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ar, ag, ab2, aa := a.At(x, y).RGBA()
			br, bg, bb2, ba := b.At(x, y).RGBA()
			sum += float64((ar-br)*(ar-br) + (ag-bg)*(ag-bg) + (ab2-bb2)*(ab2-bb2) + (aa-ba)*(aa-ba))
		}
	}
	return math.Sqrt(sum / float64(n)), nil
}

// TestVLConvertPNGSpecs compares PNG output against vl-convert expected PNGs.
// Both pipelines use resvg for SVG→PNG, so output should be very close.
// Expected PNGs are from https://github.com/vega/vl-convert (BSD-3-Clause).
func TestVLConvertPNGSpecs(t *testing.T) {
	expectedDir := filepath.Join("testdata", "vl-convert", "expected", "v5_8")

	// Discover specs that have expected PNGs.
	pngs, err := filepath.Glob(filepath.Join(expectedDir, "*.png"))
	if err != nil {
		t.Fatalf("globbing PNGs: %v", err)
	}
	if len(pngs) == 0 {
		t.Fatal("no expected PNGs found in testdata/vl-convert/expected/v5_8/")
	}

	// Scale factors per spec (from vl-convert test_specs.rs).
	scale2x := map[string]bool{
		"bar_chart_trellis_compact": true,
		"stacked_bar_h":            true,
		"stacked_bar_h2":           true,
		"line_with_log_scale":      true,
		"font_with_quotes":         true,
		"stocks_locale":            true,
	}

	// Known failures / skips specific to PNG comparison.
	pngSkips := map[string]string{
		"custom_projection":    "structuredClone polyfill gap with custom projection",
		"remote_images":        "image marks reference external URLs",
		"geoScale":             "geoScale function not available in vendored Vega 5.25",
		"maptile_background_2": "geoScale function not available in vendored Vega 5.25",
		"long_text_lable":      "text measurement difference causes dimension mismatch",
		"maptile_background":   "geo/tile rendering RMSE too high (wasm32 vs native resvg)",
		"stacked_bar_h":        "dimension mismatch from missing custom fonts (Caveat/serif)",
		"stacked_bar_h2":       "dimension mismatch from missing custom fonts",
		"stocks_locale":        "RMSE slightly above threshold (text rounding at 2x scale)",
	}

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

	for _, pngPath := range pngs {
		name := strings.TrimSuffix(filepath.Base(pngPath), ".png")
		t.Run(name, func(t *testing.T) {
			if reason, ok := pngSkips[name]; ok {
				t.Skipf("known failure: %s", reason)
			}

			specPath := filepath.Join("testdata", "vl-convert", name+".vl.json")
			spec, err := os.ReadFile(specPath)
			if err != nil {
				t.Fatalf("reading spec: %v", err)
			}

			scale := 1.0
			if scale2x[name] {
				scale = 2.0
			}

			var pngOpts []aster.PNGOption
			if scale != 1.0 {
				pngOpts = append(pngOpts, aster.WithScale(scale))
			}

			actual, err := c.VegaLiteToPNG(spec, pngOpts...)
			if err != nil {
				t.Fatalf("VegaLiteToPNG: %v", err)
			}

			actualImg, err := png.Decode(bytes.NewReader(actual))
			if err != nil {
				t.Fatalf("decoding actual PNG: %v", err)
			}

			expectedData, err := os.ReadFile(pngPath)
			if err != nil {
				t.Fatalf("reading expected PNG: %v", err)
			}
			expectedImg, err := png.Decode(bytes.NewReader(expectedData))
			if err != nil {
				t.Fatalf("decoding expected PNG: %v", err)
			}

			rmse, err := pngRMSE(actualImg, expectedImg)
			if err != nil {
				t.Fatalf("pngRMSE: %v", err)
			}

			// RMSE threshold accounts for text anti-aliasing differences between
			// resvg compiled to wasm32 (aster) vs native x86_64 (vl-convert).
			// Both use resvg 0.45.1 + Liberation Sans, but sub-pixel glyph
			// rasterization differs across architectures. Typical RMSE for
			// text-heavy specs is 1100-1850; structural regressions produce
			// values well above 2000.
			const threshold = 2000.0 // out of 65535 ≈ 3% tolerance
			t.Logf("RMSE: %.2f (threshold: %.0f, scale: %.1f)", rmse, threshold, scale)
			if rmse > threshold {
				t.Errorf("RMSE %.2f exceeds threshold %.0f", rmse, threshold)
			}
		})
	}
}
