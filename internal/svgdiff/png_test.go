package svgdiff

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngHref(t *testing.T, img image.Image, level png.CompressionLevel) string {
	t.Helper()
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: level}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return pngDataPrefix + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestComparePNGImages(t *testing.T) {
	mk := func(px ...color.NRGBA) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		for i, c := range px {
			img.SetNRGBA(i%2, i/2, c)
		}
		return img
	}
	base := mk(color.NRGBA{10, 20, 30, 255}, color.NRGBA{40, 50, 60, 128}, color.NRGBA{0, 0, 0, 0}, color.NRGBA{1, 2, 3, 4})
	svg := func(href string) []byte {
		return []byte(`<svg xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="` + href + `" width="2"/></svg>`)
	}
	equal := func(a, b image.Image) bool {
		r, err := Compare(svg(pngHref(t, a, png.NoCompression)), svg(pngHref(t, b, png.BestCompression)), DefaultOptions)
		if err != nil {
			t.Fatal(err)
		}
		return r.Equal
	}
	if !equal(base, base) {
		t.Error("same pixels, different encoding: want equal")
	}
	// Invisible color channels of a fully transparent pixel are not compared.
	hidden := mk(color.NRGBA{10, 20, 30, 255}, color.NRGBA{40, 50, 60, 128}, color.NRGBA{9, 9, 9, 0}, color.NRGBA{1, 2, 3, 4})
	if !equal(base, hidden) {
		t.Error("transparent pixel color: want equal")
	}
	// Partially transparent pixels are compared exactly.
	off := mk(color.NRGBA{10, 20, 30, 255}, color.NRGBA{40, 50, 61, 128}, color.NRGBA{0, 0, 0, 0}, color.NRGBA{1, 2, 3, 4})
	if equal(base, off) {
		t.Error("semi-transparent channel differs: want different")
	}
	alpha := mk(color.NRGBA{10, 20, 30, 255}, color.NRGBA{40, 50, 60, 129}, color.NRGBA{0, 0, 0, 0}, color.NRGBA{1, 2, 3, 4})
	if equal(base, alpha) {
		t.Error("alpha differs: want different")
	}
	if equal(base, image.NewNRGBA(image.Rect(0, 0, 3, 2))) {
		t.Error("size differs: want different")
	}
	// An image that does not decode only equals a byte-identical one.
	bad := pngDataPrefix + "AAAA"
	if r, _ := Compare(svg(bad), svg(pngHref(t, base, png.DefaultCompression)), DefaultOptions); r.Equal {
		t.Error("undecodable image: want different")
	}
	if r, _ := Compare(svg(bad), svg(bad), DefaultOptions); !r.Equal {
		t.Error("identical undecodable image: want equal")
	}
}
