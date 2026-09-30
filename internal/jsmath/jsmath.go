// Package jsmath provides the transcendental functions with the exact results V8
// produces for Math.sin, cos, tan, atan, atan2, asin, acos, pow (and the **
// operator, which calls the same routine), exp, expm1, log, log1p, log2, log10,
// cbrt, sinh, cosh, tanh, asinh, acosh, atanh and hypot.
//
// V8 implements these with fdlibm and FreeBSD msun derivatives
// (src/base/ieee754.cc); Go's math package uses different algorithms (Cephes for
// sin/cos/atan/sinh/cosh/tanh, assembly for exp on some platforms) that disagree
// with fdlibm in the last bit for anywhere from a few percent to a third of
// arguments. Path strings and tick labels print shortest-round-trip numbers, so
// a one-ulp difference changes the output text, and byte-compatible SVG needs the
// same bits. The code follows V8's source, including its departures from
// fdlibm: pow divides by the whole denominator in its final step, exp special-
// cases exp(1), and log10 is built on log.
//
// V8's fdlibm is compiled by clang, which contracts a*b+c into a fused
// multiply-add on arm64 (and does not on x86-64), within one C++ expression only.
// The functions here use the fused form on every platform, written with explicit
// math.FMA calls placed where clang contracts (the left product of an add or
// subtract first, else the right one), so results are platform independent and
// equal V8 on arm64, the platform the recorded test vectors come from. On x86-64
// V8 the last bit can differ from these results for a small fraction of
// arguments (in practice only a few functions and only near rounding ties).
//
// Math.hypot is different: V8 computes it in generated code with separate
// multiply and add, which Hypot mirrors.
//
// Testing: vectors_test.go replays recorded V8 arguments and results
// (testdata/v8_*.bin.gz) and, with -live=N, compares fresh
// arguments against a running node. testdata/eval_v8.mjs is the evaluator.
package jsmath

import "math"

// mul returns the rounded product; the conversion prevents FMA fusion.
func mul(a, b float64) float64 { return float64(a * b) }

// fma is the fused multiply-add a*b+c.
func fma(a, b, c float64) float64 { return math.FMA(a, b, c) }

func hi(x float64) int32  { return int32(math.Float64bits(x) >> 32) }
func lo(x float64) uint32 { return uint32(math.Float64bits(x)) }

func withHigh(x float64, h int32) float64 {
	return math.Float64frombits(uint64(uint32(h))<<32 | math.Float64bits(x)&0xffffffff)
}

func withLow(x float64, l uint32) float64 {
	return math.Float64frombits(math.Float64bits(x)&^0xffffffff | uint64(l))
}

func fromWords(h int32, l uint32) float64 {
	return math.Float64frombits(uint64(uint32(h))<<32 | uint64(l))
}

// ---- sin / cos

const (
	s1 = -1.66666666666666324348e-01
	s2 = 8.33333333332248946124e-03
	s3 = -1.98412698298579493134e-04
	s4 = 2.75573137070700676789e-06
	s5 = -2.50507602534068634195e-08
	s6 = 1.58969099521155010221e-10

	c1 = 4.16666666666666019037e-02
	c2 = -1.38888888888741095749e-03
	c3 = 2.48015872894767294178e-05
	c4 = -2.75573143513906633035e-07
	c5 = 2.08757232129817482790e-09
	c6 = -1.13596475577881948265e-11
)

func kernelSin(x, y float64, iy int) float64 {
	ix := hi(x) & 0x7fffffff
	if ix < 0x3e400000 { // |x| < 2**-27
		if int(x) == 0 {
			return x
		}
	}
	z := mul(x, x)
	v := mul(z, x)
	r := fma(z, fma(z, fma(z, fma(z, s6, s5), s4), s3), s2)
	if iy == 0 {
		return fma(v, fma(z, r, s1), x)
	}
	return x - fma(-v, s1, fma(z, 0.5*y-mul(v, r), -y))
}

func kernelCos(x, y float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix < 0x3e400000 { // |x| < 2**-27
		if int(x) == 0 {
			return 1
		}
	}
	z := mul(x, x)
	r := mul(z, fma(z, fma(z, fma(z, fma(z, fma(z, c6, c5), c4), c3), c2), c1))
	if ix < 0x3FD33333 { // |x| < 0.3
		return 1 - (0.5*z - fma(z, r, -mul(x, y)))
	}
	var qx float64
	if ix > 0x3fe90000 { // x > 0.78125
		qx = 0.28125
	} else {
		qx = fromWords(ix-0x00200000, 0) // x/4
	}
	hz := 0.5*z - qx
	a := 1 - qx
	return a - (hz - fma(z, r, -mul(x, y)))
}

