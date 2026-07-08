package svgpdf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-text/typesetting/font"
	"github.com/mgilbir/aster/internal/textmeasure"
	pdf0 "github.com/mgilbir/pdf0"
)

const fontProbeSVG = `<svg width="300" height="60">` +
	`<text transform="translate(10,40)" font-family="sans-serif" font-size="24">Chart 42</text></svg>`

// TestEmbedModeStructure pins the default text mode: real PDF text (TJ)
// referencing a Type0/CIDFontType2 font with an embedded, subset-prefixed
// font program, a ToUnicode CMap, and a CIDSet.
func TestEmbedModeStructure(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(fontProbeSVG, m, Options{Text: TextEmbed})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)

	for _, marker := range []string{"/FontFile2", "/Type0", "/CIDFontType2", "/Identity-H", "/ToUnicode", "/CIDSet", "+LiberationSans"} {
		if !bytes.Contains(pdf, []byte(marker)) {
			t.Errorf("embed-mode PDF missing %s", marker)
		}
	}

	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	content := contentStreamOf(t, doc)
	if !strings.Contains(content, "] TJ") {
		t.Error("embed-mode content stream has no TJ text operator")
	}
	if strings.Contains(content, " c\n") {
		t.Error("embed-mode content stream contains glyph outline curves")
	}
}

// TestNamedModeStructure: same text structure, no font program — the
// assembling document embeds the font later.
func TestNamedModeStructure(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(fontProbeSVG, m, Options{Text: TextNamed})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)

	if bytes.Contains(pdf, []byte("/FontFile2")) {
		t.Error("named-mode PDF embeds a font program")
	}
	if !bytes.Contains(pdf, []byte("/BaseFont /LiberationSans")) {
		t.Error("named-mode PDF missing the un-prefixed font name")
	}
	if !bytes.Contains(pdf, []byte("/Type0")) {
		t.Error("named-mode PDF missing the Type0 font")
	}
}

// TestModeSizeOrdering pins the point of the feature: named < embed <
// outlines for text-bearing charts.
func TestModeSizeOrdering(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	// A text-heavy document, as charts are: many labels, few distinct glyphs.
	var b strings.Builder
	b.WriteString(`<svg width="400" height="400">`)
	for i := 0; i < 20; i++ {
		b.WriteString(`<text transform="translate(20,`)
		b.WriteString(strings.Repeat("1", 1)) // stable, avoids fmt import
		b.WriteString(`0)" font-size="11">Measurement 0123456789</text>`)
	}
	b.WriteString(`</svg>`)
	svg := b.String()

	sizes := map[TextMode]int{}
	for _, mode := range []TextMode{TextEmbed, TextNamed, TextOutlines} {
		pdf, err := Convert(svg, m, Options{Text: mode})
		if err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		sizes[mode] = len(pdf)
	}
	t.Logf("sizes: named=%d embed=%d outlines=%d", sizes[TextNamed], sizes[TextEmbed], sizes[TextOutlines])
	if sizes[TextNamed] >= sizes[TextEmbed] || sizes[TextEmbed] >= sizes[TextOutlines] {
		t.Errorf("size ordering violated: named=%d embed=%d outlines=%d",
			sizes[TextNamed], sizes[TextEmbed], sizes[TextOutlines])
	}
}

// TestEmbedDeterministic: same input, byte-identical output (fonts included).
func TestEmbedDeterministic(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Convert(fontProbeSVG, m, Options{Text: TextEmbed})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Convert(fontProbeSVG, m, Options{Text: TextEmbed})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("embed-mode output is not deterministic")
	}
}

// noFontDataShaper shapes normally but refuses to reveal font bytes,
// simulating a face whose source cannot be recovered (e.g. a variable
// system-font instance).
type noFontDataShaper struct{ *textmeasure.Measurer }

func (s noFontDataShaper) FontData(*font.Face) []byte { return nil }

// TestEmbedFallsBackToOutlines: a face whose bytes the shaper cannot recover
// must fall back to outlines rather than fail — mixed documents keep
// rendering.
func TestEmbedFallsBackToOutlines(t *testing.T) {
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(fontProbeSVG, noFontDataShaper{m}, Options{Text: TextEmbed})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)
	if bytes.Contains(pdf, []byte("/FontFile2")) {
		t.Error("fallback PDF should not embed fonts")
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	content := contentStreamOf(t, doc)
	if !strings.Contains(content, " c\n") {
		t.Error("fallback PDF has no outline curves; text was dropped")
	}
}
