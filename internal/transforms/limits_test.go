package transforms

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
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
	if err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("poly regression ignored the context: %v after %v", err, time.Since(start))
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	start = time.Now()
	_, err = Aggregate(ctx2, data, AggregateParams{Measures: []Measure{{Op: "ci0", Field: FieldOf("y")}}})
	if err == nil || time.Since(start) > 2*time.Second {
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