const (
	invpio2 = 6.36619772367581382433e-01
	pio2_1  = 1.57079632673412561417e+00
	pio2_1t = 6.07710050650619224932e-11
	pio2_2  = 6.07710050630396597660e-11
	pio2_2t = 2.02226624879595063154e-21
	pio2_3  = 2.02226624871116645580e-21
	pio2_3t = 8.47842766036889956997e-32
)

var npio2hw = [32]int32{
	0x3FF921FB, 0x400921FB, 0x4012D97C, 0x401921FB, 0x401F6A7A, 0x4022D97C,
	0x4025FDBB, 0x402921FB, 0x402C463A, 0x402F6A7A, 0x4031475C, 0x4032D97C,
	0x40346B9C, 0x4035FDBB, 0x40378FDB, 0x403921FB, 0x403AB41B, 0x403C463A,
	0x403DD85A, 0x403F6A7A, 0x40407E4C, 0x4041475C, 0x4042106C, 0x4042D97C,
	0x4043A28C, 0x40446B9C, 0x404534AC, 0x4045FDBB, 0x4046C6CB, 0x40478FDB,
	0x404858EB, 0x404921FB,
}

// remPio2 reduces a finite x modulo pi/2: x = n*pi/2 + y0 + y1, |y0+y1| <= pi/4.
func remPio2(x float64) (n int, y0, y1 float64) {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix <= 0x3fe921fb { // |x| ~<= pi/4
		return 0, x, 0
	}
	if ix < 0x4002d97c { // |x| < 3pi/4, special case with n=+-1
		if hx > 0 {
			z := x - pio2_1
			if ix != 0x3ff921fb {
				y0 = z - pio2_1t
				y1 = (z - y0) - pio2_1t
			} else {
				z -= pio2_2
				y0 = z - pio2_2t
				y1 = (z - y0) - pio2_2t
			}
			return 1, y0, y1
		}
		z := x + pio2_1
		if ix != 0x3ff921fb {
			y0 = z + pio2_1t
			y1 = (z - y0) + pio2_1t
		} else {
			z += pio2_2
			y0 = z + pio2_2t
			y1 = (z - y0) + pio2_2t
		}
		return -1, y0, y1
	}
	if ix <= 0x413921fb { // |x| ~<= 2^19*(pi/2), medium size
		t := math.Abs(x)
		n = int(int32(fma(t, invpio2, 0.5)))
		fn := float64(n)
		r := fma(-fn, pio2_1, t)
		w := mul(fn, pio2_1t) // 1st round good to 85 bit
		if n < 32 && ix != npio2hw[n-1] {
			y0 = r - w // quick check no cancellation
		} else {
			j := ix >> 20
			y0 = r - w
			i := j - ((hi(y0) >> 20) & 0x7ff)
			if i > 16 { // 2nd iteration needed, good to 118
				t = r
				w = mul(fn, pio2_2)
				r = t - w
				w = fma(fn, pio2_2t, -((t - r) - w))
				y0 = r - w
				i = j - ((hi(y0) >> 20) & 0x7ff)
				if i > 49 { // 3rd iteration need, 151 bits acc
					t = r
					w = mul(fn, pio2_3)
					r = t - w
					w = fma(fn, pio2_3t, -((t - r) - w))
					y0 = r - w
				}
			}
		}
		y1 = (r - y0) - w
		if hx < 0 {
			return -n, -y0, -y1
		}
		return n, y0, y1
	}
	return remPio2Large(x)
}

