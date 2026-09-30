package transforms

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func bNumPtr(v jsval.Value) *float64 {
	if v.IsNum() {
		f := v.NumValue()
		return &f
	}
	return nil
}

func bExtentPtr(v jsval.Value) *[2]float64 {
	if !v.IsArr() {
		return nil
	}
	e := [2]float64{v.Index(0).NumValue(), v.Index(1).NumValue()}
	return &e
}

func bAsPair(v jsval.Value) [2]string {
	return [2]string{v.Index(0).StrValue(), v.Index(1).StrValue()}
}

func bDistSpecFrom(v jsval.Value) DistSpec {
	s := DistSpec{
		Kind: v.Get("function").StrValue(),
		Mean: bNumPtr(v.Get("mean")), Stdev: bNumPtr(v.Get("stdev")), Min: bNumPtr(v.Get("min")), Max: bNumPtr(v.Get("max")),
		Bandwidth: bOptNum(v.Get("bandwidth")),
	}
	if f := v.Get("field"); f.IsStr() {
		s.Field = FieldOf(f.StrValue())
	}
	for _, d := range v.Get("distributions").Items() {
		s.Distributions = append(s.Distributions, bDistSpecFrom(d))
	}
	for _, w := range v.Get("weights").Items() {
		s.Weights = append(s.Weights, bOptNum(w))
	}
	return s
}

func bRunGoldenTransform(t *testing.T, c goldenCase) ([]jsval.Value, error) {
	spec := param(c, 0)
	data := cloneTuples(c.Input)
	ctx := context.Background()
	switch spec.Get("type").StrValue() {
	case "bin":
		cfg := bBinConfigFrom(spec)
		p := BinParams{Field: FieldOf(spec.Get("field").StrValue()), Config: cfg, Name: spec.Get("name").StrValue(), Anchor: bNumPtr(spec.Get("anchor"))}
		if iv := spec.Get("interval"); iv.IsBool() && !iv.BoolValue() {
			p.NoInterval = true
		}
		if as := spec.Get("as"); as.IsArr() {
			p.As = bAsPair(as)
		}
		_, err := BinTuples(ctx, data, p)
		return data, err
	case "dotbin":
		p := DotBinParams{Field: FieldOf(spec.Get("field").StrValue()), GroupBy: fieldList(spec.Get("groupby")),
			Step: bOptNum(spec.Get("step")), Smooth: spec.Get("smooth").IsTruthy(), As: spec.Get("as").StrValue()}
		_, err := DotBinTuples(ctx, data, p)
		return data, err
	case "density":
		p := DensityParams{Distribution: bDistSpecFrom(spec.Get("distribution")), Method: spec.Get("method").StrValue(),
			Extent: bExtentPtr(spec.Get("extent")), Steps: bOptNum(spec.Get("steps")), MinSteps: bOptNum(spec.Get("minsteps")), MaxSteps: bOptNum(spec.Get("maxsteps"))}
		if as := spec.Get("as"); as.IsArr() {
			p.As = bAsPair(as)
		}
		return Density(ctx, data, p)
	case "kde":
		p := KDEParams{GroupBy: fieldList(spec.Get("groupby")), Field: FieldOf(spec.Get("field").StrValue()),
			Cumulative: spec.Get("cumulative").IsTruthy(), Counts: spec.Get("counts").IsTruthy(), Bandwidth: bOptNum(spec.Get("bandwidth")),
			Extent: bExtentPtr(spec.Get("extent")), Resolve: spec.Get("resolve").StrValue(),
			Steps: bOptNum(spec.Get("steps")), MinSteps: bOptNum(spec.Get("minsteps")), MaxSteps: bOptNum(spec.Get("maxsteps"))}
		if as := spec.Get("as"); as.IsArr() {
			p.As = bAsPair(as)
		}
		return KDE(ctx, data, p)
	case "quantile":
		p := QuantileParams{GroupBy: fieldList(spec.Get("groupby")), Field: FieldOf(spec.Get("field").StrValue()),
			Step: bOptNum(spec.Get("step"))}
		if pr := spec.Get("probs"); pr.IsArr() {
			p.Probs = bFloats(pr)
		}
		if as := spec.Get("as"); as.IsArr() {
			p.As = bAsPair(as)
		}
		return Quantile(ctx, data, p)
	}
	t.Fatalf("unknown transform in %s", c.Name)
	return nil, nil
}

func TestTransformsBGolden(t *testing.T) {
	for _, c := range loadGolden(t, "bin_transforms.json") {
		t.Run(c.Name, func(t *testing.T) {
			got, err := bRunGoldenTransform(t, c)
			// Vega logs a failing operator and leaves its output empty.
			if c.Error != "" || c.Output.Len() == 0 && err != nil {
				if err == nil {
					t.Fatalf("expected error (%s)", c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Output.Len() == 0 && len(got) == 0 {
				return
			}
			if d := diffValues("out", tupleArr(got), c.Output, bStatsTol); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestDensityErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Density(ctx, nil, DensityParams{Distribution: DistSpec{Kind: "normal"}}); err == nil || !strings.Contains(err.Error(), "extent") {
		t.Errorf("want extent error, got %v", err)
	}
	deep := DistSpec{Kind: "normal"}
	for i := 0; i < 100; i++ {
		deep = DistSpec{Kind: "mixture", Distributions: []DistSpec{deep}}
	}
	e := [2]float64{0, 1}
	if _, err := Density(ctx, nil, DensityParams{Distribution: deep, Extent: &e}); err == nil {
		t.Error("want nesting error")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Density(cctx, nil, DensityParams{Distribution: DistSpec{Kind: "normal"}, Extent: &e}); err == nil {
		t.Error("want cancellation")
	}
	if _, err := Quantile(ctx, nil, QuantileParams{Step: 1e-12}); err == nil {
		t.Error("want limit error")
	}
	_ = math.NaN
}

func bBenchTuples(n int) []jsval.Value {
	data := make([]jsval.Value, n)
	for i := range data {
		data[i] = jsval.Obj(jsval.ObjectOf("x", jsval.Num(float64((i*7919)%100000)/1000), "g", jsval.Int(i%5)))
	}
	return data
}

func BenchmarkBin100k(b *testing.B) {
	data := bBenchTuples(100_000)
	p := BinParams{Field: FieldOf("x"), Config: BinConfig{Extent: [2]float64{0, 100}, MaxBins: 30}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := BinTuples(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKDE10k(b *testing.B) {
	data := bBenchTuples(10_000)
	p := KDEParams{Field: FieldOf("x"), GroupBy: []Field{FieldOf("g")}, Steps: 100}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := KDE(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDensityKDE(b *testing.B) {
	data := bBenchTuples(5_000)
	p := DensityParams{Distribution: DistSpec{Kind: "kde", Field: FieldOf("x")}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Density(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}
