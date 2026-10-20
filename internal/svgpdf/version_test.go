package svgpdf

import (
	"bytes"
	"testing"
)

// TestVersionCoversWhatIsUsed is C159 (audit 2026-10-18): the header
// declares a version that has every feature the document uses. The output
// is PDF 1.4 (transparency, Type0 fonts), but ActualText in marked content,
// which a glyph standing for two texts and a colour run without a font are
// written with, is PDF 1.5 (ISO 32000-1, 14.9.4): such a document says 1.5.
func TestVersionCoversWhatIsUsed(t *testing.T) {
	m := testMeasurer(t)
	for name, svg := range map[string]string{
		"plain": `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><text x="5" y="30" font-size="20">a b</text></svg>`,
		// The Greek capital omega and the ohm sign are one glyph: its second text
		// is ActualText (C58 of 2026-10-11).
		"one glyph, two texts": `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><text x="5" y="30" font-size="20">` + "caf\u00e9" + `</text><text x="5" y="10" font-size="20">` + "cafe\u0301" + `</text></svg>`,
	} {
		t.Run(name, func(t *testing.T) {
			pdf, err := Convert(svg, m, Options{})
			if err != nil {
				t.Fatal(err)
			}
			actual := bytes.Contains(pdf, []byte("/ActualText"))
			if !actual {
				for _, s := range [][]byte{contentOf(t, pdf)} {
					actual = actual || bytes.Contains(s, []byte("/ActualText"))
				}
			}
			header := string(pdf[:8])
			switch {
			case actual && header < "%PDF-1.5":
				t.Errorf("the content has ActualText, the header says %s", header)
			case !actual && header != "%PDF-1.4":
				t.Errorf("nothing past 1.4 is used, the header says %s", header)
			}
			if name != "plain" && !actual {
				t.Error("the case uses no ActualText: it tests nothing")
			}
		})
	}
}

// contentOf is the content of a PDF's page and its forms, as bytes.
func contentOf(t *testing.T, pdf []byte) []byte { return []byte(allContent(t, pdf)) }
