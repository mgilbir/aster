package svgpdf

import (
	"bytes"
	"encoding/binary"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/aster/internal/fontsubset"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
)

// fontDescriptors are the FontDescriptor dictionaries of a PDF.
func fontDescriptors(t *testing.T, pdf []byte) (*pdf0.Document, []*pdf0.Dictionary) {
	t.Helper()
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	var out []*pdf0.Dictionary
	for _, o := range doc.Objects {
		if d, ok := o.Value.(*pdf0.Dictionary); ok && d.Get("Type") == pdf0.Name("FontDescriptor") {
			out = append(out, d)
		}
	}
	return doc, out
}

func streamData(t *testing.T, doc *pdf0.Document, ref pdf0.Object) []byte {
	t.Helper()
	s, ok := doc.Resolve(ref).(*pdf0.Stream)
	if !ok {
		t.Fatalf("not a stream: %v", ref)
	}
	b, err := flateDecode(s.Data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// extracted is pdf's text as pdftotext reads it, or skips t without it.
func extracted(t *testing.T, pdf []byte) string {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler) not installed")
	}
	in := filepath.Join(t.TempDir(), "s.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdftotext", in, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestCIDSetListsTheKeptGlyphs is C114 (audit 2026-10-18): an embedded
// subset's CIDSet lists every glyph its font program holds, the components
// of an accented letter's composite glyph included (ISO 19005-1, 6.3.5).
func TestCIDSetListsTheKeptGlyphs(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><text x="5" y="30" font-family="Liberation Sans" font-size="20">àéîõü Åç</text></svg>`
	pdf, err := Convert(svg, testMeasurer(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, ds := fontDescriptors(t, pdf)
	if len(ds) != 1 {
		t.Fatalf("%d font descriptors", len(ds))
	}
	program := streamData(t, doc, ds[0].Get("FontFile2"))
	set := streamData(t, doc, ds[0].Get("CIDSet"))
	sub, err := fontsubset.Parse(program)
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for g := range sub.NumGlyphs() {
		// A glyph the program holds has an outline: a loca range of its own.
		if !glyphHeld(t, program, g) {
			continue
		}
		held++
		if g/8 >= len(set) || set[g/8]&(0x80>>(g%8)) == 0 {
			t.Errorf("the program holds glyph %d, which the CIDSet does not list", g)
		}
	}
	if held < 10 {
		t.Errorf("only %d glyphs held: no composite was drawn", held)
	}
}

// glyphHeld reports whether glyph g of TrueType program b has outline data.
func glyphHeld(t *testing.T, b []byte, g int) bool {
	t.Helper()
	head, loca := fontsubset.Table(b, "head"), fontsubset.Table(b, "loca")
	if binary.BigEndian.Uint16(head[50:]) == 1 {
		return binary.BigEndian.Uint32(loca[4*g+4:]) > binary.BigEndian.Uint32(loca[4*g:])
	}
	return binary.BigEndian.Uint16(loca[2*g+2:]) > binary.BigEndian.Uint16(loca[2*g:])
}

// withOverrun is Liberation Sans with the instruction length of the glyph
// of r set past the glyph's record (0 for .notdef).
func withOverrun(t *testing.T, r rune) []byte {
	t.Helper()
	ttf := append([]byte(nil), liberation.SansRegular...)
	gid := 0
	if r != 0 {
		f, err := shape.Load(ttf)
		if err != nil {
			t.Fatal(err)
		}
		gid, _ = f.GlyphID(r)
	}
	head, loca, glyf := fontsubset.Table(ttf, "head"), fontsubset.Table(ttf, "loca"), fontsubset.Table(ttf, "glyf")
	var start int
	if binary.BigEndian.Uint16(head[50:]) == 1 {
		start = int(binary.BigEndian.Uint32(loca[4*gid:]))
	} else {
		start = 2 * int(binary.BigEndian.Uint16(loca[2*gid:]))
	}
	g := glyf[start:]
	n := int(int16(binary.BigEndian.Uint16(g)))
	if n <= 0 {
		t.Fatalf("glyph %d is not simple", gid)
	}
	binary.BigEndian.PutUint16(g[10+2*n:], 0xffff)
	return ttf
}

// TestMalformedGlyphFallsBack is C116 (audit 2026-10-18): a glyph that
// cannot be subset (an instruction length past its record) does not fail
// the PDF. A run with such a glyph is drawn as outlines, its text kept as
// invisible text in the font by name; with .notdef so, every glyph of the
// font is. The rest of the font is embedded.
func TestMalformedGlyphFallsBack(t *testing.T) {
	for name, r := range map[string]rune{"a glyph": 'A', ".notdef": 0} {
		t.Run(name, func(t *testing.T) {
			m, err := text.New(text.WithFont("Bad", withOverrun(t, r)))
			if err != nil {
				t.Fatal(err)
			}
			svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><rect width="200" height="40" fill="#fff"/>` +
				`<text x="5" y="30" font-family="Bad" font-size="20">BAD</text><text x="100" y="30" font-family="Bad" font-size="20">bed</text></svg>`
			pdf, err := Convert(svg, m, Options{})
			if err != nil {
				t.Fatalf("a malformed glyph failed the PDF: %v", err)
			}
			verifyPDF(t, pdf)
			if _, err := exec.LookPath("pdftoppm"); err == nil {
				got := popplerImage(t, pdf)
				want, err := raster.Render([]byte(svg), raster.Options{Shaper: raster.NewShaperFromMeasurer(m.Exact())})
				if err != nil {
					t.Fatal(err)
				}
				if d := darkDiff(got, want); d > 0.15 {
					t.Errorf("the PDF's text differs from the PNG's in %.0f%% of its dark pixels", 100*d)
				}
			}
			if got := strings.Fields(extracted(t, pdf)); strings.Join(got, " ") != "BAD bed" {
				t.Errorf("text extracted %q", got)
			}
		})
	}
}

