package purego_test

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/mgilbir/aster/purego"
)

const smokeSVG = `<svg xmlns="http://www.w3.org/2000/svg" class="marks" width="120" height="40" viewBox="0 0 120 40"><rect width="120" height="40" fill="white"/><g fill="none" stroke-miterlimit="10" transform="translate(5,5)"><path d="M0,0h40v30h-40Z" fill="#4c78a8"/><text text-anchor="start" transform="translate(50,20)" font-family="sans-serif" font-size="12px" fill="#000">Hi</text></g></svg>`

func TestSVGToPNG(t *testing.T) {
	c, err := purego.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.SVGToPNG(smokeSVG, purego.WithScale(2))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 240 || b.Dy() != 80 {
		t.Fatalf("size %v, want 240x80", b)
	}
	// Inside the bar: the fill colour.
	r, g, bl, _ := img.At(20, 30).RGBA()
	if r>>8 != 0x4c || g>>8 != 0x78 || bl>>8 != 0xa8 {
		t.Errorf("bar pixel = %02x%02x%02x, want 4c78a8", r>>8, g>>8, bl>>8)
	}
	if _, err := c.SVGToPNG(smokeSVG, purego.WithQuantizePNG(16)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SVGToPNG(smokeSVG, purego.WithScale(0)); err == nil {
		t.Error("scale 0 should fail")
	}
}

func TestSVGToPDF(t *testing.T) {
	c, err := purego.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.SVGToPDF(smokeSVG)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %.20q", out)
	}
}

func TestNewRejectsUnknownOptions(t *testing.T) {
	if _, err := purego.New(purego.WithVegaLiteVersion("9.9")); err == nil {
		t.Error("unsupported Vega-Lite version accepted")
	}
	if _, err := purego.New(purego.WithTimezone("Not/AZone")); err == nil {
		t.Error("unknown timezone accepted")
	}
	c, err := purego.New(purego.WithTimezone("Europe/Amsterdam"))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := c.SVGToPNG(smokeSVG); err == nil {
		t.Error("closed converter rendered")
	}
}
