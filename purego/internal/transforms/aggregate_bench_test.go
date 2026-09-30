package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func aggBenchData(n int) []jsval.Value {
	cats := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	data := make([]jsval.Value, n)
	for i := range data {
		data[i] = obj("c", jsval.Str(cats[i%len(cats)]), "k", jsval.Int(i%100), "x", jsval.Num(float64(i%1013)*0.37), "y", jsval.Num(float64(i%17)))
	}
	return data
}

func BenchmarkAggregateSumMean100k(b *testing.B) {
	data := aggBenchData(100_000)
	p := AggregateParams{
		GroupBy: FieldsOf("c", "k"),
		Measures: []Measure{
			{Op: "count"}, {Op: "sum", Field: FieldOf("x")}, {Op: "mean", Field: FieldOf("x")},
			{Op: "max", Field: FieldOf("y")}, {Op: "stdev", Field: FieldOf("y")},
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Aggregate(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAggregateMedian100k(b *testing.B) {
	data := aggBenchData(100_000)
	p := AggregateParams{GroupBy: FieldsOf("c"), Measures: []Measure{{Op: "median", Field: FieldOf("x")}, {Op: "distinct", Field: FieldOf("y")}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Aggregate(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}