// darkDiff is the share of the pixels dark in either image that are dark in
// only one: what differs in where text is drawn.
func darkDiff(a, b image.Image) float64 {
	dark := func(c interface{ RGBA() (r, g, b, a uint32) }) bool {
		r, g, b := overWhite(c)
		return r+g+b < 3*128
	}
	var either, one int
	bd := b.Bounds()
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			da, db := dark(a.At(x, y)), dark(b.At(x, y))
			if da || db {
				either++
				if da != db {
					one++
				}
			}
		}
	}
	if either == 0 {
		return 1
	}
	return float64(one) / float64(either)
}

// TestCFFTextEmbedded is C43 (audit 2026-10-18): text in a CFF (OpenType
// OTTO) font is embedded in the default mode, as a subset of its CFF
// program (FontFile3, CIDFontType0C), so that it is selectable and
// searchable, and drawn as the PNG draws it. A CID-keyed CFF (most CJK
// OpenType fonts) is addressed by CID, which its subset's charset maps to
// its glyphs; one is tested when the machine has GNU Unifont's.
func TestCFFTextEmbedded(t *testing.T) {
	fonts := map[string]struct {
		path, text string
	}{
		"name-keyed": {"../text/testdata/CFFTest.otf", "0110Q01"},
		"CID-keyed":  {"/usr/share/fonts/opentype/unifont/unifont.otf", "Aé中文Ω"},
	}
	for name, c := range fonts {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(c.path)
			if err != nil {
				t.Skip(err)
			}
			m, err := text.New(text.WithFont("Src", data))
			if err != nil {
				t.Fatal(err)
			}
			// Large: Unifont's strokes are a pixel of its em, which at a
			// small size are all edge, anti-aliased differently.
			svg := `<svg xmlns="http://www.w3.org/2000/svg" width="480" height="80"><rect width="480" height="80" fill="#fff"/>` +
				`<text x="5" y="60" font-family="Src" font-size="64">` + c.text + `</text></svg>`
			pdf, err := Convert(svg, m, Options{})
			if err != nil {
				t.Fatal(err)
			}
			verifyPDF(t, pdf)
			doc, ds := fontDescriptors(t, pdf)
			if len(ds) != 1 {
				t.Fatalf("%d font descriptors, want the CFF font's", len(ds))
			}
			ff, ok := doc.Resolve(ds[0].Get("FontFile3")).(*pdf0.Stream)
			if !ok || ff.Dict.Get("Subtype") != pdf0.Name("CIDFontType0C") {
				t.Fatalf("the CFF font is not embedded as a CIDFontType0C program: %v", ds[0])
			}
			if prog := streamData(t, doc, ds[0].Get("FontFile3")); len(prog) >= len(data) {
				t.Errorf("the CFF program embedded is %d bytes, of a %d byte font: not a subset", len(prog), len(data))
			}
			if _, err := exec.LookPath("pdftoppm"); err == nil {
				got := popplerImage(t, pdf)
				want, err := raster.Render([]byte(svg), raster.Options{Shaper: raster.NewShaperFromMeasurer(m.Exact())})
				if err != nil {
					t.Fatal(err)
				}
				if d := darkDiff(got, want); d > 0.15 {
					t.Errorf("the PDF's text differs from the PNG's in %.0f%% of its dark pixels", 100*d)
				}
			}
			if got := extracted(t, pdf); got != c.text {
				t.Errorf("text extracted %q, want %q", got, c.text)
			}
		})
	}
}
