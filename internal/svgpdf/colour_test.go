package svgpdf

import (
	"bytes"
	"os"
	"strings"
	"testing"

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
func imagesDrawn(content string) int {
	seen := map[string]bool{}
	f := strings.Fields(content)
	for i := 1; i < len(f); i++ {
		if f[i] == "Do" {
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
	for _, c := range []struct {
		what, family, s, attrs string
	}{
		{"sweep gradient", "ColourTest", "\U0001F300", ""},
		{"repeated radial gradient", "ColourTest", "\U0001F31E", ""},
		{"SrcIn", "ColourTest", "\U0001F3A8", ""},
		{"Multiply", "ColourTest", "\U0001F4A0", ""},
		{"sbix", "SbixTest", "\U0001F600", ""},
	} {
		content, images := colourPDF(t, c.family, c.s, c.attrs, TextEmbed)
		if images != 1 {
			t.Errorf("%s: %d images, want one", c.what, images)
		}
		if !strings.Contains(content, " Do\n") {
			t.Errorf("%s: no image drawn", c.what)
		}
	}
	// The two layers of a translucent COLRv0 glyph are drawn as one image,
	// not as two paths seen through one another.
	if _, images := colourPDF(t, "ColourTest", "\U0001F534", ` fill-opacity="0.5"`, TextEmbed); images != 1 {
		t.Errorf("translucent layers: %d images, want one", images)
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
	// A glyph drawn many times writes its shading and its image once.
	pdf, err := Convert(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="100">`+
		`<text transform="translate(0,80)" font-family="ColourTest" font-size="20">`+strings.Repeat("\U0001F308\U0001F300", 10)+`</text></svg>`,
		colourShaper(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(pdf, []byte("/ShadingType")); n != 1 {
		t.Errorf("%d shadings, want one", n)
	}
	// The sweep's image and its alpha, a soft mask.
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n != 2 {
		t.Errorf("%d images, want the sweep's and its soft mask", n)
	}
}
