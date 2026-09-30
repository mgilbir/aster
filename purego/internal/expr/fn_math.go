package expr

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

func jsMax2(a, b float64) float64 {
	switch {
	case math.IsNaN(a) || math.IsNaN(b):
		return math.NaN()
	case a == 0 && b == 0:
		if math.Signbit(a) {
			return b
		}
		return a
	case a > b:
		return a
	}
	return b
}

func jsMin2(a, b float64) float64 {
	switch {
	case math.IsNaN(a) || math.IsNaN(b):
		return math.NaN()
	case a == 0 && b == 0:
		if math.Signbit(a) {
			return a
		}
		return b
	case a < b:
		return a
	}
	return b
}

// jsHypot is Math.hypot: Infinity wins over NaN, and the sum is scaled so
// that squaring cannot overflow.
func jsHypot(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	inf, nan := false, false
	maxv := 0.0
	for _, x := range v {
		if math.IsInf(x, 0) {
			inf = true
		}
		if math.IsNaN(x) {
			nan = true
		}
		if a := math.Abs(x); a > maxv {
			maxv = a
		}
	}
	switch {
	case inf:
		return math.Inf(1)
	case nan:
		return math.NaN()
	case maxv == 0:
		return 0
	}
	// Kahan summation of squares of x/max, as V8 does.
	sum, comp := 0.0, 0.0
	for _, x := range v {
		r := x / maxv
		y := float64(r*r) - comp
		t := sum + y
		comp = (t - sum) - y
		sum = t
	}
	return math.Sqrt(sum) * maxv
}

func numArgs(s *Scope, args []jsval.Value) []float64 {
	out := make([]float64, len(args))
	for i, a := range args {
		out[i] = s.num(a)
	}
	return out
}

func init() {
	for name, f := range map[string]func(float64) float64{
		"abs": math.Abs, "acos": jsmath.Acos, "asin": jsmath.Asin, "atan": jsmath.Atan,
		"ceil": math.Ceil, "cos": jsmath.Cos, "exp": math.Exp, "floor": math.Floor,
		"log": math.Log, "round": jsRound, "sin": jsmath.Sin, "sqrt": math.Sqrt, "tan": math.Tan,
	} {
		def(name, &funcDef{math1: f})
	}
	def("atan2", &funcDef{math2: jsmath.Atan2})
	def("pow", &funcDef{math2: jsmath.Pow})
	fn("max", func(s *Scope, args []jsval.Value) jsval.Value {
		r := math.Inf(-1)
		for _, a := range args {
			r = jsMax2(r, s.num(a))
		}
		return jsval.Num(r)
	})
	fn("min", func(s *Scope, args []jsval.Value) jsval.Value {
		r := math.Inf(1)
		for _, a := range args {
			r = jsMin2(r, s.num(a))
		}
		return jsval.Num(r)
	})
	fn("hypot", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(jsHypot(numArgs(s, args)))
	})
	// isNaN and isFinite are Number.isNaN / Number.isFinite: no coercion, so
	// a string or date is never NaN.
	fn("isNaN", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		return jsval.Bool(v.IsNum() && math.IsNaN(v.NumValue()))
	})
	fn("isFinite", func(s *Scope, args []jsval.Value) jsval.Value {
		v := arg(args, 0)
		return jsval.Bool(v.IsNum() && !math.IsNaN(v.NumValue()) && !math.IsInf(v.NumValue(), 0))
	})
	fn("random", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(s.Rand.next())
	})
}
