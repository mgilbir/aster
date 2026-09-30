package budget

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestNilBudgetIsUnlimited(t *testing.T) {
	var b *Budget
	if b.Reserve(1<<60) != nil || b.AddRows(1<<30) != nil || b.Load(1<<60) != nil || b.Points(1<<60) != nil {
		t.Fatal("nil budget must not limit")
	}
	if From(context.Background()) != nil {
		t.Fatal("no budget in a plain context")
	}
}

func TestReserveBeforeAllocation(t *testing.T) {
	b := &Budget{MaxRows: 100}
	if err := b.AddRows(60); err != nil {
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
	if !errors.Is(b.AddRows(41), ErrLimit) {
		t.Fatal("AddRows past the limit must fail")
	}
}

func TestContextRoundTrip(t *testing.T) {
	b := &Budget{MaxRows: 5}
	ctx := With(context.Background(), b)
	if From(ctx) != b {
		t.Fatal("budget lost")
	}
	if !errors.Is(ReserveOut(ctx, 10, 2), ErrLimit) {
		t.Fatal("ReserveOut")
	}
	if ReserveOut(ctx, 10, 9) != nil {
		t.Fatal("net growth of 1 fits")
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
