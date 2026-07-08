package resvg_test

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"image/png"
	"testing"

	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
)

// serifFaces returns the four embedded Liberation Serif faces as resvg fonts.
func serifFaces() []resvg.Font {
	return []resvg.Font{
		{Data: liberation.SerifRegular},
		{Data: liberation.SerifBold},
		{Data: liberation.SerifItalic},
		{Data: liberation.SerifBoldItalic},
	}
}

// textSVG renders the word "Serif" as black text in the given CSS family on a
// white background, sized so the glyphs cover a meaningful pixel area.
func textSVG(family string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="220" height="60">`+
		`<rect width="220" height="60" fill="white"/>`+
		`<text x="5" y="40" font-size="32" font-family="%s" fill="black">Serif</text></svg>`, family)
}

func countDarkPixels(t *testing.T, pngBytes []byte) int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// 16-bit channels; treat clearly non-white pixels as glyph ink.
			if r < 0x8000 && g < 0x8000 && bl < 0x8000 {
				n++
			}
		}
	}
	return n
}

// The generic "serif" family must actually rasterize. With the Liberation Serif
// faces loaded and FamilyMapping.Serif wired, serif text is drawn; with no serif
// font loaded and no serif mapping (the pre-fix state), resvg has nothing to
// resolve "serif" to and drops the glyphs. Asserting the wired render produces
// ink while the bare render does not proves the serif setter is honored end to
// end (the wasm export + the Go binding + the family mapping).
func TestResvgSerifIsRendered(t *testing.T) {
	ctx := context.Background()

	wired, err := resvg.New(ctx, serifFaces(), resvg.FamilyMapping{Serif: "Liberation Serif"})
	if err != nil {
		t.Fatalf("New (wired): %v", err)
	}
	defer func() { _ = wired.Close(ctx) }()

	wiredPNG, err := wired.Render(ctx, []byte(textSVG("serif")), 1.0)
	if err != nil {
		t.Fatalf("Render (wired): %v", err)
	}
	if ink := countDarkPixels(t, wiredPNG); ink == 0 {
		t.Fatalf("serif text produced no ink with the serif face wired in")
	}

	bare, err := resvg.New(ctx, nil, resvg.FamilyMapping{})
	if err != nil {
		t.Fatalf("New (bare): %v", err)
	}
	defer func() { _ = bare.Close(ctx) }()

	barePNG, err := bare.Render(ctx, []byte(textSVG("serif")), 1.0)
	if err != nil {
		t.Fatalf("Render (bare): %v", err)
	}
	if ink := countDarkPixels(t, barePNG); ink != 0 {
		t.Fatalf("expected no serif ink without a serif font loaded, got %d dark pixels", ink)
	}
}

// FamilyMapping.Serif must select the serif face specifically, not merely any
// loaded font: with both Liberation Sans and Liberation Serif loaded and mapped,
// the same word rendered as "serif" and as "sans-serif" must differ pixel-wise
// (the two typefaces draw distinct glyph outlines).
func TestResvgSerifDiffersFromSans(t *testing.T) {
	ctx := context.Background()

	fonts := append(serifFaces(),
		resvg.Font{Data: liberation.SansRegular},
		resvg.Font{Data: liberation.SansBold},
		resvg.Font{Data: liberation.SansItalic},
		resvg.Font{Data: liberation.SansBoldItalic},
	)
	r, err := resvg.New(ctx, fonts, resvg.FamilyMapping{
		SansSerif: "Liberation Sans",
		Serif:     "Liberation Serif",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close(ctx) }()

	serifPNG, err := r.Render(ctx, []byte(textSVG("serif")), 1.0)
	if err != nil {
		t.Fatalf("Render serif: %v", err)
	}
	sansPNG, err := r.Render(ctx, []byte(textSVG("sans-serif")), 1.0)
	if err != nil {
		t.Fatalf("Render sans: %v", err)
	}

	if pixelDiff(t, serifPNG, sansPNG) == 0 {
		t.Fatalf("serif and sans-serif rendered identically — serif face not selected")
	}
}

func pixelDiff(t *testing.T, a, b []byte) int {
	t.Helper()
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatalf("decode a: %v", err)
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode b: %v", err)
	}
	if ia.Bounds() != ib.Bounds() {
		return 1 // different dimensions already count as a difference
	}
	bounds := ia.Bounds()
	diff := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !sameColor(ia.At(x, y), ib.At(x, y)) {
				diff++
			}
		}
	}
	return diff
}

func sameColor(c1, c2 color.Color) bool {
	r1, g1, b1, a1 := c1.RGBA()
	r2, g2, b2, a2 := c2.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}
