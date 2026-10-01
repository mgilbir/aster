package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// The d3-ease easing functions as Vega 6.4 exposes them to the expression language (vega-functions
// ease.js), under their d3 names and at their default parameters. Each maps a normalized time to an
// eased position. The arithmetic is d3-ease's, line for line; products that are added to or subtracted
// from are rounded with float64() so that arm64 does not fuse them into multiply-adds, which V8 never
// does.
//
// The constants are variables: Go folds a constant expression exactly and rounds it once, JavaScript
// rounds each operation.

var (
	easePi     = math.Pi
	easeHalfPi = easePi / 2
	easeTau    = 2 * easePi

	bounce1 = 4.0 / 11
	bounce2 = 6.0 / 11
	bounce3 = 8.0 / 11
	bounce4 = 3.0 / 4
	bounce5 = 9.0 / 11
	bounce6 = 10.0 / 11
	bounce7 = 15.0 / 16
	bounce8 = 21.0 / 22
	bounce9 = 63.0 / 64
	bounce0 = 1 / bounce1 / bounce1

	backOvershoot = 1.70158

	elasticAmplitude = 1.0
	elasticPeriod    = 0.3
)

// easeTpmt is d3-ease's tpmt: two to the minus ten times x, scaled to [0, 1].
func easeTpmt(x float64) float64 {
	// the product is rounded here: tpmt is inlined into 1 - tpmt(t), where arm64 would fuse it
	return float64((jsmath.Pow(2, float64(-10*x)) - 0.0009765625) * 1.0009775171065494)
}

func easeQuadIn(t float64) float64  { return float64(t * t) }
func easeQuadOut(t float64) float64 { return float64(t * (2 - t)) }
func easeQuadInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return float64(t*t) / 2
	}
	t--
	return (float64(t*(2-t)) + 1) / 2
}

func easeCubicIn(t float64) float64 { return float64(float64(t*t) * t) }
func easeCubicOut(t float64) float64 {
	t--
	return float64(float64(t*t)*t) + 1
}
func easeCubicInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return float64(float64(t*t)*t) / 2
	}
	t -= 2
	return (float64(float64(t*t)*t) + 2) / 2
}

func easePolyIn(t float64) float64  { return jsmath.Pow(t, 3) }
func easePolyOut(t float64) float64 { return 1 - jsmath.Pow(1-t, 3) }
func easePolyInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return jsmath.Pow(t, 3) / 2
	}
	return (2 - jsmath.Pow(2-t, 3)) / 2
}

func easeSinIn(t float64) float64 {
	if t == 1 {
		return 1
	}
	return 1 - jsmath.Cos(float64(t*easeHalfPi))
}
func easeSinOut(t float64) float64   { return jsmath.Sin(float64(t * easeHalfPi)) }
func easeSinInOut(t float64) float64 { return (1 - jsmath.Cos(float64(easePi*t))) / 2 }

func easeExpIn(t float64) float64  { return easeTpmt(1 - t) }
func easeExpOut(t float64) float64 { return 1 - easeTpmt(t) }
func easeExpInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return easeTpmt(1-t) / 2
	}
	return (2 - easeTpmt(t-1)) / 2
}

func easeCircleIn(t float64) float64 { return 1 - math.Sqrt(1-float64(t*t)) }
func easeCircleOut(t float64) float64 {
	t--
	return math.Sqrt(1 - float64(t*t))
}
func easeCircleInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return (1 - math.Sqrt(1-float64(t*t))) / 2
	}
	t -= 2
	return (math.Sqrt(1-float64(t*t)) + 1) / 2
}

func easeBounceOut(t float64) float64 {
	switch {
	case t < bounce1:
		return float64(float64(bounce0*t) * t)
	case t < bounce3:
		t -= bounce2
		return float64(float64(bounce0*t)*t) + bounce4
	case t < bounce6:
		t -= bounce5
		return float64(float64(bounce0*t)*t) + bounce7
	}
	t -= bounce8
	return float64(float64(bounce0*t)*t) + bounce9
}
func easeBounceIn(t float64) float64 { return 1 - easeBounceOut(1-t) }
func easeBounceInOut(t float64) float64 {
	t *= 2
	if t <= 1 {
		return (1 - easeBounceOut(1-t)) / 2
	}
	return (easeBounceOut(t-1) + 1) / 2
}

