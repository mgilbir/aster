package jsval

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// JSNumberString is JavaScript's Number.prototype.toString(): the shortest
// decimal that reads back as the same double, in exponent form below 1e-6 and
// at or above 1e21.
func JSNumberString(f float64) string {
	return string(AppendJSNumber(nil, f))
}

// AppendJSNumber appends JSNumberString(f) to dst.
func AppendJSNumber(dst []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, "NaN"...)
	case math.IsInf(f, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(f, -1):
		return append(dst, "-Infinity"...)
	case f == 0:
		return append(dst, '0')
	}
	// Integers that are exactly representable print without the digit dance.
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.AppendFloat(dst, f, 'f', -1, 64)
	}
	if f < 0 {
		dst = append(dst, '-')
		f = -f
	}
	digits, n := shortestDigits(f)
	return appendDecimal(dst, digits, n)
}

// shortestDigits returns the shortest round-tripping significant digits of a
// positive finite f and the decimal exponent n such that f = 0.digits × 10^n.
func shortestDigits(f float64) (string, int) {
	var buf [32]byte
	e := strconv.AppendFloat(buf[:0], f, 'e', -1, 64)
	// e is "d.ddde±XX" or "de±XX".
	mark := 0
	for mark < len(e) && e[mark] != 'e' {
		mark++
	}
	mant := e[:mark]
	exp, _ := strconv.Atoi(string(e[mark+1:]))
	digits := make([]byte, 0, len(mant))
	for _, c := range mant {
		if c != '.' {
			digits = append(digits, c)
		}
	}
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return string(digits), exp + 1
}

// appendDecimal writes 0.digits × 10^n following ECMA-262 Number::toString.
func appendDecimal(dst []byte, digits string, n int) []byte {
	k := len(digits)
	switch {
	case k <= n && n <= 21:
		dst = append(dst, digits...)
		for i := 0; i < n-k; i++ {
			dst = append(dst, '0')
		}
	case 0 < n && n <= 21:
		dst = append(dst, digits[:n]...)
		dst = append(dst, '.')
		dst = append(dst, digits[n:]...)
	case -6 < n && n <= 0:
		dst = append(dst, "0."...)
		for i := 0; i < -n; i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, digits...)
	default:
		dst = append(dst, digits[0])
		if k > 1 {
			dst = append(dst, '.')
			dst = append(dst, digits[1:]...)
		}
		dst = append(dst, 'e')
		e := n - 1
		if e >= 0 {
			dst = append(dst, '+')
		}
		dst = strconv.AppendInt(dst, int64(e), 10)
	}
	return dst
}

// ToNumber is JavaScript's Number(x) coercion: null is 0, undefined NaN,
// "" and [] are 0, a one-element array converts its element's string.
func ToNumber(v Value) float64 {
	switch v.Kind() {
	case KindNum, KindTimestamp, KindBool:
		return v.n()
	case KindNull:
		return 0
	case KindStr:
		return StringToNumber(v.s())
	case KindArr:
		items := v.Items()
		switch len(items) {
		case 0:
			return 0
		case 1:
			return StringToNumber(items[0].AsString())
		}
	}
	return math.NaN()
}

// StringToNumber is ECMA-262 StringToNumber: whitespace-trimmed decimal
// literals (including "Infinity", ".5", "5."), 0x/0o/0b integers, and "" as 0.
// Anything else is NaN.
func StringToNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSWhitespace)
	if s == "" {
		return 0
	}
	return parseTrimmedNumber(s)
}

// parseLooseDouble is StringToNumber except that "" reads NaN.
func parseLooseDouble(s string) float64 {
	if s == "" {
		return math.NaN()
	}
	return parseTrimmedNumber(s)
}

func parseTrimmedNumber(s string) float64 {
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			var z big.Int
			if _, ok := z.SetString(s[2:], base); !ok || strings.ContainsAny(s[2:], "_+-") {
				return math.NaN()
			}
			f, _ := new(big.Float).SetInt(&z).Float64()
			return f
		}
	}
	body := s
	if body[0] == '+' || body[0] == '-' {
		body = body[1:]
	}
	if body == "Infinity" {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if !isDecimalLiteral(body) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Out-of-range literals round to ±Infinity or 0, which ParseFloat
		// reports alongside ErrRange.
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f
		}
		return math.NaN()
	}
	return f
}

