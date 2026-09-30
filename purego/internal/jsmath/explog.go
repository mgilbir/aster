package jsmath

import "math"

// The exponential and logarithm family, ported from V8's src/base/ieee754.cc
// (fdlibm and FreeBSD msun derivatives).
//
// Fused multiply-adds are placed as clang places them on arm64: it contracts
// within one C++ expression only, fusing the left product of an addition first
// and the right one otherwise, and never across statements. So a value that the
// C++ assigns to a local and reuses (t1, t2, hfsq, ...) is a separate, rounded
// product here.

const (
	ln2Hi  = 6.93147180369123816490e-01 // 0x3FE62E42, 0xFEE00000
	ln2Lo  = 1.90821492927058770002e-10 // 0x3DEA39EF, 0x35793C76
	two54  = 1.80143985094819840000e+16
	invLn2 = 1.44269504088896338700e+00
)

// ---- exp

const (
	expOThreshold = 7.09782712893383973096e+02
	expUThreshold = -7.45133219101941108420e+02
)

// Exp is Math.exp.
func Exp(x float64) float64 {
	hx := uint32(hi(x))
	xsb := int32(hx>>31) & 1
	hx &= 0x7fffffff

	if hx >= 0x40862E42 { // |x| >= 709.78...
		if hx >= 0x7ff00000 {
			if hx&0xfffff|lo(x) != 0 {
				return x + x // NaN
			}
			if xsb == 0 {
				return x
			}
			return 0 // exp(-inf)
		}
		if x > expOThreshold {
			return math.Inf(1)
		}
		if x < expUThreshold {
			return 0
		}
	}

	var hiPart, loPart float64
	k := int32(0)
	if hx > 0x3FD62E42 { // |x| > 0.5 ln2
		if hx < 0x3FF0A2B2 { // and |x| < 1.5 ln2
			// V8 special-cases exp(1) because the reduction below gets the last
			// bit of e wrong.
			if x == 1 {
				return math.E
			}
			if xsb == 0 {
				hiPart = x - ln2Hi
				loPart = ln2Lo
			} else {
				hiPart = x + ln2Hi
				loPart = -ln2Lo
			}
			k = 1 - xsb - xsb
		} else {
			half := 0.5
			if xsb != 0 {
				half = -0.5
			}
			k = int32(fma(invLn2, x, half))
			t := float64(k)
			hiPart = fma(-t, ln2Hi, x) // t*ln2Hi is exact here
			loPart = mul(t, ln2Lo)
		}
		x = hiPart - loPart
	} else if hx < 0x3E300000 { // |x| < 2**-28
		return 1 + x
	}

	t := mul(x, x)
	c := fma(-t, fma(t, fma(t, fma(t, fma(t, p5, p4), p3), p2), p1), x)
	if k == 0 {
		return 1 - (mul(x, c)/(c-2) - x)
	}
	y := 1 - ((loPart - mul(x, c)/(2-c)) - hiPart)
	if k >= -1021 {
		if k == 1024 {
			return mul(mul(y, 2), 8.988465674311579539e307)
		}
		return mul(y, fromWords(0x3ff00000+int32(uint32(k)<<20), 0))
	}
	return mul(mul(y, fromWords(0x3ff00000+int32(uint32(k+1000)<<20), 0)), 9.33263618503218878990e-302)
}

// ---- log

const (
	lg1  = 6.666666666666735130e-01
	lg2k = 3.999999999940941908e-01
	lg3  = 2.857142874366239149e-01
	lg4  = 2.222219843214978396e-01
	lg5  = 1.818357216161805012e-01
	lg6  = 1.531383769920937332e-01
	lg7  = 1.479819860511658591e-01
)

// logPoly returns R(z) of e_log.c for z = s*s, evaluated as V8 does: two
// separate products t1 and t2 that are added without fusion.
func logPoly(z float64) float64 {
	w := mul(z, z)
	t1 := mul(w, fma(w, fma(w, lg6, lg4), lg2k))
	t2 := mul(z, fma(w, fma(w, fma(w, lg7, lg5), lg3), lg1))
	return t2 + t1
}

