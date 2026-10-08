package raster

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"
)

func pngDataURI(t *testing.T, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// drawImage renders img into a w x h box over white.
func drawImage(t *testing.T, src string, w, h float64, extra string) *image.NRGBA {
	t.Helper()
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%g" height="%g">`+
		`<rect width="%g" height="%g" fill="white"/><image xlink:href="%s" width="%g" height="%g" preserveAspectRatio="none"%s/></svg>`,
		w, h, w, h, src, w, h, extra)
	out, err := Render([]byte(svg), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An image drawn far smaller than it is averages the pixels it covers: one
// pixel wide red and blue stripes, drawn at a quarter, are purple, not red or
// blue as bilinear sampling of the full image picks them.
func TestImageDownscaleAverages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			c := color.NRGBA{R: 255, A: 255}
			if x%2 == 1 {
				c = color.NRGBA{B: 255, A: 255}
			}
			src.Set(x, y, c)
		}
	}
	for _, size := range []float64{16, 10, 5} {
		out := drawImage(t, pngDataURI(t, src), size, size, "")
		for y := 0; y < int(size); y++ {
			for x := 0; x < int(size); x++ {
				c := out.NRGBAAt(x, y)
				if math.Abs(float64(c.R)-float64(c.B)) > 40 || c.R < 90 || c.B < 90 || c.G > 10 {
					t.Fatalf("at %g px, pixel (%d,%d) = %v, want an even purple", size, x, y, c)
				}
			}
		}
	}
}

// Halving averages in premultiplied space: the black of fully transparent
// pixels does not darken what is drawn.
func TestImageDownscaleIgnoresTransparentColour(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			if (x/2+y/2)%2 == 0 {
				src.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			} // the rest stays transparent black
		}
	}
	out := drawImage(t, pngDataURI(t, src), 8, 8, "")
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if c := out.NRGBAAt(x, y); c.R < 250 || c.G < 250 || c.B < 250 {
				t.Fatalf("pixel (%d,%d) = %v: transparent black bled into white", x, y, c)
			}
		}
	}
}

// Images drawn at half their size or larger are sampled from the image
// itself, as before. Smaller unrotated ones are averaged by area over the
// image, or a halved copy, on which the footprint is two to four pixels; rotated
// ones are sampled from the copy on which it is one to two.
func TestSampledLevel(t *testing.T) {
	img := &rasterImage{w: 64, h: 64, pix: make([]uint8, 64*64*4)}
	for _, tc := range []struct {
		inv  matrix
		w    int // of what is sampled
		area bool
		unit float64 // pixels of it a device pixel spans along x
	}{
		{identity, 64, false, 1},
		{matrix{2, 0, 0, 2, 0, 0}, 64, false, 2}, // half size: as before
		{matrix{2.1, 0, 0, 1, 0, 0}, 64, true, 2.1},
		{matrix{1, 0, 0, 3.9, 0, 0}, 64, true, 1},
		{matrix{4, 0, 0, 4, 8, 8}, 32, true, 2},
		{matrix{6, 0, 0, 6, 0, 0}, 32, true, 3},
		{matrix{0, 8, -8, 0, 0, 0}, 8, false, 1}, // rotated, an eighth
		{matrix{3, 3, -3, 3, 0, 0}, 16, false, 1.0606601717798212},
		{matrix{1000, 0, 0, 1000, 0, 0}, 1, true, 0},
	} {
		lvl, inv, area := sampledLevel(img, tc.inv)
		if lvl.w != tc.w || area != tc.area {
			t.Errorf("inv %v: sampled a %d px level by area %v, want %d by area %v", tc.inv, lvl.w, area, tc.w, tc.area)
		}
		if got := math.Hypot(inv.a, inv.b); tc.unit != 0 && math.Abs(got-tc.unit) > 1e-9 {
			t.Errorf("inv %v: %g pixels a device pixel, want %g", tc.inv, got, tc.unit)
		}
	}
}

func TestHalve(t *testing.T) {
	img := &rasterImage{w: 3, h: 1, pix: []uint8{
		200, 0, 0, 200, // premultiplied
		0, 0, 0, 0,
		0, 100, 0, 100,
	}}
	h := img.halve()
	if h.w != 2 || h.h != 1 {
		t.Fatalf("halved to %dx%d, want 2x1", h.w, h.h)
	}
	want := []uint8{100, 0, 0, 100, 0, 100, 0, 100}
	if !bytes.Equal(h.pix, want) {
		t.Errorf("halved pixels %v, want %v", h.pix, want)
	}
	if l, k := img.level(5); k != 2 || l.w != 1 || l.h != 1 {
		t.Errorf("level(5) = %dx%d at %d, want 1x1 at 2", l.w, l.h, k)
	}
}

// TestImageShrunkToNothing samples an image through a transform that shrinks
// it to far less than a pixel, as a colour glyph's image box can: averaging a
// device pixel's footprint visited every image pixel under it, out to the
// edge copies past the image, which took longer than the life of the process.
func TestImageShrunkToNothing(t *testing.T) {
	img := &rasterImage{w: 5, h: 7, pix: make([]uint8, 5*7*4)}
	for i := range img.pix {
		img.pix[i] = 0xff
	}
	for _, inv := range []matrix{
		{1e25, 0, 0, 1e38, 0, 0},
		{math.Inf(1), 0, 0, 1, 0, 0},
		{1, 0, 0, math.NaN(), 0, 0},
	} {
		lvl, linv, area := sampledLevel(img, inv)
		s := &imageShader{img: lvl, inv: linv, area: area, opacity: 255}
		done := make(chan struct{})
		go func() {
			s.shadeRow(0, 0, make([]uint8, 4*64))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("sampling through %v did not finish in 10s", inv)
		}
	}
}
