package jsmath

import "math"

// Math.pow and the ** operator are not fdlibm in current V8. Since V8 11 the
// default (--use-std-math-pow, src/numbers/ieee754.cc math::pow) is the C
// library's pow after the ECMAScript special cases; node 24 (V8 13.6) calls
// glibc's on Linux. That is the ARM optimized-routines pow (glibc
// sysdeps/ieee754/dbl-64/e_pow.c, with e_pow_log_data.c and e_exp_data.c in
// powdata.go): log(x) as a double-double from a 128-entry table and a degree-7
// polynomial, multiplied by y, then exp of that. It is within 0.52 ULP, not
// correctly rounded, so it is ported rather than approximated.
//
// The FMA placement mirrors gcc, which builds glibc, not the clang convention of
// the other files: gcc contracts across statements, and on x86-64 glibc selects
// its FMA build of the file (e_pow-fma.c, -mfma -mavx2) wherever the CPU has
// FMA, which node on x86-64 and on arm64 Linux therefore agree on bit for bit.
// A product that is used more than once, or by anything but an add, stays
// unfused (ar2, r2, ehi, and scale*tmp in the subnormal path of specialcase).
// macOS's libm is a different routine and is not reproduced: node there differs
// from this in about 0.2% of random arguments.

// Constants of the pow polynomial and the log and exp tables, scaled as glibc's
// e_pow_log_data.c and e_exp_data.c scale them.
const (
	powLn2hi = 0x1.62e42fefa3800p-1
	powLn2lo = 0x1.ef35793c76730p-45
	powA0    = -0x1p-1
	powA1    = 0x1.555555555556p-2 * -2
	powA2    = -0x1.0000000000006p-2 * -2
	powA3    = 0x1.999999959554ep-3 * 4
	powA4    = -0x1.555555529a47ap-3 * 4
	powA5    = 0x1.2495b9b4845e9p-3 * -8
	powA6    = -0x1.0002b8b263fc3p-3 * -8

	expInvLn2N   = 0x1.71547652b82fep0 * 128
	expNegLn2hiN = -0x1.62e42fefa0000p-8
	expNegLn2loN = -0x1.cf79abc9e3b3ap-47
	expShift     = 0x1.8p52
	expC2        = 0x1.ffffffffffdbdp-2
	expC3        = 0x1.555555555543cp-3
	expC4        = 0x1.55555cf172b91p-5
	expC5        = 0x1.1111167a4d017p-7

	powOff   = 0x3fe6955500000000
	signBias = 0x800 << 7 // makes exp_inline return a negative result
)

// Pow is Math.pow and the ** operator: V8's math::pow, which handles the
// ECMAScript special cases and leaves the rest to the C library's pow.
func Pow(x, y float64) float64 {
	// ECMAScript differs from C here: any NaN exponent gives NaN, and ±1 raised
	// to an infinite power is NaN rather than 1.
	if y != y || (math.IsInf(y, 0) && (x == 1 || x == -1)) {
		return math.NaN()
	}
	// V8's optimizing compilers do these two without calling pow; the runtime
	// does the same so every tier agrees.
	if y == 2 {
		return x * x
	}
	if y == 0.5 {
		if math.IsInf(x, 0) {
			return math.Inf(1)
		}
		return math.Sqrt(x + 0) // the +0 turns -0 into +0
	}
	return libmPow(x, y)
}

// top12 is the sign and exponent bits of x.
func top12(x float64) uint32 { return uint32(math.Float64bits(x) >> 52) }

// checkint reports whether the finite non-zero value with bits iy is not an
// integer (0), an odd integer (1) or an even one (2).
func checkint(iy uint64) int {
	e := int(iy >> 52 & 0x7ff)
	if e < 0x3ff {
		return 0
	}
	if e > 0x3ff+52 {
		return 2
	}
	if iy&(1<<(0x3ff+52-e)-1) != 0 {
		return 0
	}
	if iy&(1<<(0x3ff+52-e)) != 0 {
		return 1
	}
	return 2
}

