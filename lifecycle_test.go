package aster_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

const tinySVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`

// Every rendering method must fail with a clear "closed" error after Close —
// not a fake WASM panic (SVG path) or a silent success that leaks a fresh
// WASM runtime (PNG path).
func TestRenderAfterCloseFailsClearly(t *testing.T) {
	spec := []byte(`{
		"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
		"data": {"values": [{"a": "A", "b": 28}]},
		"mark": "bar",
		"encoding": {"x": {"field": "a", "type": "nominal"}, "y": {"field": "b", "type": "quantitative"}}
	}`)

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertClosed := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s after Close should fail", name)
			return
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("%s after Close: want a 'closed' error, got: %v", name, err)
		}
	}

	_, err = c.VegaLiteToSVG(spec)
	assertClosed("VegaLiteToSVG", err)
	_, err = c.VegaToSVG(spec)
	assertClosed("VegaToSVG", err)
	_, err = c.VegaLiteToVega(spec)
	assertClosed("VegaLiteToVega", err)
	_, err = c.SVGToPNG(tinySVG)
	assertClosed("SVGToPNG", err)
	_, err = c.VegaLiteToPNG(spec)
	assertClosed("VegaLiteToPNG", err)
	_, err = c.SVGToPDF(tinySVG)
	assertClosed("SVGToPDF", err)
	_, err = c.VegaToPDF(spec)
	assertClosed("VegaToPDF", err)
	_, err = c.VegaLiteToPDF(spec)
	assertClosed("VegaLiteToPDF", err)
	_, _, err = c.SVGToPDFUsage(tinySVG)
	assertClosed("SVGToPDFUsage", err)
	_, _, err = c.VegaToPDFUsage(spec)
	assertClosed("VegaToPDFUsage", err)
	_, _, err = c.VegaLiteToPDFUsage(spec)
	assertClosed("VegaLiteToPDFUsage", err)
}

// Close is idempotent, including when the PNG renderer was initialized.
func TestCloseIdempotentWithPNGRenderer(t *testing.T) {
	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.SVGToPNG(tinySVG); err != nil {
		t.Fatalf("SVGToPNG: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
