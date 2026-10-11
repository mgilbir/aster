package budget

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestNilLedgerIsUnlimited(t *testing.T) {
	var l *Ledger
	if l.Alloc(math.MaxInt64) != nil || l.Work(math.MaxInt64) != nil || l.Err() != nil {
		t.Fatal("a nil ledger must not limit")
	}
	l.Free(1)
	l.MustAlloc(1 << 62)
	l.MustWork(1 << 62)
	if l.Bytes() != 0 || l.PeakBytes() != 0 || l.WorkDone() != 0 {
		t.Fatal("a nil ledger records nothing")
	}
	if LedgerFrom(context.Background()) != nil || LedgerFrom(nil) != nil { //nolint:staticcheck // nil is what the API accepts
		t.Fatal("no ledger in a plain context")
	}
}

func TestLedgerAllocBeforeAllocation(t *testing.T) {
	l := NewLedger(context.Background(), LedgerLimits{MaxBytes: 100})
	if err := l.Alloc(60); err != nil {
		t.Fatal(err)
	}
	if err := l.Alloc(40); err != nil {
		t.Fatalf("100 bytes fit: %v", err)
	}
	err := l.Alloc(1)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("101 bytes must not fit: %v", err)
	}
	if l.Bytes() != 100 {
		t.Fatalf("a refused charge must record nothing: %d", l.Bytes())
	}
	l.Free(70)
	if l.Bytes() != 30 || l.PeakBytes() != 100 {
		t.Fatalf("free: %d, peak %d", l.Bytes(), l.PeakBytes())
	}
	if !errors.Is(l.Alloc(math.MaxInt64), ErrLimit) || l.Bytes() != 30 {
		t.Fatal("a huge charge must fail without wrapping")
	}
	l.Free(1 << 40)
	if l.Bytes() != 0 {
		t.Fatalf("free never goes below zero: %d", l.Bytes())
	}
	if l.Alloc(-5) != nil || l.Bytes() != 0 {
		t.Fatal("a negative charge is nothing")
	}

	u := NewLedger(nil, LedgerLimits{}) //nolint:staticcheck // a nil ctx is documented as never done
	if u.Alloc(math.MaxInt64) != nil || u.Alloc(math.MaxInt64) != nil || u.Bytes() != math.MaxInt64 {
		t.Fatalf("an unlimited ledger saturates: %d", u.Bytes())
	}
}

func TestLedgerMustPanicsWithStop(t *testing.T) {
	l := NewLedger(context.Background(), LedgerLimits{MaxBytes: 10})
	defer func() {
		s, ok := recover().(*Stop)
		if !ok || !errors.Is(s, ErrLimit) {
			t.Fatalf("MustAlloc past the limit must panic with a *Stop wrapping ErrLimit, got %v", s)
		}
	}()
	l.MustAlloc(11)
	t.Fatal("not reached")
}

// Work and a Meter return the context's error at most pollEvery units after
// the call is over, whatever the size of the charges.
func TestLedgerWorkPollsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := NewLedger(ctx, LedgerLimits{})
	cctx := WithLedger(ctx, l)
	if LedgerFrom(cctx) != l {
		t.Fatal("ledger lost")
	}
	for range 3 * pollEvery {
		if err := l.Work(1); err != nil {
			t.Fatalf("not over yet: %v", err)
		}
	}
	cancel()
	units := 0
	for l.Work(1) == nil {
		units++
		if units > pollEvery {
			t.Fatalf("Work missed the cancellation for %d units", units)
		}
	}
	if !errors.Is(l.Work(pollEvery), context.Canceled) {
		t.Fatal("a large charge polls at once")
	}
	for _, c := range []context.Context{cctx, ctx} { // with a ledger, and without
		m := NewMeter(c)
		units = 0
		for m.Work(1) == nil {
			units++
			if units > pollEvery {
				t.Fatalf("Meter missed the cancellation for %d units", units)
			}
		}
		if err := m.Work(pollEvery); !errors.Is(err, context.Canceled) {
			t.Fatalf("Meter: %v", err)
		}
	}
	if l.WorkDone() < 3*pollEvery {
		t.Fatalf("work not counted: %d", l.WorkDone())
	}
	var nm Meter // the zero Meter (no context) never stops
	for range 2 {
		if nm.Work(math.MaxInt64) != nil {
			t.Fatal("zero Meter")
		}
	}
	defer func() {
		if s, ok := recover().(*Stop); !ok || !errors.Is(s, context.Canceled) {
			t.Fatalf("MustWork must panic with a *Stop wrapping the context's error, got %v", s)
		}
	}()
	l.MustWork(pollEvery)
}

// A deadline is honoured within a few milliseconds by a loop that meters each
// of its steps, the steps here costing about what a loop's step does.
func TestMeterHonoursDeadline(t *testing.T) {
	const timeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ctx = WithLedger(ctx, NewLedger(ctx, LedgerLimits{}))
	t0 := time.Now()
	m := NewMeter(ctx)
	x := 1.0
	var err error
	for err == nil {
		for range 50 { // ~100 ns of arithmetic
			x = x*1.0000001 + 1e-9
		}
		err = m.Work(1)
	}
	late := time.Since(t0) - timeout
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// A poll every 1024 steps of ~100 ns is a poll every ~0.1 ms; the slack
	// is for a loaded machine's scheduling, not for the meter.
	if late > 20*time.Millisecond {
		t.Errorf("the deadline was honoured %v late (x=%v)", late, x)
	}
}

// The byte meter holds under concurrent charges: the total never passes the
// limit and every byte freed comes back.
func TestLedgerConcurrent(t *testing.T) {
	l := NewLedger(context.Background(), LedgerLimits{MaxBytes: 1000})
	var wg sync.WaitGroup
	var refused sync.Map
	for g := range 16 {
		wg.Go(func() {
			for i := range 2000 {
				if err := l.Alloc(100); err != nil {
					refused.Store(g, true)
					continue
				}
				if b := l.Bytes(); b > 1000 {
					t.Errorf("bytes %d past the limit", b)
				}
				l.Work(int64(i))
				l.Free(100)
			}
		})
	}
	wg.Wait()
	if l.Bytes() != 0 || l.PeakBytes() > 1000 || l.PeakBytes() < 100 {
		t.Fatalf("bytes %d, peak %d", l.Bytes(), l.PeakBytes())
	}
}

func BenchmarkMeterWork(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithLedger(ctx, NewLedger(ctx, LedgerLimits{}))
	m := NewMeter(ctx)
	for b.Loop() {
		if m.Work(1) != nil {
			b.Fatal()
		}
	}
}

func BenchmarkLedgerWork(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := NewLedger(ctx, LedgerLimits{})
	for b.Loop() {
		if l.Work(1) != nil {
			b.Fatal()
		}
	}
}

func BenchmarkLedgerAllocFree(b *testing.B) {
	l := NewLedger(context.Background(), LedgerLimits{MaxBytes: 1 << 30})
	for b.Loop() {
		if l.Alloc(64) != nil {
			b.Fatal()
		}
		l.Free(64)
	}
}
