package pngopt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
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

// gradient is an image of fewer distinct colours than the quantizer's
// limit, more than a palette: one that goes through the median cut, the
// dithering and the encoding.
func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x ^ y) & 0x3f), 255})
		}
	}
	return img
}

// Quantization and recoding poll the call's context throughout, so a
// deadline ends them promptly whatever the machine (C29; image/png's encoder,
// which never polls, ran 1.3 s past a deadline on CI). Counted, not timed: a
// run polls at least once a row of the image (the encoder polls every row,
// and the colour scans and the mapping are metered), and a context that ends
// at the run's middle poll stops it, so the polls reach into its second half.
func TestPollsThroughout(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  *image.NRGBA
		run  func(ctx context.Context, img *image.NRGBA, data []byte) error
	}{
		{"quantize", gradient(2400, 2400), func(ctx context.Context, img *image.NRGBA, data []byte) error {
			_, err := QuantizeOrRecodeImage(ctx, img, data, 256)
			return err
		}},
		{"quantize-fallback", noise(2400, 2400), func(ctx context.Context, img *image.NRGBA, data []byte) error {
			_, err := QuantizeOrRecodeImage(ctx, img, data, 256)
			return err
		}},
		{"recode", noise(2400, 2400), func(ctx context.Context, img *image.NRGBA, data []byte) error {
			_, err := RecodeImage(ctx, img, data)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := encode(t, tc.img)
			full := &pollCtx{Context: context.Background()}
			if err := tc.run(full, tc.img, data); err != nil {
				t.Fatal(err)
			}
			rows := tc.img.Bounds().Dy()
			t.Logf("%d polls over %d rows", full.polls, rows)
			if full.polls < rows {
				t.Errorf("%d polls for an image of %d rows: a stretch of the run does not poll", full.polls, rows)
			}
			half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
			if err := tc.run(half, tc.img, data); !errors.Is(err, context.Canceled) {
				t.Errorf("a context ended at poll %d of %d did not stop the run: %v", half.stop, full.polls, err)
			}
		})
	}
}

// The encoders charge their work per row to a meter that polls the context
// every 1,024 units, so the last stage of a run, which writes the whole image,
// cannot run long without a poll (the run-level counts above are dominated by
// the metered colour scans): a 600x400 image is at least a poll every four
// rows, and a context that ends at the middle poll stops it. Counted, as above.
func TestEncodersPoll(t *testing.T) {
	img := gradient(600, 400)
	pal := image.NewPaletted(img.Bounds(), color.Palette{color.Black, color.White})
	for _, tc := range []struct {
		name string
		run  func(ctx context.Context) error
	}{
		{"truecolour", func(ctx context.Context) error { return Encode(ctx, &bytes.Buffer{}, img) }},
		{"paletted", func(ctx context.Context) error { return EncodePaletted(ctx, &bytes.Buffer{}, pal) }},
	} {
		full := &pollCtx{Context: context.Background()}
		if err := tc.run(full); err != nil {
			t.Fatal(err)
		}
		if rows := img.Bounds().Dy(); full.polls < rows/4 {
			t.Errorf("%s: %d polls for %d rows, fewer than one every four", tc.name, full.polls, rows)
		}
		if err := tc.run(&pollCtx{Context: context.Background(), stop: full.polls / 2}); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: a context ended at poll %d of %d did not stop the encoding: %v", tc.name, full.polls/2, full.polls, err)
		}
	}
}
