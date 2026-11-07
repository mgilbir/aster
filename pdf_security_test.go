package aster

// PDF-level injection: attacker-controlled strings that reach PDF syntax.

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/mgilbir/pdf0"
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
	src, err := os.ReadFile("internal/fonts/liberation/LiberationSans-Regular.ttf")
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

// dropPSName renames every PostScript name record (name ID 6) of a TrueType
// font to an ID nothing reads, so the font states no PostScript name.
func dropPSName(t *testing.T, font []byte) []byte {
	t.Helper()
	b := append([]byte(nil), font...)
	for _, tb := range sfntTables(b) {
		if tb.tag != "name" {
			continue
		}
		nm := b[tb.off : tb.off+tb.length]
		count := int(binary.BigEndian.Uint16(nm[2:]))
		for i := 0; i < count; i++ {
			r := nm[6+i*12:]
			if binary.BigEndian.Uint16(r[6:]) == 6 {
				binary.BigEndian.PutUint16(r[6:], 0x99)
			}
		}
		return b
	}
	t.Fatal("no name table")
	return nil
}

// TestFontNameAgreesAcrossUsageSubsetAndPDF: a document assembled later embeds
// SubsetFont's program under the name SubsetFont returns, for the font a
// PDFTextNamed chart refers to by /BaseFont and FontUsage reports. The three
// must be one name, also for a font that states none and for one whose name
// is not a valid PDF name as it stands.
func TestFontNameAgreesAcrossUsageSubsetAndPDF(t *testing.T) {
	src, err := os.ReadFile("internal/fonts/liberation/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		label string
		font  []byte
	}{
		{"as published", src},
		{"no PostScript name", dropPSName(t, src)},
		{"PDF delimiters in the name", setPSName(t, src, "Ab(c)d/e[f] g%h")},
	} {
		c, err := New(WithFont("Probe", tc.font))
		if err != nil {
			t.Fatal(err)
		}
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="30"><text x="5" y="20" font-family="Probe" font-size="12px">Hi</text></svg>`
		pdf, uses, err := c.SVGToPDFUsage(svg, WithPDFText(PDFTextNamed))
		c.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.label, err)
		}
		if len(uses) != 1 {
			t.Fatalf("%s: %d faces used, want 1", tc.label, len(uses))
		}
		u := uses[0]
		_, name, err := SubsetFont(u.Source, u.GIDs)
		if err != nil {
			t.Fatalf("%s: SubsetFont: %v", tc.label, err)
		}
		if name != u.PostScriptName {
			t.Errorf("%s: SubsetFont names the font %q, FontUsage %q", tc.label, name, u.PostScriptName)
		}
		doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		bases := 0
		for _, o := range doc.Objects {
			d, ok := o.Value.(*pdf0.Dictionary)
			if !ok {
				continue
			}
			if b, ok := d.Get("BaseFont").(pdf0.Name); ok {
				bases++
				if string(b) != u.PostScriptName {
					t.Errorf("%s: the PDF refers to the font as /%s, FontUsage reports %q", tc.label, b, u.PostScriptName)
				}
			}
		}
		if bases == 0 {
			t.Errorf("%s: no /BaseFont in the PDF", tc.label)
		}
	}
}
