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

// Budget is the mutable state of one render. It is not safe for concurrent
// use: a render is single-threaded.
type Budget struct {
	// MaxRows bounds the net number of data rows alive across the render
	// (<= 0: unlimited), MaxLoadBytes the bytes the Loader returned in total,
	// MaxPoints the path points geographic marks may emit.
	MaxRows      int
	MaxLoadBytes int64
	MaxPoints    int64
	// MaxCanvasBytes bounds the pixel memory of the bitmaps transforms paint
	// (the heatmap's canvases), in total across the render.
	MaxCanvasBytes int64
	// RowBytes is the memory one counted row stands for (0: rows are only
	// counted). With it set, AddRowExcess charges the bytes by which rows
	// outweigh that, so that MaxRows bounds their memory (MaxRows * RowBytes)
	// and not just their number.
	RowBytes int64

	rows   int64
	excess int64
	loaded int64
	points int64
	canvas int64
}

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

// Reserve checks that n more rows (net of the rows a transform replaces) fit,
// without recording them; the dataflow records the real growth when the
// transform returns. Call it before allocating.
func (b *Budget) Reserve(n int64) error {
	if b == nil || b.MaxRows <= 0 || n <= 0 {
		return nil
	}
	if n > int64(b.MaxRows) || b.used()+n > int64(b.MaxRows) {
		return over("data rows", b.used()+min(n, math.MaxInt64-b.used()), int64(b.MaxRows))
	}
	return nil
}

// ReserveWeight is Reserve for a transform that grows the weight of the rows
// as well as their number: it checks that rows more rows and excess more bytes
// of row excess (see AddRowExcess) fit, without recording them.
func (b *Budget) ReserveWeight(rows, excess int64) error {
	if b == nil || b.RowBytes <= 0 {
		return b.Reserve(rows)
	}
	return b.Reserve(rows + excess/b.RowBytes)
}

// AddRows records n more rows (negative when rows were dropped) and fails when
// the total exceeds MaxRows.
func (b *Budget) AddRows(n int) error {
	if b == nil {
		return nil
	}
	b.rows += int64(n)
	return b.checkRows()
}

// AddRowExcess records n more bytes (negative when rows were dropped or
// shrank) by which the rows weigh more than the RowBytes each is counted for,
// and fails when the rows and their excess exceed MaxRows.
func (b *Budget) AddRowExcess(n int64) error {
	if b == nil || b.RowBytes <= 0 {
		return nil
	}
	b.excess = max(b.excess+n, 0)
	return b.checkRows()
}

func (b *Budget) checkRows() error {
	if b.MaxRows > 0 && b.used() > int64(b.MaxRows) {
		return over("data rows", b.used(), int64(b.MaxRows))
	}
	return nil
}

// used is the rows recorded, the excess weight of the heavy ones counted in
// rows.
func (b *Budget) used() int64 {
	if b.RowBytes <= 0 {
		return b.rows
	}
	return b.rows + b.excess/b.RowBytes
}

// Rows is the number of rows recorded so far.
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

// RowsLeft is how many more rows may be created (MaxInt64 when unlimited).
func (b *Budget) RowsLeft() int64 {
	if b == nil || b.MaxRows <= 0 {
		return math.MaxInt64
	}
	return max(int64(b.MaxRows)-b.used(), 0)
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

// ReserveOut checks that a transform producing out rows from in input rows
// fits; the input rows are already counted.
func ReserveOut(ctx context.Context, out, in int64) error {
	return From(ctx).Reserve(out - in)
}

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
