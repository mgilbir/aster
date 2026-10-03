package format

import (
	"math"
	"math/bits"
	"strconv"

	"github.com/mgilbir/aster/internal/jsval"
)

// The functions in this file are Number.prototype.toFixed / toExponential /
// toPrecision for non-negative finite inputs. jsval implements the exact
// ECMAScript algorithms with big arithmetic; here strconv (which is correctly
// rounded, but rounds exact ties to even where ECMAScript rounds them up) is
// used whenever the input provably is not an exact tie, and jsval otherwise.

// toFixed is (x).toFixed(p) for 0 <= p <= 100.
func toFixed(x float64, p int) string {
	if x == 0 {
		x = 0 // drop the sign of -0: (-0).toFixed(2) is "0.00"
	}
	if !(math.Abs(x) < 1e21) || p > 100 { // also NaN and Infinity
		return jsval.ToFixed(x, p)
	}
	if x < 0 {
		return "-" + toFixed(-x, p)
	}
	if isFixedTie(x, p) {
		return jsval.ToFixed(x, p)
	}
	return strconv.FormatFloat(x, 'f', p, 64)
}

// isFixedTie reports whether x (finite, >= 0) lies exactly half way between
// two p-decimal numbers. x = m * 2^e with m odd has exactly -e fractional
// decimal digits when e < 0 (the last being 5), so a tie needs -e == p+1.
func isFixedTie(x float64, p int) bool {
	b := math.Float64bits(x)
	exp := int(b >> 52 & 0x7ff)
	m := b & (1<<52 - 1)
	e := -1074
	if exp != 0 {
		m |= 1 << 52
		e = exp - 1075
	}
	if m == 0 {
		return false
	}
	tz := bits.TrailingZeros64(m)
	e += tz
	return e < 0 && -e == p+1
}

// sigDigits returns the decimal significand digits of x (finite, > 0) and
// the exponent of the first digit, rounded to p significant digits as
// toExponential(p-1)/toPrecision(p) round. p == 0 asks for the shortest
// round-tripping digits (toExponential() without an argument).
func sigDigits(buf []byte, x float64, p int) ([]byte, int) {
	// minNormal is the smallest normal float64.
	const minNormal = 0x1p-1022
	var tmp [40]byte
	if p == 0 {
		return parseExp(buf, strconv.AppendFloat(tmp[:0], x, 'e', -1, 64))
	}
	if p <= 14 {
		// A tie needs the exact expansion to have p+1 <= 15 digits ending in
		// 5. Any decimal of at most 15 digits is its own shortest form, so a
		// tie is visible as such in the shortest digits.
		short, shortExp := parseExp(buf, strconv.AppendFloat(tmp[:0], x, 'e', -1, 64))
		if len(short) <= p && x >= minNormal {
			// The shortest digits are within half an ulp (1.1e-16 relative) of
			// x, far inside the rounding step of p <= 14 digits, so rounding x
			// to p digits gives them again, padded with zeros. A subnormal's
			// ulp is not that small: it takes the general path.
			for len(short) < p {
				short = append(short, '0')
			}
			return short, shortExp
		}
		if !(len(short) == p+1 && short[p] == '5') {
			return parseExp(buf[:0], strconv.AppendFloat(tmp[:0], x, 'e', p-1, 64))
		}
		buf = buf[:0]
	}
	return parseExp(buf, []byte(jsval.ToExponential(x, p-1)))
}

// parseExp splits "d.ddde+XX" into its digits (appended to dst) and exponent.
func parseExp(dst []byte, s []byte) ([]byte, int) {
	i := 0
	for ; i < len(s) && s[i] != 'e'; i++ {
		if s[i] != '.' {
			dst = append(dst, s[i])
		}
	}
	e := 0
	neg := false
	for i++; i < len(s); i++ {
		switch c := s[i]; {
		case c == '-':
			neg = true
		case c >= '0' && c <= '9':
			e = e*10 + int(c-'0')
		}
	}
	if neg {
		e = -e
	}
	return dst, e
}

// toExponential is (x).toExponential(d) for x >= 0 finite, d < 0 meaning
// "as many digits as necessary".
func toExponential(x float64, d int) string {
	if x == 0 || d > 100 || math.IsInf(x, 0) || math.IsNaN(x) {
		return jsval.ToExponential(x, d)
	}
	var buf [32]byte
	p := d + 1
	if d < 0 {
		p = 0
	}
	digits, e := sigDigits(buf[:0], x, p)
	out := make([]byte, 0, len(digits)+8)
	out = append(out, digits[0])
	if len(digits) > 1 {
		out = append(out, '.')
		out = append(out, digits[1:]...)
	}
	out = append(out, 'e')
	if e >= 0 {
		out = append(out, '+')
	}
	out = strconv.AppendInt(out, int64(e), 10)
	return string(out)
}

// toPrecision is (x).toPrecision(p) for x >= 0 finite, 1 <= p <= 100.
func toPrecision(x float64, p int) string {
	if x == 0 || p > 100 || p < 1 || math.IsInf(x, 0) || math.IsNaN(x) {
		return jsval.ToPrecision(x, p)
	}
	var buf [32]byte
	digits, e := sigDigits(buf[:0], x, p)
	var obuf [64]byte
	out := obuf[:0]
	if p+8 > len(obuf) {
		out = make([]byte, 0, p+8)
	}
	if e < -6 || e >= p {
		out = append(out, digits[0])
		if p > 1 {
			out = append(out, '.')
			out = append(out, digits[1:]...)
		}
		out = append(out, 'e')
		if e >= 0 {
			out = append(out, '+')
		}
		return string(strconv.AppendInt(out, int64(e), 10))
	}
	switch {
	case e == p-1:
		out = append(out, digits...)
	case e >= 0:
		out = append(out, digits[:e+1]...)
		out = append(out, '.')
		out = append(out, digits[e+1:]...)
	default:
		out = append(out, "0."...)
		for i := 0; i < -(e + 1); i++ {
			out = append(out, '0')
		}
		out = append(out, digits...)
	}
	return string(out)
}

// decimalParts is d3-format's formatDecimalParts: the coefficient digits and
// decimal exponent of x with p significant digits (p == 0: shortest). ok is
// false for NaN, +-Infinity and +-0.
func decimalParts(x float64, p int) (coef string, exp int, ok bool) {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return "", 0, false
	}
	var buf [32]byte
	digits, e := sigDigits(buf[:0], math.Abs(x), p)
	return string(digits), e, true
}

// decimalExponent is d3-format's exponent(): the decimal exponent of the
// shortest representation of |x|, or NaN when x is 0, NaN or infinite.
func decimalExponent(x float64) float64 {
	_, e, ok := decimalParts(x, 0)
	if !ok {
		return math.NaN()
	}
	return float64(e)
}
