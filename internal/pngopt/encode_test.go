package pngopt

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"testing"
)

// encodeNRGBA is Encode into a fresh buffer.
func encodeNRGBA(img *image.NRGBA) ([]byte, error) {
	var b bytes.Buffer
	err := Encode(context.Background(), &b, img)
	return b.Bytes(), err
}

// TestEncodePNGMatchesStdlib: Encode must write image/png's bytes for
// every kind of image a render can produce.
func TestEncodePNGMatchesStdlib(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	white := func(img *image.NRGBA) {
		for i := range img.Pix {
			img.Pix[i] = 255
		}
	}
	kinds := map[string]func(img *image.NRGBA){
		"noise": func(img *image.NRGBA) {
			for i := range img.Pix {
				img.Pix[i] = byte(rng.IntN(256))
			}
		},
		"blank":       white,
		"transparent": func(img *image.NRGBA) {},
		"gradient": func(img *image.NRGBA) {
			b := img.Bounds()
			for y := 0; y < b.Dy(); y++ {
				for x := 0; x < b.Dx(); x++ {
					o := y*img.Stride + x*4
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(x), byte(y), byte(x+y), 255
				}
			}
		},
		"chart": func(img *image.NRGBA) { // white, repeated rows, a few lines and dots
			white(img)
			b := img.Bounds()
			for y := 0; y < b.Dy(); y++ {
				if y%7 == 0 || rng.IntN(5) == 0 {
					for x := 0; x < b.Dx(); x++ {
						if rng.IntN(9) == 0 {
							o := y*img.Stride + x*4
							img.Pix[o], img.Pix[o+1], img.Pix[o+2] = byte(rng.IntN(256)), 40, 90
						}
					}
				} else if y > 0 {
					copy(img.Pix[y*img.Stride:(y+1)*img.Stride], img.Pix[(y-1)*img.Stride:y*img.Stride])
				}
			}
		},
		"alpha": func(img *image.NRGBA) {
			for i := range img.Pix {
				img.Pix[i] = byte(rng.IntN(4) * 85)
			}
		},
	}
	check := func(label string, img *image.NRGBA) {
		t.Helper()
		var want bytes.Buffer
		if err := png.Encode(&want, img); err != nil {
			t.Fatal(err)
		}
		got, err := encodeNRGBA(img)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("%s: EncodePNG differs from image/png (%d vs %d bytes)", label, len(got), want.Len())
		}
	}
	for name, fill := range kinds {
		for _, dim := range [][2]int{{1, 1}, {3, 5}, {17, 1}, {64, 64}, {301, 129}, {1000, 700}} {
			img := image.NewNRGBA(image.Rect(0, 0, dim[0], dim[1]))
			fill(img)
			check(name, img)
		}
	}
	// A sub-image has a stride wider than its rows.
	big := image.NewNRGBA(image.Rect(0, 0, 50, 40))
	kinds["noise"](big)
	check("sub-image", big.SubImage(image.Rect(5, 5, 30, 30)).(*image.NRGBA))
}

// A chart-like image: white, ruled lines, dots, text-like noise in a band.
func benchImage() *image.NRGBA {
	rng := rand.New(rand.NewPCG(1, 1))
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			if y%50 == 0 || x%80 == 0 || (y > 100 && y < 500 && rng.IntN(12) == 0) {
				o := y*img.Stride + x*4
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = byte(rng.IntN(200)), byte(rng.IntN(200)), byte(rng.IntN(256))
			}
		}
	}
	return img
}

func BenchmarkEncodePNG(b *testing.B) {
	img := benchImage()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeNRGBA(img); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePNGStdlib(b *testing.B) {
	img := benchImage()
	b.ReportAllocs()
	for b.Loop() {
		var buf bytes.Buffer
		buf.Grow(len(img.Pix) / 8)
		if err := png.Encode(&buf, img); err != nil {
			b.Fatal(err)
		}
	}
}

// EncodePaletted writes image/png's bytes for every palette size (each bit
// depth, a partial last byte) and with and without transparent colours.
func TestEncodePalettedMatchesStdlib(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 200, 256} {
		for _, alpha := range []bool{false, true} {
			pal := make(color.Palette, n)
			for i := range pal {
				a := uint8(255)
				if alpha && i%3 == 1 {
					a = uint8(rng.IntN(255))
				}
				pal[i] = color.NRGBA{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), a}
			}
			for _, dim := range [][2]int{{1, 1}, {3, 5}, {9, 2}, {17, 1}, {301, 129}, {700, 400}} {
				img := image.NewPaletted(image.Rect(0, 0, dim[0], dim[1]), pal)
				for i := range img.Pix {
					if rng.IntN(4) == 0 {
						img.Pix[i] = uint8(rng.IntN(n))
					} else if i > 0 {
						img.Pix[i] = img.Pix[i-1]
					}
				}
				var want, got bytes.Buffer
				if err := png.Encode(&want, img); err != nil {
					t.Fatal(err)
				}
				if err := EncodePaletted(context.Background(), &got, img); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Bytes(), want.Bytes()) {
					t.Fatalf("%d colours (alpha %v) %v: EncodePaletted differs from image/png", n, alpha, dim)
				}
			}
		}
	}
}
