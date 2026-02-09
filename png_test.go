package aster_test

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"github.com/mgilbir/aster"
)

func TestSVGToPNG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50">
		<rect width="100" height="50" fill="steelblue"/>
	</svg>`

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	data, err := c.SVGToPNG(svg)
	if err != nil {
		t.Fatalf("SVGToPNG: %v", err)
	}

	// Verify PNG header.
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.HasPrefix(data, pngHeader) {
		t.Fatal("output does not have PNG header")
	}

	// Decode and check dimensions.
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 50 {
		t.Errorf("expected 100x50, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestVegaLiteToPNG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	data, err := c.VegaLiteToPNG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPNG: %v", err)
	}

	// Verify valid PNG.
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() < 10 || bounds.Dy() < 10 {
		t.Errorf("unexpected small dimensions: %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestVegaLiteToPNGScale(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	data1x, err := c.VegaLiteToPNG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToPNG scale=1: %v", err)
	}
	data2x, err := c.VegaLiteToPNG(spec, aster.WithScale(2.0))
	if err != nil {
		t.Fatalf("VegaLiteToPNG scale=2: %v", err)
	}

	img1x, err := png.Decode(bytes.NewReader(data1x))
	if err != nil {
		t.Fatalf("png.Decode 1x: %v", err)
	}
	img2x, err := png.Decode(bytes.NewReader(data2x))
	if err != nil {
		t.Fatalf("png.Decode 2x: %v", err)
	}

	b1 := img1x.Bounds()
	b2 := img2x.Bounds()

	if b2.Dx() != b1.Dx()*2 || b2.Dy() != b1.Dy()*2 {
		t.Errorf("expected 2x dimensions: 1x=%dx%d, 2x=%dx%d",
			b1.Dx(), b1.Dy(), b2.Dx(), b2.Dy())
	}
}

func TestSVGToPNGError(t *testing.T) {
	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	_, err = c.SVGToPNG("not valid svg at all")
	if err == nil {
		t.Fatal("expected error for invalid SVG, got nil")
	}
}
