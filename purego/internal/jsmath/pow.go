package jsmath

import "math"

// fdlibm e_pow.c constants.
const (
	l1     = 5.99999999999994648725e-01
	l2     = 4.28571428578550184252e-01
	l3     = 3.33333329818377432918e-01
	l4     = 2.72728123808534006489e-01
	l5     = 2.30660745775561754067e-01
	l6     = 2.06975017800338417784e-01
	p1     = 1.66666666666666019037e-01
	p2     = -2.77777777770155933842e-03
	p3     = 6.61375632143793436117e-05
	p4     = -1.65339022054652515390e-06
	p5     = 4.13813679705723846039e-08
	lg2    = 6.93147180559945286227e-01
	lg2H   = 6.93147182464599609375e-01
	lg2L   = -1.90465429995776804525e-09
	ovt    = 8.0085662595372944372e-17
	cp     = 9.61796693925975554329e-01
	cpH    = 9.61796700954437255859e-01
	cpL    = -7.02846165095275826516e-09
	ivln2  = 1.44269504088896338700e+00
	ivln2H = 1.44269502162933349609e+00
	ivln2L = 1.92596299112661746887e-08
	two53  = 9007199254740992.0
	huge   = 1.0e300
)

var (
	bp  = [2]float64{1.0, 1.5}
	dpH = [2]float64{0.0, 5.84962487220764160156e-01}
	dpL = [2]float64{0.0, 1.35003920212974897128e-08}
)

