package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// The probability functions of vega-statistics. Numeric code here avoids fused
// multiply-add (a*b+c is written float64(a*b)+c) so results agree with V8 to
// the last bit on every architecture.

var sqrt2pi = math.Sqrt(2 * math.Pi)

func mulAdd(a, b, c float64) float64 { return float64(a*b) + c }

// orZero is `v || 0` followed by coercion to a number.
func (s *Scope) orZero(v jsval.Value) float64 {
	if v.IsTruthy() {
		return s.num(v)
	}
	return 0
}

// orZeroV is `v || 0` without the numeric coercion: the value itself when
// truthy (it may be a string, and then + concatenates).
func orZeroV(v jsval.Value) jsval.Value {
	if v.IsTruthy() {
		return v
	}
	return jsval.Num(0)
}

// orOne is `v == null ? 1 : v` followed by coercion to a number.
func (s *Scope) orOne(v jsval.Value) float64 {
	if v.IsNullish() {
		return 1
	}
	return s.num(v)
}

// sampleNormal is a Box-Muller transform. Like upstream it keeps the second
// value of each pair for the next call.
func (s *Scope) sampleNormal(mean jsval.Value, stdev float64) jsval.Value {
	if s.Rand == nil {
		s.Rand = &Random{}
	}
	r := s.Rand
	var x float64
	if r.hasSpare {
		x = r.spare
		r.hasSpare = false
	} else {
		var y, rds float64
		for {
			x = float64(r.next()*2) - 1
			y = float64(r.next()*2) - 1
			rds = float64(x*x) + float64(y*y)
			if rds != 0 && rds <= 1 {
				break
			}
		}
		c := math.Sqrt(-2 * jsmath.Log(rds) / rds)
		x *= c
		r.spare = y * c
		r.hasSpare = true
	}
	return s.add(mean, jsval.Num(float64(x*stdev)))
}

func densityNormal(value, mean, stdev float64) float64 {
	z := (value - mean) / stdev
	return jsmath.Exp(-0.5*z*z) / (stdev * sqrt2pi)
}

// cumulativeNormal is West (2009), "Better Approximations to Cumulative Normal
// Functions".
func cumulativeNormal(value, mean, stdev float64) float64 {
	z := (value - mean) / stdev
	Z := math.Abs(z)
	var cd float64
	if Z > 37 {
		cd = 0
	} else {
		e := jsmath.Exp(-Z * Z / 2)
		var sum float64
		if Z < 7.07106781186547 {
			sum = mulAdd(3.52624965998911e-02, Z, 0.700383064443688)
			sum = mulAdd(sum, Z, 6.37396220353165)
			sum = mulAdd(sum, Z, 33.912866078383)
			sum = mulAdd(sum, Z, 112.079291497871)
			sum = mulAdd(sum, Z, 221.213596169931)
			sum = mulAdd(sum, Z, 220.206867912376)
			cd = e * sum
			sum = mulAdd(8.83883476483184e-02, Z, 1.75566716318264)
			sum = mulAdd(sum, Z, 16.064177579207)
			sum = mulAdd(sum, Z, 86.7807322029461)
			sum = mulAdd(sum, Z, 296.564248779674)
			sum = mulAdd(sum, Z, 637.333633378831)
			sum = mulAdd(sum, Z, 793.826512519948)
			sum = mulAdd(sum, Z, 440.413735824752)
			cd = cd / sum
		} else {
			sum = Z + 0.65
			sum = Z + 4/sum
			sum = Z + 3/sum
			sum = Z + 2/sum
			sum = Z + 1/sum
			cd = e / sum / 2.506628274631
		}
	}
	if z > 0 {
		return 1 - cd
	}
	return cd
}

// erfinv is Mike Giles' "Approximating the erfinv function" (GPU Computing
// Gems, vol. 2), as vega-statistics ports it from Apache Commons Math.
func erfinv(x float64) float64 {
	w := -jsmath.Log((1 - x) * (1 + x))
	var p float64
	switch {
	case w < 6.25:
		w -= 3.125
		p = -3.6444120640178196996e-21
		for _, c := range [...]float64{
			-1.685059138182016589e-19, 1.2858480715256400167e-18, 1.115787767802518096e-17,
			-1.333171662854620906e-16, 2.0972767875968561637e-17, 6.6376381343583238325e-15,
			-4.0545662729752068639e-14, -8.1519341976054721522e-14, 2.6335093153082322977e-12,
			-1.2975133253453532498e-11, -5.4154120542946279317e-11, 1.051212273321532285e-09,
			-4.1126339803469836976e-09, -2.9070369957882005086e-08, 4.2347877827932403518e-07,
			-1.3654692000834678645e-06, -1.3882523362786468719e-05, 0.0001867342080340571352,
			-0.00074070253416626697512, -0.0060336708714301490533, 0.24015818242558961693,
			1.6536545626831027356,
		} {
			p = mulAdd(p, w, c)
		}
	case w < 16:
		w = math.Sqrt(w) - 3.25
		p = 2.2137376921775787049e-09
		for _, c := range [...]float64{
			9.0756561938885390979e-08, -2.7517406297064545428e-07, 1.8239629214389227755e-08,
			1.5027403968909827627e-06, -4.013867526981545969e-06, 2.9234449089955446044e-06,
			1.2475304481671778723e-05, -4.7318229009055733981e-05, 6.8284851459573175448e-05,
			2.4031110387097893999e-05, -0.0003550375203628474796, 0.00095328937973738049703,
			-0.0016882755560235047313, 0.0024914420961078508066, -0.0037512085075692412107,
			0.005370914553590063617, 1.0052589676941592334, 3.0838856104922207635,
		} {
			p = mulAdd(p, w, c)
		}
	case !math.IsInf(w, 0) && !math.IsNaN(w):
		w = math.Sqrt(w) - 5.0
		p = -2.7109920616438573243e-11
		for _, c := range [...]float64{
			-2.5556418169965252055e-10, 1.5076572693500548083e-09, -3.7894654401267369937e-09,
			7.6157012080783393804e-09, -1.4960026627149240478e-08, 2.9147953450901080826e-08,
			-6.7711997758452339498e-08, 2.2900482228026654717e-07, -9.9298272942317002539e-07,
			4.5260625972231537039e-06, -1.9681778105531670567e-05, 7.5995277030017761139e-05,
			-0.00021503011930044477347, -0.00013871931833623122026, 1.0103004648645343977,
			4.8499064014085844221,
		} {
			p = mulAdd(p, w, c)
		}
	default:
		p = math.Inf(1)
	}
	return p * x
}