// Sin is Math.sin.
func Sin(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

// Cos is Math.cos.
func Cos(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}

// ---- atan / atan2

var (
	atanhi = [4]float64{4.63647609000806093515e-01, 7.85398163397448278999e-01, 9.82793723247329054082e-01, 1.57079632679489655800e+00}
	atanlo = [4]float64{2.26987774529616870924e-17, 3.06161699786838301793e-17, 1.39033110312309984516e-17, 6.12323399573676603587e-17}
	aT     = [11]float64{
		3.33333333333329318027e-01, -1.99999999998764832476e-01, 1.42857142725034663711e-01,
		-1.11111104054623557880e-01, 9.09088713343650656196e-02, -7.69187620504482999495e-02,
		6.66107313738753120669e-02, -5.83357013379057348645e-02, 4.97687799461593236017e-02,
		-3.65315727442169155270e-02, 1.62858201153657823623e-02,
	}
)

// Atan is Math.atan.
func Atan(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	var id int
	if ix >= 0x44100000 { // |x| >= 2^66
		if ix > 0x7ff00000 || (ix == 0x7ff00000 && lo(x) != 0) {
			return x + x // NaN
		}
		if hx > 0 {
			return atanhi[3] + atanlo[3]
		}
		return -atanhi[3] - atanlo[3]
	}
	if ix < 0x3fdc0000 { // |x| < 0.4375
		if ix < 0x3e200000 { // |x| < 2^-29
			return x
		}
		id = -1
	} else {
		x = math.Abs(x)
		if ix < 0x3ff30000 { // |x| < 1.1875
			if ix < 0x3fe60000 { // 7/16 <= |x| < 11/16
				id = 0
				x = (2.0*x - 1) / (2.0 + x)
			} else { // 11/16 <= |x| < 19/16
				id = 1
				x = (x - 1) / (x + 1)
			}
		} else {
			if ix < 0x40038000 { // |x| < 2.4375
				id = 2
				x = (x - 1.5) / fma(1.5, x, 1)
			} else { // 2.4375 <= |x| < 2^66
				id = 3
				x = -1.0 / x
			}
		}
	}
	z := mul(x, x)
	w := mul(z, z)
	// Break the sum from i=0 to 10 aT[i]z**(i+1) into odd and even polynomials.
	s1 := mul(z, fma(w, fma(w, fma(w, fma(w, fma(w, aT[10], aT[8]), aT[6]), aT[4]), aT[2]), aT[0]))
	s2 := mul(w, fma(w, fma(w, fma(w, fma(w, aT[9], aT[7]), aT[5]), aT[3]), aT[1]))
	if id < 0 {
		return fma(-x, s1+s2, x)
	}
	z = atanhi[id] - ((fma(x, s1+s2, -atanlo[id])) - x)
	if hx < 0 {
		return -z
	}
	return z
}

const (
	tiny   = 1.0e-300
	pio4   = 7.8539816339744827900e-01
	pio2   = 1.5707963267948965580e+00
	piHi   = 3.1415926535897931160e+00
	piLo   = 1.2246467991473531772e-16
	pio2Hi = 1.57079632679489655800e+00
	pio2Lo = 6.12323399573676603587e-17
)

// Typed copies force double arithmetic where Go would otherwise fold untyped
// constants exactly.
var pio2v, piLov, pio4v float64 = pio2, piLo, pio4

// Atan2 is Math.atan2.
func Atan2(y, x float64) float64 {
	hx, lx := hi(x), lo(x)
	ix := hx & 0x7fffffff
	hy, ly := hi(y), lo(y)
	iy := hy & 0x7fffffff
	if math.IsNaN(x) || math.IsNaN(y) {
		return x + y
	}
	if (uint32(hx)-0x3ff00000)|lx == 0 {
		return Atan(y) // x = 1.0
	}
	m := int32((uint32(hy)>>31)&1 | (uint32(hx)>>30)&2) // 2*sign(x)+sign(y)

	// when y = 0
	if uint32(iy)|ly == 0 {
		switch m {
		case 0, 1:
			return y
		case 2:
			return piHi + tiny
		}
		return -piHi - tiny
	}
	// when x = 0
	if uint32(ix)|lx == 0 {
		if hy < 0 {
			return -pio2 - tiny
		}
		return pio2 + tiny
	}
	// when x is INF
	if ix == 0x7ff00000 {
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return pio4 + tiny
			case 1:
				return -pio4 - tiny
			case 2:
				return 3.0*pio4v + tiny
			}
			return -3.0*pio4v - tiny
		}
		switch m {
		case 0:
			return 0
		case 1:
			return math.Copysign(0, -1)
		case 2:
			return piHi + tiny
		}
		return -piHi - tiny
	}
	// when y is INF
	if iy == 0x7ff00000 {
		if hy < 0 {
			return -pio2 - tiny
		}
		return pio2 + tiny
	}

	// compute y/x
	k := (iy - ix) >> 20
	var z float64
	switch {
	case k > 60: // |y/x| > 2**60
		z = pio2v + 0.5*piLov
		m &= 1 // as in FreeBSD's and V8's version, but not in fdlibm 5.3
	case hx < 0 && k < -60: // |y|/x < -2**60
		z = 0
	default:
		z = Atan(math.Abs(y / x))
	}
	switch m {
	case 0:
		return z
	case 1:
		return withHigh(z, hi(z)^-0x80000000)
	case 2:
		return piHi - (z - piLo)
	}
	return (z - piLo) - piHi
}

