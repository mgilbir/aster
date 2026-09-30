package raster

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/oracle"
)

// The reference rasterizer is resvg, run by the node oracle
// (internal/oracle) with the engine's default font plan: the embedded
// Liberation faces plus Noto Emoji, generic families mapped to Liberation.
// Tests that compare with it skip without the oracle.

// refPNG is resvg's rendering of svg at scale, or nil when resvg rejects it.
func refPNG(t testing.TB, svg []byte, scale float64) *image.NRGBA {
	t.Helper()
	res, err := oracle.For(t, oracle.VL6).PNG(svg, scale)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if res.Err != "" {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(res.PNG))
	if err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	return toNRGBA(img)
}

// corpusSVG is upstream's SVG for a Vega-Lite example, rendered by the node
// oracle: the input the corpus tests and benchmarks rasterize.
func corpusSVG(t testing.TB, name string) []byte {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join(corpusDir, name+".vl.json"))
	if err != nil {
		t.Skipf("corpus not found: %v", err)
	}
	res, err := oracle.For(t, oracle.VL6).SVG(true, spec)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if res.Err != "" {
		t.Skipf("%s: upstream: %s", name, res.Err)
	}
	return []byte(res.SVG)
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

// diffStats compares two images in premultiplied space.
type diffStats struct {
	MAE      float64 // mean absolute error per channel, 0..255
	PctOver  float64 // % of pixels whose worst channel differs by more than thr
	PctBig   float64 // % of pixels differing by more than 64
	PSNR     float64
	Max      int
	SizeDiff bool
}

func compareImages(a, b *image.NRGBA, thr int) diffStats {
	if a.Rect.Dx() != b.Rect.Dx() || a.Rect.Dy() != b.Rect.Dy() {
		return diffStats{SizeDiff: true, MAE: 255, PSNR: 0, PctOver: 100, PctBig: 100}
	}
	var sum float64
	var sq float64
	var over, big int
	mx := 0
	n := a.Rect.Dx() * a.Rect.Dy()
	for i := 0; i < len(a.Pix); i += 4 {
		aa, ba := int(a.Pix[i+3]), int(b.Pix[i+3])
		worst := 0
		// Premultiplied channels.
		for c := 0; c < 3; c++ {
			pa := (int(a.Pix[i+c])*aa + 127) / 255
			pb := (int(b.Pix[i+c])*ba + 127) / 255
			d := pa - pb
			if d < 0 {
				d = -d
			}
			sum += float64(d)
			sq += float64(d * d)
			if d > worst {
				worst = d
			}
		}
		d := aa - ba
		if d < 0 {
			d = -d
		}
		sum += float64(d)
		sq += float64(d * d)
		if d > worst {
			worst = d
		}
		if worst > thr {
			over++
		}
		if worst > 64 {
			big++
		}
		if worst > mx {
			mx = worst
		}
	}
	mse := sq / float64(4*n)
	psnr := 99.0
	if mse > 0 {
		psnr = 10 * math.Log10(255*255/mse)
	}
	return diffStats{
		MAE: sum / float64(4*n), PctOver: 100 * float64(over) / float64(n),
		PctBig: 100 * float64(big) / float64(n), PSNR: psnr, Max: mx,
	}
}

// writeDiff writes ours|reference|diff side by side for inspection.
func writeDiff(path string, ours, ref *image.NRGBA) {
	w, h := ours.Rect.Dx(), ours.Rect.Dy()
	if ref.Rect.Dx() != w || ref.Rect.Dy() != h {
		return
	}
	out := image.NewNRGBA(image.Rect(0, 0, w*3, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*ours.Stride + x*4
			for c := 0; c < 4; c++ {
				out.Pix[y*out.Stride+x*4+c] = ours.Pix[i+c]
				out.Pix[y*out.Stride+(w+x)*4+c] = ref.Pix[i+c]
			}
			d := 0
			for c := 0; c < 4; c++ {
				v := int(ours.Pix[i+c]) - int(ref.Pix[i+c])
				if v < 0 {
					v = -v
				}
				if v > d {
					d = v
				}
			}
			v := uint8(255 - min(255, d*4))
			o := y*out.Stride + (2*w+x)*4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = 255, v, v, 255
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, out)
	_ = os.WriteFile(path, buf.Bytes(), 0o644)
}

func rgbaColor(r, g, b, a uint8) color.Color { return color.NRGBA{r, g, b, a} }
