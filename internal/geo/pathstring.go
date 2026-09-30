package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// pathString is d3-geo's PathString stream: it renders projected geometry as an
// SVG path data string. Coordinates are rounded to `digits` decimals the way
// d3-geo v3 does: Math.round(x * 10^digits) / 10^digits, printed with
// JavaScript's shortest number formatting (so trailing zeros vanish and -0
// prints as 0). digits < 0 disables rounding, and more than 15 digits also
// means no rounding, as upstream.
type pathString struct {
	buf    []byte
	radius float64
	k      float64 // 10^digits, or 0 when not rounding
	d      int     // digits when rounding

	line  float64 // 0 inside a polygon (rings close), NaN otherwise
	point int     // 0: next point starts a line, 1: continues, 2: isolated point

	circle       []byte // cached circle fragment for `circleRadius`
	circleRadius float64
	circleValid  bool
}

func newPathString(digits int) *pathString {
	s := &pathString{radius: 4.5, line: nan, point: 2}
	s.setDigits(digits)
	return s
}

func (s *pathString) setDigits(digits int) {
	s.circleValid = false
	if digits < 0 || digits > 15 {
		s.k, s.d = 0, 0
		return
	}
	s.d = digits
	s.k = math.Pow10(digits)
}

func (s *pathString) pointRadius(r float64) { s.radius = r }

// num appends one coordinate.
func (s *pathString) num(v float64) {
	if s.k == 0 {
		s.buf = jsval.AppendJSNumber(s.buf, v)
		return
	}
	n := jsRound(v * s.k)
	if s.d <= 6 && n > -1e15 && n < 1e15 {
		s.buf = appendScaled(s.buf, int64(n), s.d)
		return
	}
	s.buf = jsval.AppendJSNumber(s.buf, n/s.k)
}

// appendScaled writes n / 10^d as a shortest decimal: the same text JavaScript
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

func (s *pathString) xy(cmd byte, x, y float64) {
	s.buf = append(s.buf, cmd)
	s.num(x)
	s.buf = append(s.buf, ',')
	s.num(y)
}

func (s *pathString) PolygonStart() { s.line = 0 }
func (s *pathString) PolygonEnd()   { s.line = nan }
func (s *pathString) LineStart()    { s.point = 0 }
func (s *pathString) LineEnd() {
	if s.line == 0 {
		s.buf = append(s.buf, 'Z')
	}
	s.point = 2
}
func (s *pathString) Sphere() {}

func (s *pathString) Point(x, y float64) {
	switch s.point {
	case 0:
		s.xy('M', x, y)
		s.point = 1
	case 1:
		s.xy('L', x, y)
	default:
		s.xy('M', x, y)
		if !s.circleValid || s.circleRadius != s.radius {
			saved := s.buf
			s.buf = nil
			r := s.radius
			s.buf = append(s.buf, "m0,"...)
			s.num(r)
			s.buf = append(s.buf, 'a')
			s.num(r)
			s.buf = append(s.buf, ',')
			s.num(r)
			s.buf = append(s.buf, " 0 1,1 0,"...)
			s.num(-2 * r)
			s.buf = append(s.buf, 'a')
			s.num(r)
			s.buf = append(s.buf, ',')
			s.num(r)
			s.buf = append(s.buf, " 0 1,1 0,"...)
			s.num(2 * r)
			s.buf = append(s.buf, 'z')
			s.circle = s.buf
			s.circleRadius = s.radius
			s.circleValid = true
			s.buf = saved
		}
		s.buf = append(s.buf, s.circle...)
	}
}

// result returns the accumulated path and resets the stream; ok is false when
// nothing was drawn (upstream returns null).
func (s *pathString) result() (string, bool) {
	if len(s.buf) == 0 {
		return "", false
	}
	r := string(s.buf)
	s.buf = s.buf[:0]
	return r, true
}
