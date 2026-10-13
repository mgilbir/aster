package svgpdf

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/text"
)

// pageContent converts svg with m (nil for no text) in mode and returns the
// page's content stream, after verifyPDF.
func pageContent(t *testing.T, svg string, m TextShaper, mode TextMode) string {
	t.Helper()
	pdf, err := Convert(svg, m, Options{Text: mode})
	if err != nil {
		t.Fatalf("Convert: %v\n%s", err, svg)
	}
	verifyPDF(t, pdf)
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	return contentStreamOf(t, doc)
}

func page(body string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50">` + body + `</svg>`
}

// TestInvalidPresentationValuesIgnored is SVG's rule for a presentation
// attribute whose value does not parse: it is ignored, as if absent, and the
// inherited or initial value applies (C33, C57, X2). The element is drawn as
// the same element without the attribute is.
func TestInvalidPresentationValuesIgnored(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, bad, good string }{
		{"colour", `<rect width="5" height="5" fill="notacolor"/>`, `<rect width="5" height="5"/>`},
		{"inherited colour", `<g fill="red"><rect width="5" height="5" fill="notacolor"/></g>`, `<g fill="red"><rect width="5" height="5"/></g>`},
		{"stroke colour", `<rect width="5" height="5" stroke="#12"/>`, `<rect width="5" height="5"/>`},
		{"opacity", `<rect width="5" height="5" opacity="half"/>`, `<rect width="5" height="5"/>`},
		{"NaN opacity", `<rect width="5" height="5" fill-opacity="NaN"/>`, `<rect width="5" height="5"/>`},
		{"NaN stroke width", `<rect width="5" height="5" stroke="red" stroke-width="NaN"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"Inf stroke width", `<rect width="5" height="5" stroke="red" stroke-width="Infinity"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"negative stroke width", `<rect width="5" height="5" stroke="red" stroke-width="-2"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"NaN miter limit", `<rect width="5" height="5" stroke="red" stroke-miterlimit="NaN"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"NaN dash", `<rect width="5" height="5" stroke="red" stroke-dasharray="NaN,2"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"NaN dash offset", `<rect width="5" height="5" stroke="red" stroke-dasharray="1,2" stroke-dashoffset="NaN"/>`, `<rect width="5" height="5" stroke="red" stroke-dasharray="1,2"/>`},
		{"line cap", `<rect width="5" height="5" stroke="red" stroke-linecap="Round"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"line join", `<rect width="5" height="5" stroke="red" stroke-linejoin="pointy"/>`, `<rect width="5" height="5" stroke="red"/>`},
		{"fill rule", `<rect width="5" height="5" fill-rule="odd"/>`, `<rect width="5" height="5"/>`},
		{"transform", `<rect width="5" height="5" transform="translate(1&quot; onmouseover=&quot;alert(6),10)"/>`, `<rect width="5" height="5"/>`},
		{"NaN transform", `<rect width="5" height="5" transform="translate(NaN,2)"/>`, `<rect width="5" height="5"/>`},
		{"NaN width", `<rect width="NaN" height="5"/><rect width="5" height="5"/>`, `<rect height="5"/><rect width="5" height="5"/>`},
		{"text anchor", `<text font-size="10px" text-anchor="centre">Hi</text>`, `<text font-size="10px">Hi</text>`},
		{"font size", `<g font-size="12px"><text font-size="-20px">Hi</text></g>`, `<g font-size="12px"><text>Hi</text></g>`},
		{"font weight", `<text font-size="10px" font-weight="heavy">Hi</text>`, `<text font-size="10px">Hi</text>`},
		{"font style", `<text font-size="10px" font-style="slanted">Hi</text>`, `<text font-size="10px">Hi</text>`},
		{"blend", `<rect width="5" height="5" style="mix-blend-mode: sideways;"/>`, `<rect width="5" height="5"/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := pageContent(t, page(tc.bad), m, TextEmbed)
			if good := pageContent(t, page(tc.good), m, TextEmbed); bad != good {
				t.Errorf("drawn differently from the element without it:\n%s\nwant\n%s", bad, good)
			}
		})
	}
}

// TestVegaPresentationConverts is the C33 list of what Vega writes that
// failed the whole PDF: each converts.
func TestVegaPresentationConverts(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"font-variant": `<text font-family="sans-serif" font-size="11px" font-variant="small-caps">Hi</text>`,
		"blend":        `<rect width="5" height="5" fill="red" style="mix-blend-mode: multiply;"/>`,
		"blend text":   `<text font-size="11px" style="mix-blend-mode: screen;">Hi</text>`,
		"currentColor": `<rect width="5" height="5" fill="currentColor"/>`,
		"image style":  `<image width="2" height="2" style="image-rendering: optimizeSpeed; image-rendering: pixelated; mix-blend-mode: darken;" href="data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACwAAAAAAQABAAACAkQBADs="/>`,
		"translucent stops": `<defs><linearGradient id="g" x1="0" x2="1" y1="0" y2="0"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="rgba(255,0,0,0)"/></linearGradient></defs>` +
			`<path d="M0,0L0,40L50,40Z" fill="url(#g)"/>`,
		"stop opacity": `<defs><linearGradient id="g"><stop offset="0" stop-color="red" stop-opacity="0.5"/><stop offset="1" stop-color="blue"/></linearGradient></defs>` +
			`<rect width="50" height="20" fill="url(#g)" stroke="url(#g)"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			pageContent(t, page(body), m, TextEmbed)
		})
	}
}

