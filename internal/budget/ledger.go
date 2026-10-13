package budget

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
)

// Ledger is the bound of one public call, from the specification's JSON to the
// last byte of PNG or PDF, carried in the call's context.Context so every
// layer reaches it without a signature change (LedgerFrom). It meters two
// things where they happen, rather than at a stage's boundary:
//
//   - work: the steps a specification makes the engine take, charged by every
//     loop whose trip count the input controls (Work, or a Meter in a tight
//     loop). Charging polls the call's context, so a timeout or a Close ends
//     the loop that is running, whichever it is. Work is counted, not capped:
//     time is bounded by WithTimeout, and work units are not comparable
//     across packages.
//   - bytes: the memory the input makes the engine allocate, charged before
//     the allocation (Alloc) and given back when it is released (Free), and
//     failing with ErrLimit past MaxBytes.
//
// The meters older than the ledger stay where they are: data rows, loaded
// bytes, path points and transform bitmaps (Budget, one per Vega render), the
// rasterizer's canvas, shaping work, SVG size. Each bounds its own class of
// memory with its own semantics (rows are replaced, canvases come and go).
// The byte meter bounds what none of them does, so an allocation is charged
// to exactly one meter: its own if it has one, the ledger's bytes otherwise.
//
// A nil *Ledger is valid and unlimited, and never stops: code below the public
// API (tests, tools) runs without one. A Ledger is safe for concurrent use
// (images are fetched in parallel); its methods are a few atomic operations,
// with no allocation.
type Ledger struct {
	ctx      context.Context
	maxBytes int64 // <= 0: unlimited

	bytes atomic.Int64 // charged and not freed
	peak  atomic.Int64 // the most bytes ever charged at once
	work  atomic.Int64 // work units charged
}

// LedgerLimits configures a Ledger.
type LedgerLimits struct {
	// MaxBytes bounds the bytes charged with Alloc and not yet given back
	// with Free (<= 0: unlimited).
	MaxBytes int64
}

// pollEvery is how many work units pass between two polls of the context.
// A unit is one step of a loop whose iterations each cost O(1), tens of
// nanoseconds to a microsecond, so a poll comes every millisecond of work at
// most; a loop whose steps cost more charges more units per step.
const pollEvery = 1 << 10

// maxCharge caps one charge of work, so the count cannot wrap: work is
// counted to be reported, never compared with a bound.
const maxCharge = 1 << 40

// NewLedger returns a Ledger for a call that runs under ctx (nil: one that is
// never done).
func NewLedger(ctx context.Context, lim LedgerLimits) *Ledger {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Ledger{ctx: ctx, maxBytes: lim.MaxBytes}
}

type ledgerKey struct{}

// WithLedger returns ctx carrying l.
func WithLedger(ctx context.Context, l *Ledger) context.Context {
	return context.WithValue(ctx, ledgerKey{}, l)
}

// LedgerFrom returns the Ledger in ctx, or nil (which every method accepts as
// unlimited).
func LedgerFrom(ctx context.Context) *Ledger {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(ledgerKey{}).(*Ledger)
	return l
}

// Work charges n units of work and returns the call's context error (wrapping
// context.DeadlineExceeded or context.Canceled) once the call is over. The
// context is polled every pollEvery units, and on every charge of at least
// that many, so a loop that charges each of its steps stops within about a
// millisecond of the deadline. Charge a coarse step (a row, a feature, a
// glyph) here; in a tight loop of cheap steps, charge a Meter instead, which
// costs no atomic operation per step.
func (l *Ledger) Work(n int64) error {
	if l == nil {
		return nil
	}
	n = min(max(n, 0), maxCharge)
	w := l.work.Add(n)
	if n >= pollEvery || (w-n)/pollEvery != w/pollEvery {
		return l.ctx.Err()
	}
	return nil
}

