package aster_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// One image pixel bound holds for every stage that decodes an image: with
// WithMemoryLimit(16 MiB) an image is at most 1 Mi pixels, and an embedded
// 1100×1000 PNG (1.1 M pixels, whose decoding the 16 MiB ledger would
// still admit) is refused by PNG and PDF output alike, by that bound, rather
// than drawn in one and refused in the other.
func TestImagePixelLimitIsEveryStages(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1100, 1000))); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	spec := []byte(`{"width":40,"height":40,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"` + uri + `"},"width":{"value":20},"height":{"value":20}}}}]}`)
	c, err := aster.New(aster.WithMemoryLimit(16 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.VegaToPNG(spec); !errors.Is(err, aster.ErrLimit) || !strings.Contains(err.Error(), "image dimensions") {
		t.Errorf("PNG: err = %v, want the pixel bound", err)
	}
	if _, err := c.VegaToPDF(spec); !errors.Is(err, aster.ErrLimit) || !strings.Contains(err.Error(), "image dimensions") {
		t.Errorf("PDF: err = %v, want the pixel bound", err)
	}
	// Within the bound, both draw it.
	small, err := aster.New(aster.WithMemoryLimit(64 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	if _, err := small.VegaToPNG(spec); err != nil {
		t.Errorf("PNG within the bound: %v", err)
	}
	if _, err := small.VegaToPDF(spec); err != nil {
		t.Errorf("PDF within the bound: %v", err)
	}
}
