package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// compareSpec builds a comparator from an upstream {field, order} object.
func compareSpec(v jsval.Value) Comparator {
	if !v.IsObj() {
		return nil
	}
	fv := v.Get("field")
	var paths []string
	if fv.IsStr() {
		paths = []string{fv.StrValue()}
	} else {
		paths = strList(fv)
	}
	var orders []string
	if ov := v.Get("order"); ov.IsStr() {
		orders = []string{ov.StrValue()}
	} else {
		orders = strList(ov)
	}
	return CompareBy(FieldsOf(paths...), orders)
}

func asPair(v jsval.Value) [2]string {
	return [2]string{v.Index(0).StrValue(), v.Index(1).StrValue()}
}

func TestStackPieGolden(t *testing.T) {
	for _, c := range loadGolden(t, "stack_pie.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			in := cloneTuples(c.Input)
			var got []jsval.Value
			var err error
			field := Field{}
			if f := spec.Get("field"); f.IsStr() {
				field = FieldOf(f.StrValue())
			}
			switch spec.Get("type").StrValue() {
			case "stack":
				got, err = Stack(context.Background(), in, StackParams{
					Field: field, GroupBy: fieldList(spec.Get("groupby")), Sort: compareSpec(spec.Get("sort")),
					Offset: spec.Get("offset").StrValue(), As: asPair(spec.Get("as")),
				})
			case "pie":
				p := PieParams{Field: field, Sort: spec.Get("sort").IsTruthy(), As: asPair(spec.Get("as")),
					StartAngle: spec.Get("startAngle").NumValue()}
				if e := spec.Get("endAngle"); e.IsNum() {
					x := e.NumValue()
					p.EndAngle = &x
				}
				got, err = Pie(context.Background(), in, p)
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 1e-12); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestStackCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := make([]jsval.Value, 10)
	for i := range data {
		data[i] = obj("v", jsval.Int(i), "g", jsval.Int(i))
	}
	if _, err := Stack(ctx, data, StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("g")}); err == nil {
		t.Fatal("expected cancellation")
	}
}

func benchRows(n int) []jsval.Value {
	rows := make([]jsval.Value, n)
	for i := range rows {
		rows[i] = obj("g", jsval.Int(i%50), "h", jsval.Str([]string{"a", "b", "c"}[i%3]), "v", jsval.Num(float64(i%97)+0.5))
	}
	return rows
}

func BenchmarkStack100k(b *testing.B) {
	rows := benchRows(100_000)
	p := StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("g", "h"), Offset: StackZero}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Stack(context.Background(), rows, p); err != nil {
			b.Fatal(err)
		}
	}
}
