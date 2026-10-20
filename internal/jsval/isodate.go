package jsval

import "math"

// AppendISODate appends Date.prototype.toISOString of the time ms (epoch
// milliseconds): YYYY-MM-DDTHH:mm:ss.sssZ, a year outside 0..9999 written
// with a sign and six digits. ok is false for an Invalid Date (NaN, or past
// ±8.64e15 ms), whose toJSON is null.
func AppendISODate(dst []byte, ms float64) (out []byte, ok bool) {
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return dst, false
	}
	t := int64(math.Floor(ms))
	day := floorDiv(t, 86400000)
	rem := t - day*86400000
	y, m, d := civilFromDays(day)
	switch {
	case y >= 0 && y <= 9999:
		dst = appendDigits(dst, y, 4)
	case y < 0:
		dst = append(dst, '-')
		dst = appendDigits(dst, -y, 6)
	default:
		dst = append(dst, '+')
		dst = appendDigits(dst, y, 6)
	}
	dst = append(dst, '-')
	dst = appendDigits(dst, m, 2)
	dst = append(dst, '-')
	dst = appendDigits(dst, d, 2)
	dst = append(dst, 'T')
	dst = appendDigits(dst, rem/3600000, 2)
	dst = append(dst, ':')
	dst = appendDigits(dst, rem/60000%60, 2)
	dst = append(dst, ':')
	dst = appendDigits(dst, rem/1000%60, 2)
	dst = append(dst, '.')
	dst = appendDigits(dst, rem%1000, 3)
	return append(dst, 'Z'), true
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// civilFromDays is the proleptic Gregorian date of a day count from
// 1970-01-01 (H. Hinnant's days_from_civil, inverted).
func civilFromDays(z int64) (y, m, d int64) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func appendDigits(dst []byte, v int64, width int) []byte {
	var b [20]byte
	i := len(b)
	for v > 0 || i == len(b) {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	for len(b)-i < width {
		i--
		b[i] = '0'
	}
	return append(dst, b[i:]...)
}
