package svgpdf

import (
	"bytes"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/text"
)

func colourShaper(t *testing.T) *text.Measurer {
	t.Helper()
	var opts []text.Option
	for _, name := range []string{"ColourTest", "SbixTest"} {
		data, err := os.ReadFile("../../testdata/colourfonts/" + name + ".ttf")
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, text.WithFont(name, data))
	}
	m, err := text.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// colourPDF converts one line of text in family to a PDF and returns its
// content stream and the number of images it draws.
func colourPDF(t *testing.T, family, s, attrs string, mode TextMode) (string, int) {
	content, _ := colourPDFBytes(t, family, s, attrs, mode)
	return content, imagesDrawn(content)
}

// imagesDrawn counts the different image XObjects a content stream draws.
func imagesDrawn(content string) int { return xobjectsDrawn(content, "/Im") }

// xobjectsDrawn counts the different XObjects named with prefix a content
// stream draws.
func xobjectsDrawn(content, prefix string) int {
	seen := map[string]bool{}
	f := strings.Fields(content)
	for i := 1; i < len(f); i++ {
		if f[i] == "Do" && strings.HasPrefix(f[i-1], prefix) {
			seen[f[i-1]] = true
		}
	}
	return len(seen)
}

func colourPDFBytes(t *testing.T, family, s, attrs string, mode TextMode) (string, []byte) {
	t.Helper()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100">` +
		`<text transform="translate(10,80)" font-family="` + family + `" font-size="60" fill="#00aa88"` + attrs + `>` + s + `</text></svg>`
	pdf, err := Convert(svg, colourShaper(t), Options{Text: mode})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	return contentStreamOf(t, doc), pdf
}

func TestColourGlyphsVector(t *testing.T) {
	// COLRv0 layers, a solid, an opaque padded linear gradient, and a
	// rotated layer in the foreground are all PDF can draw: paths under
	// clips, and a shading.
	content, pdf := colourPDFBytes(t, "ColourTest", "\U0001F534\U0001F7E2\U0001F308\U0001F600", "", TextEmbed)
	if n := imagesDrawn(content); n != 0 {
		t.Errorf("%d images, want none", n)
	}
	for _, want := range []string{
		" sh\n",              // the linear gradient
		"\nW\nn\n",           // a glyph's clip
		"0 0.6667 0.5333 rg", // the foreground, the fill
		"/Span <</ActualText <FEFFD83DDD34D83DDFE2D83CDF08D83DDE00>>> BDC", "EMC",
		"3 Tr", // the run as invisible text
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content stream has no %q", want)
		}
	}
	// The foreground at the font's 0.8.
	if !bytes.Contains(pdf, []byte("/ca 0.8")) {
		t.Error("no ExtGState with the foreground's alpha, 0.8")
	}
}

func TestColourGlyphsImage(t *testing.T) {
	// A bitmap glyph is its image.
	if _, images := colourPDF(t, "SbixTest", "\U0001F600", "", TextEmbed); images != 1 {
		t.Errorf("sbix: %d images, want one", images)
	}
	// What PDF cannot composite, Xor and Plus, sends a glyph to an image.
	for _, m := range []shape.CompositeMode{shape.CompositeXor, shape.CompositePlus} {
		var c colourCheck
		c.PushGroup()
		c.PushGroup()
		c.PopGroup(m)
		c.PopGroup(shape.CompositeSrcOver)
		if c.vector() {
			t.Errorf("mode %d: drawn as vectors", m)
		}
	}
	// So does a Porter-Duff operator onto a backdrop with a clip open
	// around the source.
	var c colourCheck
	c.PushGroup()
	c.PushClipRect(shape.Rect{XMax: 1})
	c.PushGroup()
	c.PopGroup(shape.CompositeSrcIn)
	c.PopClip()
	c.PopGroup(shape.CompositeSrcOver)
	if c.vector() {
		t.Error("SrcIn under an open clip: drawn as vectors")
	}
	for _, c := range []struct {
		size float64
		want int
	}{{10, 256}, {60, 480}, {500, 1024}} {
		if got := colourImagePPEM(c.size); got != c.want {
			t.Errorf("a %vpt glyph's image at %d pixels per em, want %d", c.size, got, c.want)
		}
	}
}

func TestColourGlyphsGradients(t *testing.T) {
	for _, c := range []struct {
		what, s string
		want    []string // in the PDF
	}{
		{"sweep", "\U0001F300", []string{"/ShadingType 4", "/BitsPerFlag 8"}},
		// A repeated radial gradient's function runs over the periods the
		// glyph needs, stitching its colour line once a period.
		{"repeated radial", "\U0001F31E", []string{"/ShadingType 3", "/Domain [", "/FunctionType 3"}},
		// A translucent stop: the colour under a soft mask of the alpha,
		// the same gradient in grey.
		{"translucent, reflected", "\U0001F52E", []string{"/S /Luminosity", "/ColorSpace /DeviceGray"}},
		{"translucent, radial", "\U0001F4A7", []string{"/S /Luminosity", "/ColorSpace /DeviceGray"}},
	} {
		content, pdf := colourPDFBytes(t, "ColourTest", c.s, "", TextEmbed)
		if n := imagesDrawn(content); n != 0 {
			t.Errorf("%s: %d images, want none", c.what, n)
		}
		for _, want := range c.want {
			if !bytes.Contains(pdf, []byte(want)) {
				t.Errorf("%s: no %q", c.what, want)
			}
		}
	}
}