// zeroInfNaN reports whether the bits are those of 0, an infinity or a NaN.
func zeroInfNaN(i uint64) bool {
	return 2*i-1 >= 2*math.Float64bits(math.Inf(1))-1
}

// powXflow is the result of an overflow (or, with under, an underflow), negative
// when negative is set.
func powXflow(negative, under bool) float64 {
	r := math.Inf(1)
	if under {
		r = 0
	}
	if negative {
		r = -r
	}
	return r
}

// libmPow is glibc's __pow for finite or infinite x and a y that is not NaN.
func libmPow(x, y float64) float64 {
	var sb uint32
	ix, iy := math.Float64bits(x), math.Float64bits(y)
	topx, topy := top12(x), top12(y)
	const one = 0x3ff0000000000000 // bits of 1.0
	if topx-0x001 >= 0x7ff-0x001 || (topy&0x7ff)-0x3be >= 0x43e-0x3be {
		// x < 0x1p-126 or inf or nan, or |y| < 0x1p-65 or |y| >= 0x1p63 or nan.
		if zeroInfNaN(iy) {
			if 2*iy == 0 {
				return 1
			}
			if ix == one {
				return 1
			}
			if 2*ix > 2*math.Float64bits(math.Inf(1)) {
				return math.NaN()
			}
			if (2*ix < 2*one) == (iy>>63 == 0) {
				return 0 // |x|<1 && y==inf or |x|>1 && y==-inf
			}
			return y * y
		}
		if zeroInfNaN(ix) {
			x2 := x * x
			if ix>>63 != 0 && checkint(iy) == 1 {
				x2 = -x2
			}
			if iy>>63 != 0 {
				return 1 / x2
			}
			return x2
		}
		// x and y are finite and non-zero here.
		if ix>>63 != 0 {
			yint := checkint(iy)
			if yint == 0 {
				return math.NaN()
			}
			if yint == 1 {
				sb = signBias
			}
			ix &= 0x7fffffffffffffff
			topx &= 0x7ff
		}
		if (topy&0x7ff)-0x3be >= 0x43e-0x3be {
			if ix == one {
				return 1
			}
			if topy&0x7ff < 0x3be {
				// |y| < 2^-65, x^y ~= 1 + y*log(x).
				if ix > one {
					return 1 + y
				}
				return 1 - y
			}
			return powXflow(false, (ix > one) != (topy < 0x800))
		}
		if topx == 0 {
			// Normalize a subnormal x so that its exponent becomes negative.
			ix = math.Float64bits(x*0x1p52) & 0x7fffffffffffffff
			ix -= 52 << 52
		}
	}

	hi, lo := powLog(ix)
	ehi := mul(y, hi)
	elo := fma(y, lo, fma(y, hi, -ehi))
	return powExp(ehi, elo, sb)
}

// powLog returns y+tail = log(x), where y is the rounded result and tail has
// about 15 further bits. ix is the bits of x, normalized in the subnormal range
// by using the sign bit for the exponent.
func powLog(ix uint64) (y, tail float64) {
	// x = 2^k z with z in [OFF, 2*OFF), split into 128 subintervals; the ith holds
	// z and c is near its centre.
	tmp := ix - powOff
	i := tmp >> (52 - 7) % 128
	k := int64(tmp) >> 52
	z := math.Float64frombits(ix - tmp&(0xfff<<52))
	kd := float64(k)

	// log(x) = k*Ln2 + log(c) + log1p(z/c-1). 1/c is j/128 or j/256 for an
	// integer j, so r = z/c - 1 is exact.
	e := &powLogTab[i]
	r := fma(z, e.invc, -1)

	// k*Ln2 + log(c) + r.
	t1 := fma(kd, powLn2hi, e.logc)
	t2 := t1 + r
	lo1 := fma(kd, powLn2lo, e.logctail)
	lo2 := t1 - t2 + r

	// k*Ln2 + log(c) + r + A[0]*r*r.
	ar := mul(powA0, r)
	ar2 := mul(r, ar)
	ar3 := mul(r, ar2)
	hi := t2 + ar2
	lo3 := fma(ar, r, -ar2)
	lo4 := t2 - hi + ar2

	// p = log1p(r) - r - A[0]*r*r.
	p := fma(ar2, fma(ar2, fma(r, powA6, powA5), fma(r, powA4, powA3)), fma(r, powA2, powA1))
	lo := fma(ar3, p, lo1+lo2+lo3+lo4)
	y = hi + lo
	return y, hi - y + lo
}

