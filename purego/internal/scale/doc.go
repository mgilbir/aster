// Package scale implements the scale layer of Vega: d3-scale's scales,
// d3-interpolate's interpolators, d3-array's ticks, vega-scale's registry, tick
// and label helpers, and the colour schemes. Colour parsing and conversion
// (d3-color) live in the color subpackage, which has no dependency on the value
// model so that the expression layer can use it too.
//
// # Scales
//
// New builds a scale from its vega-scale type name ("linear", "band",
// "sequential-log", "time", ...) and the Is* functions answer vega-scale's
// metadata questions. Scales are dynamically typed the way Vega's are: domains
// and ranges are slices of jsval.Value, and Apply takes a jsval.Value. Every
// scale implements Scale; optional behaviour (Clamper, Niceable, Ticker,
// Inverter, ...) is exposed through small capability interfaces that mirror
// the methods a d3 scale object may or may not have, so callers test with a
// type assertion where vega-encode tests `isFunction(scale[key])`. Set and Get
// do the name-based dispatch for the plain property setters.
//
// The concrete types (Continuous, Sequential, Diverging, Band, Ordinal,
// Quantile, Quantize, Threshold, BinOrdinal, Identity, Time) also offer
// allocation-free numeric entry points where they matter (ApplyFloat,
// Band.Position, InvertNumber, ...).
//
// # Ticks and labels
//
// Ticks, TickStep, TickIncrement and Nice are d3-array's, including its
// floating point behaviour. TickCountFor, TickValues, ValidTicks, TickFormat,
// LabelValues, LabelFormat, LabelFraction and DomainCaption are vega-scale's;
// the formatters come from a format.Locale.
//
// # Numeric parity
//
// Results are meant to match V8 bit for bit where the arithmetic is plain
// IEEE-754. Products that upstream rounds before adding are written
// float64(x*y)+z so the compiler cannot fuse them into an FMA (arm64 does).
// Transcendental functions (Pow, Exp) come from Go's math package and can
// differ from V8 in the last few bits.
package scale

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// jsRound is JavaScript's Math.round: halves round toward +Infinity, and the
// result keeps the sign of a negative zero. math.Round differs (away from zero)
// and floor(x+0.5) is wrong for 0.49999999999999994.
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

// jsSign is Math.sign.
func jsSign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x // 0, -0 or NaN
}

// log10 reproduces V8's Math.log10 (fdlibm's e_log10.c). Go's math.Log10 is
// computed as log2(x)*(ln2/ln10), which is not exact for powers of ten
// (log10(1000) comes out as 2.9999999999999996), and tick generation depends on
// Math.floor(Math.log10(step)) being exact there.
func log10(x float64) float64 {
	const (
		two54   = 1.80143985094819840000e+16
		ivln10  = 4.34294481903251816668e-01
		log102h = 3.01029995663611771306e-01
		log102l = 3.69423907715893078616e-13
	)
	bits := math.Float64bits(x)
	hx := int32(bits >> 32)
	lx := uint32(bits)
	k := int32(0)
	if hx < 0x00100000 { // x < 2**-1022
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		bits = math.Float64bits(x)
		hx = int32(bits >> 32)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	i := int32(uint32(k) >> 31)
	hx = (hx & 0x000fffff) | ((0x3ff - i) << 20)
	y := float64(k + i)
	bits = uint64(uint32(hx))<<32 | (math.Float64bits(x) & 0xffffffff)
	x = math.Float64frombits(bits)
	z := float64(y*log102l) + float64(ivln10*jsmath.Log(x))
	return z + float64(y*log102h)
}

// log2 mirrors Math.log2 closely enough for exact powers of two, which is the
// only place d3 relies on it (base 2 log scales).
func log2(x float64) float64 { return jsmath.Log2(x) }

// jsPow is Math.pow. It differs from Go's math.Pow in two places: any power
// with a NaN exponent is NaN (Go returns 1 for base 1), and a base of +-1 with
// an infinite exponent is NaN (Go returns 1).
func jsPow(x, y float64) float64 {
	if math.IsNaN(y) || (math.IsInf(y, 0) && (x == 1 || x == -1)) {
		return math.NaN()
	}
	return jsmath.Pow(x, y)
}
