package svgpdf

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"sync"
	"testing"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/imageref"
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

func bands(w, h int) int { return (h + imageref.BandRows(w) - 1) / imageref.BandRows(w) }

// twoColourPNG is a side x side PNG of two colours, one translucent, so that
// it is converted to NRGBA and has a soft mask: a few KiB, millions of
// pixels.
func twoColourPNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.NRGBA{255, 0, 0, 128}, color.Black})
	for y := 0; y < side; y += 2 {
		img.Pix[y*side+y/2] = 1
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// An image's decoding, its conversion to NRGBA, the split into planes and
// their compression poll the call's context throughout (the class of the
// rasterizer's image deadline overshoot). Counted, not timed: at least once
// a band of each stage, and a context that ends at the middle poll stops it
// with the context's error, which is not reported as an image that could
// not be read.
func TestImageDecodePolls(t *testing.T) {
	const side = 2048
	data := twoColourPNG(t, side)
	lim := Limits{}.WithDefaults()
	dec := &pollCtx{Context: context.Background()}
	if _, err := imageref.Decode(dec, data, lim.imageLimits()); err != nil {
		t.Fatal(err)
	}
	full := &pollCtx{Context: context.Background()}
	c := newImageCatalog(nil, lim, nil)
	c.ctx = full
	if _, err := c.decode(data); err != nil {
		t.Fatal(err)
	}
	px := side * side
	converting, planes := bands(side, side), bands(side, side)
	chunks := func(n int) int { return (n + 4*imageref.BandPixels - 1) / (4 * imageref.BandPixels) }
	compressing := chunks(3*px) + chunks(px) // the colour plane and the soft mask
	t.Logf("%d polls: %d decoding, %d converting, %d splitting, %d compressing", full.polls, dec.polls, converting, planes, compressing)
	if want := dec.polls + converting + planes + compressing; full.polls < want {
		t.Errorf("%d polls, want at least %d: a stage of the image's work does not poll", full.polls, want)
	}
	reported := 0
	half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
	c = newImageCatalog(map[string][]byte{"img": data}, lim, nil)
	c.ctx, c.onErr = half, func(string, error) { reported++ }
	if _, err := c.get("img"); !errors.Is(err, context.Canceled) || reported != 0 {
		t.Errorf("a context ended at poll %d of %d: err = %v, %d reported", half.stop, full.polls, err, reported)
	}
}

// The whole conversion, through Convert's options, polls as the
// catalog does: the call's context reaches it.
func TestConvertImageStops(t *testing.T) {
	href := "data:image/png;base64," + base64.StdEncoding.EncodeToString(twoColourPNG(t, 2048))
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><image width="64" height="64" href="` + href + `"/></svg>`
	full := &pollCtx{Context: context.Background()}
	if _, err := Convert(svg, nil, Options{Context: full}); err != nil {
		t.Fatal(err)
	}
	if px := 2048 * 2048; full.polls < 2*px/imageref.BandPixels {
		t.Errorf("%d polls converting an image of %d pixels", full.polls, px)
	}
	half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
	if _, err := Convert(svg, nil, Options{Context: half}); !errors.Is(err, context.Canceled) {
		t.Errorf("a context ended at poll %d of %d: err = %v", half.stop, full.polls, err)
	}
}

// compressPolled is compress's bytes, however the input splits into bands,
// and polls once a band.
func TestCompressPolled(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{0, 1, 4*imageref.BandPixels - 1, 4 * imageref.BandPixels, 10*imageref.BandPixels + 17} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(r.Intn(4)) // compressible, with matches across bands
		}
		want, err := compress(data)
		if err != nil {
			t.Fatal(err)
		}
		pc := &pollCtx{Context: context.Background()}
		got, err := compressPolled(pc, data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%d bytes: compressPolled differs from compress", n)
		}
		if b := (n + 4*imageref.BandPixels - 1) / (4 * imageref.BandPixels); pc.polls < b {
			t.Errorf("%d bytes: %d polls for %d bands", n, pc.polls, b)
		}
		if n > 4*imageref.BandPixels {
			half := &pollCtx{Context: context.Background(), stop: pc.polls / 2}
			if _, err := compressPolled(half, data); !errors.Is(err, context.Canceled) {
				t.Errorf("%d bytes: a context ended at poll %d of %d: err = %v", n, half.stop, pc.polls, err)
			}
		}
	}
}

// A bitmap glyph's PNG and a mask strike are converted a band at a time,
// polling: their size is the font's to choose.
func TestGlyphBitmapsPoll(t *testing.T) {
	const side = 1024
	data := twoColourPNG(t, side)
	lim := Limits{}.WithDefaults()
	dec := &pollCtx{Context: context.Background()}
	if _, err := imageref.Decode(dec, data, lim.imageLimits()); err != nil {
		t.Fatal(err)
	}
	full := &pollCtx{Context: context.Background()}
	if img, err := decodeNRGBA(full, data, lim); err != nil || img == nil {
		t.Fatalf("decodeNRGBA: %v", err)
	}
	if want := dec.polls + bands(side, side); full.polls < want {
		t.Errorf("a bitmap glyph: %d polls, want at least %d", full.polls, want)
	}
	if _, err := decodeNRGBA(&pollCtx{Context: context.Background(), stop: full.polls / 2}, data, lim); !errors.Is(err, context.Canceled) {
		t.Errorf("a bitmap glyph stopped at poll %d of %d: err = %v", full.polls/2, full.polls, err)
	}

	mask := shape.Image{Width: side, Height: side, Data: make([]byte, side*side), Color: shape.Color{A: 255}}
	full = &pollCtx{Context: context.Background()}
	if img, err := maskNRGBA(full, mask); err != nil || img == nil {
		t.Fatalf("maskNRGBA: %v", err)
	}
	if want := bands(side, side); full.polls < want {
		t.Errorf("a mask strike: %d polls, want at least %d", full.polls, want)
	}
	if _, err := maskNRGBA(&pollCtx{Context: context.Background(), stop: full.polls / 2}, mask); !errors.Is(err, context.Canceled) {
		t.Errorf("a mask strike stopped at poll %d of %d: err = %v", full.polls/2, full.polls, err)
	}
}
