package svgpdf

import (
	"bytes"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/text"
)

// pageLinks reads back the URI and Rect of each Link annotation of pdf.
func pageLinks(t *testing.T, pdf []byte) (uris []string, rects [][4]float64) {
	t.Helper()
	verifyPDF(t, pdf)
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	root := doc.ResolveDict(doc.Trailer.Get("Root"))
	pages := doc.ResolveDict(root.Get("Pages"))
	page := doc.ResolveDict(pages.Get("Kids").(pdf0.Array)[0])
	annots, _ := doc.Resolve(page.Get("Annots")).(pdf0.Array)
	for _, a := range annots {
		d := doc.ResolveDict(a)
		if d.Get("Subtype") != pdf0.Name("Link") {
			t.Fatalf("annotation %v", d.Get("Subtype"))
		}
		act := doc.ResolveDict(d.Get("A"))
		if act.Get("S") != pdf0.Name("URI") {
			t.Fatalf("action %v", act.Get("S"))
		}
		uris = append(uris, string(act.Get("URI").(pdf0.String).Value))
		var r [4]float64
		for i, v := range d.Get("Rect").(pdf0.Array) {
			switch n := v.(type) {
			case pdf0.Real:
				r[i] = float64(n)
			case pdf0.Integer:
				r[i] = float64(n)
			}
		}
		rects = append(rects, r)
	}
	return uris, rects
}

// TestLinks: Vega's <a> around a mark draws the mark and adds a Link
// annotation over it whose URI is the href, exactly, escaped as PDF needs.
func TestLinks(t *testing.T) {
	svg := page(`<g transform="translate(10,5)"><a xlink:href="https://example.com/a?b=(1)&amp;c=\é d"><path d="M0,0h20v10h-20Z" fill="red"/></a></g>`)
	svg = strings.Replace(svg, `<svg `, `<svg xmlns:xlink="http://www.w3.org/1999/xlink" `, 1)
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	uris, rects := pageLinks(t, pdf)
	if len(uris) != 1 || uris[0] != `https://example.com/a?b=(1)&c=\%C3%A9%20d` {
		t.Fatalf("URIs %q", uris)
	}
	// The path's box on the page: x 10..30, y 5..15 from the top of 50.
	if rects[0] != [4]float64{10, 35, 30, 45} {
		t.Errorf("Rect %v", rects[0])
	}
}

// TestLinksUntrusted: SVGToPDF's input is untrusted: a link of any scheme
// but http, https and mailto, or a relative one, draws its contents and no
// annotation.
func TestLinksUntrusted(t *testing.T) {
	for _, href := range []string{"javascript:alert(1)", " JavaScript:alert(1)", "jAvA\tscript:alert(1)", "data:text/html;base64,PHNjcmlwdD4=",
		"file:///etc/passwd", "page.html", "//evil.example/x", ":x", "vbscript:x", "https", ""} {
		pdf, err := Convert(page(`<a href="`+strings.ReplaceAll(href, "\t", "&#9;")+`"><rect width="5" height="5"/></a>`), nil, Options{})
		if err != nil {
			t.Fatalf("%q: %v", href, err)
		}
		if uris, _ := pageLinks(t, pdf); len(uris) != 0 {
			t.Errorf("%q became a link: %q", href, uris)
		}
		if !bytes.Contains(pdf, []byte("/Contents")) {
			t.Fatal("no page content")
		}
	}
	for _, href := range []string{"mailto:a@b.c", "HTTP://X.Y/"} {
		pdf, err := Convert(page(`<a href="`+href+`"><rect width="5" height="5"/></a>`), nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if uris, _ := pageLinks(t, pdf); len(uris) != 1 || uris[0] != href {
			t.Errorf("%q: links %q", href, uris)
		}
	}
}

// TestLinkTextAndClip: a link around text covers the text's em box, and a
// clip the linked mark has does not grow it.
func TestLinkTextAndClip(t *testing.T) {
	m := testShaper(t)
	svg := page(`<defs><clipPath id="c"><rect width="100" height="50"/></clipPath></defs>` +
		`<a href="https://x.y/"><text transform="translate(10,20)" font-size="10px" clip-path="url(#c)">Hi</text></a>`)
	pdf, err := Convert(svg, m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, rects := pageLinks(t, pdf)
	if len(rects) != 1 {
		t.Fatalf("%d links", len(rects))
	}
	r := rects[0]
	if r[0] != 10 || r[1] != 27.5 || r[3] != 40 || !(r[2] > 15 && r[2] < 30) {
		t.Errorf("Rect %v", r)
	}
}

func testShaper(t *testing.T) TextShaper {
	t.Helper()
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	return m
}
