package pngopt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
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

// The image forms give what the byte forms give for the PNG they decode.
func TestImageFormsMatchByteForms(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 120, 80))
	for y := range 80 {
		for x := range 120 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 2), uint8(y * 3), uint8((x + y) % 7 * 30), uint8(200 + x%56)})
		}
	}
	data := encode(t, img)
	for _, n := range []int{8, 64, 256} {
		got, err := QuantizeOrRecodeImage(context.Background(), img, data, n)
		if err != nil || !bytes.Equal(got, QuantizeOrRecode(data, n)) {
			t.Errorf("quantize %d: the image form differs (err %v)", n, err)
		}
	}
	got, err := RecodeImage(context.Background(), img, data)
	if err != nil || !bytes.Equal(got, Recode(data)) {
		t.Errorf("recode: the image form differs (err %v)", err)
	}
}
