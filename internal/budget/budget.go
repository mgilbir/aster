// Package budget holds the per-render resource budget: how many data rows,
// loaded bytes and path points one render may create. A Budget travels in the
// context.Context of the render so every transform and loader can charge it
// without a signature change; charges happen before the allocation they pay
// for, so a hostile specification fails before it consumes the memory.
package budget

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// ErrLimit is wrapped by every error reporting an exceeded budget.
var ErrLimit = errors.New("limit exceeded")

// Stop is the panic value that ends a render from code that cannot return an
// error, such as a scenegraph callback walking a geographic path or a text
// measurement shaping a run: a budget is spent or the render's context is
// done. It wraps the cause. Callers that can return an error recover it, and
// the renderer's own recover reports the rest.
type Stop struct{ Err error }

func (e *Stop) Error() string { return e.Err.Error() }
func (e *Stop) Unwrap() error { return e.Err }

// Budget is the mutable state of one render. It is not safe for concurrent
// use: a render is single-threaded.
type Budget struct {
	// MaxRows bounds the data rows alive across the render (<= 0:
	// unlimited): the rows every holder holds (see Hold). MaxLoadBytes bounds
	// the bytes the Loader returned in total, MaxPoints the path points
	// geographic marks may emit.
	MaxRows      int
	MaxLoadBytes int64
	MaxPoints    int64
	// MaxCanvasBytes bounds the pixel memory of the bitmaps transforms paint
	// (the heatmap's canvases), in total across the render.
	MaxCanvasBytes int64
	// RowBytes is the memory one counted row stands for (0: rows are only
	// counted). With it set, the bytes by which rows outweigh that are held
	// with them (Hold), so that MaxRows bounds their memory (MaxRows *
	// RowBytes) and not just their number.
	RowBytes int64

	rows   int64
	excess int64
	// replacing is the holding about to be replaced: what it holds is free
	// to a reservation (see Replacing).
	replacing *Held
	loaded    int64
	points    int64
	canvas    int64
}

// Held is what one holder of rows holds of a Budget: a dataflow operator
// holds the tuples it made and still outputs. The zero value holds nothing.
type Held struct {
	rows, excess int64
}

// Rows is the number of rows h holds.
func (h *Held) Rows() int64 { return h.rows }

type key struct{}

// With returns ctx carrying b.
func With(ctx context.Context, b *Budget) context.Context {
	return context.WithValue(ctx, key{}, b)
}

// From returns the Budget in ctx, or nil (which every method accepts and
// treats as unlimited).
func From(ctx context.Context) *Budget {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(key{}).(*Budget)
	return b
}

func over(what string, n, limit int64) error {
	return fmt.Errorf("%w: %s %d exceeds the limit of %d", ErrLimit, what, n, limit)
}

// Mul multiplies non-negative counts, saturating instead of overflowing.
func Mul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// MulInt is Mul for int, which is 32 bits on some platforms: a product of
// pixel counts there must saturate at math.MaxInt rather than wrap.
func MulInt(a, b int) int {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt/b {
		return math.MaxInt
	}
	return a * b
}

// Reserve checks that n more rows fit, without recording them: a holder
// reserves the rows it is about to make, then holds them (Hold) once they
// exist. What the holding being replaced holds (Replacing) counts as free.
// Call it before allocating.
func (b *Budget) Reserve(n int64) error {
	if b == nil || b.MaxRows <= 0 || n <= 0 {
		return nil
	}
	used := max(b.used()-b.credit(), 0)
	if n > int64(b.MaxRows) || used+n > int64(b.MaxRows) {
		return over("data rows", used+min(n, math.MaxInt64-used), int64(b.MaxRows))
	}
	return nil
}

// ReserveWeight is Reserve for rows that weigh more than RowBytes each: it
// checks that rows more rows, outweighing RowBytes by excess bytes in all,
// fit, without recording them.
func (b *Budget) ReserveWeight(rows, excess int64) error {
	if b == nil || b.RowBytes <= 0 {
		return b.Reserve(rows)
	}
	return b.Reserve(rows + max(excess, 0)/b.RowBytes)
}

