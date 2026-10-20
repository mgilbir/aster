package pngopt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

func noise(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	x := uint32(1)
	for i := range img.Pix {
		x = x*1664525 + 1013904223
		img.Pix[i] = uint8(x >> 24)
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	return img
}

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Quantization runs under the call's context and ledger (C29): a done
// context stops it with the context's error, a ledger too small for its
// maps with ErrLimit, and an image of more distinct colours than a palette
// can stand for falls back to the lossless recode without a median cut.
func TestQuantizeBounded(t *testing.T) {
	img := noise(600, 600)
	data := encode(t, img)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := QuantizeOrRecodeImage(ctx, img, data, 256); !errors.Is(err, context.Canceled) {
		t.Errorf("a done context: err = %v", err)
	}
	if _, err := RecodeImage(ctx, img, data); !errors.Is(err, context.Canceled) {
		t.Errorf("recode, a done context: err = %v", err)
	}
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	if _, err := QuantizeOrRecodeImage(budget.WithLedger(context.Background(), l), img, data, 256); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("a ledger of 1 MiB: err = %v", err)
	}
	out, ok, reason, err := QuantizeImage(context.Background(), img, data, 256)
	if err != nil || ok || reason != "too many distinct colors" || !bytes.Equal(out, data) {
		t.Errorf("noise: ok %v, reason %q, err %v", ok, reason, err)
	}
}

// Quantization charges its colour maps as the histogram grows, not for the
// 65,536 colours it may come to (C107): a two-colour image costs its
// indexed copy, not 15 MiB. What it charges still grows with the colours.
func TestQuantizeChargesTheColoursItHas(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1000, 1000))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = uint8(255*(i/4%1000/500)), 255
	}
	data := encode(t, img)
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 4 << 20})
	if _, err := QuantizeOrRecodeImage(budget.WithLedger(context.Background(), l), img, data, 256); err != nil {
		t.Fatalf("a two-colour 1000x1000 image under a 4 MiB ledger: %v", err)
	}
	peak := func(colours int) int64 {
		img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		for i := 0; i < len(img.Pix); i += 4 {
			c := i / 4 % colours
			img.Pix[i], img.Pix[i+1], img.Pix[i+3] = uint8(c), uint8(c>>8), 255
		}
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		if _, _, _, err := QuantizeImage(budget.WithLedger(context.Background(), l), img, encode(t, img), 256); err != nil {
			t.Fatal(err)
		}
		return l.PeakBytes()
	}
	few, many := peak(16), peak(16384)
	if many-few < 16000*mapEntryBytes {
		t.Errorf("quantizing 16 colours charged %d bytes, 16,384 colours %d: the maps grow with the colours", few, many)
	}
}

// Recoding an opaque image the rasterizer wrote returns its bytes: the
// encoder already wrote it as RGB, and encoding it again gives the same
// bytes (C158). An indexed recoding is kept only where it does not grow the
// file past the tolerance quantization allows.
func TestRecodeKeepsWhatIsAlreadyCheapest(t *testing.T) {
	img := noise(300, 200)
	var b bytes.Buffer
	if err := Encode(context.Background(), &b, img); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	out, err := RecodeImage(context.Background(), img, data)
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("recode of an opaque image: err %v, same bytes %v", err, bytes.Equal(out, data))
	}
	if &out[0] != &data[0] {
		t.Error("recoding an opaque image already RGB encoded it again")
	}
	// A few colours in a tiny image: the indexed form, with its palette, is
	// larger than the RGBA one; the RGBA bytes are kept.
	small := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(small.Pix); i += 4 {
		small.Pix[i], small.Pix[i+1], small.Pix[i+2], small.Pix[i+3] = uint8(i*40), 1, 2, 128
	}
	sdata := encode(t, small)
	out, err = RecodeImage(context.Background(), small, sdata)
	if err != nil || len(out) > len(sdata)+len(sdata)/10 {
		t.Errorf("recode of a 2x2 image: %d bytes from %d (err %v)", len(out), len(sdata), err)
	}
}
