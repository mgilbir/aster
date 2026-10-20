package contour

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"
)

var errTooManyThresholds = fmt.Errorf("contour: too many contour levels: %w", budget.ErrLimit)

// Quantize returns vega-geo's threshold generator for k levels: it spans the
// data extent (starting at min(extent, 0) when zero is set) and yields
// evenly spaced interior levels, on "nice" tick steps when nice is set.
// Upstream's `count || 10` default is the caller's job.
func Quantize(k float64, nice, zero bool) func(values []float64) ([]float64, error) {
	return quantizeK1(k, k+1, nice, zero)
}

// quantizeK1 is Quantize whose `k + 1` is k1, as JavaScript's + makes it
// for a k that is no number ("3" + 1 is "31", read as 31).
func quantizeK1(k, k1 float64, nice, zero bool) func(values []float64) ([]float64, error) {
	return func(values []float64) ([]float64, error) {
		lo, hi := extent(values)
		return quantizeAt(k, k1, nice, zero, lo, hi)
	}
}

// quantizeAt is the threshold generator once the extent [lo, hi] is known.
func quantizeAt(k, k1 float64, nice, zero bool, lo, hi float64) ([]float64, error) {
	{
		start := lo
		if zero {
			start = math.Min(lo, 0)
		}
		span := hi - start
		var step float64
		if nice {
			step = tickStep(start, hi, k)
		} else {
			step = span / k1
		}
		return rangeSteps(start+step, hi, step)
	}
}

// extent is vega-util's extent over numbers: NaN is skipped, and an input
// without valid values yields NaN, NaN (upstream: undefined).
func extent(values []float64) (lo, hi float64) {
	i := 0
	for i < len(values) && math.IsNaN(values[i]) {
		i++
	}
	if i == len(values) {
		return math.NaN(), math.NaN()
	}
	lo, hi = values[i], values[i]
	for ; i < len(values); i++ {
		v := values[i]
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

// rangeSteps is d3.range(start, stop, step).
func rangeSteps(start, stop, step float64) ([]float64, error) {
	n := math.Ceil((stop - start) / step)
	if !(n > 0) {
		return nil, nil
	}
	if n > MaxThresholds {
		return nil, errTooManyThresholds
	}
	out := make([]float64, int(n))
	for i := range out {
		out[i] = start + float64(float64(i)*step)
	}
	return out, nil
}

var (
	e10 = math.Sqrt(50)
	e5  = math.Sqrt(10)
	e2  = math.Sqrt(2)
)

// tickSpec is d3-array's tick specification (i1, i2, inc).
func tickSpec(start, stop, count float64) (i1, i2, inc float64) {
	step := (stop - start) / math.Max(0, count)
	power := math.Floor(jsmath.Log10(step))
	err := step / jsmath.Pow(10, power)
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
		inc = jsmath.Pow(10, -power) / factor
		i1 = jsmath.Round(float64(start * inc))
		i2 = jsmath.Round(float64(stop * inc))
		if i1/inc < start {
			i1++
		}
		if i2/inc > stop {
			i2--
		}
		inc = -inc
	} else {
		inc = jsmath.Pow(10, power) * factor
		i1 = jsmath.Round(start / inc)
		i2 = jsmath.Round(stop / inc)
		if i1*inc < start {
			i1++
		}
		if i2*inc > stop {
			i2--
		}
	}
	if i2 < i1 && 0.5 <= count && count < 2 {
		return tickSpec(start, stop, count*2)
	}
	return
}

func tickIncrement(start, stop, count float64) float64 {
	_, _, inc := tickSpec(start, stop, count)
	return inc
}

// tickStep is d3.tickStep.
func tickStep(start, stop, count float64) float64 {
	reverse := stop < start
	var inc float64
	if reverse {
		inc = tickIncrement(stop, start, count)
	} else {
		inc = tickIncrement(start, stop, count)
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