func TestColourGlyphsComposite(t *testing.T) {
	for _, c := range []struct {
		what, s, attrs string
		forms          int      // drawn with Do; a soft mask's is not
		want           []string // in the PDF
	}{
		// The source, a form, blended onto the backdrop drawn as it is.
		{"Multiply", "\U0001F4A0", "", 1, []string{"/BM /Multiply"}},
		{"Luminosity", "\U0001F4A1", "", 1, []string{"/BM /Luminosity"}},
		// The source masked by the backdrop's alpha.
		{"SrcIn", "\U0001F3A8", "", 1, []string{"/SMask", "/S /Alpha", "/Group"}},
		// A translucent glyph is one form, drawn at the text's alpha, so that
		// its square is not seen through its triangle.
		{"translucent layers", "\U0001F534", ` fill-opacity="0.5"`, 1, []string{"/ca 0.5"}},
	} {
		content, pdf := colourPDFBytes(t, "ColourTest", c.s, c.attrs, TextEmbed)
		if n := imagesDrawn(content); n != 0 {
			t.Errorf("%s: %d images, want none", c.what, n)
		}
		if n := xobjectsDrawn(content, "/Fm"); n != c.forms {
			t.Errorf("%s: %d forms drawn, want %d", c.what, n, c.forms)
		}
		for _, want := range c.want {
			if !bytes.Contains(pdf, []byte(want)) {
				t.Errorf("%s: no %q", c.what, want)
			}
		}
	}
}

// TestColourGlyphsInvertedMask composites with SrcOut: the source masked by
// the inverse of the backdrop's alpha.
func TestColourGlyphsInvertedMask(t *testing.T) {
	r := &renderer{w: newContentWriter()}
	p := &pdfPainter{r: r, upem: 1000, box: shape.Rect{XMax: 1000, YMax: 1000}, m: []Matrix{Identity()}}
	p.beginGroup()
	p.PushClipRect(shape.Rect{XMax: 500, YMax: 500})
	p.Solid(shape.Color{R: 255, A: 255}, false)
	p.PopClip()
	p.PushGroup()
	p.PushClipRect(shape.Rect{XMax: 1000, YMax: 1000})
	p.Solid(shape.Color{B: 255, A: 255}, false)
	p.PopClip()
	p.PopGroup(shape.CompositeSrcOut)
	content := string(p.endGroup())
	if len(r.forms) != 2 || !strings.Contains(content, "/Fm0 Do") {
		t.Fatalf("forms %d, content\n%s", len(r.forms), content)
	}
	if gs := r.w.gsNames; len(gs) != 1 || gs[0].mask != "Fm1" || !gs[0].maskInvert {
		t.Errorf("ExtGStates %+v, want Fm1's alpha inverted", gs)
	}
}

func TestColourGlyphsTextModes(t *testing.T) {
	// Outlines writes no text: no font, no ActualText.
	content, _ := colourPDF(t, "ColourTest", "\U0001F534A", "", TextOutlines)
	for _, unwanted := range []string{"ActualText", " Tr\n", " Tf\n"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("TextOutlines content has %q", unwanted)
		}
	}
	// Named writes the text, invisible, under ActualText, as Embed does.
	content, _ = colourPDF(t, "ColourTest", "\U0001F534A", "", TextNamed)
	for _, want := range []string{"/Span <</ActualText <FEFFD83DDD340041>>> BDC", "3 Tr", " Tf\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("TextNamed content has no %q", want)
		}
	}
}

