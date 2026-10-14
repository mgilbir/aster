package vega

import (
	"image"
	"math/rand"
	"testing"
)

// TestMarkSamplesMatchesEveryPixel checks markSamples, which reads only the
// ink rectangle and steps through the mask columns by counting, against
// testing every sample of the image for every mask pixel.
func TestMarkSamplesMatchesEveryPixel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := range 2000 {
		ss := 1 + rng.Intn(4)
		w, h := 1+rng.Intn(40), 1+rng.Intn(40)
		// The canvas may be larger than the mask (its size is rounded up).
		img := image.NewRGBA(image.Rect(0, 0, w*ss+rng.Intn(3), h*ss+rng.Intn(3)))
		b := img.Rect
		var ink image.Rectangle
		for range rng.Intn(12) {
			x, y := rng.Intn(b.Dx()), rng.Intn(b.Dy())
			img.Pix[y*img.Stride+x*4+3] = uint8(1 + rng.Intn(255))
			ink = ink.Union(image.Rect(x, y, x+1, y+1))
		}
		// The ink rectangle may be larger than the ink, and pixels may be
		// coloured with zero alpha.
		ink = ink.Union(image.Rect(rng.Intn(b.Dx()), rng.Intn(b.Dy()), rng.Intn(b.Dx()+1), rng.Intn(b.Dy()+1)).Canon())
		img.Pix[rng.Intn(len(img.Pix)/4)*4] = 255

		want := map[[2]int]bool{}
		for y := 0; y < b.Dy() && y/ss < h; y++ {
			for x := 0; x < b.Dx() && x/ss < w; x++ {
				if img.Pix[y*img.Stride+x*4+3] != 0 {
					want[[2]int{x / ss, y / ss}] = true
				}
			}
		}
		got := map[[2]int]bool{}
		markSamples(img, ink, ss, w, h, func(x, y int) {
			if x < 0 || y < 0 || x >= w || y >= h {
				t.Fatalf("case %d: set(%d, %d) outside the %dx%d mask", i, x, y, w, h)
			}
			got[[2]int{x, y}] = true
		}, func() {})
		if len(got) != len(want) {
			t.Fatalf("case %d (ss %d, %dx%d, ink %v): %d pixels set, want %d", i, ss, w, h, ink, len(got), len(want))
		}
		for p := range want {
			if !got[p] {
				t.Fatalf("case %d (ss %d, %dx%d, ink %v): pixel %v not set", i, ss, w, h, ink, p)
			}
		}
	}
}