// Alloc charges n bytes the caller is about to allocate, and fails with an
// error wrapping ErrLimit, recording nothing, when they would bring the bytes
// charged and not freed past MaxBytes. Call it before the allocation, with
// the size the allocation will really have (capacity, not length, and the
// bytes behind pointers it will own). The arithmetic saturates: a size
// computed from hostile input cannot wrap into a small one (use Mul).
func (l *Ledger) Alloc(n int64) error {
	if l == nil || n <= 0 {
		return nil
	}
	for {
		cur := l.bytes.Load()
		next := cur + min(n, math.MaxInt64-cur)
		if l.maxBytes > 0 && next > l.maxBytes {
			return fmt.Errorf("%w: memory of %d bytes exceeds the limit of %d", ErrLimit, next, l.maxBytes)
		}
		if l.bytes.CompareAndSwap(cur, next) {
			for {
				p := l.peak.Load()
				if next <= p || l.peak.CompareAndSwap(p, next) {
					return nil
				}
			}
		}
	}
}

// Free gives back n bytes charged with Alloc, once the memory is released (no
// longer reachable from the call). The bytes charged never go below zero.
func (l *Ledger) Free(n int64) {
	if l == nil || n <= 0 {
		return
	}
	for {
		cur := l.bytes.Load()
		if l.bytes.CompareAndSwap(cur, max(cur-n, 0)) {
			return
		}
	}
}

// MustWork is Work for code that cannot return an error (a scenegraph
// callback, a geographic stream sink, text measurement): it panics with a
// *Stop wrapping the error, which the public API's recover turns back into
// the call's error. Recover it where an error can be returned.
func (l *Ledger) MustWork(n int64) {
	if err := l.Work(n); err != nil {
		panic(&Stop{Err: err})
	}
}

// MustAlloc is Alloc for code that cannot return an error; see MustWork.
func (l *Ledger) MustAlloc(n int64) {
	if err := l.Alloc(n); err != nil {
		panic(&Stop{Err: err})
	}
}

// Err is the call's context error, nil while it runs: an explicit poll, for
// a stage boundary.
func (l *Ledger) Err() error {
	if l == nil {
		return nil
	}
	return l.ctx.Err()
}

// Bytes is what is charged and not freed.
func (l *Ledger) Bytes() int64 {
	if l == nil {
		return 0
	}
	return l.bytes.Load()
}

// Room is how many more bytes Alloc would accept now: MaxBytes less what is
// charged, never negative, and math.MaxInt64 for an unlimited (or nil)
// Ledger. It bounds memory that a library allocates where it cannot be
// charged piece by piece (a regular expression search's backtracking): such
// memory never takes the call past MaxBytes, although the ledger does not
// count it.
func (l *Ledger) Room() int64 {
	if l == nil || l.maxBytes <= 0 {
		return math.MaxInt64
	}
	return max(l.maxBytes-l.bytes.Load(), 0)
}

// PeakBytes is the most bytes charged at once.
func (l *Ledger) PeakBytes() int64 {
	if l == nil {
		return 0
	}
	return l.peak.Load()
}

// WorkDone is the work charged so far.
func (l *Ledger) WorkDone() int64 {
	if l == nil {
		return 0
	}
	return l.work.Load()
}

// Meter charges the work of one loop to the Ledger in a context, keeping the
// count locally between polls, so that a step costs an addition and a
// comparison: the form to use in a tight loop. Without a Ledger in the
// context it polls the context itself, so the loop stops on cancellation
// either way. A Meter is a value owned by one goroutine; make it with
// NewMeter where the loop starts.
//
//	m := budget.NewMeter(ctx)
//	for ... {
//		if err := m.Work(1); err != nil {
//			return err
//		}
//	}
type Meter struct {
	ctx context.Context
	l   *Ledger
	n   int64
}

// NewMeter returns a Meter charging the Ledger in ctx (polling ctx itself
// when there is none; a nil ctx is never done).
func NewMeter(ctx context.Context) Meter {
	return Meter{ctx: ctx, l: LedgerFrom(ctx)}
}

// Work charges n units and returns the context's error once the call is over;
// see Ledger.Work. The context is polled every pollEvery units.
func (m *Meter) Work(n int64) error {
	m.n += min(max(n, 0), maxCharge)
	if m.n < pollEvery {
		return nil
	}
	n, m.n = m.n, 0
	if m.l != nil {
		m.l.work.Add(n)
		return m.l.ctx.Err()
	}
	if m.ctx != nil {
		return m.ctx.Err()
	}
	return nil
}

// MustWork is Work for code that cannot return an error; see Ledger.MustWork.
func (m *Meter) MustWork(n int64) {
	if err := m.Work(n); err != nil {
		panic(&Stop{Err: err})
	}
}