// TestBlendMode: mix-blend-mode draws the element as a group under the
// blend mode, PDF's ExtGState /BM.
func TestBlendMode(t *testing.T) {
	pdf, err := Convert(page(`<rect width="5" height="5" fill="red" stroke="blue" style="mix-blend-mode: color-dodge"/>`), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)
	if !bytes.Contains(pdf, []byte("/BM /ColorDodge")) {
		t.Error("no /BM /ColorDodge ExtGState")
	}
	content := pageContent(t, page(`<rect width="5" height="5" fill="red" stroke="blue" style="mix-blend-mode: color-dodge"/>`), nil, TextEmbed)
	if !strings.Contains(content, " Do") || strings.Contains(content, " re") {
		t.Errorf("the blended element is not drawn as a group:\n%s", content)
	}
}

// TestTranslucentGradient: a gradient with translucent stops is its colour
// shading under a soft mask of its alpha (C33).
func TestTranslucentGradient(t *testing.T) {
	svg := page(`<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="red" stop-opacity="0"/></linearGradient></defs>` +
		`<rect width="50" height="20" fill="url(#g)"/>`)
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)
	if !bytes.Contains(pdf, []byte("/S /Luminosity")) || !bytes.Contains(pdf, []byte("/DeviceGray")) {
		t.Error("no luminosity soft mask of a grey shading")
	}
	// An opaque gradient has none.
	pdf, err = Convert(strings.ReplaceAll(svg, ` stop-opacity="0"`, ""), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pdf, []byte("/SMask")) {
		t.Error("an opaque gradient is drawn under a soft mask")
	}
}

// TestPathDataUpToError is SVG's rule for path data with an error: the path
// is drawn up to the command where the error is (X12). Upstream writes NaN
// into d for a null value under a transform.
func TestPathDataUpToError(t *testing.T) {
	for _, tc := range []struct{ bad, good string }{
		{"M0,0L10,10L20,NaNL30,30", "M0,0L10,10"},
		{"M0,0L10,10L20", "M0,0L10,10"},
		{"M0,0L10,10 20,20 30", "M0,0L10,10 20,20"},
		{"M0,0L10,10X20,20", "M0,0L10,10"},
		{"M0,0h1e400", "M0,0"},
		{"L10,10", ""},
	} {
		bad := pageContent(t, page(`<rect width="1" height="1"/><path stroke="red" d="`+tc.bad+`"/>`), nil, TextEmbed)
		good := pageContent(t, page(`<rect width="1" height="1"/><path stroke="red" d="`+tc.good+`"/>`), nil, TextEmbed)
		if bad != good {
			t.Errorf("d=%q drawn as\n%s\nwant as d=%q\n%s", tc.bad, bad, tc.good, good)
		}
	}
}

// TestNonFiniteGeometryRefused: a coordinate that overflows to infinity
// fails the conversion; it is never written (C57).
func TestNonFiniteGeometryRefused(t *testing.T) {
	_, err := Convert(page(`<path stroke="red" d="M1e308,0l1e308,0"/>`), nil, Options{})
	if err == nil {
		t.Fatal("a path overflowing to infinity converted")
	}
	if strings.Contains(err.Error(), "internal error") {
		t.Errorf("error: %v", err)
	}
}

