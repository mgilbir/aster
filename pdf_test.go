package aster_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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
	defer func() { _ = c.Close() }()

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

// TestPDFTextModes exercises the WithPDFText option end to end and pins the
// size relationship that motivates the font modes: named < embed < outlines
// for text-bearing charts.
func TestPDFTextModes(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	sizes := map[string]int{}
	for _, tc := range []struct {
		name string
		mode aster.PDFTextMode
	}{
		{"embed", aster.PDFTextEmbed},
		{"named", aster.PDFTextNamed},
		{"outlines", aster.PDFTextOutlines},
	} {
		pdf, err := c.VegaLiteToPDF(spec, aster.WithPDFText(tc.mode))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
			t.Fatalf("%s: not a PDF", tc.name)
		}
		sizes[tc.name] = len(pdf)
	}
	t.Logf("bar-chart PDF sizes: named=%d embed=%d outlines=%d",
		sizes["named"], sizes["embed"], sizes["outlines"])
	if sizes["named"] >= sizes["embed"] || sizes["embed"] >= sizes["outlines"] {
		t.Errorf("size ordering violated: named=%d embed=%d outlines=%d",
			sizes["named"], sizes["embed"], sizes["outlines"])
	}
}

// TestPDFTextIsExtractable verifies the ToUnicode CMap: text in an
// embedded-font PDF must survive extraction (selection, search, copy/paste).
// Gated on pdftotext (poppler).
func TestPDFTextIsExtractable(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler) not installed")
	}
	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="60">` +
		`<text transform="translate(10,40)" font-size="20">Weighted Average 42</text></svg>`
	pdf, err := c.SVGToPDF(svg)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdftotext", in, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	if !strings.Contains(string(out), "Weighted Average 42") {
		t.Errorf("extracted text %q does not contain the source string", strings.TrimSpace(string(out)))
	}
}
