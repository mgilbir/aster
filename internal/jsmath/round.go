package jsmath

import "math"

// Round is Math.round: the integer nearest x, halves toward +Infinity, so
// Round(-2.5) is -2 and Round(0.49999999999999994) is 0 (math.Round rounds
// halves away from zero, and math.Floor(x+0.5) is wrong for the latter). A
// result of zero keeps the sign of a negative x (Round(-0.3) is -0); NaN and
// the infinities are returned as they are.
func Round(x float64) float64 {
	x = float64(x) // a caller's product is rounded before it is compared
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && (x < 0 || math.Signbit(x)) {
		return math.Copysign(0, -1)
	}
	return r
}

// Sign is Math.sign: 1, -1, or x itself for 0, -0 and NaN.
func Sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x
}
