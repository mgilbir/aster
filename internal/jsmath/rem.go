package jsmath

import "math"

// Rem is JavaScript's x % y, which is C's fmod: math.Mod. Its result is exact,
// so where x and y are integers that fit a uint64 it is the integer remainder,
// which is many times faster than math.Mod's loop over the exponent
// difference: the seeded random generator computes one per number it draws.
func Rem(x, y float64) float64 {
	if x >= 1 && x < 1<<63 && y >= 1 && y < 1<<63 {
		if ux, uy := uint64(x), uint64(y); float64(ux) == x && float64(uy) == y {
			return float64(ux % uy)
		}
	}
	return math.Mod(x, y)
}
