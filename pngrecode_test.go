package aster_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/mgilbir/aster"
)

// Rendering with WithRecodePNG must change only the storage format: the
// decoded pixels have to match the plain render exactly, and a chart (flat
// colors on an opaque background) should come back smaller than the RGBA
// original.
func TestVegaLiteToPNGRecode(t *testing.T) {
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

	if len(recoded) >= len(plain) {
		t.Errorf("recoded PNG (%d bytes) is not smaller than plain render (%d bytes)", len(recoded), len(plain))
	}

	want, err := png.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("decode plain render: %v", err)
	}
	got, err := png.Decode(bytes.NewReader(recoded))
	if err != nil {
		t.Fatalf("decode recoded render: %v", err)
	}
	if want.Bounds() != got.Bounds() {
		t.Fatalf("bounds changed: plain %v, recoded %v", want.Bounds(), got.Bounds())
	}
	assertEqualPixels(t, want, got)
}

func assertEqualPixels(t *testing.T, want, got image.Image) {
	t.Helper()
	for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
		for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
			wr, wg, wb, wa := want.At(x, y).RGBA()
			gr, gg, gb, ga := got.At(x, y).RGBA()
			if wr != gr || wg != gg || wb != gb || wa != ga {
				t.Fatalf("pixel (%d,%d) changed: want %v, got %v", x, y, want.At(x, y), got.At(x, y))
			}
		}
	}
}