// quantileNormal returns a Value because `(mean || 0) + ...` concatenates when
// mean is a string.
func (s *Scope) quantileNormal(p float64, mean jsval.Value, stdev float64) jsval.Value {
	if p < 0 || p > 1 {
		return jsval.Num(math.NaN())
	}
	return s.add(mean, jsval.Num(float64(stdev*math.Sqrt2*erfinv(float64(2*p)-1))))
}

// uniformRange maps the (min, max) arguments of the uniform distribution
// functions: with max omitted the range is [0, min] (or [0, 1] when both are
// omitted). lo stays a Value because `min + ...` concatenates for strings.
func (s *Scope) uniformRange(minV, maxV jsval.Value) (lo jsval.Value, hi float64) {
	if maxV.IsNullish() {
		hi = 1
		if !minV.IsNullish() {
			hi = s.num(minV)
		}
		return jsval.Num(0), hi
	}
	return minV, s.num(maxV)
}

func init() {
	fn("sampleNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return s.sampleNormal(orZeroV(arg(args, 0)), s.orOne(arg(args, 1)))
	})
	fn("densityNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(densityNormal(s.num(arg(args, 0)), s.orZero(arg(args, 1)), s.orOne(arg(args, 2))))
	})
	fn("cumulativeNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(cumulativeNormal(s.num(arg(args, 0)), s.orZero(arg(args, 1)), s.orOne(arg(args, 2))))
	})
	fn("quantileNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return s.quantileNormal(s.num(arg(args, 0)), orZeroV(arg(args, 1)), s.orOne(arg(args, 2)))
	})

	fn("sampleLogNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		mean, stdev := orZeroV(arg(args, 0)), s.orOne(arg(args, 1))
		z := s.num(s.sampleNormal(jsval.Num(0), 1))
		return jsval.Num(jsmath.Exp(s.num(s.add(mean, jsval.Num(float64(z*stdev))))))
	})
	fn("densityLogNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		value := s.num(arg(args, 0))
		if value <= 0 {
			return jsval.Num(0)
		}
		mean, stdev := s.orZero(arg(args, 1)), s.orOne(arg(args, 2))
		z := (jsmath.Log(value) - mean) / stdev
		return jsval.Num(jsmath.Exp(-0.5*z*z) / (stdev * sqrt2pi * value))
	})
	fn("cumulativeLogNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(cumulativeNormal(jsmath.Log(s.num(arg(args, 0))), s.orZero(arg(args, 1)), s.orOne(arg(args, 2))))
	})
	fn("quantileLogNormal", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(jsmath.Exp(s.num(s.quantileNormal(s.num(arg(args, 0)), orZeroV(arg(args, 1)), s.orOne(arg(args, 2))))))
	})

	fn("sampleUniform", func(s *Scope, args []jsval.Value) jsval.Value {
		lo, hi := s.uniformRange(arg(args, 0), arg(args, 1))
		if s.Rand == nil {
			s.Rand = &Random{}
		}
		return s.add(lo, jsval.Num(float64((hi-s.num(lo))*s.Rand.next())))
	})
	fn("densityUniform", func(s *Scope, args []jsval.Value) jsval.Value {
		loV, hi := s.uniformRange(arg(args, 1), arg(args, 2))
		lo := s.num(loV)
		v := s.num(arg(args, 0))
		if v >= lo && v <= hi {
			return jsval.Num(1 / (hi - lo))
		}
		return jsval.Num(0)
	})
	fn("cumulativeUniform", func(s *Scope, args []jsval.Value) jsval.Value {
		loV, hi := s.uniformRange(arg(args, 1), arg(args, 2))
		lo := s.num(loV)
		v := s.num(arg(args, 0))
		switch {
		case v < lo:
			return jsval.Num(0)
		case v > hi:
			return jsval.Num(1)
		}
		return jsval.Num((v - lo) / (hi - lo))
	})
	fn("quantileUniform", func(s *Scope, args []jsval.Value) jsval.Value {
		loV, hi := s.uniformRange(arg(args, 1), arg(args, 2))
		p := s.num(arg(args, 0))
		if p >= 0 && p <= 1 {
			return s.add(loV, jsval.Num(float64(p*(hi-s.num(loV)))))
		}
		return jsval.Num(math.NaN())
	})
}
