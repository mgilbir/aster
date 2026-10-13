package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// The text a loaded body is copied to is charged to the call's ledger before
// it is made, small bodies and chunked ones alike, and refused past the limit.
func TestTextOfChargesTheLedger(t *testing.T) {
	for _, n := range []int{10, 3*textChunk + 5} {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 30})
		ctx := budget.WithLedger(context.Background(), l)
		s, err := textOf(ctx, make([]byte, n))
		if err != nil || len(s) != n {
			t.Fatalf("%d bytes: %d, %v", n, len(s), err)
		}
		if got := l.Bytes(); got != int64(n) {
			t.Errorf("%d bytes copied, %d charged", n, got)
		}
		small := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: int64(n) - 1})
		if _, err := textOf(budget.WithLedger(context.Background(), small), make([]byte, n)); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%d bytes under a %d-byte ledger: %v, want ErrLimit", n, n-1, err)
		}
	}
}
