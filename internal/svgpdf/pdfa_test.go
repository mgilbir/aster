package svgpdf

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"

	"github.com/mgilbir/aster/internal/text"
)

// embedderRules are the PDF/A-2 and -3 rules of the document as a whole,
// which a chart written for embedding leaves to the document that embeds it:
// the trailer's file identifier and the catalog's XMP metadata (with the
// PDF/A identification in it).
var embedderRules = map[string]bool{
	"6.1.3":   true,
	"6.6.2.1": true,
}

// pdfaLevels are the levels Options.PDFA is for.
var pdfaLevels = []struct {
	name  string
	level pdfa.Level
}{{"2b", pdfa.PDFA2b}, {"2u", pdfa.PDFA2u}, {"3b", pdfa.PDFA3b}}

// pdfaRules is what pdf0's PDF/A validator (veraPDF's rules) finds in pdf at
// level, by rule, with a message of each.
func pdfaRules(t *testing.T, pdf []byte, level pdfa.Level) map[string]string {
	t.Helper()
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range pdf0.ValidatePDFA(doc, level) {
		got[v.Rule] = v.Message
	}
	return got
}

// pdfaFeatures is an SVG with every construct the PDF writer turns into
// something a PDF/A rule is about: translucent paint (ExtGState), a gradient
// with a translucent stop (a soft mask and its transparency group), an
// image with alpha (an SMask) drawn smooth (/Interpolate), a blend mode, a
// link (an annotation), filled and stroked text in an embedded font (a
// CIDSet), and a character no font has (.notdef).
func pdfaFeatures(t *testing.T) string {
	src := dataURL("image/png", pngBytes(t, true))
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

// TestPDFAFeatures: under Options.PDFA, a chart with every construct that
// touches a PDF/A rule passes PDF/A-2b, -2u and -3b but for the embedding
// document's rules, in both modes that embed or outline text. Without it,
// the same chart breaks each rule PDFA is there for, so the input does
// exercise them.
func TestPDFAFeatures(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	svg := pdfaFeatures(t)
	plain, err := Convert(svg, m, Options{Text: TextEmbed})
	if err != nil {
		t.Fatal(err)
	}
	broken := pdfaRules(t, plain, pdfa.PDFA2b)
	for rule, what := range map[string]string{
		"6.2.8":    "/Interpolate true",
		"6.2.11.8": ".notdef shown",
		"6.3.2":    "a link without the print flag",
		"6.2.4.3":  "DeviceRGB without an output intent",
	} {
		if _, ok := broken[rule]; !ok {
			t.Errorf("without PDFA, the chart does not break %s (%s): the test does not exercise it", rule, what)
		}
	}
	for _, mode := range []TextMode{TextEmbed, TextOutlines} {
		pdf, err := Convert(svg, m, Options{Text: mode, PDFA: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, lv := range pdfaLevels {
			for rule, msg := range pdfaRules(t, pdf, lv.level) {
				if !embedderRules[rule] {
					t.Errorf("text mode %d, PDF/A-%s: %s: %s", mode, lv.name, rule, msg)
				}
			}
		}
	}
}

// TestPDFAGoldens: the golden charts' SVGs pass PDF/A-2b, -2u and -3b under
// PDFA but for the embedding document's rules.
func TestPDFAGoldens(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden SVGs: %v", err)
	}
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		svg, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		pdf, err := Convert(string(svg), m, Options{Text: TextEmbed, PDFA: true})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, lv := range pdfaLevels {
			for rule, msg := range pdfaRules(t, pdf, lv.level) {
				if !embedderRules[rule] {
					t.Errorf("%s, PDF/A-%s: %s: %s", filepath.Base(f), lv.name, rule, msg)
				}
			}
		}
	}
}

// TestPDFAStructure pins what PDFA writes, rule by rule: sRGB as DefaultRGB
// and as the blending space of the page's and the forms' transparency
// groups, links with the print flag, and .notdef text as
// ActualText around outlines rather than shown.
func TestPDFAStructure(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(pdfaFeatures(t), m, Options{Text: TextEmbed, PDFA: true})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	var page *pdf0.Dictionary
	icc := 0
	groups := 0
	for _, num := range sortedObjects(doc) {
		obj := doc.Objects[num].Value
		var d *pdf0.Dictionary
		switch v := obj.(type) {
		case *pdf0.Dictionary:
			d = v
		case *pdf0.Stream:
			d = &v.Dict
		default:
			continue
		}
		if n, ok := d.Get("Type").(pdf0.Name); ok && n == "Page" {
			page = d
		}
		if n, ok := d.Get("N").(pdf0.Integer); ok && n == 3 {
			if a, ok := d.Get("Alternate").(pdf0.Name); ok && a == "DeviceRGB" {
				icc = num
			}
		}
		if s, ok := d.Get("S").(pdf0.Name); ok && s == "Transparency" && d != page {
			groups++
			if !isSRGB(d.Get("CS"), icc) {
				t.Errorf("object %d: a transparency group blends in %v, not the sRGB profile", num, d.Get("CS"))
			}
		}
	}
	if icc == 0 {
		t.Fatal("no sRGB ICC profile object")
	}
	if page == nil {
		t.Fatal("no page")
	}
	g, ok := page.Get("Group").(*pdf0.Dictionary)
	if !ok || !isSRGB(g.Get("CS"), icc) {
		t.Errorf("the page's group is %v, want a transparency group in sRGB", page.Get("Group"))
	}
	res := resolveDict(doc, page.Get("Resources"))
	cs := resolveDict(doc, res.Get("ColorSpace"))
	if cs == nil || !isSRGB(cs.Get("DefaultRGB"), icc) {
		t.Errorf("the resources' DefaultRGB is not the sRGB profile")
	}
	annots, _ := page.Get("Annots").(pdf0.Array)
	if len(annots) == 0 {
		t.Fatal("no link annotation")
	}
	for _, a := range annots {
		ad, _ := a.(*pdf0.Dictionary)
		if f, ok := ad.Get("F").(pdf0.Integer); !ok || f&4 == 0 {
			t.Errorf("a link annotation's flags are %v, want print (4)", ad.Get("F"))
		}
	}
	if groups == 0 {
		t.Error("no form transparency group: the gradient's soft mask should make one")
	}
	if !bytes.Contains(contentOf(t, pdf), []byte("/ActualText")) {
		t.Error("the .notdef run is not written as ActualText")
	}
}

// TestPDFAWithoutNamedFonts: PDFA refuses TextNamed, whose fonts are not
// embedded, before doing any work.
func TestPDFAWithoutNamedFonts(t *testing.T) {
	_, err := Convert(imageSVG(), nil, Options{Text: TextNamed, PDFA: true})
	if !errors.Is(err, errPDFANamed) {
		t.Fatalf("PDFA with TextNamed: %v, want errPDFANamed", err)
	}
}

func sortedObjects(doc *pdf0.Document) []int {
	nums := make([]int, 0, len(doc.Objects))
	for n := range doc.Objects {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

func resolveDict(doc *pdf0.Document, o pdf0.Object) *pdf0.Dictionary {
	if r, ok := o.(pdf0.IndirectRef); ok {
		obj := doc.Objects[r.Number]
		if obj == nil {
			return nil
		}
		o = obj.Value
	}
	d, _ := o.(*pdf0.Dictionary)
	return d
}

// isSRGB reports whether cs is [/ICCBased icc].
func isSRGB(cs pdf0.Object, icc int) bool {
	a, ok := cs.(pdf0.Array)
	if !ok || len(a) != 2 {
		return false
	}
	n, ok := a[0].(pdf0.Name)
	r, ok2 := a[1].(pdf0.IndirectRef)
	return ok && ok2 && n == "ICCBased" && r.Number == icc
}

// TestPDFANumbersWithinLimits: a drawing translated far off its page keeps its
// numbers within what a PDF/A file may hold (ISO 19005-2 6.1.13, ISO 32000-1
// Annex C): a whole number past a 32-bit integer is written as a real, and a
// number past the largest real refuses the conversion (errPastPDFALimit),
// where the plain PDF, which has no such rule, still converts.
func TestPDFANumbersWithinLimits(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	page := func(tx string) string {
		return `<svg width="40" height="20"><rect width="9" height="9"/><text transform="translate(` + tx + `,10)" font-size="9px">a</text><path d="M0,0L` + tx + `,1"/></svg>`
	}
	for _, mode := range []TextMode{TextEmbed, TextOutlines} {
		pdf, err := Convert(page("3181179205078"), m, Options{Text: mode, PDFA: true})
		if err != nil {
			t.Fatalf("text mode %d: %v", mode, err)
		}
		for rule, msg := range pdfaRules(t, pdf, pdfa.PDFA2b) {
			if rule == "6.1.13" {
				t.Errorf("text mode %d: PDF/A-2b %s: %s", mode, rule, msg)
			}
		}
		if _, err := Convert(page("1e39"), m, Options{Text: mode, PDFA: true}); !errors.Is(err, errPastPDFALimit) {
			t.Errorf("text mode %d: a coordinate of 1e39 in PDF/A: %v, want errPastPDFALimit", mode, err)
		}
		if _, err := Convert(page("1e39"), m, Options{Text: mode}); err != nil {
			t.Errorf("text mode %d: a coordinate of 1e39 without PDF/A: %v", mode, err)
		}
	}
	// A number of the objects rather than of the drawing: the page's box.
	wide := `<svg width="1e39" height="20"><rect width="9" height="9"/></svg>`
	if _, err := Convert(wide, m, Options{PDFA: true}); !errors.Is(err, errPastPDFALimit) {
		t.Errorf("a page 1e39 wide in PDF/A: %v, want errPastPDFALimit", err)
	}
	if _, err := Convert(wide, m, Options{}); err != nil {
		t.Errorf("a page 1e39 wide without PDF/A: %v", err)
	}
}

// TestPDFAPageSize: a PDF/A page's side is from 3 to 14,400 units (ISO 32000-1
// Annex C, ISO 19005-2 6.1.13), so a drawing outside that fails the PDF/A
// conversion (errPDFAPageSize), found by FuzzConvert with a 1×1 SVG; the plain
// PDF, which has no such rule, still converts, and a drawing at the limits
// converts to a page PDF/A-2b accepts.
func TestPDFAPageSize(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	page := func(w, h string) string {
		return `<svg width="` + w + `" height="` + h + `"><rect width="1" height="1"/></svg>`
	}
	for _, c := range []struct{ w, h string }{{"1", "1"}, {"2.9", "20"}, {"20", "2.9"}, {"14400.5", "20"}, {"20", "15000"}} {
		if _, err := Convert(page(c.w, c.h), m, Options{PDFA: true}); !errors.Is(err, errPDFAPageSize) {
			t.Errorf("a %s×%s page in PDF/A: %v, want errPDFAPageSize", c.w, c.h, err)
		}
		if _, err := Convert(page(c.w, c.h), m, Options{}); err != nil {
			t.Errorf("a %s×%s page without PDF/A: %v", c.w, c.h, err)
		}
	}
	for _, c := range []struct{ w, h string }{{"3", "3"}, {"14400", "14400"}} {
		pdf, err := Convert(page(c.w, c.h), m, Options{PDFA: true})
		if err != nil {
			t.Fatalf("a %s×%s page in PDF/A: %v", c.w, c.h, err)
		}
		for rule, msg := range pdfaRules(t, pdf, pdfa.PDFA2b) {
			if rule == "6.1.13" {
				t.Errorf("a %s×%s page: PDF/A-2b %s: %s", c.w, c.h, rule, msg)
			}
		}
	}
}
