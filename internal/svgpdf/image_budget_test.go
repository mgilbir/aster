package svgpdf

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"

	"image/png"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// bigPNG is a size×size opaque PNG, small encoded (one colour).
func bigPNG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := range len(img.Pix) / 4 {
		img.Pix[4*i], img.Pix[4*i+3] = 200, 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestImageDecodeCharged: the pixels an image decodes to, and what is made
// from them, are charged to the call's ledger before they are made (the
// class of C7), so a memory limit stops a large image with ErrLimit; within
// it, an image drawn by many elements, under one href or another of the
// same content, is decoded once and written once.
func TestImageDecodeCharged(t *testing.T) {
	src := bigPNG(t, 2048)
	href := dataURL("image/png", src)
	svg := imageSVG(imageEl(href, ""), imageEl(href, ` x="1"`), imageEl(" "+href+" ", ` x="2"`))
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 16 << 20})
	if _, err := Convert(svg, nil, Options{Context: budget.WithLedger(context.Background(), led)}); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("a 2048² image under a 16 MiB ledger: err %v, want ErrLimit", err)
	}

	led = budget.NewLedger(context.Background(), budget.LedgerLimits{})
	// The same pixels under a second href: another media type parameter.
	other := "data:image/png;charset=x;base64," + href[len("data:image/png;base64,"):]
	svg = imageSVG(imageEl(href, ""), imageEl(href, ` x="1"`), imageEl(other, ` x="2"`))
	pdf, err := Convert(svg, nil, Options{Context: budget.WithLedger(context.Background(), led)})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pdfImages(t, pdf)); n != 1 {
		t.Errorf("%d image XObjects, want 1", n)
	}
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n != 1 {
		t.Errorf("%d image streams written, want 1", n)
	}
	// Decoded once: the pixels' 16 MiB at least once, not three times.
	if b := led.PeakBytes(); b < 2048*2048*4 || b > 3*2048*2048*4+1<<20 {
		t.Errorf("charged %d MB at the peak", b>>20)
	}
}

// TestEncodePlanesMatchesDraw: reading premultiplied RGBA planes straight
// from the decoder's pixels gives what drawing them into NRGBA first gave.
func TestEncodePlanesMatchesDraw(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 5, 40, 30))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7919 >> 3)
	}
	for i := 3; i < len(src.Pix); i += 4 { // premultiplied: no channel above alpha
		for k := 1; k <= 3; k++ {
			src.Pix[i-k] = min(src.Pix[i-k], src.Pix[i])
		}
	}
	src.Pix[3] = 255
	nrgba := image.NewNRGBA(image.Rect(0, 0, src.Rect.Dx(), src.Rect.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), src, src.Rect.Min, draw.Src)
	want, err := encodeNRGBA(context.Background(), nrgba)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodePlanes(context.Background(), src.Pix, src.Stride, src.Rect.Dx(), src.Rect.Dy(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.data, want.data) || !bytes.Equal(got.smask, want.smask) {
		t.Error("the planes differ from those drawn through NRGBA")
	}
}
