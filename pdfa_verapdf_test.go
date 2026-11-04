package aster_test

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
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

// The veraPDF tests validate charts written WithPDFAEmbeddable with veraPDF
// itself (the PDF/A reference validator) inside a PDF/A document, as the
// standard asks of an embedded chart: pdf0 builds a PDF/A-2b, -2u or -3b
// document (output intent, XMP metadata, file identifier) and appends the
// chart's page to it, and every such document must be compliant. They run
// when ASTER_VERAPDF names a veraPDF command line (scripts/verapdf/docker.sh
// runs the veraPDF Consortium's image), are skipped otherwise, and fail with
// ASTER_VERAPDF=require and none named.
func veraPDFCommand(t *testing.T) string {
	t.Helper()
	cmd := os.Getenv("ASTER_VERAPDF")
	switch cmd {
	case "":
		t.Skip("ASTER_VERAPDF is not set (scripts/verapdf/docker.sh runs veraPDF in Docker)")
	case "require":
		t.Fatal("ASTER_VERAPDF=require: set it to a veraPDF command line, as scripts/verapdf/docker.sh")
	}
	return cmd
}

// veraPDFReport is the part of veraPDF's machine-readable report (--format
// mrr) the tests read.
type veraPDFReport struct {
	Jobs []struct {
		Name   string `xml:"item>name"`
		Report *struct {
			Compliant bool `xml:"isCompliant,attr"`
			Rules     []struct {
				Clause      string `xml:"clause,attr"`
				Test        string `xml:"testNumber,attr"`
				Description string `xml:"description"`
			} `xml:"details>rule"`
		} `xml:"validationReport"`
	} `xml:"jobs>job"`
}

// veraPDF validates every PDF in dir against flavour and returns, by file
// name, the clauses each breaks (none for a compliant file).
func veraPDF(t *testing.T, cmd, flavour, dir string) map[string][]string {
	t.Helper()
	out, err := exec.Command(cmd, "--flavour", flavour, "--format", "mrr", dir).Output()
	var rep veraPDFReport
	if perr := xml.Unmarshal(out, &rep); perr != nil || len(rep.Jobs) == 0 {
		t.Fatalf("veraPDF %s on %s: %v; report unreadable (%v):\n%.2000s", flavour, dir, err, perr, out)
	}
	got := map[string][]string{}
	for _, j := range rep.Jobs {
		name := filepath.Base(j.Name)
		if j.Report == nil {
			t.Fatalf("veraPDF %s: no validation report for %s (the job failed)", flavour, name)
		}
		got[name] = []string{}
		if j.Report.Compliant {
			continue
		}
		for _, r := range j.Report.Rules {
			got[name] = append(got[name], fmt.Sprintf("%s-%s %.120s", r.Clause, r.Test, strings.TrimSpace(r.Description)))
		}
	}
	return got
}

