package aster_test

import (
	"bytes"
	"image/png"
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

func TestVegaLiteToImageMatchesPNG(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	img, err := c.VegaLiteToImage(toImageSpec, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaLiteToImage: %v", err)
	}
	if img == nil || img.Bounds().Empty() {
		t.Fatal("VegaLiteToImage returned an empty image")
	}

	pngBytes, err := c.VegaLiteToPNG(toImageSpec, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
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

func TestVegaToImageMatchesPNG(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	vg, err := c.VegaLiteToVega(toImageSpec)
	if err != nil {
		t.Fatalf("VegaLiteToVega: %v", err)
	}

	img, err := c.VegaToImage(vg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaToImage: %v", err)
	}
	pngBytes, err := c.VegaToPNG(vg, aster.WithScale(2))
	if err != nil {
		t.Fatalf("VegaToPNG: %v", err)
	}
	dec, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decoding the PNG: %v", err)
	}
	if dec.Bounds() != img.Bounds() {
		t.Fatalf("bounds differ: image %v, png %v", img.Bounds(), dec.Bounds())
	}
}
