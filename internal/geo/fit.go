package geo

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// fitTarget is what the fit algorithms need from a projection.
type fitTarget interface {
	Stream(sink Stream) Stream
	// streamBound is Stream with the points the projection produces charged
	// to b and the points it reads metered (b nil: neither): how a path
	// generator, and a fit, stream through a projection.
	streamBound(sink Stream, b *walkBound) Stream
	// clipExtentForFit returns the user's clip extent; ok is false when there
	// is none, or when the projection has no clipExtent at all (albersUsa).
	clipExtentForFit() (ext [4]float64, ok bool)
	setClipExtentForFit(ext *[4]float64)
	// setScaleTranslate is scale(k) followed by translate([x, y]).
	setScaleTranslate(k, x, y float64)
}

// fit resets the projection to scale 150 / translate [0, 0] without clipping,
// measures the projected object, lets fitBounds choose the final scale and
// translation from the bounds, and restores the clip extent. The measuring
// walk is bounded by ctx as a path's walks are (Path.Bind): the resampler can
// make 2^16 points of each segment (C65). A walk cut short returns its error
// and leaves the projection at the reset scale and translation, with its clip
// extent restored.
func fit(ctx context.Context, p fitTarget, object jsval.Value, fitBounds func(b [2][2]float64)) (err error) {
	clip, hasClip := p.clipExtentForFit()
	p.setScaleTranslate(150, 0, 0)
	if hasClip {
		p.setClipExtentForFit(nil)
		defer p.setClipExtentForFit(&clip)
	}
	bs := newBoundsSink()
	if err := streamBounded(ctx, object, func(b *walkBound) Stream { return p.streamBound(bs, b) }); err != nil {
		return err
	}
	fitBounds(bs.result())
	return nil
}

// streamBounded streams object into the stream sinkFor builds, bounded by ctx
// (see walkBound), and returns the error of a walk cut short.
func streamBounded(ctx context.Context, object jsval.Value, sinkFor func(*walkBound) Stream) (err error) {
	defer recoverStop(&err)
	StreamObject(object, sinkFor(newWalkBound(ctx)))
	return nil
}

func fitExtent(ctx context.Context, p fitTarget, ex0, ey0, ex1, ey1 float64, object jsval.Value) error {
	return fit(ctx, p, object, func(b [2][2]float64) {
		w := ex1 - ex0
		h := ey1 - ey0
		k := minNaN(w/(b[1][0]-b[0][0]), h/(b[1][1]-b[0][1]))
		x := ex0 + (w-float64(k*(b[1][0]+b[0][0])))/2
		y := ey0 + (h-float64(k*(b[1][1]+b[0][1])))/2
		p.setScaleTranslate(150*k, x, y)
	})
}

func fitWidth(ctx context.Context, p fitTarget, width float64, object jsval.Value) error {
	return fit(ctx, p, object, func(b [2][2]float64) {
		w := width
		k := w / (b[1][0] - b[0][0])
		x := (w - float64(k*(b[1][0]+b[0][0]))) / 2
		y := -k * b[0][1]
		p.setScaleTranslate(150*k, x, y)
	})
}

func fitHeight(ctx context.Context, p fitTarget, height float64, object jsval.Value) error {
	return fit(ctx, p, object, func(b [2][2]float64) {
		h := height
		k := h / (b[1][1] - b[0][1])
		x := -k * b[0][0]
		y := (h - float64(k*(b[1][1]+b[0][1]))) / 2
		p.setScaleTranslate(150*k, x, y)
	})
}
