package transforms

import (
	"context"
	"math"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func TestRegressionGolden(t *testing.T) {
	for _, c := range loadGolden(t, "regression.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			if c.Error != "" {
				t.Skipf("upstream error: %s", c.Error)
			}
			var groupby []Field
			if g := spec.Get("groupby"); g.IsArr() {
				groupby = fieldList(g)
				if groupby == nil {
					groupby = []Field{}
				}
			}
			var as []string
			if a := spec.Get("as"); a.IsArr() {
				as = strList(a)
			}
			in := cloneTuples(c.Input)
			var got []jsval.Value
			var err error
			if spec.Get("type").StrValue() == "loess" {
				got, err = Loess(context.Background(), in, LoessParams{
					X: FieldOf(spec.Get("x").StrValue()), Y: FieldOf(spec.Get("y").StrValue()),
					GroupBy: groupby, Bandwidth: spec.Get("bandwidth").NumValue(), As: as,
				})
			} else {
				p := RegressionParams{
					X: FieldOf(spec.Get("x").StrValue()), Y: FieldOf(spec.Get("y").StrValue()),
					GroupBy: groupby, Method: spec.Get("method").StrValue(), Order: DefaultRegressionOrder,
					Params: spec.Get("params").IsTruthy(), As: as,
				}
				if o := spec.Get("order"); o.IsNum() {
					p.Order = int(o.NumValue())
				}
				if e := spec.Get("extent"); e.IsArr() {
					for _, v := range e.Items() {
						p.Extent = append(p.Extent, v.NumValue())
					}
				}
				got, err = Regression(context.Background(), in, p)
			}
			if err != nil {
				t.Fatal(err)
			}
			// 1e-8: math.Log/Exp/Pow may differ from V8 in the last ulp and the
			// normal equations of high-order polynomials amplify it.
			if d := diffValues("out", tupleArr(got), c.Output, 1e-8); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestRegressionBadInput(t *testing.T) {
	ctx := context.Background()
	x, y := FieldOf("x"), FieldOf("y")
	if _, err := Regression(ctx, nil, RegressionParams{X: x, Y: y, Method: "nope"}); err == nil {
		t.Error("expected error for unknown method")
	}
	if _, err := Regression(ctx, nil, RegressionParams{X: x, Y: y, Method: "poly", Order: 1 << 30}); err == nil {
		t.Error("expected error for huge order")
	}
	// Singular system must not panic.
	same := []jsval.Value{obj("x", jsval.Num(1), "y", jsval.Num(1)), obj("x", jsval.Num(1), "y", jsval.Num(2)),
		obj("x", jsval.Num(1), "y", jsval.Num(3)), obj("x", jsval.Num(1), "y", jsval.Num(4)), obj("x", jsval.Num(1), "y", jsval.Num(5))}
	if _, err := Regression(ctx, same, RegressionParams{X: x, Y: y, Method: "poly", Order: 4}); err != nil {
		t.Error(err)
	}
	// Reversed and infinite extents terminate.
	if _, err := Regression(ctx, same, RegressionParams{X: x, Y: y, Method: "quad", Extent: []float64{10, 0}}); err != nil {
		t.Error(err)
	}
	if _, err := Regression(ctx, same, RegressionParams{X: x, Y: y, Method: "quad", Extent: []float64{0, math.Inf(1)}}); err != nil {
		t.Error(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	big := make([]jsval.Value, 10000)
	for i := range big {
		big[i] = obj("x", jsval.Num(float64(i)), "y", jsval.Num(float64(i%7)))
	}
	if _, err := Loess(cctx, big, LoessParams{X: x, Y: y}); err == nil {
		t.Error("expected cancellation")
	}
}

func benchPoints(n int) []jsval.Value {
	d := make([]jsval.Value, n)
	for i := range d {
		x := float64(i) / 10
		d[i] = obj("x", jsval.Num(x), "y", jsval.Num(math.Sin(x/50)*10+float64(i%13)))
	}
	return d
}

func BenchmarkLoess10k(b *testing.B) {
	d := benchPoints(10000)
	p := LoessParams{X: FieldOf("x"), Y: FieldOf("y"), Bandwidth: 0.02}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Loess(context.Background(), d, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegressionPoly10k(b *testing.B) {
	d := benchPoints(10000)
	p := RegressionParams{X: FieldOf("x"), Y: FieldOf("y"), Method: "poly", Order: 4}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Regression(context.Background(), d, p); err != nil {
			b.Fatal(err)
		}
	}
}
