package transforms

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// countpattern's two patterns are paid for with the call's ledger before
// they are compiled, and given back when it returns: a pattern past the
// ledger is refused with ErrLimit having cost a parse, not a compile of tens
// of megabytes. The test counts the bytes allocated, which do not depend on
// the machine (the race detector allocates on its own).
func TestCountPatternPaysBeforeCompiling(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	const big = "(a{1000}){150}"
	cost, err := jsval.EstimatePattern(big, "g")
	if err != nil {
		t.Fatal(err)
	}
	data := []jsval.Value{obj("t", jsval.Str("a b"))}
	run := func(led *budget.Ledger, p CountPatternParams) (n uint64, err error) {
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		_, err = CountPattern(budget.WithLedger(context.Background(), led), data, p)
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc, err
	}
	roomy := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 2 * cost.Bytes})
	if n, err := run(roomy, CountPatternParams{Field: FieldOf("t"), Pattern: big}); err != nil || n < 16<<20 {
		t.Fatalf("with room: %v, %d bytes allocated", err, n)
	}
	if roomy.Bytes() != 0 || roomy.PeakBytes() < cost.Bytes {
		t.Errorf("the ledger holds %d bytes after the call, %d at its peak", roomy.Bytes(), roomy.PeakBytes())
	}
	for _, p := range []CountPatternParams{{Field: FieldOf("t"), Pattern: big}, {Field: FieldOf("t"), Stopwords: big}} {
		led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: cost.Bytes / 2})
		if n, err := run(led, p); !errors.Is(err, budget.ErrLimit) || n > 1<<20 {
			t.Errorf("%+v past the ledger: %v, %d bytes allocated", p, err, n)
		}
		if led.Bytes() != 0 {
			t.Errorf("the ledger holds %d bytes after a refusal", led.Bytes())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CountPattern(ctx, data, CountPatternParams{Field: FieldOf("t"), Pattern: big}); !errors.Is(err, context.Canceled) {
		t.Errorf("after the deadline: %v", err)
	}
}
