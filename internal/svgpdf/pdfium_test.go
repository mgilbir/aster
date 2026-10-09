package svgpdf

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
)

// pdfium reads a PDF back with PDFium (scripts/pdfium/render.py), at one
// pixel a point, for the tests that check what aster's PDFs draw rather than
// what they say. They run when ASTER_PDFIUM names a Python with pypdfium2
// (scripts/pdfium/requirements.txt), as CI's pdfium job does, and are
// skipped otherwise, or fail with ASTER_PDFIUM=require and no such Python.
func pdfium(t *testing.T, pdf []byte) *image.NRGBA {
	t.Helper()
	python := os.Getenv("ASTER_PDFIUM")
	if python == "" || python == "require" {
		if python == "require" {
			t.Fatal("ASTER_PDFIUM=require: set it to a Python with pypdfium2 instead")
		}
		t.Skip("ASTER_PDFIUM is not set")
	}
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.pdf"), filepath.Join(dir, "out.rgba")
	if err := os.WriteFile(in, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(python, "../../scripts/pdfium/render.py", in, out).CombinedOutput(); err != nil {
		t.Fatalf("PDFium: %v\n%s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var w, h int
	if _, err := fmt.Fscanf(r, "%d %d\n", &w, &h); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if _, err := io.ReadFull(r, img.Pix); err != nil {
		t.Fatal(err)
	}
	return img
}

// glyphCell compares a glyph's cell of the two drawings, PDFium's of the PDF
// and the PNG writer's: the mean of the largest channel difference, and the
// share of pixels more than 48 off.
func glyphCell(a, b image.Image, x0, y0, w, h int) (mean, far float64) {
	var sum, n, nfar int
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			ca := color.NRGBAModel.Convert(a.At(x, y)).(color.NRGBA)
			cb := color.NRGBAModel.Convert(b.At(x, y)).(color.NRGBA)
			d := 0
			for _, p := range [][2]uint8{{ca.R, cb.R}, {ca.G, cb.G}, {ca.B, cb.B}} {
				d = max(d, int(math.Abs(float64(p[0])-float64(p[1]))))
			}
			sum += d
			if d > 48 {
				nfar++
			}
			n++
		}
	}
	return float64(sum) / float64(n), float64(nfar) / float64(n)
}

func TestPDFiumColourGlyphs(t *testing.T) {
	fonts := map[string]string{"ColourTest": "ColourTest.ttf", "SbixTest": "SbixTest.ttf", "SvgTest": "SvgTest.ttf", "Noto": "NotoColorEmojiSubset.ttf"}
	var opts []text.Option
	var rfonts []raster.FontData
	for family, file := range fonts {
		data, err := os.ReadFile("../../testdata/colourfonts/" + file)
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, text.WithFont(family, data))
		rfonts = append(rfonts, raster.FontData{Family: family, Data: data})
	}
	m, err := text.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	type glyph struct {
		s         string
		mean, far float64 // the most they may differ by
	}
	line := func(gs ...glyph) []glyph { return gs }
	g := func(s string) glyph { return glyph{s, 3, 0.02} }
	for _, c := range []struct {
		family  string
		advance float64
		attrs   string
		glyphs  []glyph
	}{
		{"ColourTest", 60, "", line(g("\U0001F534"), g("\U0001F7E2"), g("\U0001F308"),
			// The repeated rings' hard edges alias differently.
			glyph{"\U0001F31E", 15, 0.08},
			// Half-degree wedges against a per-pixel angle.
			glyph{"\U0001F300", 6, 0.03},
			g("\U0001F600"), g("\U0001F3A8"), g("\U0001F4A0"), g("\U0001F4A1"), g("A"),
			glyph{"\U0001F52E", 7, 0.03}, g("\U0001F4A7"), g("\U0001F315"), g("\U0001F311"))},
		// A translucent glyph is a group, whose edge PDFium places a pixel
		// apart.
		{"ColourTest", 60, ` fill-opacity="0.5"`, func() []glyph {
			var gs []glyph
			for _, e := range []string{"\U0001F534", "\U0001F600", "\U0001F3A8", "\U0001F4A0"} {
				gs = append(gs, glyph{e, 6, 0.05})
			}
			return gs
		}()},
		// Bitmaps and SVG glyphs are images, resampled.
		{"SbixTest", 60, "", line(glyph{"\U0001F600", 4, 0.03})},
		{"SvgTest", 60, "", line(glyph{"\U0001F600", 4, 0.03}, glyph{"\U0001F308", 4, 0.03}, glyph{"\U0001F534", 4, 0.03})},
		// Noto's busy art: edges.
		{"Noto", 1275.0 / 1024 * 60, "", func() []glyph {
			var gs []glyph
			for _, e := range []string{"\U0001F600", "\U0001F602", "\U0001F970", "\U0001F308", "\U0001F525", "\U0001F389", "\U0001F984",
				"\U0001F355", "\U0001F3A8", "\U0001F30D", "\U0001F680", "\U0001F49C", "\U0001F1F3\U0001F1F1", "\U0001F469\u200d\U0001F4BB", "\U0001F44D\U0001F3FD"} {
				gs = append(gs, glyph{e, 6, 0.05})
			}
			return gs
		}()},
	} {
		var s strings.Builder
		for _, gl := range c.glyphs {
			s.WriteString(gl.s)
		}
		w := int(math.Ceil(20 + c.advance*float64(len(c.glyphs))))
		svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="80"><rect width="%d" height="80" fill="#eee"/>`+
			`<text transform="translate(10,62)" font-family="%s" font-size="60" fill="#00aa88"%s>%s</text></svg>`, w, w, c.family, c.attrs, s.String())
		pdf, err := Convert(svg, m, Options{})
		if err != nil {
			t.Fatal(err)
		}
		got := pdfium(t, pdf)
		want, err := raster.Render([]byte(svg), raster.Options{Fonts: rfonts})
		if err != nil {
			t.Fatal(err)
		}
		for i, gl := range c.glyphs {
			x0 := 10 + int(float64(i)*c.advance)
			mean, far := glyphCell(got, want, x0, 0, int(c.advance), 80)
			if mean > gl.mean || far > gl.far {
				t.Errorf("%s%s %U: PDFium's drawing of the PDF differs from the PNG by %.2f on average, %.1f%% of pixels far off",
					c.family, c.attrs, []rune(gl.s)[0], mean, 100*far)
			}
		}
	}
}

// TestPDFiumOpenClipComposites reads back Porter-Duff operators onto a
// backdrop, blue, with a clip, its right half, left open around the source,
// a green band: inside the clip the operator's result, outside it the
// backdrop. PDFium is one reader; Quartz, which needs inverted soft masks
// set as invertedMaskForm sets them, was checked by hand.
func TestPDFiumOpenClipComposites(t *testing.T) {
	blue, green, white := color.NRGBA{0, 0, 255, 255}, color.NRGBA{0, 200, 0, 255}, color.NRGBA{255, 255, 255, 255}
	for _, c := range []struct {
		mode shape.CompositeMode
		// in the band and out of it, in the clip (the left half is blue)
		band, out color.NRGBA
	}{
		{shape.CompositeSrcIn, green, white},
		{shape.CompositeSrcOut, white, white},
		{shape.CompositeDestOut, white, blue},
		{shape.CompositeSrcAtop, green, blue},
		{shape.CompositeDestOver, blue, blue},
	} {
		r := &renderer{w: newContentWriter(), images: newImageCatalog(nil, Limits{}.withDefaults()), lim: Limits{}.withDefaults(), pageW: 100, pageH: 100}
		r.w.concat(Matrix{A: 1, D: -1, F: 100})
		r.w.save()
		r.w.concat(Matrix{A: 0.1, D: -0.1, F: 90})
		p := &pdfPainter{r: r, upem: 1000, box: shape.Rect{XMax: 1000, YMax: 800}, m: []Matrix{Identity()}}
		p.setOrigin()
		p.beginGroup()
		p.PushGroup()
		p.PushClipRect(shape.Rect{XMax: 1000, YMax: 800})
		p.Solid(shape.Color{B: 255, A: 255}, false)
		p.PopClip()
		p.PushClipRect(shape.Rect{XMin: 500, XMax: 1000, YMax: 800})
		p.PushGroup()
		p.PushClipRect(shape.Rect{XMax: 1000, YMin: 300, YMax: 500})
		p.Solid(shape.Color{G: 200, A: 255}, false)
		p.PopClip()
		p.PopGroup(c.mode)
		p.PopClip()
		p.PopGroup(shape.CompositeSrcOver)
		p.unwind()
		r.w.buf = append(r.w.buf, p.endGroup()...)
		r.w.restore()
		pdf, err := buildPDF(r.w.stream(), r.w.gsNames, nil, r.images, paintDefs{r.shadings, r.patterns, r.forms}, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		img := pdfium(t, pdf)
		for _, at := range []struct {
			x, y int
			want color.NRGBA
		}{{25, 50, blue}, {25, 20, blue}, {75, 50, c.band}, {75, 20, c.out}} {
			if got := img.NRGBAAt(at.x, at.y); got != at.want {
				t.Errorf("mode %d at (%d, %d): %v, want %v", c.mode, at.x, at.y, got, at.want)
			}
		}
	}
}