// TestErrorsDoNotEchoSpecText: an error names what it refuses within a
// bound, however long the text was (X2).
func TestErrorsDoNotEchoSpecText(t *testing.T) {
	long := strings.Repeat("x", 100000)
	for _, svg := range []string{
		page(`<rect width="5" height="5" ` + long + `="1"/>`),
		page(`<rect width="5" height="5" clip-path="url(#` + long + `)"/>`),
		page(`<rect width="5" height="5" fill="url(#` + long + `)"/>`),
		page(`<rect width="5" height="5" style="` + long + `: 1"/>`),
	} {
		_, err := Convert(svg, nil, Options{})
		if err == nil {
			t.Error("converted")
			continue
		}
		if len(err.Error()) > 300 {
			t.Errorf("a %d byte error message", len(err.Error()))
		}
	}
}

var tfRe = regexp.MustCompile(`/F\d+ (\S+) Tf`)

// TestFontFromParts is C12 and C59 in the PDF: the font is read from the
// element's font attributes, never through a font string.
func TestFontFromParts(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ attrs, size string }{
		{`font-size="12px" font-weight="450"`, "12"},
		{`font-size="12px" font-weight="1000"`, "12"},
		{`font-size="1e+06px"`, "65536"}, // the text package's size bound
		{`font-size="1000000px"`, "65536"},
		{`font-size="9pt"`, "12"},
		{`font-size="0px"`, ""},
		{`font-size="12px" style="font-size: 7px"`, "7"},
	} {
		content := pageContent(t, page(`<text font-family="sans-serif" `+tc.attrs+`>Hello</text>`), m, TextEmbed)
		got := ""
		if s := tfRe.FindStringSubmatch(content); s != nil {
			got = s[1]
		}
		if got != tc.size {
			t.Errorf("%s: Tf %q, want %q", tc.attrs, got, tc.size)
		}
	}
}

// TestTextWhitespace is SVG's whitespace rule (xml:space default), as the
// PNG writer applies it: newlines are dropped, a tab is a space, and spaces
// collapse (C59). No control character reaches the shaper as a .notdef.
func TestTextWhitespace(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	want := pageContent(t, page(`<text font-size="10px">a b</text>`), m, TextOutlines)
	for _, s := range []string{"a\tb", "a \t b", "a\n b", "\n a  b ", "a\r\n b"} {
		if got := pageContent(t, page(`<text font-size="10px">`+s+`</text>`), m, TextOutlines); got != want {
			t.Errorf("%q drawn differently from \"a b\"", s)
		}
	}
}