// Hold replaces what h holds with rows rows, which outweigh RowBytes by
// excess bytes in all, and fails when the rows held across the render then
// exceed MaxRows. A holder that runs again replaces what it held rather than
// adding to it, and one that holds less gives the difference back; nothing
// can give back more than it holds.
func (b *Budget) Hold(h *Held, rows, excess int64) error {
	rows, excess = max(rows, 0), max(excess, 0)
	if b == nil {
		return nil
	}
	if b.RowBytes <= 0 {
		excess = 0
	}
	b.rows += rows - h.rows
	b.excess += excess - h.excess
	h.rows, h.excess = rows, excess
	return b.checkRows()
}

// Replacing marks h as the holding about to be replaced, until the holding it
// returns is restored with another call: a reservation made meanwhile counts
// what h holds as free, since the rows it reserves will replace them.
func (b *Budget) Replacing(h *Held) (prev *Held) {
	if b == nil {
		return nil
	}
	prev, b.replacing = b.replacing, h
	return prev
}

// credit is what the holding being replaced holds, counted in rows.
func (b *Budget) credit() int64 {
	h := b.replacing
	if h == nil {
		return 0
	}
	if b.RowBytes <= 0 {
		return h.rows
	}
	return h.rows + h.excess/b.RowBytes
}

func (b *Budget) checkRows() error {
	if b.MaxRows > 0 && b.used() > int64(b.MaxRows) {
		return over("data rows", b.used(), int64(b.MaxRows))
	}
	return nil
}

// used is the rows held, the excess weight of the heavy ones counted in
// rows.
func (b *Budget) used() int64 {
	if b.RowBytes <= 0 {
		return b.rows
	}
	return b.rows + b.excess/b.RowBytes
}

// Rows is the number of rows held.
func (b *Budget) Rows() int {
	if b == nil {
		return 0
	}
	return int(b.rows)
}

// Load records n bytes returned by a Loader and fails past MaxLoadBytes.
func (b *Budget) Load(n int64) error {
	if b == nil {
		return nil
	}
	b.loaded += n
	if b.MaxLoadBytes > 0 && b.loaded > b.MaxLoadBytes {
		return over("loaded bytes", b.loaded, b.MaxLoadBytes)
	}
	return nil
}

// LoadLeft is how many more bytes may be loaded (MaxInt64 when unlimited).
func (b *Budget) LoadLeft() int64 {
	if b == nil || b.MaxLoadBytes <= 0 {
		return math.MaxInt64
	}
	return max(b.MaxLoadBytes-b.loaded, 0)
}

// RowsLeft is how many more rows may be created (MaxInt64 when unlimited),
// counting what the holding being replaced holds as free.
func (b *Budget) RowsLeft() int64 {
	if b == nil || b.MaxRows <= 0 {
		return math.MaxInt64
	}
	return max(int64(b.MaxRows)-max(b.used()-b.credit(), 0), 0)
}

// Points records n path points and fails past MaxPoints.
func (b *Budget) Points(n int64) error {
	if b == nil {
		return nil
	}
	b.points += n
	if b.MaxPoints > 0 && b.points > b.MaxPoints {
		return over("path points", b.points, b.MaxPoints)
	}
	return nil
}

// Canvas records n bytes of bitmap about to be allocated and fails past
// MaxCanvasBytes. It is cumulative: a bitmap repainted by a later run of the
// dataflow is charged again, which only makes the bound conservative.
func (b *Budget) Canvas(n int64) error {
	if b == nil {
		return nil
	}
	if n < 0 {
		n = 0
	}
	if b.MaxCanvasBytes > 0 && (n > b.MaxCanvasBytes || b.canvas+n > b.MaxCanvasBytes) {
		return over("canvas bytes", b.canvas+min(n, math.MaxInt64-b.canvas), b.MaxCanvasBytes)
	}
	b.canvas += n
	return nil
}

// Reserve is Budget.Reserve on the Budget in ctx.
func Reserve(ctx context.Context, n int64) error { return From(ctx).Reserve(n) }

// ReserveWeight is Budget.ReserveWeight on the Budget in ctx.
func ReserveWeight(ctx context.Context, rows, excess int64) error {
	return From(ctx).ReserveWeight(rows, excess)
}

// Ticker polls a context once per accumulated unit of work, for loops whose
// iterations cost very different amounts.
type Ticker struct {
	ctx context.Context
	n   int
}

// NewTicker makes a Ticker for ctx.
func NewTicker(ctx context.Context) *Ticker { return &Ticker{ctx: ctx} }

// Add records work units and returns the context's error once every 4096.
func (t *Ticker) Add(work int) error {
	t.n += work
	if t.n >= 4096 {
		t.n = 0
		return t.ctx.Err()
	}
	return nil
}
