package scene

import (
	"math"
	"strconv"
)

// AppendNumber appends f formatted as JavaScript's Number.prototype.toString
// does (the text vega writes into every path and attribute). It matches
// jsval.AppendJSNumber but allocates nothing, which matters because path data
// is almost entirely numbers.
func AppendNumber(dst []byte, f float64) []byte {
	switch {
	case f != f:
		return append(dst, "NaN"...)
	case f == 0:
		return append(dst, '0')
	case math.IsInf(f, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(f, -1):
		return append(dst, "-Infinity"...)
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		if math.Abs(f) < 1e15 {
			return strconv.AppendInt(dst, int64(f), 10)
		}
		return strconv.AppendFloat(dst, f, 'f', -1, 64)
	}
	if f < 0 {
		dst = append(dst, '-')
		f = -f
	}
	// Shortest round-trip digits as "d.ddde±XX" (or "de±XX").
	var buf [32]byte
	e := strconv.AppendFloat(buf[:0], f, 'e', -1, 64)
	mark := 0
	for e[mark] != 'e' {
		mark++
	}
	var digits [24]byte
	k := 0
	for _, c := range e[:mark] {
		if c != '.' {
			digits[k] = c
			k++
		}
	}
	for k > 1 && digits[k-1] == '0' {
		k--
	}
	exp := 0
	neg := e[mark+1] == '-'
	for _, c := range e[mark+2:] {
		exp = exp*10 + int(c-'0')
	}
	if neg {
		exp = -exp
	}
	n := exp + 1 // f = 0.digits × 10^n
	d := digits[:k]
	switch {
	case k <= n && n <= 21:
		dst = append(dst, d...)
		for i := 0; i < n-k; i++ {
			dst = append(dst, '0')
		}
	case 0 < n && n <= 21:
		dst = append(dst, d[:n]...)
		dst = append(dst, '.')
		dst = append(dst, d[n:]...)
	case -6 < n && n <= 0:
		dst = append(dst, "0."...)
		for i := 0; i < -n; i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, d...)
	default:
		dst = append(dst, d[0])
		if k > 1 {
			dst = append(dst, '.')
			dst = append(dst, d[1:]...)
		}
		dst = append(dst, 'e')
		x := n - 1
		if x >= 0 {
			dst = append(dst, '+')
		}
		dst = strconv.AppendInt(dst, int64(x), 10)
	}
	return dst
}

// appendScaled writes n / 10^d as a shortest decimal: the text JavaScript
// prints for the double nearest to n/10^d, valid because |n| < 1e15 has fewer
// digits than a double can distinguish and d <= 6 keeps the value out of
// exponent notation.
func appendScaled(dst []byte, n int64, d int) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	if n < 0 {
		dst = append(dst, '-')
		n = -n
	}
	var tmp [24]byte
	i := len(tmp)
	for n > 0 || i > len(tmp)-d-1 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	digits := tmp[i:]
	intLen := len(digits) - d
	frac := digits[intLen:]
	for len(frac) > 0 && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	dst = append(dst, digits[:intLen]...)
	if len(frac) > 0 {
		dst = append(dst, '.')
		dst = append(dst, frac...)
	}
	return dst
}
