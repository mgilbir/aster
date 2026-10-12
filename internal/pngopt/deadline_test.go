package pngopt

import (
	"context"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"
)

// gapCtx records the longest interval between two polls of its Err.
type gapCtx struct {
	context.Context
	mu    sync.Mutex
	last  time.Time
	max   time.Duration
	polls int
}

func (c *gapCtx) Err() error {
	c.poll()
	return c.Context.Err()
}

func (c *gapCtx) poll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.max = max(c.max, now.Sub(c.last))
	c.last = now
	c.polls++
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

// Quantization and recoding poll the call's context throughout: no stretch
// of them, histogram, median cut, dithering, the encoding or the fallback
// recode, runs long without a poll, so a deadline ends them promptly
// whatever the machine (C29; the encoding of a 2000x2000 image ran 1.3 s
// past a deadline on CI). The guard is machine-independent: the longest gap
// between polls as a fraction of the whole run, which a stretch without
// polls makes large on any hardware (it was 73-96% of the run). What is left
// is allocating the output, once, a few percent.
func TestPollsThroughout(t *testing.T) {
	const maxFraction = 0.10
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
			gc := &gapCtx{Context: context.Background()}
			start := time.Now()
			gc.last = start
			if err := tc.run(gc, tc.img, data); err != nil {
				t.Fatal(err)
			}
			gc.poll()
			total := time.Since(start)
			t.Logf("%v, %d polls, longest gap %v", total, gc.polls, gc.max)
			if f := float64(gc.max) / float64(total); f > maxFraction {
				t.Errorf("%.0f%% of the run (%v of %v) without polling the context; at most %.0f%%", 100*f, gc.max, total, 100*maxFraction)
			}
		})
	}
}