// inPDFADocument is a PDF/A document of level with chart's page in it.
func inPDFADocument(t *testing.T, chart []byte, level pdfa.Level) []byte {
	t.Helper()
	src, err := pdf0.Read(bytes.NewReader(chart), int64(len(chart)))
	if err != nil {
		t.Fatal(err)
	}
	host, err := pdf0.NewPDFADocument(level)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := host.AppendPages(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Omitted) > 0 {
		t.Fatalf("appending the chart omitted %v: the document would not hold all of it", rep.Omitted)
	}
	var buf bytes.Buffer
	if err := host.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pdfaFeatureSVG has every construct a PDF/A rule is about: translucent
// paint, a gradient with a translucent stop, an image with alpha drawn
// smooth, a blend mode, a link, filled and stroked text, and a character no
// font has.
func pdfaFeatureSVG(t *testing.T) string {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 30), uint8(y * 30), 200, uint8(60 + x*20)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="120">` +
		`<defs><linearGradient id="g"><stop offset="0" stop-color="red" stop-opacity="0.3"/><stop offset="1" stop-color="blue"/></linearGradient></defs>` +
		`<rect width="200" height="120" fill="#fff"/>` +
		`<rect x="5" y="5" width="60" height="40" fill="steelblue" fill-opacity="0.6" stroke="black" stroke-opacity="0.5"/>` +
		`<rect x="70" y="5" width="60" height="40" fill="url(#g)"/>` +
		`<rect x="20" y="20" width="60" height="40" fill="orange" style="mix-blend-mode: multiply"/>` +
		`<image xlink:href="` + src + `" x="140" y="5" width="50" height="40"/>` +
		`<a href="https://example.com/"><rect x="5" y="60" width="40" height="20" fill="green"/></a>` +
		`<text x="5" y="100" font-family="sans-serif" font-size="12px" fill="black">Axis 42 ` + "\U000F0000" + `</text>` +
		`<text x="100" y="100" font-family="sans-serif" font-size="12px" fill="none" stroke="black">stroked</text>` +
		`</svg>`
}

// TestPDFAEmbeddableVeraPDF: charts written WithPDFAEmbeddable, in a PDF/A-2b,
// -2u or -3b document, are compliant by veraPDF: an SVG of every construct a
// rule is about and a set of Vega and Vega-Lite charts (the whole of both
// corpora without -short), with fonts embedded and as outlines. The same
// SVG written without the option is not (6.2.8, /Interpolate), so veraPDF
// is seen to check.
func TestPDFAEmbeddableVeraPDF(t *testing.T) {
	cmd := veraPDFCommand(t)
	c, err := aster.New(aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	type chart struct {
		name string
		pdf  func(...aster.PDFOption) ([]byte, error)
	}
	svg := pdfaFeatureSVG(t)
	charts := []chart{{"features", func(o ...aster.PDFOption) ([]byte, error) { return c.SVGToPDF(svg, o...) }}}
	specs := []string{
		"testdata/vega-lite/v6.4.3/specs/bar.vl.json",
		"testdata/vega-lite/v6.4.3/specs/point_href.vl.json",
		"testdata/vega-lite/v6.4.3/specs/area_gradient.vl.json",
		"testdata/corpus/vg-gallery/stacked-area-chart.vg.json",
		"testdata/corpus/vg-gallery/density-heatmaps.vg.json",
		"testdata/corpus/vg-gallery/serpentine-timeline.vg.json",
		"testdata/corpus/vg-gallery/word-cloud.vg.json",
	}
	if !testing.Short() {
		specs = nil
		for _, g := range []string{"testdata/vega-lite/v6.4.3/specs/*.vl.json", "testdata/corpus/vg-gallery/*.vg.json"} {
			m, err := filepath.Glob(g)
			if err != nil || len(m) == 0 {
				t.Fatalf("no specs match %s", g)
			}
			specs = append(specs, m...)
		}
	}
	for _, f := range specs {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lite := strings.HasSuffix(f, ".vl.json")
		charts = append(charts, chart{strings.TrimSuffix(filepath.Base(f), ".json"), func(o ...aster.PDFOption) ([]byte, error) {
			if lite {
				return c.VegaLiteToPDF(spec, o...)
			}
			return c.VegaToPDF(spec, o...)
		}})
	}

	levels := []struct {
		flavour string
		level   pdfa.Level
		all     bool // every chart, not only the first few
	}{{"2b", pdfa.PDFA2b, true}, {"2u", pdfa.PDFA2u, true}, {"3b", pdfa.PDFA3b, false}}
	modes := []struct {
		name string
		mode aster.PDFTextMode
	}{{"embed", aster.PDFTextEmbed}, {"outlines", aster.PDFTextOutlines}}
	root := t.TempDir()
	written := 0
	for _, lv := range levels {
		dir := filepath.Join(root, lv.flavour)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i, ch := range charts {
			if !lv.all && i >= 8 {
				break
			}
			for _, m := range modes {
				if m.mode == aster.PDFTextOutlines && i >= 8 {
					continue // outlines for the feature SVG and the named charts
				}
				pdf, err := ch.pdf(aster.WithPDFAEmbeddable(), aster.WithPDFText(m.mode))
				if err != nil {
					continue // a chart that does not render is other tests' concern
				}
				name := ch.name + "." + m.name + ".pdf"
				if err := os.WriteFile(filepath.Join(dir, name), inPDFADocument(t, pdf, lv.level), 0o644); err != nil {
					t.Fatal(err)
				}
				written++
			}
		}
		for name, failed := range veraPDF(t, cmd, lv.flavour, dir) {
			if len(failed) > 0 {
				t.Errorf("PDF/A-%s, %s: %s", lv.flavour, name, strings.Join(failed, "; "))
			}
		}
	}
	if written < 3*len(modes) {
		t.Fatalf("only %d documents written", written)
	}

	// The control: without the option, the feature SVG is not compliant.
	control := filepath.Join(root, "control")
	if err := os.Mkdir(control, 0o755); err != nil {
		t.Fatal(err)
	}
	pdf, err := charts[0].pdf()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "features.default.pdf"), inPDFADocument(t, pdf, pdfa.PDFA2b), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := veraPDF(t, cmd, "2b", control)["features.default.pdf"]
	if !strings.Contains(strings.Join(failed, " "), "6.2.8-") {
		t.Errorf("veraPDF finds the default PDF's /Interpolate true compliant (%v): the check above proves nothing", failed)
	}
}
