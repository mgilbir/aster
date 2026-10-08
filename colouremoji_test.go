package aster_test

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"regexp"
	"testing"

	"github.com/mgilbir/aster"
)

// emojiSpec is a Vega text mark: an emoji and a letter, in the default font,
// 60 pixels high, its baseline at y 80.
const emojiSpec = `{
  "$schema": "https://vega.github.io/schema/vega/v5.json",
  "width": 200, "height": 100, "padding": 0, "background": "white",
  "marks": [{"type": "text", "encode": {"enter": {
    "x": {"value": 0}, "y": {"value": 80}, "text": {"value": "🔴A"},
    "fontSize": {"value": 60}, "fill": {"value": "#00aa88"}, "baseline": {"value": "alphabetic"}}}}]
}`

func emojiConverter(t *testing.T, colour bool) *aster.Converter {
	t.Helper()
	var opts []aster.Option
	if colour {
		data, err := os.ReadFile("testdata/colourfonts/ColourTest.ttf")
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, aster.WithFont("Colour Test", data))
	}
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestColourEmojiPNG(t *testing.T) {
	for _, c := range []struct {
		colour bool
		at     []image.Point
		want   []color.NRGBA
	}{
		// In colour, the square is red at its left edge and the triangle blue
		// at its middle.
		{true, []image.Point{{8, 40}, {30, 56}}, []color.NRGBA{{230, 26, 26, 255}, {26, 51, 230, 255}}},
		// Without, the emoji is Noto Emoji's monochrome glyph, in the fill
		// (see below).
		{false, nil, nil},
	} {
		out, err := emojiConverter(t, c.colour).VegaToPNG([]byte(emojiSpec))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range c.at {
			got := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
			if !closeTo(got, c.want[i]) {
				t.Errorf("colour font %v: %v at %v, want %v", c.colour, got, p, c.want[i])
			}
		}
		if !c.colour {
			fill := 0
			for y := 0; y < 100; y++ {
				for x := 0; x < 60; x++ {
					if closeTo(color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA), color.NRGBA{0, 170, 136, 255}) {
						fill++
					}
				}
			}
			if fill < 100 || hasRed(img) {
				t.Errorf("monochrome emoji: %d pixels in the fill, red drawn %v", fill, hasRed(img))
			}
		}
	}
}

func closeTo(a, b color.NRGBA) bool {
	d := func(x, y uint8) bool { return int(x)-int(y) < 4 && int(y)-int(x) < 4 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
}

func hasRed(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 < 60 && bl>>8 < 60 {
				return true
			}
		}
	}
	return false
}

func TestColourEmojiPDF(t *testing.T) {
	out, err := emojiConverter(t, true).VegaToPDF([]byte(emojiSpec))
	if err != nil {
		t.Fatal(err)
	}
	content := pdfContent(t, out)
	// The emoji is painted, and written as invisible text whose font maps it
	// back to U+1F534, D83D DD34.
	for _, want := range []string{"<D83DDD34>", "3 Tr", "0.902 0.102 0.102 rg"} {
		if !bytes.Contains(content, []byte(want)) {
			t.Errorf("PDF content has no %q", want)
		}
	}
}

// pdfContent inflates every Flate stream of a PDF, which aster writes for its
// content, and joins them.
func pdfContent(t *testing.T, pdf []byte) []byte {
	t.Helper()
	var all []byte
	re := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, m := range re.FindAllSubmatch(pdf, -1) {
		r, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			continue
		}
		b, err := io.ReadAll(r)
		if err == nil {
			all = append(all, b...)
		}
	}
	return all
}

// TestAppleColorEmoji draws emoji from the system's Apple Color Emoji, where
// there is one, named by the spec: system fonts are no fallback.
func TestAppleColorEmoji(t *testing.T) {
	if _, err := os.Stat("/System/Library/Fonts/Apple Color Emoji.ttc"); err != nil {
		t.Skip("no Apple Color Emoji")
	}
	c, err := aster.New(aster.WithSystemFonts())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec := []byte(`{"width": 200, "height": 100, "padding": 0, "background": "white",
  "marks": [{"type": "text", "encode": {"enter": {"x": {"value": 0}, "y": {"value": 80},
    "text": {"value": "😀🌈"}, "font": {"value": "Apple Color Emoji"}, "fontSize": {"value": 60}}}}]}`)
	out, err := c.VegaToPNG(spec)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// The grinning face is yellow.
	yellow := 0
	for y := 0; y < 100; y++ {
		for x := 0; x < 70; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 > 150 && b>>8 < 100 {
				yellow++
			}
		}
	}
	if yellow < 500 {
		t.Errorf("%d yellow pixels where the grinning face is", yellow)
	}
	pdf, err := c.VegaToPDF(spec)
	if err != nil {
		t.Fatal(err)
	}
	// The text is invisible text in a subset of the font, cut from its
	// outline tables, not its 190 MB of bitmaps, which maps the glyphs back
	// to U+1F600 and U+1F308.
	content := pdfContent(t, pdf)
	for _, want := range []string{"3 Tr", "<D83DDE00>", "<D83CDF08>"} {
		if !bytes.Contains(content, []byte(want)) {
			t.Errorf("the PDF has no %q", want)
		}
	}
	if !bytes.Contains(pdf, []byte("/FontFile2")) {
		t.Error("no font embedded")
	}
	if len(pdf) > 1<<20 {
		t.Errorf("the PDF is %d bytes", len(pdf))
	}
}
