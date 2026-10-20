package svgpdf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
)

// strokedText is what vega-scenegraph writes for a text mark with a stroke
// (marks/text.js strokes the text after filling it, as a browser does an
// SVG <text>): fill, stroke, and the stroke's width and dashes.
func strokedText(attrs string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="220" height="60"><rect width="220" height="60" fill="#ffffff"/>` +
		`<text transform="translate(10,45)" font-family="Liberation Sans" font-size="40px" font-weight="bold"` + attrs + `>HALO</text></svg>`
}

// TestTextStroke is C42 (audit 2026-10-18): text with a stroke is stroked,
// as upstream strokes it, after it is filled, with the stroke's width,
// joins and dashes; text with a stroke and no fill is drawn by its stroke.
// Either way its text is extracted once. Read back with poppler against
// the PNG: the stroke's red pixels are where the PNG's are.
func TestTextStroke(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"fill and stroke":   ` fill="#ffffff" stroke="#ff0000" stroke-width="4"`,
		"stroke only":       ` fill="none" stroke="#ff0000" stroke-width="3"`,
		"dashed, round":     ` fill="#0000ff" stroke="#ff0000" stroke-width="3" stroke-dasharray="6 3" stroke-linejoin="round"`,
		"translucent group": ` fill="#ffffff" stroke="#ff0000" stroke-width="4" opacity="0.6"`,
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			svg := strokedText(attrs)
			for _, mode := range []TextMode{TextEmbed, TextOutlines} {
				pdf, err := Convert(svg, m, Options{Text: mode})
				if err != nil {
					t.Fatal(err)
				}
				verifyPDF(t, pdf)
				// The stroke is painted (S, or text rendering mode 1), on
				// the page or in its group.
				content := allContent(t, pdf)
				if !strings.Contains(content, "\nS\n") && !strings.Contains(content, "1 Tr") {
					t.Fatalf("%v: no stroke in the content stream", mode)
				}
				if _, err := exec.LookPath("pdftoppm"); err != nil {
					continue
				}
				got := popplerImage(t, pdf)
				want, err := raster.Render([]byte(svg), raster.Options{Shaper: raster.NewShaperFromMeasurer(m.Exact())})
				if err != nil {
					t.Fatal(err)
				}
				red := func(c interface{ RGBA() (r, g, b, a uint32) }) bool {
					r, g, b := overWhite(c)
					return r > 200 && r-g > 80 && r-b > 80
				}
				var both, pdfOnly, pngOnly int
				b := want.Bounds()
				for y := b.Min.Y; y < b.Max.Y; y++ {
					for x := b.Min.X; x < b.Max.X; x++ {
						p, q := red(got.At(x, y)), red(want.At(x, y))
						switch {
						case p && q:
							both++
						case p:
							pdfOnly++
						case q:
							pngOnly++
						}
					}
				}
				// Anti-aliasing differs at the stroke's edges.
				if both == 0 || pdfOnly+pngOnly > both/3 {
					t.Errorf("%v: red pixels in both %d, the PDF alone %d, the PNG alone %d", mode, both, pdfOnly, pngOnly)
				}
				if mode == TextOutlines {
					continue
				}
				if _, err := exec.LookPath("pdftotext"); err != nil {
					continue
				}
				in := filepath.Join(t.TempDir(), "s.pdf")
				if err := os.WriteFile(in, pdf, 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command("pdftotext", in, "-").Output()
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.TrimSpace(string(out)); got != "HALO" {
					t.Errorf("text extracted %q, want HALO once", got)
				}
			}
		})
	}
}

// allContent is the content of the page and of every form it paints.
func allContent(t *testing.T, pdf []byte) string {
	t.Helper()
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	all := contentStreamOf(t, doc)
	for _, o := range doc.Objects {
		s, ok := o.Value.(*pdf0.Stream)
		if !ok || s.Dict.Get("Subtype") != pdf0.Name("Form") {
			continue
		}
		c, err := flateDecode(s.Data)
		if err != nil {
			t.Fatal(err)
		}
		all += string(c)
	}
	return all
}
