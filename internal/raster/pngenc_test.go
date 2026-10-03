package raster

import (
	"bytes"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"
)

// TestEncodePNGMatchesStdlib: EncodePNG must write image/png's bytes for
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
		got, err := EncodePNG(img)
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
		if _, err := EncodePNG(img); err != nil {
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
