package jsmath

import (
	"math"
	"testing"
)

// Each benchmark cycles through the same 1024 arguments for the package's
// function and for Go's math equivalent, so the ratio is the cost of matching
// V8 bit for bit.

var benchSink float64

func benchArgs(lo, hi float64) []float64 {
	r := &splitmix{s: 42}
	a := make([]float64, 1024)
	for i := range a {
		a[i] = lo + (hi-lo)*r.unit()
	}
	return a
}

func benchUnary(b *testing.B, args []float64, js, gm func(float64) float64) {
	for _, c := range []struct {
		name string
		f    func(float64) float64
	}{{"jsmath", js}, {"gomath", gm}} {
		b.Run(c.name, func(b *testing.B) {
			var s float64
			for i := 0; i < b.N; i++ {
				s += c.f(args[i&1023])
			}
			benchSink = s
		})
	}
}

func BenchmarkExp(b *testing.B)   { benchUnary(b, benchArgs(-30, 30), Exp, math.Exp) }
func BenchmarkExpm1(b *testing.B) { benchUnary(b, benchArgs(-30, 30), Expm1, math.Expm1) }
func BenchmarkLog(b *testing.B)   { benchUnary(b, benchArgs(1e-3, 1e6), Log, math.Log) }
func BenchmarkLog1p(b *testing.B) { benchUnary(b, benchArgs(-0.9, 1e3), Log1p, math.Log1p) }
func BenchmarkLog2(b *testing.B)  { benchUnary(b, benchArgs(1e-3, 1e6), Log2, math.Log2) }
func BenchmarkLog10(b *testing.B) { benchUnary(b, benchArgs(1e-3, 1e6), Log10, math.Log10) }
func BenchmarkCbrt(b *testing.B)  { benchUnary(b, benchArgs(-1e6, 1e6), Cbrt, math.Cbrt) }
func BenchmarkSinh(b *testing.B)  { benchUnary(b, benchArgs(-30, 30), Sinh, math.Sinh) }
func BenchmarkCosh(b *testing.B)  { benchUnary(b, benchArgs(-30, 30), Cosh, math.Cosh) }
func BenchmarkTanh(b *testing.B)  { benchUnary(b, benchArgs(-5, 5), Tanh, math.Tanh) }
func BenchmarkAsinh(b *testing.B) { benchUnary(b, benchArgs(-1e3, 1e3), Asinh, math.Asinh) }
func BenchmarkAcosh(b *testing.B) { benchUnary(b, benchArgs(1, 1e3), Acosh, math.Acosh) }
func BenchmarkAtanh(b *testing.B) { benchUnary(b, benchArgs(-0.99, 0.99), Atanh, math.Atanh) }
func BenchmarkTan(b *testing.B)   { benchUnary(b, benchArgs(-10, 10), Tan, math.Tan) }
func BenchmarkTanLarge(b *testing.B) {
	benchUnary(b, benchArgs(1e7, 1e300), Tan, math.Tan)
}

func BenchmarkPow(b *testing.B) {
	xs, ys := benchArgs(0.1, 100), benchArgs(-5, 5)
	b.Run("jsmath", func(b *testing.B) {
		var s float64
		for i := 0; i < b.N; i++ {
			s += Pow(xs[i&1023], ys[i&1023])
		}
		benchSink = s
	})
	b.Run("gomath", func(b *testing.B) {
		var s float64
		for i := 0; i < b.N; i++ {
			s += math.Pow(xs[i&1023], ys[i&1023])
		}
		benchSink = s
	})
}