// powExp returns sign*exp(x+xtail), where |xtail| < 2^-8/128 and |xtail| <= |x|;
// sb is signBias or 0 and sets the sign to - or +.
func powExp(x, xtail float64, sb uint32) float64 {
	abstop := top12(x) & 0x7ff
	if abstop-0x3c9 >= 0x408-0x3c9 { // |x| < 2^-54 or |x| >= 512
		if abstop-0x3c9 >= 0x80000000 {
			// Tiny x, including 0: avoid a spurious underflow.
			if sb != 0 {
				return -(1 + x)
			}
			return 1 + x
		}
		if abstop >= 0x409 { // |x| >= 1024
			return powXflow(sb != 0, math.Float64bits(x)>>63 != 0)
		}
		abstop = 0 // large x is special-cased below
	}

	// exp(x) = 2^(k/128) * exp(r), with exp(r) in [2^(-1/256), 2^(1/256)] and
	// x = ln2/128*k + r, with k an integer and r in [-ln2/256, ln2/256].
	kd := fma(x, expInvLn2N, expShift)
	ki := math.Float64bits(kd)
	kd -= expShift
	r := fma(kd, expNegLn2loN, fma(kd, expNegLn2hiN, x))
	// The code assumes 2^-200 < |xtail| < 2^-8/128.
	r += xtail
	// 2^(k/128) ~= scale * (1 + tail).
	idx := 2 * (ki % 128)
	top := (ki + uint64(sb)) << (52 - 7)
	tail := math.Float64frombits(powExpTab[idx])
	sbits := powExpTab[idx+1] + top // a valid scale only when -1023*128 < k < 1024*128
	// exp(x) = 2^(k/128) * exp(r) ~= scale + scale * (tail + exp(r) - 1).
	r2 := mul(r, r)
	tmp := fma(mul(r2, r2), fma(r, expC5, expC4), fma(r2, fma(r, expC3, expC2), tail+r))
	if abstop == 0 {
		return powSpecial(tmp, sbits, ki)
	}
	scale := math.Float64frombits(sbits)
	return fma(scale, tmp, scale)
}

// powSpecial handles the results of powExp that may overflow or underflow when
// computed as scale*(1+tmp) without an intermediate rounding. sbits are the bits
// of scale, whose computed exponent may have overflown into the sign bit; the
// sign of int32(ki) says which way: positive k may overflow, negative underflow.
func powSpecial(tmp float64, sbits, ki uint64) float64 {
	if ki&0x80000000 == 0 {
		// k > 0, the exponent of scale might have overflowed by <= 460.
		sbits -= 1009 << 52
		scale := math.Float64frombits(sbits)
		return 0x1p1009 * fma(scale, tmp, scale)
	}
	// k < 0, with care in the subnormal range. sbits is the signed scale.
	sbits += 1022 << 52
	scale := math.Float64frombits(sbits)
	// scale*tmp is used twice below, so it is rounded once and stays unfused.
	st := mul(scale, tmp)
	y := scale + st
	if math.Abs(y) < 1 {
		// Round y to the right precision before scaling it into the subnormal
		// range, to avoid the double rounding that can cause 0.5+E/2 ulp error.
		one := 1.0
		if y < 0 {
			one = -1
		}
		lo := scale - y + st
		hi := one + y
		lo = one - hi + y + lo
		y = (hi + lo) - one
		if y == 0 {
			y = math.Float64frombits(sbits & 0x8000000000000000) // fix the sign of 0
		}
	}
	return 0x1p-1022 * y
}
