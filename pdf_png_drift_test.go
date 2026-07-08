package aster_test

// Drift check: the vector PDF output must look like the raster PNG output of
// the same chart. For each golden SVG we render a PNG via resvg (the report's
// historical raster path) and a PDF via the svgpdf translator, rasterize the
// PDF back to a bitmap at matched resolution with pdftoppm, and measure the
// per-pixel difference. This bounds the combined translator+rasterizer drift
// so the vector path can be trusted as a drop-in for the PNG one.
//
// Gated on pdftoppm (poppler). Set ASTER_DRIFT_DIR to dump png/pdf-raster/diff
// triples for visual inspection.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster"
)

// scale/DPI are matched: SVGToPNG at scale 2 yields 2px per SVG user unit;
// the PDF page is sized in points == SVG user units, so 144 DPI (2*72) gives
// the same pixel dimensions.
const (
	driftScale = 2.0
	driftDPI   = 144
	// Thresholds on the 0-255 channel scale. Charts are flat fills plus
	// antialiased edges; two different rasterizers disagree mostly on edge
	// pixels, so the mean must stay low while a modest fraction of pixels may
	// differ noticeably at edges.
	maxMeanChannelDrift = 6.0
	maxFracPixelsOver32 = 0.06
)

func TestVectorPDFMatchesPNG(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm (poppler) not installed; skipping PDF/PNG drift check")
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	goldens, err := filepath.Glob(filepath.Join("testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(goldens) == 0 {
		t.Skip("no golden SVGs")
	}
	dumpDir := os.Getenv("ASTER_DRIFT_DIR")
	if dumpDir != "" {
		if err := os.MkdirAll(dumpDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, g := range goldens {
		name := filepath.Base(g)
		t.Run(name, func(t *testing.T) {
			svg, err := os.ReadFile(g)
			if err != nil {
				t.Fatal(err)
			}

			pngBytes, err := c.SVGToPNG(string(svg), aster.WithScale(driftScale))
			if err != nil {
				t.Fatalf("SVGToPNG: %v", err)
			}
			pdfBytes, err := c.SVGToPDF(string(svg))
			if err != nil {
				t.Fatalf("SVGToPDF: %v", err)
			}
			pdfPNG := rasterizePDF(t, pdfBytes)

			ref := decodePNG(t, pngBytes)
			got := decodePNG(t, pdfPNG)

			// Rasterizers can round dimensions by a pixel; compare the shared
			// top-left region and fail only if they diverge materially.
			w := min(ref.Bounds().Dx(), got.Bounds().Dx())
			h := min(ref.Bounds().Dy(), got.Bounds().Dy())
			if dw, dh := absi(ref.Bounds().Dx()-got.Bounds().Dx()), absi(ref.Bounds().Dy()-got.Bounds().Dy()); dw > 2 || dh > 2 {
				t.Fatalf("dimension mismatch: png %dx%d vs pdf-raster %dx%d",
					ref.Bounds().Dx(), ref.Bounds().Dy(), got.Bounds().Dx(), got.Bounds().Dy())
			}

			mean, fracOver, diffImg := compareRGBA(ref, got, w, h)
			t.Logf("%s: mean channel drift %.2f, %.1f%% pixels >32", name, mean, fracOver*100)

			if dumpDir != "" {
				base := name[:len(name)-len(".svg")]
				writePNGFile(t, filepath.Join(dumpDir, base+".png.png"), ref)
				writePNGFile(t, filepath.Join(dumpDir, base+".pdf.png"), got)
				writePNGFile(t, filepath.Join(dumpDir, base+".diff.png"), diffImg)
			}

			if mean > maxMeanChannelDrift {
				t.Errorf("%s: mean channel drift %.2f exceeds %.2f", name, mean, maxMeanChannelDrift)
			}
			if fracOver > maxFracPixelsOver32 {
				t.Errorf("%s: %.1f%% of pixels drift >32, exceeds %.1f%%", name, fracOver*100, maxFracPixelsOver32*100)
			}
		})
	}
}

func rasterizePDF(t *testing.T, pdf []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	// -r DPI, single page, PNG, white background flattened.
	outPrefix := filepath.Join(dir, "out")
	cmd := exec.Command("pdftoppm", "-r", fmt.Sprint(driftDPI), "-png", "-singlefile", in, outPrefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v\n%s", err, out)
	}
	data, err := os.ReadFile(outPrefix + ".png")
	if err != nil {
		t.Fatalf("read rasterized pdf: %v", err)
	}
	return data
}

func decodePNG(t *testing.T, b []byte) *image.RGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	bounds := img.Bounds()
	// Flatten onto white: the resvg PNG may carry alpha, the pdftoppm one is
	// opaque; compare both over an opaque white background.
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, gg, bb, a := img.At(x, y).RGBA()
			af := float64(a) / 0xffff
			blend := func(c uint32) uint8 {
				v := float64(c)/0xffff*af + (1 - af)
				return uint8(v*255 + 0.5)
			}
			rgba.SetRGBA(x, y, color.RGBA{blend(r), blend(gg), blend(bb), 255})
		}
	}
	return rgba
}

func compareRGBA(ref, got *image.RGBA, w, h int) (mean, fracOver float64, diff *image.RGBA) {
	diff = image.NewRGBA(image.Rect(0, 0, w, h))
	var sum float64
	var over int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rc := ref.RGBAAt(x, y)
			gc := got.RGBAAt(x, y)
			dr, dg, db := absi(int(rc.R)-int(gc.R)), absi(int(rc.G)-int(gc.G)), absi(int(rc.B)-int(gc.B))
			pixMax := max(dr, max(dg, db))
			sum += float64(dr + dg + db)
			if pixMax > 32 {
				over++
			}
			// Diff visualization: red intensity scaled by the drift.
			d := uint8(min(255, pixMax*3))
			diff.SetRGBA(x, y, color.RGBA{255, 255 - d, 255 - d, 255})
		}
	}
	n := float64(w * h)
	return sum / (n * 3), float64(over) / n, diff
}

func writePNGFile(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func absi(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
