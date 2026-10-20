package budget

import (
	"fmt"
	"sync/atomic"
)

// Allowance is a byte allowance that loads running at once charge together,
// as they read: the loaded-bytes allowance of one call, which the images PNG
// and PDF output fetch in parallel share (C19, audit 2026-10-18). It is safe
// for concurrent use. Bytes charged are kept, whether the load that read
// them succeeds or not, so whether a call goes past it depends only on the
// bytes its loads read, not on the order they read them in.
type Allowance struct {
	max  int64
	left atomic.Int64
}

// NewAllowance is an allowance of n bytes.
func NewAllowance(n int64) *Allowance {
	a := &Allowance{max: n}
	a.left.Store(n)
	return a
}

// Max is the allowance's size.
func (a *Allowance) Max() int64 { return a.max }

// Left is what is left of it, never negative.
func (a *Allowance) Left() int64 { return max(a.left.Load(), 0) }

// Take charges n bytes if they are left, and reports whether they were.
func (a *Allowance) Take(n int64) bool {
	for {
		left := a.left.Load()
		if n > left {
			return false
		}
		if a.left.CompareAndSwap(left, left-n) {
			return true
		}
	}
}

// Force charges n bytes whether or not they are left: bytes a bound of their
// own already held (a Vega run's data, within the same allowance).
func (a *Allowance) Force(n int64) { a.left.Add(-n) }

// Streams reports whether b charges a shared allowance as a loader reads
// (see Stream).
func (b *Budget) Streams() bool { return b != nil && b.Shared != nil }

// Stream charges n bytes a loader has just read to the shared allowance, and
// fails with ErrLimit when they are not left: the loader stops reading
// there. It does nothing without a shared allowance.
func (b *Budget) Stream(n int64) error {
	if !b.Streams() {
		return nil
	}
	if !b.Shared.Take(n) {
		b.sharedBound = true
		return fmt.Errorf("%w: the data and images of the call are over its %d loaded bytes (WithMemoryLimit raises it)", ErrLimit, b.Shared.Max())
	}
	b.streamed += n
	return nil
}

// Streamed is how many bytes Stream charged.
func (b *Budget) Streamed() int64 {
	if b == nil {
		return 0
	}
	return b.streamed
}

// SharedBound reports whether the shared allowance was the bound a loader
// met: what LoadLeft reported, or what Stream refused. Otherwise the load's
// own MaxLoadBytes was.
func (b *Budget) SharedBound() bool { return b != nil && b.sharedBound }
