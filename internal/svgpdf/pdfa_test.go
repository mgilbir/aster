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
		"6.2.8":      "/Interpolate true",
		"6.2.11.8":   ".notdef shown",
		"6.3.2":      "a link without the print flag",
		"6.2.4.3":    "DeviceRGB without an output intent",
		"6.2.11.4.2": "an incomplete CIDSet",
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
// groups, links with the print flag, no CIDSet, and .notdef text as
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
		if _, ok := d.Get("CIDSet").(pdf0.IndirectRef); ok {
			t.Errorf("object %d: a font descriptor has a CIDSet", num)
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
