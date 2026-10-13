package transforms

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// countpattern's two patterns are paid for with the call's ledger before
// they are parsed and compiled, and given back when it returns: a pattern
// past the ledger is refused with ErrLimit having cost a parse, not a
// compile of tens of megabytes. The test counts the bytes allocated, which do not depend on
// the machine (the race detector allocates on its own).
func TestCountPatternPaysBeforeCompiling(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	const big = "(a{1000}){150}"
	ref, err := jsval.NewPattern(context.Background(), big, "g", func(int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	size := ref.Bytes()
	data := []jsval.Value{obj("t", jsval.Str("a b"))}
	run := func(led *budget.Ledger, p CountPatternParams) (n uint64, err error) {
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		_, err = CountPattern(budget.WithLedger(context.Background(), led), data, p)
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc, err
	}
	roomy := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 2 * size})
	if n, err := run(roomy, CountPatternParams{Field: FieldOf("t"), Pattern: big}); err != nil || n < 8<<20 { // 13 MB on 32-bit platforms
		t.Fatalf("with room: %v, %d bytes allocated", err, n)
	}
	if roomy.Bytes() != 0 || roomy.PeakBytes() < size {
		t.Errorf("the ledger holds %d bytes after the call, %d at its peak", roomy.Bytes(), roomy.PeakBytes())
	}
	for _, p := range []CountPatternParams{{Field: FieldOf("t"), Pattern: big}, {Field: FieldOf("t"), Stopwords: big}} {
		led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: size / 2})
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

// countpattern's searches hold for backtracking at most what the call's
// ledger has left, and run out of it, or of steps, as an engine limit
// (ErrLimit), as a pattern past goecma262's limits is; they stop at the
// call's deadline, which goecma262 polls as it matches.
func TestCountPatternSearchBounds(t *testing.T) {
	// The search holds between 2 and 4 MiB for backtracking.
	data := []jsval.Value{obj("t", jsval.Str(strings.Repeat("ab", 50000)))}
	p := CountPatternParams{Field: FieldOf("t"), Pattern: "^(a|b|ab)*$"}
	run := func(ctx context.Context, room int64) error {
		led := budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: 64 << 20})
		if err := led.Alloc(64<<20 - room); err != nil {
			t.Fatal(err)
		}
		_, err := CountPattern(budget.WithLedger(ctx, led), data, p)
		return err
	}
	if err := run(context.Background(), 32<<20); err != nil {
		t.Fatalf("with room: %v", err)
	}
	if err := run(context.Background(), 1<<20); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("with 1 MiB left: %v", err)
	}
	// The same search as a stopword, on the one token [\w']+ finds.
	p = CountPatternParams{Field: FieldOf("t"), Stopwords: "(a|b|ab)*"}
	if err := run(context.Background(), 32<<20); err != nil {
		t.Fatalf("stopwords with room: %v", err)
	}
	if err := run(context.Background(), 1<<20); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("stopwords with 1 MiB left: %v", err)
	}
	p = CountPatternParams{Field: FieldOf("t"), Pattern: "^(a|b|ab)*$"}
	if err := run(newPollsLeft(8), 32<<20); !errors.Is(err, context.Canceled) || errors.Is(err, budget.ErrLimit) {
		t.Errorf("past the deadline: %v", err)
	}
	_, err := CountPattern(context.Background(), data, CountPatternParams{Field: FieldOf("t"), Stopwords: "a{10001}"})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("past goecma262's quantifier limit: %v", err)
	}
	_, err = CountPattern(context.Background(), data, CountPatternParams{Field: FieldOf("t"), Stopwords: "("})
	if err == nil || errors.Is(err, budget.ErrLimit) {
		t.Errorf("an invalid pattern: %v", err)
	}
}

// pollsLeft is a context that is done from its n-th poll on: a deadline
// reached in the middle of a search, without a clock.
type pollsLeft struct {
	context.Context
	n    atomic.Int64
	done chan struct{}
}

func newPollsLeft(n int64) *pollsLeft {
	c := &pollsLeft{Context: context.Background(), done: make(chan struct{})}
	c.n.Store(n)
	return c
}

func (c *pollsLeft) Done() <-chan struct{} { return c.done }

func (c *pollsLeft) Err() error {
	if c.n.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}