// Pow is Math.pow and the ** operator, following V8's port of fdlibm's
// __ieee754_pow.
//
// It is bit-exact with V8. Two details differ from a textbook fdlibm port and
// matter for that: the last step divides z*t1 by the whole (t1-2)-(w+z*w), not
// by t1-2 with w+z*w subtracted afterwards, and the overflow/underflow results
// of a huge exponent carry the sign of a negative base with an odd exponent.
func Pow(x, y float64) float64 {
	// ECMAScript differs from C here: any NaN exponent gives NaN, and ±1 raised
	// to an infinite power is NaN rather than 1.
	if y != y || (math.IsInf(y, 0) && (x == 1 || x == -1)) {
		return math.NaN()
	}
	hx, lx := hi(x), lo(x)
	hy, ly := hi(y), lo(y)
	ix, iy := hx&0x7fffffff, hy&0x7fffffff

	// y == 0: x**0 = 1
	if uint32(iy)|ly == 0 {
		return 1
	}
	// x|y == NaN return NaN unless x == 1 then return 1
	if ix > 0x7ff00000 || (ix == 0x7ff00000 && lx != 0) ||
		iy > 0x7ff00000 || (iy == 0x7ff00000 && ly != 0) {
		if (uint32(ix)-0x3ff00000)|lx == 0 {
			return 1
		}
		return math.NaN()
	}

	// yisint: 0 y is not an integer, 1 y is an odd int, 2 y is an even int
	yisint := int32(0)
	if hx < 0 {
		if iy >= 0x43400000 {
			yisint = 2
		} else if iy >= 0x3ff00000 {
			k := (iy >> 20) - 0x3ff
			if k > 20 {
				j := ly >> uint(52-k)
				if j<<uint(52-k) == ly {
					yisint = 2 - int32(j&1)
				}
			} else if ly == 0 {
				j := iy >> uint(20-k)
				if j<<uint(20-k) == iy {
					yisint = 2 - (j & 1)
				}
			}
		}
	}

	// special value of y
	if ly == 0 {
		if iy == 0x7ff00000 { // y is +-inf
			switch {
			case (uint32(ix)-0x3ff00000)|lx == 0:
				return 1 // +-1**+-inf = 1
			case ix >= 0x3ff00000: // (|x|>1)**+-inf = inf,0
				if hy >= 0 {
					return y
				}
				return 0
			default: // (|x|<1)**-,+inf = inf,0
				if hy < 0 {
					return -y
				}
				return 0
			}
		}
		if iy == 0x3ff00000 { // y is +-1
			if hy < 0 {
				return 1 / x
			}
			return x
		}
		if hy == 0x40000000 { // y is 2
			return x * x
		}
		if hy == 0x3fe00000 { // y is 0.5
			if hx >= 0 {
				return math.Sqrt(x)
			}
		}
	}

	ax := math.Abs(x)
	// special value of x
	if lx == 0 {
		if ix == 0x7ff00000 || ix == 0 || ix == 0x3ff00000 {
			z := ax // x is +-0, +-inf, +-1
			if hy < 0 {
				z = 1 / z // z = (1/|x|)
			}
			if hx < 0 {
				if (ix-0x3ff00000)|yisint == 0 {
					z = math.NaN() // (-1)**non-int is NaN
				} else if yisint == 1 {
					z = -z // (x<0)**odd = -(|x|**odd)
				}
			}
			return z
		}
	}

	// (x<0)**(non-int) is NaN
	if ((uint32(hx)>>31)-1)|uint32(yisint) == 0 {
		return math.NaN()
	}

	sgn := 1.0 // sign of result: -1 for (-ve)**(odd int)
	if ((uint32(hx)>>31)-1)|uint32(yisint-1) == 0 {
		sgn = -1
	}

	var t1, t2 float64
	// |y| is huge
	if iy > 0x41e00000 { // if |y| > 2**31
		if iy > 0x43f00000 { // if |y| > 2**64, must o/uflow
			if ix <= 0x3fefffff {
				if hy < 0 {
					return math.Inf(1)
				}
				return 0
			}
			if ix >= 0x3ff00000 {
				if hy > 0 {
					return math.Inf(1)
				}
				return 0
			}
		}
		// over/underflow if x is not close to one
		if ix < 0x3fefffff {
			if hy < 0 {
				return sgn * math.Inf(1)
			}
			return sgn * 0
		}
		if ix > 0x3ff00000 {
			if hy > 0 {
				return sgn * math.Inf(1)
			}
			return sgn * 0
		}
		// now |1-x| is tiny <= 2**-20, suffice to compute
		// log(x) by x-x^2/2+x^3/3-x^4/4
		t := ax - 1 // t has 20 trailing zeros
		w := mul(t, t) * fma(-t, fma(-t, 0.25, 0.3333333333333333333333), 0.5)
		u := mul(ivln2H, t) // ivln2_h has 21 sig. bits
		v := fma(t, ivln2L, -mul(w, ivln2))
		t1 = withLow(u+v, 0)
		t2 = v - (t1 - u)
	} else {
		n := int32(0)
		// take care of subnormal number
		if ix < 0x00100000 {
			ax *= two53
			n -= 53
			ix = hi(ax)
		}
		n += (ix >> 20) - 0x3ff
		j := ix & 0x000fffff
		// determine interval
		ix = j | 0x3ff00000 // normalize ix
		var k int
		switch {
		case j <= 0x3988E: // |x| < sqrt(3/2)
			k = 0
		case j < 0xBB67A: // |x| < sqrt(3)
			k = 1
		default:
			k = 0
			n++
			ix -= 0x00100000
		}
		ax = withHigh(ax, ix)

		// compute s = s_h+s_l = (x-1)/(x+1) or (x-1.5)/(x+1.5)
		u := ax - bp[k] // bp[0]=1.0, bp[1]=1.5
		v := 1 / (ax + bp[k])
		s := mul(u, v)
		sH := withLow(s, 0)
		// t_h = ax + bp[k] High
		tH := fromWords(((ix>>1)|0x20000000)+0x00080000+int32(k<<18), 0)
		tL := ax - (tH - bp[k])
		sL := mul(v, fma(-sH, tL, fma(-sH, tH, u)))
		// compute log(ax)
		s2 := mul(s, s)
		r := mul(mul(s2, s2), fma(s2, fma(s2, fma(s2, fma(s2, fma(s2, l6, l5), l4), l3), l2), l1))
		r = fma(sL, sH+s, r)
		s2 = mul(sH, sH)
		tH = withLow(3.0+s2+r, 0)
		tL = r - ((tH - 3.0) - s2)
		// u+v = s*(1+...)
		u = mul(sH, tH)
		v = fma(sL, tH, mul(tL, s))
		// 2/(3log2)*(s+...)
		pH := withLow(u+v, 0)
		pL := v - (pH - u)
		zH := mul(cpH, pH) // cp_h+cp_l = 2/(3*log2)
		zL := fma(cpL, pH, mul(pL, cp)) + dpL[k]
		// log2(ax) = (s+..)*2/(3*log2) = n + dp_h + z_h + z_l
		t := float64(n)
		t1 = withLow(((zH+zL)+dpH[k])+t, 0)
		t2 = zL - (((t1 - t) - dpH[k]) - zH)
	}

	// split up y into y1+y2 and compute (y1+y2)*(t1+t2)
	y1 := withLow(y, 0)
	pL := fma(y-y1, t1, mul(y, t2))
	pH := mul(y1, t1)
	z := pL + pH
	j, i := hi(z), lo(z)
	if j >= 0x40900000 { // z >= 1024
		if uint32(j-0x40900000)|i != 0 { // if z > 1024
			return sgn * math.Inf(1) // overflow
		}
		if pL+ovt > z-pH {
			return sgn * math.Inf(1) // overflow
		}
	} else if uint32(j)&0x7fffffff >= 0x4090cc00 { // z <= -1075
		if (uint32(j)-0xc090cc00)|i != 0 { // z < -1075
			return sgn * 0 // underflow
		}
		if pL <= z-pH {
			return sgn * 0 // underflow
		}
	}
	// compute 2**(p_h+p_l)
	ii := j & 0x7fffffff
	k := (ii >> 20) - 0x3ff
	n := int32(0)
	if ii > 0x3fe00000 { // if |z| > 0.5, set n = [z+0.5]
		n = j + (0x00100000 >> uint(k+1))
		k = ((n & 0x7fffffff) >> 20) - 0x3ff // new k for n
		t := withHigh(0, n&^(0x000fffff>>uint(k)))
		n = ((n & 0x000fffff) | 0x00100000) >> uint(20-k)
		if j < 0 {
			n = -n
		}
		pH -= t
	}
	t := withLow(pL+pH, 0)
	u := mul(t, lg2H)
	v := fma(pL-(t-pH), lg2, mul(t, lg2L))
	z = u + v
	w := v - (z - u)
	t = mul(z, z)
	t1 = fma(-t, fma(t, fma(t, fma(t, fma(t, p5, p4), p3), p2), p1), z)
	r := mul(z, t1) / ((t1 - 2) - fma(z, w, w)) // V8 divides by the whole denominator, unlike fdlibm's e_pow.c
	z = 1 - (r - z)
	j = hi(z)
	j += n << 20
	if (j >> 20) <= 0 {
		z = math.Ldexp(z, int(n)) // subnormal output
	} else {
		z = withHigh(z, j)
	}
	return sgn * z
}