// ---- asin / acos

const (
	pS0 = 1.66666666666666657415e-01
	pS1 = -3.25565818622400915405e-01
	pS2 = 2.01212532134862925881e-01
	pS3 = -4.00555345006794114027e-02
	pS4 = 7.91534994289814532176e-04
	pS5 = 3.47933107596021167570e-05
	qS1 = -2.40339491173441421878e+00
	qS2 = 2.02094576023350569471e+00
	qS3 = -6.88283971605453293030e-01
	qS4 = 7.70381505559019352791e-02
)

func asinPoly(t float64) (p, q float64) {
	p = mul(t, fma(t, fma(t, fma(t, fma(t, fma(t, pS5, pS4), pS3), pS2), pS1), pS0))
	q = fma(t, fma(t, fma(t, fma(t, qS4, qS3), qS2), qS1), 1)
	return
}

// Asin is Math.asin.
func Asin(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 { // |x| >= 1
		if (uint32(ix)-0x3ff00000)|lo(x) == 0 {
			return fma(x, pio2Hi, mul(x, pio2Lo)) // asin(1) = +-pi/2 with inexact
		}
		return math.NaN()
	} else if ix < 0x3fe00000 { // |x| < 0.5
		if ix < 0x3e400000 { // |x| < 2**-27
			return x
		}
		t := mul(x, x)
		p, q := asinPoly(t)
		w := p / q
		return fma(x, w, x)
	}
	// 1 > |x| >= 0.5
	w := 1 - math.Abs(x)
	t := w * 0.5
	p, q := asinPoly(t)
	s := math.Sqrt(t)
	if ix >= 0x3FEF3333 { // |x| > 0.975
		w = p / q
		t = pio2Hi - (2.0*fma(s, w, s) - pio2Lo)
	} else {
		w = withLow(s, 0)
		c := fma(-w, w, t) / (s + w)
		r := p / q
		p = fma(2.0*s, r, -(pio2Lo - 2.0*c))
		q = pio4 - 2.0*w
		t = pio4 - (p - q)
	}
	if hx > 0 {
		return t
	}
	return -t
}

// Acos is Math.acos.
func Acos(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 { // |x| >= 1
		if (uint32(ix)-0x3ff00000)|lo(x) == 0 {
			if hx > 0 {
				return 0
			}
			return piHi + 2.0*pio2Lo
		}
		return math.NaN()
	}
	if ix < 0x3fe00000 { // |x| < 0.5
		if ix <= 0x3c600000 {
			return pio2Hi + pio2Lo
		}
		z := mul(x, x)
		p, q := asinPoly(z)
		r := p / q
		return pio2Hi - (x - fma(-x, r, pio2Lo))
	} else if hx < 0 { // x < -0.5
		z := (1 + x) * 0.5
		p, q := asinPoly(z)
		s := math.Sqrt(z)
		r := p / q
		w := fma(r, s, -pio2Lo)
		return piHi - 2.0*(s+w)
	}
	// x > 0.5
	z := (1 - x) * 0.5
	s := math.Sqrt(z)
	df := withLow(s, 0)
	c := fma(-df, df, z) / (s + df)
	p, q := asinPoly(z)
	r := p / q
	w := fma(r, s, c)
	return 2.0 * (df + w)
}

// ---- hypot

// Hypot is Math.hypot for two arguments, computed as V8 does: scale by the
// largest magnitude and sum the squares with Kahan compensation.
func Hypot(a, b float64) float64 {
	abs := [2]float64{math.Abs(a), math.Abs(b)}
	max := 0.0
	nan := false
	for _, v := range abs {
		if v != v {
			nan = true
		} else if v > max {
			max = v
		}
	}
	switch {
	case math.IsInf(max, 1):
		return math.Inf(1)
	case nan:
		return math.NaN()
	case max == 0:
		return 0
	}
	sum, comp := 0.0, 0.0
	for _, v := range abs {
		n := v / max
		summand := mul(n, n) - comp
		prelim := sum + summand
		comp = (prelim - sum) - summand
		sum = prelim
	}
	return math.Sqrt(sum) * max
}
