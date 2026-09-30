package scale

import (
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func nums(xs ...float64) []jsval.Value { return numsToValues(xs) }

func benchInputs(n int) []float64 {
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = float64(i%1000) / 10
	}
	return xs
}

func BenchmarkLinearApplyFloat(b *testing.B) {
	s := NewLinear()
	s.SetDomain(nums(0, 100))
	s.SetRange(nums(0, 500))
	xs := benchInputs(1024)
	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		sink += s.ApplyFloat(xs[i&1023])
	}
	_ = sink
}

func BenchmarkLinearApplyValue(b *testing.B) {
	s := NewLinear()
	s.SetDomain(nums(0, 100))
	s.SetRange(nums(0, 500))
	vs := numsToValues(benchInputs(1024))
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.Apply(vs[i&1023])
	}
	_ = sink
}

func BenchmarkLinearPolyApply(b *testing.B) {
	s := NewLinear()
	s.SetDomain(nums(0, 25, 50, 75, 100))
	s.SetRange(nums(0, 10, 40, 50, 500))
	xs := benchInputs(1024)
	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		sink += s.ApplyFloat(xs[i&1023])
	}
	_ = sink
}

func BenchmarkLogApply(b *testing.B) {
	s := NewLog()
	s.SetDomain(nums(1, 1000))
	s.SetRange(nums(0, 500))
	xs := benchInputs(1024)
	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		sink += s.ApplyFloat(xs[i&1023] + 1)
	}
	_ = sink
}

func BenchmarkLinearColorApply(b *testing.B) {
	s := NewLinear()
	s.SetDomain(nums(0, 100))
	s.SetRange([]jsval.Value{jsval.Str("steelblue"), jsval.Str("orange")})
	xs := benchInputs(1024)
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.ApplyNumber(xs[i&1023])
	}
	_ = sink
}

func BenchmarkSequentialScheme(b *testing.B) {
	sc, _ := LookupScheme("viridis")
	s := NewSequential()
	s.SetDomain(nums(0, 100))
	s.SetInterpolator(sc.Interpolator)
	xs := benchInputs(1024)
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.ApplyNumber(xs[i&1023])
	}
	_ = sink
}

func BenchmarkLinearTicks(b *testing.B) {
	s := NewLinear()
	s.SetDomain(nums(0, 1234.5))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.Ticks(Count(10))
	}
}

func BenchmarkLogTicks(b *testing.B) {
	s := NewLog()
	s.SetDomain(nums(1, 1e6))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.Ticks(Count(10))
	}
}

func BenchmarkBandApply(b *testing.B) {
	s := NewBand()
	dom := make([]jsval.Value, 200)
	keys := make([]jsval.Value, 200)
	for i := range dom {
		dom[i] = jsval.Str("category-" + jsval.JSNumberString(float64(i)))
		keys[i] = dom[i]
	}
	s.SetDomain(dom)
	s.SetRange(nums(0, 800))
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.Apply(keys[i%200])
	}
	_ = sink
}

func BenchmarkOrdinalApply(b *testing.B) {
	s := NewOrdinal()
	s.SetRange([]jsval.Value{jsval.Str("#1f77b4"), jsval.Str("#ff7f0e"), jsval.Str("#2ca02c")})
	keys := make([]jsval.Value, 50)
	for i := range keys {
		keys[i] = jsval.Str("k" + jsval.JSNumberString(float64(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.Apply(keys[i%50])
	}
	_ = sink
}

func BenchmarkQuantizeApply(b *testing.B) {
	s := NewQuantize()
	s.SetDomain(nums(0, 100))
	s.SetRange([]jsval.Value{jsval.Str("a"), jsval.Str("b"), jsval.Str("c"), jsval.Str("d")})
	vs := numsToValues(benchInputs(1024))
	b.ReportAllocs()
	b.ResetTimer()
	var sink jsval.Value
	for i := 0; i < b.N; i++ {
		sink = s.Apply(vs[i&1023])
	}
	_ = sink
}
