package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

// The constants are typed float64 so that expressions combining them (30 *
// radians, 961 / tau) are evaluated with double rounding like JavaScript does,
// not exactly as untyped constants would be: exact 30*pi/180 rounds to a
// different double than 30 * (pi/180).
const (
	epsilon   float64 = 1e-6
	epsilon2  float64 = 1e-12
	pi        float64 = math.Pi
	halfPi    float64 = pi / 2
	quarterPi float64 = pi / 4
	tau       float64 = pi * 2

	degrees float64 = 180 / pi
	radians float64 = pi / 180
)

// acos and asin clamp instead of returning NaN for arguments a rounding error
// pushed slightly outside [-1, 1].
func acos(x float64) float64 {
	switch {
	case x > 1:
		return 0
	case x < -1:
		return pi
	}
	return jsmath.Acos(x)
}

func asin(x float64) float64 {
	switch {
	case x > 1:
		return halfPi
	case x < -1:
		return -halfPi
	}
	return jsmath.Asin(x)
}

func haversin(x float64) float64 {
	x = jsmath.Sin(x / 2)
	return float64(x * x)
}

// sign is Math.sign: NaN and zeros are returned unchanged.
func sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x
}

// jsRound is Math.round: halves round toward +Infinity, and the sign of zero
// is preserved for values in [-0.5, -0].
func jsRound(x float64) float64 {
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

// jsMod is the JavaScript % operator (result takes the dividend's sign).
func jsMod(a, b float64) float64 { return math.Mod(a, b) }

// truthy is JavaScript truthiness of a number.
func truthy(x float64) bool { return x != 0 && !math.IsNaN(x) }

func abs(x float64) float64 { return math.Abs(x) }

// Adder is d3-array's Adder: exact floating-point summation (Shewchuk
// partials, after CPython's math.fsum). d3-geo relies on its exactness for the
// sign of small spherical areas.
type adder struct {
	p [32]float64
	n int
}

func (a *adder) add(x float64) {
	i := 0
	for j := 0; j < a.n && j < 32; j++ {
		y := a.p[j]
		hi := x + y
		var lo float64
		if math.Abs(x) < math.Abs(y) {
			lo = x - (hi - y)
		} else {
			lo = y - (hi - x)
		}
		if lo != 0 {
			a.p[i] = lo
			i++
		}
		x = hi
	}
	// A double cannot need 32 non-overlapping partials in practice; the guard
	// only keeps a hostile input from indexing past the array.
	if i < len(a.p) {
		a.p[i] = x
		a.n = i + 1
	} else {
		a.n = len(a.p)
	}
}

func (a *adder) value() float64 {
	n := a.n
	var x, y, lo, hi float64
	if n > 0 {
		n--
		hi = a.p[n]
		for n > 0 {
			x = hi
			n--
			y = a.p[n]
			hi = x + y
			lo = y - (hi - x)
			if lo != 0 {
				break
			}
		}
		if n > 0 && ((lo < 0 && a.p[n-1] < 0) || (lo > 0 && a.p[n-1] > 0)) {
			y = lo * 2
			x = hi + y
			if y == x-hi {
				hi = x
			}
		}
	}
	return hi
}

var nan = math.NaN()

// minNaN is Math.min for two arguments (NaN wins), which math.Min matches; it
// is named so the intent shows at call sites.
func minNaN(a, b float64) float64 { return math.Min(a, b) }
