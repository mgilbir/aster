package budget

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestNilBudgetIsUnlimited(t *testing.T) {
	var b *Budget
	var h Held
	if b.Reserve(1<<60) != nil || b.Hold(&h, 1<<30, 0) != nil || b.Load(1<<60) != nil || b.Points(1<<60) != nil {
		t.Fatal("nil budget must not limit")
	}
	if b.Replacing(&h) != nil {
		t.Fatal("nil budget has no holding")
	}
	if From(context.Background()) != nil {
		t.Fatal("no budget in a plain context")
	}
}

func TestReserveBeforeAllocation(t *testing.T) {
	b := &Budget{MaxRows: 100}
	var h Held
	if err := b.Hold(&h, 60, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(40); err != nil {
		t.Fatalf("40 rows fit: %v", err)
	}
	err := b.Reserve(41)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("41 rows must not fit: %v", err)
	}
	if b.Rows() != 60 {
		t.Fatalf("Reserve must not record: %d", b.Rows())
	}
	if !errors.Is(b.Reserve(math.MaxInt64), ErrLimit) {
		t.Fatal("huge reservation must fail without overflow")
	}
	var g Held
	if !errors.Is(b.Hold(&g, 41, 0), ErrLimit) {
		t.Fatal("holding past the limit must fail")
	}
}

// A holder that holds again replaces what it held: it is never charged
// twice, and it gives back what it no longer holds, never more.
func TestHoldReplaces(t *testing.T) {
	b := &Budget{MaxRows: 100}
	var a, c Held
	if b.Hold(&a, 60, 0) != nil || b.Hold(&a, 70, 0) != nil || b.Rows() != 70 || a.Rows() != 70 {
		t.Fatalf("a rerun of 70 rows holds 70, not 130: %d", b.Rows())
	}
	if b.Hold(&a, 10, 0) != nil || b.Rows() != 10 {
		t.Fatalf("holding 10 gives 60 back: %d", b.Rows())
	}
	if b.Hold(&c, 90, 0) != nil {
		t.Fatal("what a gave back is free")
	}
	if b.Hold(&a, -5, 0) != nil || b.Rows() != 90 || a.Rows() != 0 {
		t.Fatalf("nothing gives back more than it holds: %d", b.Rows())
	}
}

// What the holding being replaced holds is free to a reservation: a
// transform that runs again may make as many rows as it held before.
func TestReplacingCountsTheHoldingAsFree(t *testing.T) {
	b := &Budget{MaxRows: 100}
	var a, c Held
	_ = b.Hold(&a, 70, 0)
	_ = b.Hold(&c, 20, 0)
	if !errors.Is(b.Reserve(71), ErrLimit) || b.RowsLeft() != 10 {
		t.Fatal("80 held rows leave 10")
	}
	prev := b.Replacing(&a)
	if b.Reserve(80) != nil || !errors.Is(b.Reserve(81), ErrLimit) || b.RowsLeft() != 80 {
		t.Fatalf("a's 70 rows are being replaced: %d left", b.RowsLeft())
	}
	b.Replacing(prev)
	if !errors.Is(b.Reserve(11), ErrLimit) {
		t.Fatal("restored: a's rows count again")
	}
}

func TestContextRoundTrip(t *testing.T) {
	b := &Budget{MaxRows: 5}
	ctx := With(context.Background(), b)
	if From(ctx) != b {
		t.Fatal("budget lost")
	}
	if !errors.Is(Reserve(ctx, 6), ErrLimit) || Reserve(ctx, 5) != nil {
		t.Fatal("Reserve")
	}
}

func TestLoadAndPoints(t *testing.T) {
	b := &Budget{MaxLoadBytes: 10, MaxPoints: 3}
	if b.Load(10) != nil || !errors.Is(b.Load(1), ErrLimit) {
		t.Fatal("load limit")
	}
	if b.LoadLeft() != 0 {
		t.Fatal("LoadLeft")
	}
	if b.Points(3) != nil || !errors.Is(b.Points(1), ErrLimit) {
		t.Fatal("points limit")
	}
	if Mul(math.MaxInt64, 2) != math.MaxInt64 || Mul(3, 4) != 12 || Mul(-1, 4) != 0 {
		t.Fatal("Mul")
	}
	// 2^32 is math.MaxInt where int is 32 bits, not 0.
	if MulInt(math.MaxInt, 2) != math.MaxInt || MulInt(1<<16, 1<<16) <= 0 || MulInt(3, 4) != 12 || MulInt(-1, 4) != 0 {
		t.Fatal("MulInt")
	}
}

func TestRowExcessCountsInRows(t *testing.T) {
	b := &Budget{MaxRows: 10, RowBytes: 100}
	var h Held
	if err := b.Hold(&h, 4, 550); err != nil { // five rows' worth more
		t.Fatalf("9 rows' worth fit: %v", err)
	}
	if b.RowsLeft() != 1 || !errors.Is(b.Reserve(2), ErrLimit) {
		t.Fatalf("excess must take from what is left: %d", b.RowsLeft())
	}
	if !errors.Is(b.Hold(&h, 4, 750), ErrLimit) {
		t.Fatal("excess past the limit must fail")
	}
	if b.Hold(&h, 4, 0) != nil || b.RowsLeft() != 6 {
		t.Fatal("rows that shrank give their excess back")
	}
	if !errors.Is(b.ReserveWeight(1, 600), ErrLimit) || b.ReserveWeight(1, 500) != nil {
		t.Fatal("ReserveWeight counts the excess in rows")
	}
	var g Held
	if err := (&Budget{MaxRows: 1}).Hold(&g, 1, 1<<40); err != nil {
		t.Fatalf("without RowBytes only rows are counted: %v", err)
	}
}

func TestTickerPollsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tk := NewTicker(ctx)
	cancel()
	if err := tk.Add(10); err != nil {
		t.Fatalf("too early: %v", err)
	}
	if err := tk.Add(5000); err == nil {
		t.Fatal("cancelled context must surface")
	}
}

// A shared allowance is charged only with what is left of it, and bounds the
// LoadLeft of every Budget sharing it, which then says it was the bound.
func TestSharedAllowance(t *testing.T) {
	a := NewAllowance(100)
	b := &Budget{MaxLoadBytes: 1000, Shared: a}
	if got := b.LoadLeft(); got != 100 || !b.SharedBound() {
		t.Errorf("LoadLeft %d (shared bound %v), want 100 from the allowance", got, b.SharedBound())
	}
	if err := b.Stream(60); err != nil || b.Streamed() != 60 || a.Left() != 40 {
		t.Fatalf("Stream(60): %v; streamed %d, left %d", err, b.Streamed(), a.Left())
	}
	if err := b.Stream(41); !errors.Is(err, ErrLimit) || a.Left() != 40 {
		t.Errorf("Stream past it: %v, left %d", err, a.Left())
	}
	a.Force(50)
	if a.Left() != 0 || a.Take(1) {
		t.Errorf("forced past it: left %d", a.Left())
	}
	c := &Budget{MaxLoadBytes: 10, Shared: NewAllowance(100)}
	if c.LoadLeft() != 10 || c.SharedBound() {
		t.Error("the Budget's own bound, when smaller, is not the shared one")
	}
	if (&Budget{}).Stream(5) != nil || (*Budget)(nil).Streams() {
		t.Error("without a shared allowance, Stream does nothing")
	}
}
