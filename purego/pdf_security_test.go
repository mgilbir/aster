package purego

// PDF-level injection: attacker-controlled strings that reach PDF syntax.

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// setPSName rewrites the PostScript name (name ID 6, Windows UTF-16BE record)
// of a TrueType font in place, keeping its length.
func setPSName(t *testing.T, font []byte, name string) []byte {
	t.Helper()
	b := append([]byte(nil), font...)
	for _, tb := range sfntTables(b) {
		if tb.tag != "name" {
			continue
		}
		nm := b[tb.off : tb.off+tb.length]
		count := int(binary.BigEndian.Uint16(nm[2:]))
		so := int(binary.BigEndian.Uint16(nm[4:]))
		for i := 0; i < count; i++ {
			r := nm[6+i*12:]
			if binary.BigEndian.Uint16(r[6:]) != 6 {
				continue
			}
			plat := binary.BigEndian.Uint16(r)
			l := int(binary.BigEndian.Uint16(r[8:]))
			o := so + int(binary.BigEndian.Uint16(r[10:]))
			if plat == 3 {
				for j := 0; j < l/2; j++ {
					c := byte('x')
					if j < len(name) {
						c = name[j]
					}
					nm[o+2*j], nm[o+2*j+1] = 0, c
				}
				return b
			}
		}
	}
	t.Skip("no Windows PostScript name record in the font")
	return nil
}

// TestPDFFontNameInjection: a font's PostScript name comes from an untrusted
// file (WithFont) and is written as the /BaseFont and /FontName of the PDF.
// It must be written as a valid PDF name (delimiters and whitespace escaped as
// #xx) or sanitized; it must never let the font author add PDF syntax.
func TestPDFFontNameInjection(t *testing.T) {
	src, err := os.ReadFile("../internal/fonts/liberation/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skip(err)
	}
	evil := "A)>>/Evil<</B(x /Z"
	font := setPSName(t, src, evil)
	c, err := New(WithFont("Evil", font))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><text text-anchor="start" transform="translate(5,20)" font-family="Evil" font-size="14px" fill="#000">Hi</text></svg>`
	for _, mode := range []PDFTextMode{PDFTextEmbed, PDFTextNamed} {
		pdf, err := c.SVGToPDF(svg, WithPDFText(mode))
		if err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		if bytes.Contains(pdf, []byte("/Evil")) {
			i := bytes.Index(pdf, []byte("/Evil"))
			t.Errorf("mode %d: font name injected PDF syntax; PDF around it: %q", mode, pdf[max(0, i-40):min(len(pdf), i+60)])
		}
	}
	// SubsetFont returns the same name to the host, which is likely to write it
	// into its own PDF; it should be safe to use as a PDF name too.
	_, name, err := SubsetFont(font, []uint16{0, 1, 2})
	if err == nil && bytes.ContainsAny([]byte(name), "()<>/ []%#") {
		t.Errorf("SubsetFont returned an unsanitized PostScript name %q", name)
	}
}
