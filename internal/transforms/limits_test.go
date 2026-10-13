package transforms

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

func budgetCtx(rows int) context.Context {
	return budget.With(context.Background(), &budget.Budget{MaxRows: rows})
}

func rowsOf(n int, f func(i int) map[string]jsval.Value) []jsval.Value {
	out := make([]jsval.Value, n)
	for i := range out {
		o := jsval.NewObject(2)
		for k, v := range f(i) {
			o.Set(k, v)
		}
		out[i] = jsval.Obj(o)
	}
	return out
}

func TestImputeRespectsRowBudgetBeforeAllocating(t *testing.T) {
	data := rowsOf(3000, func(i int) map[string]jsval.Value {
		return map[string]jsval.Value{"g": jsval.Int(i), "k": jsval.Int(i)}
	})
	_, err := Impute(budgetCtx(100_000), data, ImputeParams{
		GroupBy: []Field{FieldOf("g")}, Key: FieldOf("k"), Field: FieldOf("v"),
	})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("3000 groups x 3000 keys must exceed 100k rows: %v", err)
	}
}

func TestFlattenFoldSequenceCrossKDEQuantileLimits(t *testing.T) {
	ctx := budgetCtx(1000)
	if _, err := Sequence(ctx, SequenceParams{Stop: 5000}); !errors.Is(err, ErrLimit) {
		t.Errorf("sequence: %v", err)
	}
	arr := make([]jsval.Value, 600)
	for i := range arr {
		arr[i] = jsval.Int(i)
	}
	rows := rowsOf(10, func(i int) map[string]jsval.Value { return map[string]jsval.Value{"a": jsval.Arr(arr)} })
	if _, err := Flatten(ctx, rows, FlattenParams{Fields: []Field{FieldOf("a")}}); !errors.Is(err, ErrLimit) {
		t.Errorf("flatten: %v", err)
	}
	wide := rowsOf(600, func(i int) map[string]jsval.Value { return map[string]jsval.Value{"a": jsval.Int(i)} })
	if _, err := Fold(ctx, wide, FoldParams{Fields: []Field{FieldOf("a"), FieldOf("a"), FieldOf("a")}}); !errors.Is(err, ErrLimit) {
		t.Errorf("fold: %v", err)
	}
	if _, err := Cross(ctx, wide, CrossParams{}); !errors.Is(err, ErrLimit) {
		t.Errorf("cross: %v", err)
	}
	if _, err := Cross(ctx, wide, CrossParams{Filter: func(jsval.Value) bool { return true }}); !errors.Is(err, ErrLimit) {
		t.Errorf("cross with filter: %v", err)
	}
	grouped := rowsOf(300, func(i int) map[string]jsval.Value {
		return map[string]jsval.Value{"g": jsval.Int(i), "v": jsval.Num(float64(i))}
	})
	if _, err := KDE(ctx, grouped, KDEParams{GroupBy: []Field{FieldOf("g")}, Field: FieldOf("v"), Steps: 100}); !errors.Is(err, ErrLimit) {
		t.Errorf("kde: %v", err)
	}
	if _, err := Quantile(ctx, grouped, QuantileParams{GroupBy: []Field{FieldOf("g")}, Field: FieldOf("v")}); !errors.Is(err, ErrLimit) {
		t.Errorf("quantile: %v", err)
	}
}

func TestPivotWorkIsBounded(t *testing.T) {
	data := rowsOf(10000, func(i int) map[string]jsval.Value {
		return map[string]jsval.Value{"k": jsval.Int(i), "v": jsval.Int(1)}
	})
	_, err := Pivot(context.Background(), data, PivotParams{Field: FieldOf("k"), Value: FieldOf("v")})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("10k rows x 10k columns: %v", err)
	}
}

func TestLongLoopsStopOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	data := rowsOf(50000, func(i int) map[string]jsval.Value {
		return map[string]jsval.Value{"x": jsval.Num(float64(i) / 1000), "y": jsval.Num(math.Sin(float64(i)))}
	})
	start := time.Now()
	_, err := Regression(ctx, data, RegressionParams{Method: "poly", Order: 100, X: FieldOf("x"), Y: FieldOf("y")})
	testutil.Timing(t, "poly regression stops at a 50ms deadline", time.Since(start), testutil.Scaled(2*time.Second))
	if err == nil || time.Since(start) > testutil.Scaled(2*time.Second) {
		t.Errorf("poly regression ignored the context: %v after %v", err, time.Since(start))
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	start = time.Now()
	_, err = Aggregate(ctx2, data, AggregateParams{Measures: []Measure{{Op: "ci0", Field: FieldOf("y")}}})
	testutil.Timing(t, "bootstrap stops at a 50ms deadline", time.Since(start), testutil.Scaled(2*time.Second))
	if err == nil || time.Since(start) > testutil.Scaled(2*time.Second) {
		t.Errorf("bootstrap ignored the context: %v after %v", err, time.Since(start))
	}
}

func TestFoldRefusesHeavyCopiesBeforeMakingThem(t *testing.T) {
	// 200 rows of 20 fields folded four ways: 800 rows, which fit 1000 rows,
	// but each copy weighs about 2 KiB against the 512 bytes it is counted for.
	wide := rowsOf(200, func(i int) map[string]jsval.Value {
		m := map[string]jsval.Value{}
		for f := range 20 {
			m[string(rune('a'+f))] = jsval.Int(i)
		}
		return m
	})
	fold := FoldParams{Fields: []Field{FieldOf("a"), FieldOf("b"), FieldOf("c"), FieldOf("d")}}
	if _, err := Fold(budgetCtx(1000), wide, fold); err != nil {
		t.Fatalf("rows only: %v", err)
	}
	ctx := budget.With(context.Background(), &budget.Budget{MaxRows: 1000, RowBytes: 512})
	if _, err := Fold(ctx, wide, fold); !errors.Is(err, ErrLimit) {
		t.Errorf("by weight: %v", err)
	}
}

// TestLimitMessagesNameTheNumber is the audit's C99: a count past a limit
// was clamped (or wrapped) to an int before it was printed, so the message
// named 2147483647 or MinInt64 instead of what the specification asked for.
func TestLimitMessagesNameTheNumber(t *testing.T) {
	ctx := context.Background()
	check := func(what string, err error, want string) {
		t.Helper()
		if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it to name %s", what, err, want)
		}
	}
	_, err := Sequence(ctx, SequenceParams{Start: 0, Stop: 1e300})
	check("sequence", err, "sequence length 1e+300 exceeds")
	_, err = Bin(BinConfig{Extent: [2]float64{0, 1}, MaxBins: 1e300})
	check("bin", err, "maxbins 1e+300 exceeds")
	_, err = Quantile(ctx, nil, QuantileParams{Field: FieldOf("x"), Step: 1e-300})
	check("quantile", err, "quantile probabilities 9.9999999999999e+299 exceeds")
	_, err = SampleCurve(func(float64) float64 { return 0 }, [2]float64{0, 1}, 1e300, 1e300)
	check("density steps", err, "steps 1e+300 exceeds")
}

// TestCountPatternReservesRows: every distinct token is an output row, which
// nothing reserved before the rows existed.
func TestCountPatternReservesRows(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "w%d ", i)
	}
	data := []jsval.Value{obj("t", jsval.Str(sb.String()))}
	if _, err := CountPattern(budgetCtx(1000), data, CountPatternParams{Field: FieldOf("t")}); !errors.Is(err, ErrLimit) {
		t.Fatalf("5,000 distinct tokens under 1,000 rows: %v", err)
	}
}
