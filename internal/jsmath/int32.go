package jsmath

import "math"

// ToInt32 is JavaScript's ToInt32 (what `x | 0`, `~~x` and the bitwise
// operators apply to a number): truncation toward zero, wrapped modulo 2^32
// into the signed range, with NaN and the infinities giving 0.
//
// A Go conversion is not that: int32(f) and int64(f) of a value out of the
// target's range are implementation-defined (amd64 gives the minimum, arm64
// saturates), so even int32(int64(f)) differs between platforms once |f|
// passes 2^63.
func ToInt32(f float64) int32 {
	if f >= -2147483648 && f <= 2147483647 {
		return int32(f) // in range: truncates toward zero; NaN fails the test
	}
	return toInt32Wide(f)
}

func toInt32Wide(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	// math.Mod is exact and keeps the sign, so the remainder lies strictly
	// within ±2^32, where the int64 conversion is defined.
	return int32(uint32(int64(math.Mod(math.Trunc(f), 4294967296))))
}

// ToUint32 is JavaScript's ToUint32 (what `x >>> 0` applies to a number):
// ToInt32's bits read as unsigned.
func ToUint32(f float64) uint32 { return uint32(ToInt32(f)) }