// isDecimalLiteral matches StrUnsignedDecimalLiteral without Infinity:
// digits [. digits] [e[+-]digits], where either side of the point may be empty
// but not both.
func isDecimalLiteral(s string) bool {
	i, n := 0, len(s)
	intDigits := 0
	for i < n && s[i] >= '0' && s[i] <= '9' {
		i++
		intDigits++
	}
	fracDigits := 0
	if i < n && s[i] == '.' {
		i++
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
			fracDigits++
		}
	}
	if intDigits == 0 && fracDigits == 0 {
		return false
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < n && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := 0
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return false
		}
	}
	return i == n
}

func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// exactDecimal returns the exact decimal expansion of a positive finite f as
// its digit string (no leading zeros) and exponent n, f = 0.digits × 10^n.
func exactDecimal(f float64) (string, int) {
	// A double's exact value has at most 1074 fractional digits.
	s := new(big.Float).SetPrec(0).SetFloat64(f).Text('e', 1100)
	mark := strings.IndexByte(s, 'e')
	exp, _ := strconv.Atoi(s[mark+1:])
	digits := strings.Replace(s[:mark], ".", "", 1)
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return "0", 1
	}
	return digits, exp + 1
}

// roundDigits rounds digits (0.digits × 10^n) to keep the first keep digits,
// rounding half up in magnitude as ECMA-262's toFixed/toPrecision/
// toExponential do ("pick the larger n"). It returns the new digit string of
// exactly keep digits (keep ≥ 0) and the possibly incremented exponent.
func roundDigits(digits string, n, keep int) (string, int) {
	if keep < 0 {
		return "", n
	}
	if keep >= len(digits) {
		return digits + strings.Repeat("0", keep-len(digits)), n
	}
	out := []byte(digits[:keep])
	if digits[keep] >= '5' {
		i := keep - 1
		for i >= 0 {
			if out[i] == '9' {
				out[i] = '0'
				i--
				continue
			}
			out[i]++
			break
		}
		if i < 0 {
			out = append([]byte{'1'}, out...)
			n++
			out = out[:keep]
			if keep == 0 {
				return "1", n
			}
		}
	}
	return string(out), n
}

// ToFixed is Number.prototype.toFixed(d) for 0 ≤ d ≤ 100: the integer n
// nearest to f × 10^d (the larger on an exact tie), written with d decimals.
func ToFixed(f float64, d int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) >= 1e21 {
		return JSNumberString(f)
	}
	neg := f < 0
	if neg {
		f = -f
	}
	r := new(big.Rat).SetFloat64(f)
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)))
	r.Add(r, big.NewRat(1, 2))
	q := new(big.Int).Quo(r.Num(), r.Denom())
	digits := q.String()
	if d > 0 {
		if len(digits) <= d {
			digits = strings.Repeat("0", d-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-d] + "." + digits[len(digits)-d:]
	}
	if neg {
		// (-0.0001).toFixed(2) is "-0.00": the sign survives rounding to zero.
		return "-" + digits
	}
	return digits
}

// ToExponential is Number.prototype.toExponential(d); d < 0 means undefined
// (as many digits as necessary).
func ToExponential(f float64, d int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return JSNumberString(f)
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	var digits string
	var n int
	switch {
	case f == 0:
		if d < 0 {
			d = 0
		}
		digits, n = strings.Repeat("0", d+1), 1
	case d < 0:
		digits, n = shortestDigits(f)
	default:
		digits, n = exactDecimal(f)
		digits, n = roundDigits(digits, n, d+1)
	}
	var b strings.Builder
	b.WriteString(sign)
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	e := n - 1
	if f == 0 {
		e = 0
	}
	b.WriteByte('e')
	if e >= 0 {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(e))
	return b.String()
}

// ToPrecision is Number.prototype.toPrecision(p) for 1 ≤ p ≤ 100.
func ToPrecision(f float64, p int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return JSNumberString(f)
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	var digits string
	var n int
	if f == 0 {
		digits, n = strings.Repeat("0", p), 1
	} else {
		digits, n = exactDecimal(f)
		digits, n = roundDigits(digits, n, p)
	}
	e := n - 1
	if f != 0 && (e < -6 || e >= p) {
		var b strings.Builder
		b.WriteString(sign)
		b.WriteByte(digits[0])
		if p > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		b.WriteByte('e')
		if e >= 0 {
			b.WriteByte('+')
		}
		b.WriteString(strconv.Itoa(e))
		return b.String()
	}
	if e == p-1 {
		return sign + digits
	}
	if e >= 0 {
		return sign + digits[:e+1] + "." + digits[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + digits
}
