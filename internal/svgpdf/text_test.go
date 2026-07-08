package svgpdf

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/textmeasure"
	pdf0 "github.com/mgilbir/pdf0"
)

func textSVG(anchor string) string {
	return `<svg width="100" height="50">` +
		`<text transform="translate(50,25)" text-anchor="` + anchor + `" font-family="sans-serif" font-size="10px" fill="#000">Hop</text>` +
		`</svg>`
}

// firstMoveToX extracts the x coordinate of the first "m" (moveto) operator.
func firstMoveToX(t *testing.T, content string) float64 {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[2] == "m" {
			x, err := strconv.ParseFloat(fields[0], 64)
			if err != nil {
				t.Fatalf("parsing moveto x %q: %v", fields[0], err)
			}
			return x
		}
	}
	t.Fatal("no moveto found in content stream")
	return 0
}

// TestTextAnchor verifies that text-anchor shifts glyph geometry by the
// shaped advance: middle sits half an advance left of start, end a full
// advance left.
func TestTextAnchor(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	_, advance := m.ShapeText("Hop", "10px sans-serif")
	if advance <= 0 {
		t.Fatalf("advance: got %g", advance)
	}

	xs := make(map[string]float64)
	for _, anchor := range []string{"start", "middle", "end"} {
		pdf, err := Convert(textSVG(anchor), m, Options{Text: TextOutlines})
		if err != nil {
			t.Fatalf("Convert(%s): %v", anchor, err)
		}
		doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		xs[anchor] = firstMoveToX(t, contentStreamOf(t, doc))
	}

	if got := xs["start"] - xs["middle"]; !within(got, advance/2, 0.01) {
		t.Errorf("middle shift: got %g, want %g", got, advance/2)
	}
	if got := xs["start"] - xs["end"]; !within(got, advance, 0.01) {
		t.Errorf("end shift: got %g, want %g", got, advance)
	}
}

func within(a, b, tol float64) bool {
	d := a - b
	return d < tol && d > -tol
}

// TestTextProducesOutlinesNotFonts confirms TextOutlines mode: the PDF
// contains no /Font resources and no text-showing operators, only filled
// path geometry.
func TestTextProducesOutlinesNotFonts(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(textSVG("start"), m, Options{Text: TextOutlines})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pdf, []byte("/Font")) {
		t.Error("PDF contains a /Font resource; text should be outlines")
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	content := contentStreamOf(t, doc)
	for _, op := range []string{"BT", "Tj", "TJ"} {
		if strings.Contains(content, op) {
			t.Errorf("content stream contains text operator %s", op)
		}
	}
	// Glyph outlines must include curves.
	if !strings.Contains(content, " c\n") {
		t.Error("no cubic operators in content stream; glyph outlines missing")
	}
}

// TestTextWhitespaceOnly renders nothing but must not error.
func TestTextWhitespaceOnly(t *testing.T) {
	svg := `<svg width="10" height="10"><text transform="translate(5,5)" font-size="10px"> </text></svg>`
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(svg, m, Options{}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
}
