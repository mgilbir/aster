package raster

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/notoemoji"
)

// The reference renderer is resvg (WASM) with the same font plan the root
// package uses: the embedded Liberation faces plus Noto Emoji, generic
// families mapped to Liberation.
var (
	refOnce sync.Once
	refR    *resvg.Renderer
	refErr  error
	refMu   sync.Mutex
)

func refRenderer() (*resvg.Renderer, error) {
	refOnce.Do(func() {
		fonts := []resvg.Font{
			{Data: liberation.SansRegular}, {Data: liberation.SansBold}, {Data: liberation.SansItalic}, {Data: liberation.SansBoldItalic},
			{Data: liberation.MonoRegular}, {Data: liberation.MonoBold}, {Data: liberation.MonoItalic}, {Data: liberation.MonoBoldItalic},
			{Data: liberation.SerifRegular}, {Data: liberation.SerifBold}, {Data: liberation.SerifItalic}, {Data: liberation.SerifBoldItalic},
			{Data: notoemoji.Regular},
		}
		refR, refErr = resvg.New(context.Background(), fonts, resvg.FamilyMapping{
			SansSerif: "Liberation Sans", Serif: "Liberation Serif", Monospace: "Liberation Mono",
			Cursive: "Liberation Sans", Fantasy: "Liberation Sans",
		})
	})
	return refR, refErr
}

// refPNG renders svg with resvg, caching results on disk when RASTER_REF_DIR is set.
func refPNG(t testing.TB, name string, svg []byte, scale float64) *image.NRGBA {
	t.Helper()
	var cache string
	if d := os.Getenv("RASTER_REF_DIR"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		cache = filepath.Join(d, fmt.Sprintf("%s@%g.png", name, scale))
		if b, err := os.ReadFile(cache); err == nil {
			if img, err := png.Decode(bytes.NewReader(b)); err == nil {
				return toNRGBA(img)
			}
		}
	}
	r, err := refRenderer()
	if err != nil {
		t.Fatalf("resvg: %v", err)
	}
	refMu.Lock()
	defer refMu.Unlock()
	b, err := r.Render(context.Background(), svg, scale)
	if err != nil {
		return nil
	}
	if cache != "" {
		_ = os.WriteFile(cache, b, 0o644)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	return toNRGBA(img)
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
