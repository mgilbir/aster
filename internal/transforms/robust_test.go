package transforms

import (
	"context"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// hostileTuples are inputs no well-formed dataset has: non-object tuples,
// missing and mistyped fields, NaN and infinities.
func hostileTuples() []jsval.Value {
	return []jsval.Value{
		jsval.Null, jsval.Undefined, jsval.Num(3), jsval.Str("s"), jsval.ArrOf(jsval.Num(1), jsval.Num(2)),
		jsval.Obj(nil),
		obj("x", jsval.Num(math.NaN()), "y", jsval.Num(math.Inf(1)), "g", jsval.Null),
		obj("x", jsval.Str("abc"), "y", jsval.ArrOf(jsval.Num(1)), "g", jsval.Obj(nil)),
		obj("x", jsval.Num(-math.MaxFloat64), "y", jsval.Num(math.MaxFloat64), "g", jsval.Str("a")),
		obj("x", jsval.Timestamp(math.NaN()), "y", jsval.Bool(true), "g", jsval.Num(1)),
		obj("x", jsval.Num(1), "y", jsval.Num(2), "g", jsval.Str("a")),
		obj("x", jsval.Num(2), "y", jsval.Num(4), "g", jsval.Str("a")),
		obj("x", jsval.Num(3), "y", jsval.Num(5), "g", jsval.Str("b")),
	}
}

// TestNoPanicOnHostileInput runs the data transforms over hostile tuples. The
// results are not checked, only that nothing panics or hangs.
func TestNoPanicOnHostileInput(t *testing.T) {
	ctx := context.Background()
	x, y, g := FieldOf("x"), FieldOf("y"), FieldOf("g")
	steps := map[string]func(data []jsval.Value){
		"aggregate": func(d []jsval.Value) {
			Aggregate(ctx, d, AggregateParams{GroupBy: []Field{g}, Cross: true, Measures: []Measure{
				{Op: "sum", Field: x}, {Op: "median", Field: y}, {Op: "argmin", Field: x}, {Op: "ci0", Field: y},
				{Op: "distinct", Field: g}, {Op: "exponential", Field: x, Param: 0.5}, {Op: "values", Field: x}}})
		},
		"window": func(d []jsval.Value) {
			Window(ctx, d, WindowParams{Sort: CompareBy([]Field{x}, nil), GroupBy: []Field{g}, Frame: []FrameBound{{Offset: -1}, {Offset: 2}},
				Ops: []WindowOpSpec{{Op: "sum", Field: y}, {Op: "rank"}, {Op: "lag", Field: x, Param: math.NaN()}, {Op: "median", Field: x},
					{Op: "next_value", Field: y, Param: math.NaN()}, {Op: "nth_value", Field: x, Param: 2}, {Op: "percent_rank"}}})
		},
		"collect": func(d []jsval.Value) { Collect(ctx, d, CompareBy([]Field{x, y}, []string{Desc})) },
		"joinagg": func(d []jsval.Value) {
			JoinAggregate(ctx, d, JoinAggregateParams{GroupBy: []Field{g}, Measures: []Measure{{Op: "mean", Field: x}, {Op: "max", Field: y}}})
		},
		"stack": func(d []jsval.Value) {
			Stack(ctx, d, StackParams{Field: y, GroupBy: []Field{g}, Offset: StackNormalize})
		},
		"pie": func(d []jsval.Value) { Pie(ctx, d, PieParams{Field: y, Sort: true}) },
		"impute": func(d []jsval.Value) {
			Impute(ctx, d, ImputeParams{Field: y, Key: x, GroupBy: []Field{g}, Method: "mean"})
		},
		"pivot":   func(d []jsval.Value) { Pivot(ctx, d, PivotParams{GroupBy: []Field{g}, Field: x, Value: y}) },
		"fold":    func(d []jsval.Value) { Fold(ctx, d, FoldParams{Fields: []Field{x, y}}) },
		"flatten": func(d []jsval.Value) { Flatten(ctx, d, FlattenParams{Fields: []Field{y}}) },
		"project": func(d []jsval.Value) { Project(ctx, d, ProjectParams{Fields: []Field{x}}) },
		"regress": func(d []jsval.Value) {
			Regression(ctx, d, RegressionParams{X: x, Y: y, Method: "poly", Order: 3, GroupBy: []Field{g}})
		},
		"regresslog": func(d []jsval.Value) { Regression(ctx, d, RegressionParams{X: x, Y: y, Method: "log"}) },
		"loess":      func(d []jsval.Value) { Loess(ctx, d, LoessParams{X: x, Y: y, GroupBy: []Field{g}}) },
		"kde":        func(d []jsval.Value) { KDE(ctx, d, KDEParams{Field: x, GroupBy: []Field{g}}) },
		"quantile": func(d []jsval.Value) {
			Quantile(ctx, d, QuantileParams{Field: x, GroupBy: []Field{g}, Probs: []float64{0.1, 0.9}})
		},
		"extent": func(d []jsval.Value) { FieldExtent(ctx, d, x) },
		"sample": func(d []jsval.Value) { Sample(ctx, d, 3, LCG(1)) },
		"cross":  func(d []jsval.Value) { Cross(ctx, d, CrossParams{}) },
		"dotbin": func(d []jsval.Value) { DotBinTuples(ctx, d, DotBinParams{Field: x, GroupBy: []Field{g}}) },
		"countpat": func(d []jsval.Value) {
			CountPattern(ctx, d, CountPatternParams{Field: FieldOf("g")})
		},
	}
	for name, fn := range steps {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			fn(cloneTuples(hostileTuples()))
			fn(nil)
		})
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := aggBenchData(10_000)
	if _, err := Aggregate(ctx, data, AggregateParams{GroupBy: FieldsOf("c")}); err == nil {
		t.Error("Aggregate ignored a cancelled context")
	}
	if _, err := Window(ctx, data, WindowParams{Ops: []WindowOpSpec{{Op: "row_number"}}}); err == nil {
		t.Error("Window ignored a cancelled context")
	}
}
