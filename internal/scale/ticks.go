package scale

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

var (
	e10 = math.Sqrt(50)
	e5  = math.Sqrt(10)
	e2  = math.Sqrt(2)
)

// tickSpec is d3-array's tickSpec: the integer index range [i1, i2] of ticks
// and the increment. A negative inc means the step is 1/-inc (used so that
// fractional ticks are computed as i/inc, which is the more accurate way to
// produce decimals such as 0.3).
func tickSpec(start, stop, count float64) (i1, i2, inc float64) {
	for {
		step := (stop - start) / math.Max(0, count)
		power := math.Floor(log10(step))
		err := step / pow10n(power)
		factor := 1.0
		switch {
		case err >= e10:
			factor = 10
		case err >= e5:
			factor = 5
		case err >= e2:
			factor = 2
		}
		if power < 0 {
			inc = pow10n(-power) / factor
			i1 = jsRound(start * inc)
			i2 = jsRound(stop * inc)
			if i1/inc < start {
				i1++
			}
			if i2/inc > stop {
				i2--
			}
			inc = -inc
		} else {
			inc = pow10n(power) * factor
			i1 = jsRound(start / inc)
			i2 = jsRound(stop / inc)
			if i1*inc < start {
				i1++
			}
			if i2*inc > stop {
				i2--
			}
		}
		if i2 < i1 && 0.5 <= count && count < 2 {
			count *= 2
			continue
		}
		return i1, i2, inc
	}
}

// maxTicks bounds the number of values Ticks will materialise; a specification
// can ask for an absurd count over a huge domain.
const maxTicks = 1 << 20

// Ticks is d3.ticks: about count nicely rounded values covering
// [start, stop], in the direction of the arguments. Returns nil for a
// non-positive or NaN count, and [start] for a degenerate interval.
func Ticks(start, stop, count float64) []float64 {
	if !(count > 0) {
		return nil
	}
	if start == stop {
		return []float64{start}
	}
	reverse := stop < start
	var i1, i2, inc float64
	if reverse {
		i1, i2, inc = tickSpec(stop, start, count)
	} else {
		i1, i2, inc = tickSpec(start, stop, count)
	}
	if !(i2 >= i1) {
		return nil
	}
	nf := i2 - i1 + 1
	if !(nf <= maxTicks) {
		return nil
	}
	n := int(nf)
	ticks := make([]float64, n)
	switch {
	case reverse && inc < 0:
		for i := 0; i < n; i++ {
			ticks[i] = (i2 - float64(i)) / -inc
		}
	case reverse:
		for i := 0; i < n; i++ {
			ticks[i] = (i2 - float64(i)) * inc
		}
	case inc < 0:
		for i := 0; i < n; i++ {
			ticks[i] = (i1 + float64(i)) / -inc
		}
	default:
		for i := 0; i < n; i++ {
			ticks[i] = (i1 + float64(i)) * inc
		}
	}
	return ticks
}

// TickIncrement is d3.tickIncrement. A negative result -k denotes a step of 1/k.
func TickIncrement(start, stop, count float64) float64 {
	_, _, inc := tickSpec(start, stop, count)
	return inc
}

// TickStep is d3.tickStep: the (signed) distance between ticks.
func TickStep(start, stop, count float64) float64 {
	reverse := stop < start
	var inc float64
	if reverse {
		inc = TickIncrement(stop, start, count)
	} else {
		inc = TickIncrement(start, stop, count)
	}
	s := inc
	if inc < 0 {
		s = 1 / -inc
	}
	if reverse {
		return -s
	}
	return s
}

// Nice is d3.nice: extends [start, stop] to tick boundaries, iterating until
// the step stabilises.
func Nice(start, stop, count float64) (float64, float64) {
	prestep := math.NaN()
	for {
		step := TickIncrement(start, stop, count)
		if step == prestep || step == 0 || math.IsInf(step, 0) || math.IsNaN(step) {
			return start, stop
		}
		if step > 0 {
			start = math.Floor(start/step) * step
			stop = math.Ceil(stop/step) * step
		} else if step < 0 {
			start = math.Ceil(start*step) / step
			stop = math.Floor(stop*step) / step
		}
		prestep = step
	}
}

// bisectRight is d3.bisectRight over a numeric slice within [lo, hi).
// It returns hi when x is NaN, as d3 does (x is not self-comparable).
func bisectRight(a []float64, x float64, lo, hi int) int {
	if lo < hi {
		if x != x {
			return hi
		}
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if a[mid] <= x {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
	}
	return lo
}

// quantileSorted is d3.quantileSorted (R-7 linear interpolation) on sorted
// data. ok is false for empty data or a NaN p (upstream returns undefined).
func quantileSorted(values []float64, p float64) (float64, bool) {
	n := len(values)
	if n == 0 || p != p {
		return 0, false
	}
	if p <= 0 || n < 2 {
		return values[0], true
	}
	if p >= 1 {
		return values[n-1], true
	}
	i := float64(float64(n-1) * p)
	i0 := int(math.Floor(i))
	v0 := values[i0]
	v1 := values[i0+1]
	return v0 + float64((v1-v0)*(i-float64(i0))), true
}

// QuantileSorted exposes quantileSorted for other packages (vega-statistics
// style callers need d3.quantileSorted).
func QuantileSorted(values []float64, p float64) (float64, bool) { return quantileSorted(values, p) }

// pow10n is Math.pow(10, p) as V8 computes it: not the correctly rounded
// power for every integral exponent (68 of them differ), and d3's ticks use
// exactly what it returns.
func pow10n(p float64) float64 { return jsmath.Pow(10, p) }
