package aster_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster"
)

// TestVegaLiteToPDF renders the bar chart spec end to end and compares the
// vector PDF size against the PNG render of the same chart.
func TestVegaLiteToPDF(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("creating converter: %v", err)
	}
	defer c.Close()

	pdf, err := c.VegaLiteToPDF(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-")
	}

	png, err := c.VegaLiteToPNG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
	}
	t.Logf("bar-chart sizes: PDF %d bytes, PNG %d bytes (%.1fx)",
		len(pdf), len(png), float64(len(png))/float64(len(pdf)))
}

// TestVegaToPDF exercises the Vega (non-Lite) entry point.
func TestVegaToPDF(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vg.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("creating converter: %v", err)
	}
	defer c.Close()

	pdf, err := c.VegaToPDF(spec)
	if err != nil {
		t.Fatalf("VegaToPDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-")
	}
}

// TestSVGToPDFWithoutTextMeasurement checks that PDF output works when the
// converter was built with text measurement disabled (a shaper is created
// lazily for PDF use).
func TestSVGToPDFWithoutTextMeasurement(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("creating converter: %v", err)
	}
	defer c.Close()

	pdf, err := c.VegaLiteToPDF(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-")
	}
}

// TestWriteDemoPDF writes sample PDFs for visual inspection. It only runs
// when ASTER_DEMO_DIR is set:
//
//	ASTER_DEMO_DIR=/tmp/aster-demo go test -run TestWriteDemoPDF .
func TestWriteDemoPDF(t *testing.T) {
	dir := os.Getenv("ASTER_DEMO_DIR")
	if dir == "" {
		t.Skip("ASTER_DEMO_DIR not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("creating converter: %v", err)
	}
	defer c.Close()

	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := c.VegaLiteToPDF(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPDF: %v", err)
	}
	out := filepath.Join(dir, "bar-chart.pdf")
	if err := os.WriteFile(out, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", out, len(pdf))

	// Also translate the checked-in golden SVGs so a human can eyeball the
	// full subset (grouped bars, log-scale lines, binned circles, text).
	goldens, err := filepath.Glob(filepath.Join("testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range goldens {
		svg, err := os.ReadFile(g)
		if err != nil {
			t.Fatal(err)
		}
		pdf, err := c.SVGToPDF(string(svg))
		if err != nil {
			t.Fatalf("SVGToPDF(%s): %v", g, err)
		}
		name := filepath.Base(g)
		out := filepath.Join(dir, name[:len(name)-len(".svg")]+".pdf")
		if err := os.WriteFile(out, pdf, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", out, len(pdf))
	}
}
