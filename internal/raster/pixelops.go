package raster

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/budget"
)

// The default pixel work budget's two terms (see PixelOps): the budget at
// scale 1, and the floor per output pixel.
const (
	DefaultMaxPixelOps      = defaultMaxPixelOps
	DefaultPixelOpsPerPixel = defaultPixelOpsPer
)

// ScaledPixelOps is a pixel work budget of base operations at scale 1 for a
// render at scale s: base times s² above scale 1, base below it, saturating
// at math.MaxInt. A chart drawn at scale s covers s² times the pixels it
// covers at scale 1, and does s² times the work, so the budget keeps the
// same margin over it at every scale; below scale 1 it stays at base.
func ScaledPixelOps(base int, s float64) int {
	f := float64(base) * max(1, s*s)
	if !(f < float64(math.MaxInt)) {
		return math.MaxInt
	}
	return int(f)
}

// PixelOps is the pixel work budget of a render at scale s whose output is
// w×h pixels: MaxPixelOps (when set; ScaledPixelOps(DefaultMaxPixelOps, s)
// when not), raised to PixelOpsPerPixel operations per output pixel
// (DefaultPixelOpsPerPixel when MaxPixelOps is not set). The public API sets
// both from aster's deriveLimits; a direct caller of Render that sets
// neither gets the same budget.
func (l Limits) PixelOps(s float64, w, h int) int {
	n, per := l.MaxPixelOps, l.PixelOpsPerPixel
	if n <= 0 {
		n = ScaledPixelOps(defaultMaxPixelOps, s)
		if per <= 0 {
			per = defaultPixelOpsPer
		}
	}
	return max(n, budget.MulInt(per, budget.MulInt(w, h)))
}

// Work is what one render spent of its pixel work budget (Limits.MaxPixelOps).
type Work struct {
	Ops, Budget   int     // pixel operations charged, and the budget
	Width, Height int     // the output's pixels
	Scale         float64 // the render's scale (1 for a colour glyph's image)
}

type workReportKey struct{}

// WithWorkReport returns ctx carrying f, which every render polling the
// returned context (or one derived from it) calls once it is done, with the
// work it spent: a measurement hook, for the tests that hold the default
// budget's margin over real charts. f is called on the rendering goroutine;
// renders running at once call it at once.
func WithWorkReport(ctx context.Context, f func(Work)) context.Context {
	return context.WithValue(ctx, workReportKey{}, f)
}

// reportWork tells ctx's work report, if any, what r spent.
func (r *renderer) reportWork(ctx context.Context, scale float64) {
	if ctx == nil {
		return
	}
	if f, ok := ctx.Value(workReportKey{}).(func(Work)); ok && f != nil {
		f(Work{Ops: r.pixelOps, Budget: r.lim.MaxPixelOps, Width: r.cw, Height: r.ch, Scale: scale})
	}
}
