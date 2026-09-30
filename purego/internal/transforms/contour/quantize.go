package contour

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

var errTooManyThresholds = errors.New("contour: too many contour levels")

// Quantize returns vega-geo's threshold generator for k levels: it spans the
// data extent (starting at min(extent, 0) when zero is set) and yields
// evenly spaced interior levels, on "nice" tick steps when nice is set.
// Upstream's `count || 10` default is the caller's job.
func Quantize(k float64, nice, zero bool) func(values []float64) ([]float64, error) {
	return func(values []float64) ([]float64, error) {
		lo, hi := extent(values)
		start := lo
		if zero {
			start = math.Min(lo, 0)
		}
		span := hi - start
		var step float64
		if nice {
			step = tickStep(start, hi, k)
		} else {
			step = span / (k + 1)
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
		i1 = jsRound(float64(start * inc))
		i2 = jsRound(float64(stop * inc))
		if i1/inc < start {
			i1++
		}
		if i2/inc > stop {
			i2--
		}
		inc = -inc
	} else {
		inc = jsmath.Pow(10, power) * factor
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
