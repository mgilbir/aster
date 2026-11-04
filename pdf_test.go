package aster_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
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
		popplerMissing(t, "pdftotext")
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

// popplerMissing skips a test whose poppler tool is not installed, or fails
// it with ASTER_POPPLER=require (as in CI), so that the check runs somewhere.
func popplerMissing(t *testing.T, tool string) {
	t.Helper()
	if os.Getenv("ASTER_POPPLER") == "require" {
		t.Fatalf("%s (poppler) required (ASTER_POPPLER=require) but not installed", tool)
	}
	t.Skipf("%s (poppler) not installed", tool)
}

// TestPDFAEmbeddable: WithPDFAEmbeddable writes a chart that passes PDF/A-2b
// but for the embedding document's own rules (file identifier, XMP
// metadata), where the default PDF of the same chart does not (issue #179:
// an image mark's /Interpolate); for a specification and for SVG input
// alike. With PDFTextNamed it fails the call.
func TestPDFAEmbeddable(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	spec := []byte(`{"data":{"values":[{"x":1}]},"mark":{"type":"image","width":40,"height":40},` +
		`"encoding":{"x":{"field":"x","type":"quantitative"},"url":{"value":"` + url + `"}}}`)
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatal(err)
	}
	rules := func(pdf []byte) map[string]string {
		doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, v := range pdf0.ValidatePDFA(doc, pdfa.PDFA2b) {
			got[v.Rule] = v.Message
		}
		return got
	}
	for name, render := range map[string]func(...aster.PDFOption) ([]byte, error){
		"spec": func(o ...aster.PDFOption) ([]byte, error) { return c.VegaLiteToPDF(spec, o...) },
		"svg":  func(o ...aster.PDFOption) ([]byte, error) { return c.SVGToPDF(svg, o...) },
	} {
		def, err := render()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rules(def)["6.2.8"]; !ok {
			t.Errorf("%s: the default PDF does not break 6.2.8 (/Interpolate): the test proves nothing", name)
		}
		pdf, err := render(aster.WithPDFAEmbeddable())
		if err != nil {
			t.Fatal(err)
		}
		for rule, msg := range rules(pdf) {
			if rule != "6.1.3" && rule != "6.6.2.1" {
				t.Errorf("%s: WithPDFAEmbeddable breaks PDF/A-2b %s: %s", name, rule, msg)
			}
		}
		if _, err := render(aster.WithPDFAEmbeddable(), aster.WithPDFText(aster.PDFTextNamed)); err == nil {
			t.Errorf("%s: WithPDFAEmbeddable with PDFTextNamed did not fail", name)
		}
	}
}

// TestPDFAEmbeddableCorpus: every chart of the Vega-Lite examples and the
// Vega gallery, written WithPDFAEmbeddable, passes PDF/A-2b and -2u but for
// the embedding document's rules (file identifier, metadata). Not with
// -short: it renders and validates every chart.
func TestPDFAEmbeddableCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("renders and validates every chart; run without -short")
	}
	c, err := aster.New(aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var files []string
	for _, g := range []string{"testdata/vega-lite/v6.4.3/specs/*.vl.json", "testdata/corpus/vg-gallery/*.vg.json"} {
		m, err := filepath.Glob(g)
		if err != nil || len(m) == 0 {
			t.Fatalf("no specs match %s", g)
		}
		files = append(files, m...)
	}
	validated := 0
	for _, f := range files {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var pdf []byte
		if strings.HasSuffix(f, ".vl.json") {
			pdf, err = c.VegaLiteToPDF(spec, aster.WithPDFAEmbeddable())
		} else {
			pdf, err = c.VegaToPDF(spec, aster.WithPDFAEmbeddable())
		}
		if err != nil {
			continue // a chart that does not render is other tests' concern
		}
		doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA2u} {
			for _, v := range pdf0.ValidatePDFA(doc, level) {
				if v.Rule != "6.1.3" && v.Rule != "6.6.2.1" {
					t.Errorf("%s: %s %s: %s", filepath.Base(f), level, v.Rule, v.Message)
				}
			}
		}
		validated++
	}
	if validated < 700 {
		t.Errorf("only %d charts validated, want the corpus (over 700)", validated)
	}
}