func easeBackIn(t float64) float64 {
	s := backOvershoot
	return float64(t*t) * (float64(s*(t-1)) + t)
}
func easeBackOut(t float64) float64 {
	s := backOvershoot
	t--
	return float64(float64(t*t)*(float64((t+1)*s)+t)) + 1
}
func easeBackInOut(t float64) float64 {
	s := backOvershoot
	t *= 2
	if t < 1 {
		return float64(t*t) * (float64((s+1)*t) - s) / 2
	}
	t -= 2
	return (float64(float64(t*t)*(float64((s+1)*t)+s)) + 2) / 2
}

// elasticParams are the s and p of d3-ease's elastic custom(a, p) at the default amplitude and period.
func elasticParams() (a, s, p float64) {
	a = math.Max(1, elasticAmplitude)
	p = elasticPeriod / easeTau
	s = float64(jsmath.Asin(1/a) * p)
	return a, s, p
}

func easeElasticIn(t float64) float64 {
	a, s, p := elasticParams()
	t--
	return float64(a*easeTpmt(-t)) * jsmath.Sin((s-t)/p)
}
func easeElasticOut(t float64) float64 {
	a, s, p := elasticParams()
	return 1 - float64(float64(a*easeTpmt(t))*jsmath.Sin((t+s)/p))
}
func easeElasticInOut(t float64) float64 {
	a, s, p := elasticParams()
	t = float64(t*2) - 1
	if t < 0 {
		return float64(float64(a*easeTpmt(-t))*jsmath.Sin((s-t)/p)) / 2
	}
	return (2 - float64(float64(a*easeTpmt(t))*jsmath.Sin((s+t)/p))) / 2
}

func init() {
	ease := func(name string, f func(t float64) float64) {
		fn(name, func(s *Scope, args []jsval.Value) jsval.Value {
			return jsval.Num(f(s.num(arg(args, 0))))
		})
	}
	ease("easeLinear", func(t float64) float64 { return t })

	ease("easeQuad", easeQuadInOut)
	ease("easeQuadIn", easeQuadIn)
	ease("easeQuadOut", easeQuadOut)
	ease("easeQuadInOut", easeQuadInOut)

	ease("easeCubic", easeCubicInOut)
	ease("easeCubicIn", easeCubicIn)
	ease("easeCubicOut", easeCubicOut)
	ease("easeCubicInOut", easeCubicInOut)

	ease("easePoly", easePolyInOut)
	ease("easePolyIn", easePolyIn)
	ease("easePolyOut", easePolyOut)
	ease("easePolyInOut", easePolyInOut)

	ease("easeSin", easeSinInOut)
	ease("easeSinIn", easeSinIn)
	ease("easeSinOut", easeSinOut)
	ease("easeSinInOut", easeSinInOut)

	ease("easeExp", easeExpInOut)
	ease("easeExpIn", easeExpIn)
	ease("easeExpOut", easeExpOut)
	ease("easeExpInOut", easeExpInOut)

	ease("easeCircle", easeCircleInOut)
	ease("easeCircleIn", easeCircleIn)
	ease("easeCircleOut", easeCircleOut)
	ease("easeCircleInOut", easeCircleInOut)

	ease("easeBounce", easeBounceOut)
	ease("easeBounceIn", easeBounceIn)
	ease("easeBounceOut", easeBounceOut)
	ease("easeBounceInOut", easeBounceInOut)

	ease("easeBack", easeBackInOut)
	ease("easeBackIn", easeBackIn)
	ease("easeBackOut", easeBackOut)
	ease("easeBackInOut", easeBackInOut)

	ease("easeElastic", easeElasticOut)
	ease("easeElasticIn", easeElasticIn)
	ease("easeElasticOut", easeElasticOut)
	ease("easeElasticInOut", easeElasticInOut)
}
