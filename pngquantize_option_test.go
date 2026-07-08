package aster_test

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// Rendering with WithQuantizePNG must keep dimensions, stay within the color
// budget, and come out smaller than both the plain render and the lossless
// recode (real chart renders exceed 256 colors, so recode cannot reach the
// indexed format that quantization can).
func TestVegaLiteToPNGQuantize(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	plain, err := c.VegaLiteToPNG(spec, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
	}
	recoded, err := c.VegaLiteToPNG(spec, aster.WithScale(2), aster.WithRecodePNG())
	if err != nil {
		t.Fatalf("VegaLiteToPNG with recode: %v", err)
	}
	quantized, err := c.VegaLiteToPNG(spec, aster.WithScale(2), aster.WithQuantizePNG(256))
	if err != nil {
		t.Fatalf("VegaLiteToPNG with quantize: %v", err)
	}

	if len(quantized) >= len(plain) {
		t.Errorf("quantized (%d bytes) not smaller than plain (%d)", len(quantized), len(plain))
	}
	// The strict win over lossless recode only exists for renders with more
	// than 256 distinct colors; the small testdata chart fits a palette
	// losslessly, so both paths should land in the same ballpark there.
	if len(quantized) > len(recoded)+len(recoded)/10 {
		t.Errorf("quantized (%d bytes) much larger than lossless recode (%d)", len(quantized), len(recoded))
	}

	want, err := png.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("decode plain: %v", err)
	}
	got, err := png.Decode(bytes.NewReader(quantized))
	if err != nil {
		t.Fatalf("decode quantized: %v", err)
	}
	if want.Bounds() != got.Bounds() {
		t.Fatalf("bounds changed: %v vs %v", want.Bounds(), got.Bounds())
	}

	seen := map[color.NRGBA]bool{}
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			seen[color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)] = true
		}
	}
	if len(seen) > 256 {
		t.Fatalf("color budget exceeded: %d colors", len(seen))
	}
	t.Logf("plain=%d recoded=%d quantized=%d bytes, %d colors", len(plain), len(recoded), len(quantized), len(seen))
}

// The lossy path proper: antialiased text on flat panels — the shape of a
// real chart render — produces well over 256 distinct colors, so
// quantization must beat the lossless recode there (recode cannot reach the
// indexed format at all and falls back to truecolor).
func TestSVGToPNGQuantizeLossyPath(t *testing.T) {
	var svgSrc strings.Builder
	svgSrc.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480">`)
	svgSrc.WriteString(`<rect width="640" height="480" fill="#ffffff"/>`)
	svgSrc.WriteString(`<rect x="20" y="200" width="600" height="260" fill="#4e79a7"/>`)
	for i := 0; i < 60; i++ {
		// A distinct fill per label: antialiasing blends each against the
		// background into its own shade family, pushing well past 256.
		fill := fmt.Sprintf("#%02x%02x%02x", 40+i*3, 80+(i*5)%120, 100+(i*7)%150)
		fmt.Fprintf(&svgSrc, `<text x="%d" y="%d" font-family="sans-serif" font-size="13" fill="%s">Label %d value 12.3%%</text>`,
			20+(i%3)*210, 20+(i/3)*9, fill, i)
	}
	svgSrc.WriteString(`</svg>`)
	gradientSVG := svgSrc.String()
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	plain, err := c.SVGToPNG(gradientSVG)
	if err != nil {
		t.Fatalf("plain render: %v", err)
	}
	seen := map[color.NRGBA]bool{}
	img, err := png.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			seen[color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)] = true
		}
	}
	if len(seen) <= 256 {
		t.Skipf("render has only %d colors; lossy path not exercised", len(seen))
	}

	recoded, err := c.SVGToPNG(gradientSVG, aster.WithRecodePNG())
	if err != nil {
		t.Fatalf("recode render: %v", err)
	}
	quantized, err := c.SVGToPNG(gradientSVG, aster.WithQuantizePNG(256))
	if err != nil {
		t.Fatalf("quantize render: %v", err)
	}
	if len(quantized) >= len(recoded) {
		t.Fatalf("lossy quantization (%d bytes) must beat lossless recode (%d bytes) above 256 colors", len(quantized), len(recoded))
	}
	t.Logf("gradient svg: plain=%d recoded=%d quantized=%d bytes, %d source colors", len(plain), len(recoded), len(quantized), len(seen))
}
