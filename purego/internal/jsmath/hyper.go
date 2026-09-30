package jsmath

import "math"

// Hyperbolic functions and their inverses, ported from V8's
// src/base/ieee754.cc. See explog.go for the fused multiply-add convention.

// Sinh is Math.sinh.
func Sinh(x float64) float64 {
	const (
		overflow = 710.4758600739439
		twoM28   = 3.725290298461914e-9
		logMaxD  = 709.7822265625
		shuge    = 1.0e307
	)
	h := 0.5
	if x < 0 {
		h = -0.5
	}
	ax := math.Abs(x)
	if ax < 22 {
		if ax < twoM28 {
			return x
		}
		t := Expm1(ax)
		if ax < 1 {
			return h * (2*t - mul(t, t)/(t+1))
		}
		return h * (t + t/(t+1))
	}
	if ax < logMaxD {
		return h * Exp(ax)
	}
	if ax <= overflow {
		w := Exp(0.5 * ax)
		t := h * w
		return t * w
	}
	return x * shuge // Infinity of the right sign, or NaN
}

// Cosh is Math.cosh.
func Cosh(x float64) float64 {
	const overflow = 710.4758600739439
	ix := hi(x) & 0x7fffffff
	if ix < 0x3FD62E43 { // |x| in [0, 0.5*log2]
		t := Expm1(math.Abs(x))
		w := 1 + t
		if ix < 0x3C800000 { // |x| < 2**-55
			return w
		}
		return 1 + mul(t, t)/(w+w)
	}
	if ix < 0x40360000 { // |x| in [0.5*log2, 22]
		t := Exp(math.Abs(x))
		return 0.5*t + 0.5/t
	}
	if ix < 0x40862E42 { // |x| in [22, log(maxdouble)]
		return 0.5 * Exp(math.Abs(x))
	}
	if math.Abs(x) <= overflow {
		w := Exp(0.5 * math.Abs(x))
		t := 0.5 * w
		return t * w
	}
	if ix >= 0x7ff00000 {
		return x * x // Infinity or NaN
	}
	return math.Inf(1)
}

// Tanh is Math.tanh.
func Tanh(x float64) float64 {
	jx := hi(x)
	ix := jx & 0x7fffffff
	if ix >= 0x7ff00000 { // x is Infinity or NaN
		if jx >= 0 {
			return 1/x + 1
		}
		return 1/x - 1
	}
	var z float64
	if ix < 0x40360000 { // |x| < 22
		if ix < 0x3E300000 { // |x| < 2**-28
			return x
		}
		if ix >= 0x3ff00000 { // |x| >= 1
			t := Expm1(2 * math.Abs(x))
			z = 1 - 2/(t+2)
		} else {
			t := Expm1(-2 * math.Abs(x))
			z = -t / (t + 2)
		}
	} else {
		z = 1 // |x| >= 22
	}
	if jx >= 0 {
		return z
	}
	return -z
}

// Asinh is Math.asinh.
func Asinh(x float64) float64 {
	const ln2 = 6.93147180559945286227e-01
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x7ff00000 {
		return x + x
	}
	if ix < 0x3E300000 { // |x| < 2**-28
		return x
	}
	var w float64
	switch {
	case ix > 0x41B00000: // |x| > 2**28
		w = Log(math.Abs(x)) + ln2
	case ix > 0x40000000: // 2**28 > |x| > 2
		t := math.Abs(x)
		w = Log(fma(2, t, 1/(math.Sqrt(fma(x, x, 1))+t)))
	default: // 2 > |x| > 2**-28
		t := mul(x, x)
		w = Log1p(math.Abs(x) + t/(1+math.Sqrt(1+t)))
	}
	if hx > 0 {
		return w
	}
	return -w
}

// Acosh is Math.acosh.
func Acosh(x float64) float64 {
	const ln2 = 6.93147180559945286227e-01
	hx := hi(x)
	lx := lo(x)
	switch {
	case hx < 0x3ff00000: // x < 1
		return math.NaN()
	case hx >= 0x41B00000: // x > 2**28
		if hx >= 0x7ff00000 {
			return x + x
		}
		return Log(x) + ln2
	case uint32(hx-0x3ff00000)|lx == 0:
		return 0
	case hx > 0x40000000: // 2**28 > x > 2
		t := mul(x, x)
		return Log(fma(2, x, -(1 / (x + math.Sqrt(t-1)))))
	}
	t := x - 1 // 1 < x < 2
	return Log1p(t + math.Sqrt(fma(2, t, mul(t, t))))
}

// Atanh is Math.atanh.
func Atanh(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	ix := hx & 0x7fffffff
	if uint32(ix)|((lx|-lx)>>31) > 0x3ff00000 { // |x| > 1
		return math.NaN()
	}
	if ix == 0x3ff00000 {
		if x > 0 {
			return math.Inf(1)
		}
		return math.Inf(-1)
	}
	if ix < 0x3E300000 { // |x| < 2**-28
		return x
	}
	x = withHigh(x, ix)
	var t float64
	if ix < 0x3fe00000 { // |x| < 0.5
		t = x + x
		t = 0.5 * Log1p(t+mul(t, x)/(1-x))
	} else {
		t = 0.5 * Log1p((x+x)/(1-x))
	}
	if hx >= 0 {
		return t
	}
	return -t
}

// ---- cbrt

const (
	cbrtB1 = 715094163 // (1023-1023/3-0.03306235651)*2**20
	cbrtB2 = 696219795 // (1023-1023/3-54/3-0.03306235651)*2**20

	cbrtP0 = 1.87595182427177009643
	cbrtP1 = -1.88497979543377169875
	cbrtP2 = 1.621429720105354466140
	cbrtP3 = -0.758397934778766047437
	cbrtP4 = 0.145996192886612446982
)

// Cbrt is Math.cbrt.
func Cbrt(x float64) float64 {
	hx := hi(x)
	low := lo(x)
	sign := uint32(hx) & 0x80000000
	hx ^= int32(sign)
	if hx >= 0x7ff00000 {
		return x + x
	}

	// Rough cbrt to 5 bits by integer division of the exponent field.
	var t float64
	if hx < 0x00100000 { // zero or subnormal
		if uint32(hx)|low == 0 {
			return x
		}
		t = fromWords(0x43500000, 0) // 2**54
		t *= x
		high := uint32(hi(t))
		t = fromWords(int32(sign|((high&0x7fffffff)/3+cbrtB2)), 0)
	} else {
		t = fromWords(int32(sign|(uint32(hx)/3+cbrtB1)), 0)
	}

	// New cbrt to 23 bits: t*P(t**3/x) with P a degree-4 polynomial.
	r := mul(mul(t, t), t/x)
	t = mul(t, fma(mul(mul(r, r), r), fma(r, cbrtP4, cbrtP3), fma(r, fma(r, cbrtP2, cbrtP1), cbrtP0)))

	// Round t away from zero to 23 bits.
	bits := math.Float64bits(t)
	bits = (bits + 0x80000000) & 0xFFFFFFFFC0000000
	t = math.Float64frombits(bits)

	// One Newton step to 53 bits.
	s := mul(t, t)
	r = x / s
	w := t + t
	r = (r - t) / (w + r)
	return fma(t, r, t)
}
