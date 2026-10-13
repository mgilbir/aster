package imageref

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand"
	"sync"
	"testing"
)

// pollCtx counts the polls of its Err and ends, if stop is set, at the
// stop-th: polls are counted, not timed, so no machine's speed changes it.
type pollCtx struct {
	context.Context
	mu    sync.Mutex
	polls int
	stop  int
}

func (c *pollCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if c.stop > 0 && c.polls >= c.stop {
		return context.Canceled
	}
	return nil
}

// sources are images of the kinds the decoders make, each drawn through a
// different path of image/draw.
func sources(w, h int) map[string]image.Image {
	r := rand.New(rand.NewSource(1))
	b := image.Rect(3, 5, 3+w, 5+h)
	pal := image.NewPaletted(b, color.Palette{color.NRGBA{255, 0, 0, 128}, color.Black, color.Transparent})
	gray := image.NewGray(b)
	nrgba := image.NewNRGBA(b)
	rgba64 := image.NewNRGBA64(b)
	ycc := image.NewYCbCr(b, image.YCbCrSubsampleRatio420)
	for _, p := range [][]byte{pal.Pix, gray.Pix, nrgba.Pix, rgba64.Pix, ycc.Y, ycc.Cb, ycc.Cr} {
		for i := range p {
			p[i] = byte(r.Intn(256))
		}
	}
	for i := range pal.Pix {
		pal.Pix[i] %= 3
	}
	return map[string]image.Image{"paletted": pal, "gray": gray, "nrgba": nrgba, "nrgba64": rgba64, "ycbcr": ycc}
}

// DrawSrc makes, band by band, the pixels one draw.Draw makes, into the
// kinds of image the writers hold, for rectangles inside and across the
// destination's edges.
func TestDrawSrcMatchesDraw(t *testing.T) {
	w, h := 300, 700 // several bands, the last one short
	for name, src := range sources(w, h) {
		for _, dst := range []func() draw.Image{
			func() draw.Image { return image.NewRGBA(image.Rect(0, 0, w, h)) },
			func() draw.Image { return image.NewNRGBA(image.Rect(0, 0, w, h)) },
		} {
			for _, tc := range []struct {
				r  image.Rectangle
				sp image.Point
			}{
				{image.Rect(0, 0, w, h), src.Bounds().Min},
				{image.Rect(-7, -9, w+20, h+30), src.Bounds().Min},
				{image.Rect(10, 20, 200, 650), src.Bounds().Min.Add(image.Pt(4, 1))},
				{image.Rect(0, 0, 0, 0), src.Bounds().Min},
			} {
				want, got := dst(), dst()
				draw.Draw(want, tc.r, src, tc.sp, draw.Src)
				if err := DrawSrc(context.Background(), got, tc.r, src, tc.sp); err != nil {
					t.Fatal(err)
				}
				var wp, gp []byte
				switch d := want.(type) {
				case *image.RGBA:
					wp, gp = d.Pix, got.(*image.RGBA).Pix
				case *image.NRGBA:
					wp, gp = d.Pix, got.(*image.NRGBA).Pix
				}
				if !bytes.Equal(wp, gp) {
					t.Errorf("%s into %T over %v from %v: the pixels differ from draw.Draw's", name, want, tc.r, tc.sp)
				}
			}
		}
	}
}

// DrawSrc polls once a band, before it, so a context that ends at the
// middle poll stops it with the image half drawn, the rest untouched.
// Counted, not timed.
func TestDrawSrcPollsEachBand(t *testing.T) {
	w, h := 1000, 1000
	src := sources(w, h)["paletted"]
	bands := (h + BandRows(w) - 1) / BandRows(w)
	full := &pollCtx{Context: context.Background()}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := DrawSrc(full, dst, dst.Bounds(), src, src.Bounds().Min); err != nil {
		t.Fatal(err)
	}
	if full.polls < bands {
		t.Fatalf("%d polls for %d bands: a band is drawn without a poll", full.polls, bands)
	}
	half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
	dst = image.NewRGBA(image.Rect(0, 0, w, h))
	if err := DrawSrc(half, dst, dst.Bounds(), src, src.Bounds().Min); !errors.Is(err, context.Canceled) {
		t.Fatalf("a context ended at poll %d of %d: err = %v", half.stop, full.polls, err)
	}
	drawn := 0
	for y := range h {
		if dst.Pix[y*dst.Stride+3] != 0 || dst.Pix[y*dst.Stride+7] != 0 { // a drawn row has an opaque or translucent pixel
			drawn++
		}
	}
	if drawn > h/2+BandRows(w) {
		t.Errorf("stopped at poll %d of %d, %d of %d rows were drawn", half.stop, full.polls, drawn, h)
	}
}

// encodePNG encodes img, failing the test on an error.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Decode polls as it reads, by how many pixels a read can become rather
// than its bytes: a 2048x2048 two-colour PNG is a thousand bytes (the
// bounded corpus has one of 533), four million pixels, and must still poll
// once every pollPixels or so; a context that ends at the middle poll stops
// it. Counted, not timed.
func TestDecodePollsByPixels(t *testing.T) {
	lim := Limits{MaxBytes: 1 << 30, MaxPixels: 1 << 30, Err: testLimits.Err}
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"one-bit-flat", image.NewPaletted(image.Rect(0, 0, 2048, 2048), color.Palette{color.White, color.Black})},
		{"two-bit-noise", sources(2048, 2048)["paletted"]},
		{"nrgba-noise", sources(1024, 1024)["nrgba"]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := encodePNG(t, tc.img)
			full := &pollCtx{Context: context.Background()}
			if _, err := Decode(full, data, lim); err != nil {
				t.Fatal(err)
			}
			b := tc.img.Bounds()
			// A read of a PNG becomes at most about pollPixels pixels
			// (DEFLATE's 1,032 bytes a byte, a little over pollPixels/8192
			// bytes a bit of depth): so at least this many reads.
			want := b.Dx() * b.Dy() / (pollPixels + pollPixels/16)
			t.Logf("%d bytes, %d polls", len(data), full.polls)
			if full.polls < want {
				t.Errorf("%d polls decoding %d pixels from %d bytes; want at least %d", full.polls, b.Dx()*b.Dy(), len(data), want)
			}
			half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
			if _, err := Decode(half, data, lim); !errors.Is(err, context.Canceled) {
				t.Errorf("a context ended at poll %d of %d: err = %v", half.stop, full.polls, err)
			}
		})
	}
}

// Poll charges the ledger the work it is given and stops with the context.
func TestPoll(t *testing.T) {
	if err := Poll(nil, 5); err != nil { //nolint:staticcheck // a nil context is never done
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := Poll(ctx, 5); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := Poll(ctx, 5); !errors.Is(err, context.Canceled) {
		t.Errorf("a done context: err = %v", err)
	}
}
