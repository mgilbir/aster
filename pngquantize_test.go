package aster

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

// syntheticChart builds a chart-like NRGBA test image: flat panels and bars,
// antialiased-looking edges with per-pixel variation, and a noisy strip
// standing in for dense content (legend text, gridline stipple) — far more
// than 256 distinct colors and, like real renders, not trivially
// filter-compressible as RGBA.
func syntheticChart(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rnd := uint32(1)
	noise := func() uint8 {
		// Deterministic LCG; test images must not depend on math/rand.
		rnd = rnd*1664525 + 1013904223
		return uint8(rnd >> 24)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch {
			case y < h/8: // dense strip: incompressible, hundreds of colors
				c = color.NRGBA{R: 60 + noise()/8, G: 90 + noise()/8, B: 140 + noise()/8, A: 255}
			case x%97 < 3: // flat vertical bars
				c = color.NRGBA{R: 78, G: 121, B: 167, A: 255}
			case x%97 == 3: // antialiased edge pixel, per-pixel variation
				v := uint8((x*7 + y*13) % 32)
				c = color.NRGBA{R: 150 + v, G: 175 + v/2, B: 200 + v, A: 255}
			default: // flat background
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func distinctColors(t *testing.T, data []byte) int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[color.NRGBA]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			seen[color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)] = true
		}
	}
	return len(seen)
}

func TestQuantizeShrinksAndRespectsBudget(t *testing.T) {
	original := syntheticChart(640, 480)
	if got := distinctColors(t, original); got <= 256 {
		t.Fatalf("fixture too simple: %d distinct colors", got)
	}

	quantized, ok, reason := quantizePNG(original, 128)
	if !ok {
		t.Fatalf("quantization rejected a chart-like image: %s", reason)
	}
	if len(quantized) >= len(original) {
		t.Fatalf("quantized (%d bytes) not smaller than original (%d)", len(quantized), len(original))
	}
	if got := distinctColors(t, quantized); got > 128 {
		t.Fatalf("palette budget exceeded: %d colors", got)
	}

	// Geometry untouched, colors close: flat regions exact, edges within a
	// small deviation, mean deviation tiny.
	want, _ := png.Decode(bytes.NewReader(original))
	got, _ := png.Decode(bytes.NewReader(quantized))
	if want.Bounds() != got.Bounds() {
		t.Fatalf("bounds changed: %v vs %v", want.Bounds(), got.Bounds())
	}
	var sum, count, maxDev float64
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
			g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
			for _, d := range []float64{
				math.Abs(float64(w.R) - float64(g.R)),
				math.Abs(float64(w.G) - float64(g.G)),
				math.Abs(float64(w.B) - float64(g.B)),
				math.Abs(float64(w.A) - float64(g.A)),
			} {
				sum += d
				count++
				if d > maxDev {
					maxDev = d
				}
			}
		}
	}
	if mean := sum / count; mean > 2 {
		t.Fatalf("mean channel deviation %.2f too high", mean)
	}
	if maxDev > 96 {
		t.Fatalf("max channel deviation %.0f too high", maxDev)
	}
}

func TestQuantizeLosslessUnderBudget(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8((x / 8) * 32), G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	original := buf.Bytes()

	quantized, ok, _ := quantizePNG(original, 256)
	if !ok {
		t.Fatal("lossless path must always succeed")
	}
	want, _ := png.Decode(bytes.NewReader(original))
	got, err := png.Decode(bytes.NewReader(quantized))
	if err != nil {
		t.Fatal(err)
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if color.NRGBAModel.Convert(want.At(x, y)) != color.NRGBAModel.Convert(got.At(x, y)) {
				t.Fatalf("pixel (%d,%d) changed on the lossless path", x, y)
			}
		}
	}
}

func TestQuantizeAlphaSurvives(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 8), G: 0, B: 255, A: uint8(64 + y*6)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	quantized, ok, reason := quantizePNG(buf.Bytes(), 64)
	if !ok {
		t.Fatalf("alpha gradient rejected: %s", reason)
	}
	got, err := png.Decode(bytes.NewReader(quantized))
	if err != nil {
		t.Fatal(err)
	}
	opaque := true
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y && opaque; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := got.At(x, y).RGBA(); a != 0xffff {
				opaque = false
				break
			}
		}
	}
	if opaque {
		t.Fatal("alpha channel was flattened to opaque")
	}
}

func TestQuantizeClampsAndFallsBack(t *testing.T) {
	original := syntheticChart(64, 64)
	if got, _, _ := quantizePNG(original, 0); len(got) == 0 {
		t.Fatal("maxColors 0 must clamp, not fail")
	}
	if got, _, _ := quantizePNG(original, 100000); len(got) == 0 {
		t.Fatal("maxColors above 256 must clamp, not fail")
	}
	garbage := []byte("not a png at all")
	if got, ok, _ := quantizePNG(garbage, 128); ok || !bytes.Equal(got, garbage) {
		t.Fatal("invalid input must be returned unchanged and flagged")
	}
	// The guarded wrapper never degrades below the lossless recode.
	if got := quantizeOrRecodePNG(garbage, 128); !bytes.Equal(got, garbage) {
		t.Fatal("wrapper must pass through invalid input")
	}
}

func TestQuantizeDeterministic(t *testing.T) {
	original := syntheticChart(320, 240)
	a, _, _ := quantizePNG(original, 128)
	b, _, _ := quantizePNG(original, 128)
	if !bytes.Equal(a, b) {
		t.Fatal("quantization is not deterministic")
	}
}

func BenchmarkQuantizePNG(b *testing.B) {
	original := syntheticChart(800, 600)
	b.SetBytes(int64(len(original)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out, _, _ := quantizePNG(original, 256); len(out) == 0 {
			b.Fatal("empty output")
		}
	}
}

// A photo-like image (every pixel distinct, no dominant colors) must trip
// the quality guard or size check and fall back rather than ship degraded.
func TestQuantizeRejectsPhotographicContent(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	rnd := uint32(7)
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			rnd = rnd*1664525 + 1013904223
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rnd >> 24), G: uint8(rnd >> 16), B: uint8(rnd >> 8), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	original := buf.Bytes()

	if _, ok, reason := quantizePNG(original, 16); ok {
		t.Fatal("full-noise image must not pass the quality guard at 16 colors")
	} else if reason == "" {
		t.Fatal("rejection must carry a reason")
	}
	// The wrapper degrades to the lossless recode path, never below it.
	out := quantizeOrRecodePNG(original, 16)
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	assertSamePixels := func() bool {
		for y := 0; y < 128; y++ {
			for x := 0; x < 128; x++ {
				if color.NRGBAModel.Convert(got.At(x, y)) != color.NRGBAModel.Convert(img.At(x, y)) {
					return false
				}
			}
		}
		return true
	}
	if !assertSamePixels() {
		t.Fatal("fallback output must be pixel-identical to the source")
	}
}