// Log is Math.log.
func Log(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	k := int32(0)
	if hx < 0x00100000 { // x < 2**-1022
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54 // subnormal, scale up
		hx = hi(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHigh(x, hx|(i^0x3ff00000)) // normalize x or x/2
	k += i >> 20
	f := x - 1
	if 0x000fffff&(2+hx) < 3 { // -2**-20 <= f < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			dk := float64(k)
			return fma(dk, ln2Hi, mul(dk, ln2Lo))
		}
		r := mul(mul(f, f), fma(-0.33333333333333333, f, 0.5))
		if k == 0 {
			return f - r
		}
		dk := float64(k)
		return fma(dk, ln2Hi, -((fma(-dk, ln2Lo, r)) - f))
	}
	s := f / (2 + f)
	dk := float64(k)
	z := mul(s, s)
	i = hx - 0x6147a
	j := 0x6b851 - hx
	i |= j
	r := logPoly(z)
	if i > 0 {
		hfsq := mul(mul(0.5, f), f)
		if k == 0 {
			return f - fma(-s, hfsq+r, hfsq)
		}
		return fma(dk, ln2Hi, -((hfsq - fma(s, hfsq+r, mul(dk, ln2Lo))) - f))
	}
	if k == 0 {
		return fma(-s, f-r, f)
	}
	return fma(dk, ln2Hi, -((fma(s, f-r, -mul(dk, ln2Lo))) - f))
}

// ---- log1p

const (
	lp1 = lg1
	lp2 = lg2k
)

// Log1p is Math.log1p.
func Log1p(x float64) float64 {
	hx := hi(x)
	ax := hx & 0x7fffffff
	k := int32(1)
	var f, c float64
	var hu int32
	if hx < 0x3FDA827A { // 1+x < sqrt(2)+
		if ax >= 0x3ff00000 { // x <= -1
			if x == -1 {
				return math.Inf(-1)
			}
			return math.NaN()
		}
		if ax < 0x3E200000 { // |x| < 2**-29
			if ax < 0x3C900000 { // |x| < 2**-54
				return x
			}
			return x - mul(mul(x, x), 0.5)
		}
		if hx > 0 || hx <= int32(-0x402d413c) { // 0xBFD2BEC4: sqrt(2)/2- <= 1+x < sqrt(2)+
			k = 0
			f = x
			hu = 1
		}
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if k != 0 {
		var u float64
		if hx < 0x43400000 {
			u = 1 + x
			hu = hi(u)
			k = (hu >> 20) - 1023
			if k > 0 {
				c = 1 - (u - x)
			} else {
				c = x - (u - 1)
			}
			c /= u
		} else {
			u = x
			hu = hi(u)
			k = (hu >> 20) - 1023
			c = 0
		}
		hu &= 0x000fffff
		if hu < 0x6A09E { // u ~< sqrt(2)
			u = withHigh(u, hu|0x3ff00000)
		} else {
			k++
			u = withHigh(u, hu|0x3fe00000) // normalize u/2
			hu = (0x00100000 - hu) >> 2
		}
		f = u - 1
	}
	hfsq := mul(mul(0.5, f), f)
	kf := float64(k)
	if hu == 0 { // |f| < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			c = fma(kf, ln2Lo, c)
			return fma(kf, ln2Hi, c)
		}
		r := mul(hfsq, fma(-0.66666666666666666, f, 1))
		if k == 0 {
			return f - r
		}
		return fma(kf, ln2Hi, -((r - fma(kf, ln2Lo, c)) - f))
	}
	s := f / (2 + f)
	z := mul(s, s)
	r := mul(z, fma(z, fma(z, fma(z, fma(z, fma(z, fma(z, lg7, lg6), lg5), lg4), lg3), lg2k), lg1))
	if k == 0 {
		return f - fma(-s, hfsq+r, hfsq)
	}
	return fma(kf, ln2Hi, -((hfsq - fma(s, hfsq+r, fma(kf, ln2Lo, c))) - f))
}

// ---- log2

const (
	ivln2Hi = 1.44269504072144627571e+00 // 0x3FF71547, 0x65200000
	ivln2Lo = 1.67517131648865118353e-10 // 0x3DE705FC, 0x2EEFA200
)

// Log2 is Math.log2.
func Log2(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = hi(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHigh(x, hx|(i^0x3ff00000))
	k += i >> 20
	y := float64(k)
	f := x - 1
	hfsq := mul(mul(0.5, f), f)

	// k_log1p(f)
	s := f / (2 + f)
	z := mul(s, s)
	r := mul(s, mul(mul(0.5, f), f)+logPoly(z))

	// f-hfsq must be evaluated in extra precision to avoid a large cancellation
	// when x is near sqrt(2) or 1/sqrt(2), and y must be added in extra
	// precision for the same reason; hence the Dekker-style splitting.
	h := withLow(f-hfsq, 0)
	l := ((f - h) - hfsq) + r
	valHi := mul(h, ivln2Hi)
	valLo := fma(l+h, ivln2Lo, mul(l, ivln2Hi))

	w := y + valHi
	valLo += (y - w) + valHi
	valHi = w
	return valLo + valHi
}

// ---- log10

const (
	ivln10   = 4.34294481903251816668e-01
	log102Hi = 3.01029995663611771306e-01 // 0x3FD34413, 0x509F6000
	log102Lo = 3.69423907715893078616e-13 // 0x3D59FEF3, 0x11F12B36
)

// Log10 is Math.log10. V8 builds it on Log (n*log10(2) + log(m)/ln(10)) rather
// than on a dedicated kernel, which is why it is not the correctly rounded value
// as often as Log2 is.
func Log10(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = hi(x)
		lx = lo(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0
	}
	k += (hx >> 20) - 1023

	i := int32(uint32(k) >> 31)
	hx = (hx & 0x000fffff) | ((0x3ff - i) << 20)
	y := float64(k + i)
	x = fromWords(hx, lx)

	z := fma(y, log102Lo, mul(ivln10, Log(x)))
	return fma(y, log102Hi, z)
}

// ---- expm1

const (
	expm1Q1 = -3.33333333333331316428e-02
	expm1Q2 = 1.58730158725481460165e-03
	expm1Q3 = -7.93650757867487942473e-05
	expm1Q4 = 4.00821782732936239552e-06
	expm1Q5 = -2.01099218183624371326e-07
)

// Expm1 is Math.expm1.
func Expm1(x float64) float64 {
	hx := uint32(hi(x))
	xsb := hx & 0x80000000
	hx &= 0x7fffffff

	if hx >= 0x4043687A { // |x| >= 56*ln2
		if hx >= 0x40862E42 { // |x| >= 709.78...
			if hx >= 0x7ff00000 {
				if hx&0xfffff|lo(x) != 0 {
					return x + x // NaN
				}
				if xsb == 0 {
					return x
				}
				return -1
			}
			if x > expOThreshold {
				return math.Inf(1)
			}
		}
		if xsb != 0 { // x < -56*ln2
			return -1
		}
	}

	var hiPart, loPart, c float64
	var k int32
	if hx > 0x3FD62E42 { // |x| > 0.5 ln2
		if hx < 0x3FF0A2B2 { // and |x| < 1.5 ln2
			if xsb == 0 {
				hiPart = x - ln2Hi
				loPart = ln2Lo
				k = 1
			} else {
				hiPart = x + ln2Hi
				loPart = -ln2Lo
				k = -1
			}
		} else {
			half := 0.5
			if xsb != 0 {
				half = -0.5
			}
			k = int32(fma(invLn2, x, half))
			t := float64(k)
			hiPart = fma(-t, ln2Hi, x)
			loPart = mul(t, ln2Lo)
		}
		x = hiPart - loPart
		c = (hiPart - x) - loPart
	} else if hx < 0x3C900000 { // |x| < 2**-54
		return x
	}

	hfx := 0.5 * x
	hxs := mul(x, hfx)
	r1 := fma(hxs, fma(hxs, fma(hxs, fma(hxs, fma(hxs, expm1Q5, expm1Q4), expm1Q3), expm1Q2), expm1Q1), 1)
	t := fma(-r1, hfx, 3)
	e := mul(hxs, (r1-t)/fma(-x, t, 6))
	if k == 0 {
		return x - fma(x, e, -hxs) // c is 0
	}
	twopk := fromWords(0x3ff00000+int32(uint32(k)<<20), 0)
	e = fma(x, e-c, -c)
	e -= hxs
	if k == -1 {
		return 0.5*(x-e) - 0.5
	}
	if k == 1 {
		if x < -0.25 {
			return -2 * (e - (x + 0.5))
		}
		return 1 + 2*(x-e)
	}
	if k <= -2 || k > 56 { // suffice to return exp(x)-1
		y := 1 - (e - x)
		if k == 1024 {
			y = mul(mul(y, 2), 8.98846567431158e+307)
		} else {
			y = mul(y, twopk)
		}
		return y - 1
	}
	var y float64
	if k < 20 {
		t = fromWords(0x3ff00000-(0x200000>>uint(k)), 0) // 1-2^-k
		y = t - (e - x)
		y = mul(y, twopk)
	} else {
		t = fromWords(int32(0x3ff-k)<<20, 0) // 2^-k
		y = x - (e + t)
		y += 1
		y = mul(y, twopk)
	}
	return y
}