// TestCDATALinear: text in many CDATA sections is joined in linear time
// (C60). Measured by the bytes the parse allocates, which a quadratic join
// makes gigabytes of: 60,000 sections of 2 bytes copy 3.6 GB.
func TestCDATALinear(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg width="10" height="10"><text font-size="1px">`)
	for range 60000 {
		b.WriteString(`<![CDATA[ab]]>`)
	}
	b.WriteString(`<tspan>x</tspan>`)
	for range 60000 {
		b.WriteString(`ab<tspan/>`)
	}
	b.WriteString(`</text></svg>`)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	root, err := parseSVG(context.Background(), b.String(), Limits{}.withDefaults())
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(root.children[0].text); n != 240000 {
		t.Errorf("text of %d bytes, want 240000", n)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 200<<20 {
		t.Errorf("parsing allocated %d MB", alloc>>20)
	}
}

// TestVegaRadialPattern: the <pattern> Vega wraps a radial gradient in
// paints the gradient over the square of the shape's larger side, centred
// (X8); a pattern of any other form is refused where it is used.
func TestVegaRadialPattern(t *testing.T) {
	defs := `<defs><pattern id="p_g" viewBox="0,0,1,1" width="100%" height="100%" preserveAspectRatio="xMidYMid slice"><rect width="1" height="1" fill="url(#g)"/></pattern>` +
		`<radialGradient id="g" fx="0.5" fy="0.5" fr="0" cx="0.5" cy="0.5" r="0.5"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></radialGradient></defs>`
	content := pageContent(t, page(defs+`<path d="M10,10h150v60h-150Z" fill="url(#p_g)"/>`), nil, TextEmbed)
	if !strings.Contains(content, "150 0 0 150 10 -35 cm") || !strings.Contains(content, " sh") {
		t.Errorf("not painted over the 150 square centred on the box:\n%s", content)
	}
	other := strings.Replace(defs, `width="100%"`, `width="50%"`, 1)
	if _, err := Convert(page(other+`<rect width="5" height="5"/>`), nil, Options{}); err != nil {
		t.Errorf("an unused pattern: %v", err)
	}
	if _, err := Convert(page(other+`<rect width="5" height="5" fill="url(#p_g)"/>`), nil, Options{}); err == nil {
		t.Error("a pattern not of Vega's form converted")
	}
}

// TestToUnicodeClusters is C58: each cluster's text is mapped once, by its
// first glyph in logical order, so that a glyph shared by text in other
// clusters does not extract as theirs; where a glyph would need two texts,
// its run carries its own as ActualText. Read back with pdftotext.
func TestToUnicodeClusters(t *testing.T) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		if os.Getenv("ASTER_POPPLER") == "require" {
			t.Fatal("pdftotext (poppler) required (ASTER_POPPLER=require) but not installed")
		}
		t.Skip("no pdftotext")
	}
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ lines []string }{
		{[]string{"ab שלום cd", "x y"}},
		{[]string{"q́ q", "q"}},
		{[]string{"q", "q́"}},
	} {
		var body strings.Builder
		for i, l := range tc.lines {
			fmt.Fprintf(&body, `<text transform="translate(5,%d)" font-size="10px">%s</text>`, 15+15*i, l)
		}
		pdf, err := Convert(page(body.String()), m, Options{})
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "t.pdf")
		if err := os.WriteFile(f, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(bin, "-raw", f, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		// pdftotext marks right-to-left text with embedding controls.
		got := strings.Fields(strings.NewReplacer("\f", "", "\u202b", "", "\u202c", "", "\u202a", "").Replace(string(out)))
		var want []string
		for _, l := range tc.lines {
			want = append(want, strings.Fields(l)...)
		}
		// pdftotext writes right-to-left words in visual order of their
		// letters or logical; compare as sets of words with letters sorted.
		norm := func(ws []string) string {
			var o []string
			for _, w := range ws {
				r := []rune(w)
				slices.Sort(r)
				o = append(o, string(r))
			}
			slices.Sort(o)
			return strings.Join(o, " ")
		}
		if norm(got) != norm(want) {
			t.Errorf("%q extracts as %q", tc.lines, got)
		}
	}
}

// TestMapRun pins mapRun's rule on made-up runs: a cluster's text runs to
// the next cluster in logical order, whatever the visual order, and only
// its first glyph in logical order maps to it.
func TestMapRun(t *testing.T) {
	r := &renderer{}
	glyphs := func(gc ...int) []text.Glyph {
		var g []text.Glyph
		for i := 0; i < len(gc); i += 2 {
			g = append(g, text.Glyph{GID: gc[i], Cluster: gc[i+1]})
		}
		return g
	}
	for _, tc := range []struct {
		name string
		run  text.Run
		want map[uint16]string
	}{
		{"rtl", text.Run{Glyphs: glyphs(4, 6, 3, 4, 2, 2, 1, 0), Start: 0, End: 8}, map[uint16]string{1: "ab", 2: "cd", 3: "ef", 4: "gh"}},
		{"marks", text.Run{Glyphs: glyphs(1, 2, 2, 2, 3, 5), Start: 2, End: 6}, map[uint16]string{1: "cde", 2: "", 3: "f"}},
		{"rtl marks", text.Run{Glyphs: glyphs(3, 3, 2, 0, 1, 0), Start: 0, End: 4}, map[uint16]string{1: "abc", 2: "", 3: "d"}},
	} {
		f := &pdfFont{toUni: map[uint16]string{}}
		if r.mapRun(f, tc.run, "abcdefgh") {
			t.Errorf("%s: a conflict in a fresh font", tc.name)
		}
		if !maps.Equal(f.toUni, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, f.toUni, tc.want)
		}
		// The same glyphs standing for other text conflict.
		if !r.mapRun(f, text.Run{Glyphs: glyphs(1, 7), Start: 7, End: 8}, "abcdefgh") {
			t.Errorf("%s: glyph 1 as \"h\" does not conflict", tc.name)
		}
	}
}

// TestNonScalingStroke: Vega writes vector-effect="non-scaling-stroke" for a
// path mark with a scale; its stroke is as wide on the page as stated.
func TestNonScalingStroke(t *testing.T) {
	content := pageContent(t, page(`<path vector-effect="non-scaling-stroke" transform="translate(10,10) scale(4,1)" d="M0,0L5,5" stroke="red" stroke-width="2"/>`), nil, TextEmbed)
	if !strings.Contains(content, "2 w") || !strings.Contains(content, "10 40 m") || !strings.Contains(content, "30 35 l") {
		t.Errorf("the stroke is not drawn in page space:\n%s", content)
	}
}
