package aster_test

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// The ToImage calls are the PNG calls minus the encode. The image they
// return is the image the PNG encodes, pixel for pixel, which is the whole
// point: a caller that composites the image itself gets exactly what a
// caller that writes a PNG would have decoded.

var toImageSpec = []byte(`{
    "data": {"values": [{"a": "A", "b": 28}, {"a": "B", "b": 55}, {"a": "C", "b": 43}]},
    "mark": "bar",
    "encoding": {
        "x": {"field": "a", "type": "nominal"},
        "y": {"field": "b", "type": "quantitative"}
    }
}`)

func newToImageConverter(t *testing.T) *aster.Converter {
	t.Helper()
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// sameAsPNG fails unless img is, bounds and every pixel, what pngBytes
// decodes to.
func sameAsPNG(t *testing.T, img *image.NRGBA, pngBytes []byte) {
	t.Helper()
	if img == nil || img.Bounds().Empty() {
		t.Fatal("empty image")
	}
	dec, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decoding the PNG: %v", err)
	}
	if dec.Bounds() != img.Bounds() {
		t.Fatalf("bounds differ: image %v, png %v", img.Bounds(), dec.Bounds())
	}
	for y := dec.Bounds().Min.Y; y < dec.Bounds().Max.Y; y++ {
		for x := dec.Bounds().Min.X; x < dec.Bounds().Max.X; x++ {
			r1, g1, b1, a1 := img.At(x, y).RGBA()
			r2, g2, b2, a2 := dec.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				t.Fatalf("pixels differ at (%d,%d): image %v, png %v", x, y,
					[4]uint32{r1, g1, b1, a1}, [4]uint32{r2, g2, b2, a2})
			}
		}
	}
}

func TestVegaLiteToImageMatchesPNG(t *testing.T) {
	c := newToImageConverter(t)
	img, err := c.VegaLiteToImage(toImageSpec, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaLiteToImage: %v", err)
	}
	out, err := c.VegaLiteToPNG(toImageSpec, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
	}
	sameAsPNG(t, img, out)
}

func TestVegaToImageMatchesPNG(t *testing.T) {
	c := newToImageConverter(t)
	vg, err := c.VegaLiteToVega(toImageSpec)
	if err != nil {
		t.Fatalf("VegaLiteToVega: %v", err)
	}
	img, err := c.VegaToImage(vg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaToImage: %v", err)
	}
	out, err := c.VegaToPNG(vg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaToPNG: %v", err)
	}
	sameAsPNG(t, img, out)
}

func TestSVGToImageMatchesPNG(t *testing.T) {
	c := newToImageConverter(t)
	svg, err := c.VegaLiteToSVG(toImageSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	img, err := c.SVGToImage(svg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("SVGToImage: %v", err)
	}
	out, err := c.SVGToPNG(svg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("SVGToPNG: %v", err)
	}
	sameAsPNG(t, img, out)
}

// An image call encodes nothing, so the options about the encoding fail it
// rather than being silently dropped.
func TestToImageRefusesEncodingOptions(t *testing.T) {
	c := newToImageConverter(t)
	svg, err := c.VegaLiteToSVG(toImageSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	for name, opt := range map[string]aster.PNGOption{
		"WithQuantizePNG": aster.WithQuantizePNG(16),
		"WithRecodePNG":   aster.WithRecodePNG(),
	} {
		calls := map[string]func() (*image.NRGBA, error){
			"VegaLiteToImage": func() (*image.NRGBA, error) { return c.VegaLiteToImage(toImageSpec, opt) },
			"VegaToImage":     func() (*image.NRGBA, error) { return c.VegaToImage([]byte(`{}`), opt) },
			"SVGToImage":      func() (*image.NRGBA, error) { return c.SVGToImage(svg, opt) },
		}
		for call, f := range calls {
			img, err := f()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s with %s: image %v, error %v; want an error naming the option", call, name, img != nil, err)
			}
		}
	}
}

// An image call's errors name its own stage.
func TestToImageErrorNamesTheImageStage(t *testing.T) {
	c := newToImageConverter(t)
	_, err := c.SVGToImage("<svg")
	if err == nil {
		t.Fatal("SVGToImage of malformed SVG succeeded")
	}
	if msg := err.Error(); !strings.Contains(msg, "rendering image") || strings.Contains(msg, "PNG") {
		t.Errorf("error %q, want one naming the image stage and no PNG", msg)
	}
}