func TestColourGlyphsShareResources(t *testing.T) {
	// A glyph drawn many times writes its shadings once.
	pdf, err := Convert(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="100">`+
		`<text transform="translate(0,80)" font-family="ColourTest" font-size="20">`+strings.Repeat("\U0001F308\U0001F300", 10)+`</text></svg>`,
		colourShaper(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The linear gradient's shading and the sweep's mesh, once each.
	if n := bytes.Count(pdf, []byte("/ShadingType")); n != 2 {
		t.Errorf("%d shadings, want two", n)
	}
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n != 0 {
		t.Errorf("%d images, want none", n)
	}
}

// TestColourGlyphFormsInPageSpace pins the forms of colour glyphs to page
// space: Quartz draws nothing of a soft mask whose group's coordinates run
// past the page's own, as a glyph's font units, in the thousands, do.
func TestColourGlyphFormsInPageSpace(t *testing.T) {
	_, pdf := colourPDFBytes(t, "ColourTest", "\U0001F3A8\U0001F4A0", ` fill-opacity="0.5"`, TextEmbed)
	boxes := regexp.MustCompile(`/BBox \[([-0-9. ]+)\]`).FindAllSubmatch(pdf, -1)
	if len(boxes) == 0 {
		t.Fatal("no forms")
	}
	for _, b := range boxes {
		for _, f := range strings.Fields(string(b[1])) {
			// The page is 200 by 100.
			if v, err := strconv.ParseFloat(f, 64); err != nil || v < -1 || v > 201 {
				t.Errorf("form BBox [%s] is not in page space", b[1])
				break
			}
		}
	}
}

// TestColourPainterUnbalanced paints what forme never does, as the fuzzer
// found: the page must stay balanced and nothing may panic.
func TestColourPainterUnbalanced(t *testing.T) {
	newPainter := func() (*renderer, *pdfPainter) {
		r := &renderer{w: newContentWriter(), images: newImageCatalog(nil, Limits{}.withDefaults()), lim: Limits{}.withDefaults()}
		p := &pdfPainter{r: r, upem: 1000, box: shape.Rect{XMax: 1000, YMax: 1000}, m: []Matrix{Identity()}}
		p.beginGroup()
		return r, p
	}
	balanced := func(what, content string) {
		if strings.Count(content, "q\n") != strings.Count(content, "Q\n") {
			t.Errorf("%s: unbalanced\n%s", what, content)
		}
	}
	// A Porter-Duff operator onto a backdrop with a clip open in it.
	_, p := newPainter()
	p.PushClipRect(shape.Rect{XMax: 10, YMax: 10})
	p.PushGroup()
	p.PopGroup(shape.CompositeClear)
	p.unwind()
	balanced("Clear onto an open clip", string(p.endGroup()))
	// Pops with nothing to pop, and pushes never popped.
	_, p = newPainter()
	p.PopClip()
	p.PopTransform()
	p.PopGroup(shape.CompositeSrcIn)
	p.PushGroup()
	p.PushTransform(shape.Transform{XX: 1, YY: 1})
	p.PushClipRect(shape.Rect{XMax: 1, YMax: 1})
	p.unwind()
	balanced("stray pops and unclosed pushes", string(p.endGroup()))
	// An image with no data, and two masks of one size that differ.
	r, p := newPainter()
	p.Image(shape.Image{Format: shape.ImagePNG})
	for _, b := range []byte{0x00, 0xff} {
		p.Image(shape.Image{Format: shape.ImageMask, Data: []byte{b, b, b, b}, Width: 2, Height: 2, Box: shape.Rect{XMax: 1, YMax: 1}, Color: shape.Color{A: 255}})
	}
	p.unwind()
	if n := imagesDrawn(string(p.endGroup())); n != 2 || len(r.images.uses) != 2 {
		t.Errorf("two different masks drawn as %d images", n)
	}
}

// TestColourMaskReturnsToGlyphSpace pins a luminosity mask's transforms: the
// mask is set in the glyph's page space and the gradient painted back in the
// glyph's own, so the two cm around the gs undo one another, wherever on the
// page the glyph is.
func TestColourMaskReturnsToGlyphSpace(t *testing.T) {
	for _, at := range []string{"10,80", "17.3,41.9"} {
		pdf, err := Convert(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><text transform="translate(`+at+
			`)" font-family="ColourTest" font-size="60">`+"\U0001F52E"+`</text></svg>`, colourShaper(t), Options{})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		content := contentStreamOf(t, doc)
		f := strings.Fields(content)
		var ms []Matrix
		for i, tok := range f {
			if tok == "gs" && i >= 8 && f[i-2] == "cm" && strings.HasPrefix(f[i-1], "/GS") {
				ms = append(ms, cmBefore(t, f, i-2))
				if i+7 < len(f) && f[i+7] == "cm" {
					ms = append(ms, cmBefore(t, f, i+7))
				}
			}
		}
		if len(ms) != 2 {
			t.Fatalf("no mask set between two cm in\n%s", content)
		}
		p := ms[0].Mul(ms[1])
		for _, v := range []float64{p.A - 1, p.B, p.C, p.D - 1, p.E, p.F} {
			if math.Abs(v) > 1e-3 {
				t.Errorf("the cm around the mask make %+v, not the identity", p)
				break
			}
		}
	}
}

// cmBefore reads the matrix of the cm at f[i].
func cmBefore(t *testing.T, f []string, i int) Matrix {
	t.Helper()
	var v [6]float64
	for k := range v {
		x, err := strconv.ParseFloat(f[i-6+k], 64)
		if err != nil {
			t.Fatalf("cm operand %q", f[i-6+k])
		}
		v[k] = x
	}
	return Matrix{A: v[0], B: v[1], C: v[2], D: v[3], E: v[4], F: v[5]}
}
