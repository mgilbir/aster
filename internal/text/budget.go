package text

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/forme/shape"
)

// ShapingBudget bounds the text shaping of one render. A run shaped through a
// Measurer bound to it (see Measurer.Bounded) is shaped under its context, so
// a timeout or a cancellation stops the run partway, and the lookup work forme
// charges for it comes out of one allowance that every run of the render
// shares. A run is also refused when its glyphs would hold more than the
// memory allowed for one run.
//
// Measurement cannot return an error, so a refused run panics with a
// *budget.Stop wrapping the context's error or one that wraps budget.ErrLimit,
// for the renderer to recover. A width already in the Measurer's cache is not
// shaped again and costs nothing.
//
// A ShapingBudget is safe for concurrent use.
type ShapingBudget struct {
	ctx       context.Context
	maxGlyphs int // 0: no bound beyond the expansion one (see runGlyphs)
	work      int64
	left      atomic.Int64
}

// ShapingLimits configures a ShapingBudget.
type ShapingLimits struct {
	// Work is the lookup work, in forme's units, all the runs of the render
	// may charge together (0: DefaultShapingWork).
	Work int64
	// RunBytes bounds the memory of the glyphs of one run (0: unbounded but
	// for the expansion bound).
	RunBytes int64
}

// DefaultShapingWork is the shaping work a call may charge when none is
// configured. Across the Vega-Lite examples and the Vega gallery, the most
// one specification charges, rendered to SVG and then to PNG, is about a
// sixtieth of it (labeled-scatter-plot, whose label transform measures every
// candidate position: 1.1e9 units).
const DefaultShapingWork = 1 << 36

// glyphBytes is what one glyph of a run holds: forme's glyph and the Glyph a
// kept run copies it into.
const glyphBytes = int64(unsafe.Sizeof(shape.Glyph{}) + unsafe.Sizeof(Glyph{}))

// NewShapingBudget returns a budget that shapes under ctx within limits.
func NewShapingBudget(ctx context.Context, limits ShapingLimits) *ShapingBudget {
	b := &ShapingBudget{ctx: ctx}
	if limits.Work <= 0 {
		limits.Work = DefaultShapingWork
	}
	b.work = limits.Work
	b.left.Store(limits.Work)
	if limits.RunBytes > 0 {
		b.maxGlyphs = int(max(limits.RunBytes/glyphBytes, 1))
	}
	return b
}

// Used is the work the runs shaped under b have charged so far.
func (b *ShapingBudget) Used() int64 { return b.work - b.left.Load() }

type budgetKey struct{}

// WithShapingBudget returns ctx carrying b.
func WithShapingBudget(ctx context.Context, b *ShapingBudget) context.Context {
	return context.WithValue(ctx, budgetKey{}, b)
}

// ShapingBudgetFrom returns the ShapingBudget in ctx, or nil.
func ShapingBudgetFrom(ctx context.Context) *ShapingBudget {
	b, _ := ctx.Value(budgetKey{}).(*ShapingBudget)
	return b
}

// Bounded returns a view of m that shapes under b and shares m's fonts and
// caches. A nil b shapes without bounds, as m does.
func (m *Measurer) Bounded(b *ShapingBudget) *Measurer {
	if m == nil {
		return nil
	}
	return &Measurer{measurerState: m.measurerState, budget: b}
}

// runGlyphs bounds the glyphs of a run of n bytes. Shaping can multiply
// glyphs (a decomposition, a font's own substitutions), but no script makes
// four of a byte; the budget's memory bound applies on top.
func (b *ShapingBudget) runGlyphs(n int) int {
	g := 4*n + 64
	if b.maxGlyphs > 0 {
		g = min(g, b.maxGlyphs)
	}
	return g
}

// runSlack is what forme counts against a run's input beyond its text: the
// face's own feature settings.
const runSlack = 4096

// shapeGlyphsBounded is shapeGlyphs under b. forme shapes the run through a
// clone of its own, so it needs none from the pool.
func (f *Face) shapeGlyphsBounded(s string, b *ShapingBudget) (g []shape.Glyph, ok bool) {
	left := b.left.Load()
	if left <= 0 {
		panic(&budget.Stop{Err: fmt.Errorf("text: shaping: %w: the render's shaping work is spent", budget.ErrLimit)})
	}
	limits := shape.RunLimits{MaxInputBytes: len(s) + runSlack, MaxGlyphs: b.runGlyphs(len(s)), MaxWork: left}
	var res shape.RunResult
	var err error
	func() {
		defer func() {
			if recover() != nil {
				ok = false // a malformed font, contained as shapeGlyphs contains it
			}
		}()
		res, err = f.shape.ShapeGlyphsContext(b.ctx, shape.RunInput{Text: s}, limits)
		ok = true
	}()
	if !ok {
		return nil, false
	}
	if err != nil {
		if errors.Is(err, shape.ErrRunLimit) {
			err = fmt.Errorf("text: shaping %d bytes in %s: %w: %w", len(s), f.Family, budget.ErrLimit, err)
		}
		panic(&budget.Stop{Err: err})
	}
	b.left.Add(-res.Work)
	f.record(res.Glyphs)
	return res.Glyphs, true
}
