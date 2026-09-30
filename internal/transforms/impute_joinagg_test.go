package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func TestImputeJoinAggregateGolden(t *testing.T) {
	for _, c := range loadGolden(t, "impute_joinagg.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			in := cloneTuples(c.Input)
			var got []jsval.Value
			var err error
			switch spec.Get("type").StrValue() {
			case "impute":
				p := ImputeParams{
					Field: FieldOf(spec.Get("field").StrValue()), Key: FieldOf(spec.Get("key").StrValue()),
					KeyVals: spec.Get("keyvals").Items(), GroupBy: fieldList(spec.Get("groupby")),
					Method: spec.Get("method").StrValue(), Value: spec.Get("value"),
				}
				got, err = Impute(context.Background(), in, p)
			case "joinaggregate":
				p := JoinAggregateParams{GroupBy: fieldList(spec.Get("groupby")), Measures: measuresFrom(spec)}
				got, err = JoinAggregate(context.Background(), in, p)
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 1e-9); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestImputeBadMethod(t *testing.T) {
	_, err := Impute(context.Background(), nil, ImputeParams{Field: FieldOf("v"), Key: FieldOf("k"), Method: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestJoinAggregateArgmax(t *testing.T) {
	data := []jsval.Value{obj("g", jsval.Str("a"), "v", jsval.Int(1)), obj("g", jsval.Str("a"), "v", jsval.Int(5)), obj("g", jsval.Str("b"), "v", jsval.Int(2))}
	_, err := JoinAggregate(context.Background(), data, JoinAggregateParams{
		GroupBy: FieldsOf("g"), Measures: []Measure{{Op: "argmax", Field: FieldOf("v"), As: "top"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := data[0].Get("top").Get("v").NumValue(); got != 5 {
		t.Fatalf("argmax v = %v", got)
	}
	if got := data[2].Get("top").Get("v").NumValue(); got != 2 {
		t.Fatalf("argmax v = %v", got)
	}
}

func BenchmarkJoinAggregate100k(b *testing.B) {
	rows := benchRows(100_000)
	p := JoinAggregateParams{GroupBy: FieldsOf("g", "h"), Measures: []Measure{
		{Op: "mean", Field: FieldOf("v")}, {Op: "max", Field: FieldOf("v")}, {Op: "count"}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := JoinAggregate(context.Background(), rows, p); err != nil {
			b.Fatal(err)
		}
	}
}
