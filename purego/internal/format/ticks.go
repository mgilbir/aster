package format

import (
	"context"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// tickSpec is d3-array's tickSpec: the integer bounds and increment of "nice"
// ticks between start and stop. A negative inc means the reciprocal.
func tickSpec(start, stop, count float64) (i1, i2, inc float64) {
	step := (stop - start) / math.Max(0, count)
	power := floorLog10(step)
	errv := step / pow10f(power)
	var factor float64
	switch {
	case errv >= math.Sqrt(50):
		factor = 10
	case errv >= math.Sqrt(10):
		factor = 5
	case errv >= math.Sqrt(2):
		factor = 2
	default:
		factor = 1
	}
	if power < 0 {
		inc = pow10f(-power) / factor
		i1 = jsRound(float64(start * inc))
		i2 = jsRound(float64(stop * inc))
		if float64(i1/inc) < start {
			i1++
		}
		if float64(i2/inc) > stop {
			i2--
		}
		inc = -inc
	} else {
		inc = float64(pow10f(power) * factor)
		i1 = jsRound(float64(start / inc))
		i2 = jsRound(float64(stop / inc))
		if float64(i1*inc) < start {
			i1++
		}
		if float64(i2*inc) > stop {
			i2--
		}
	}
	if i2 < i1 && 0.5 <= count && count < 2 {
		return tickSpec(start, stop, count*2)
	}
	return
}

// pow10f is Math.pow(10, p). V8's pow is not correctly rounded for every
// integral exponent (Math.pow(10, -4) !== 1e-4), and d3 relies on its exact
// results, so this is jsmath.Pow rather than math.Pow10.
func pow10f(p float64) float64 { return jsmath.Pow(10, p) }

// floorLog10 is Math.floor(Math.log10(x)), with V8's log10.
func floorLog10(x float64) float64 {
	return math.Floor(jsmath.Log10(x))
}

// TickIncrement is d3.tickIncrement(start, stop, count).
func TickIncrement(start, stop, count float64) float64 {
	_, _, inc := tickSpec(start, stop, count)
	return inc
}

// TickStep is d3.tickStep(start, stop, count).
func TickStep(start, stop, count float64) float64 {
	reverse := stop < start
	var inc float64
	if reverse {
		inc = TickIncrement(stop, start, count)
	} else {
		inc = TickIncrement(start, stop, count)
	}
	sign := 1.0
	if reverse {
		sign = -1
	}
	if inc < 0 {
		return sign * (1 / -inc)
	}
	return sign * inc
}

var tickIntervals = [...]struct {
	unit unitKind
	step float64
	dur  float64
}{
	{kSecond, 1, msPerSecond},
	{kSecond, 5, 5 * msPerSecond},
	{kSecond, 15, 15 * msPerSecond},
	{kSecond, 30, 30 * msPerSecond},
	{kMinute, 1, msPerMinute},
	{kMinute, 5, 5 * msPerMinute},
	{kMinute, 15, 15 * msPerMinute},
	{kMinute, 30, 30 * msPerMinute},
	{kHour, 1, msPerHour},
	{kHour, 3, 3 * msPerHour},
	{kHour, 6, 6 * msPerHour},
	{kHour, 12, 12 * msPerHour},
	{kDay, 1, msPerDay},
	{kDay, 2, 2 * msPerDay},
	{kWeek, 1, msPerWeek},
	{kMonth, 1, 30 * msPerDay},
	{kMonth, 3, 3 * 30 * msPerDay},
	{kYear, 1, 365 * msPerDay},
}

// TickInterval is d3.timeTickInterval / d3.utcTickInterval: the interval
// whose boundaries make about count ticks between start and stop. ok is false
// where d3 returns null.
func (z Zone) TickInterval(start, stop, count float64) (Interval, bool) {
	target := math.Abs(stop-start) / count
	// bisector.right over the durations: first index whose duration > target.
	i := 0
	for i < len(tickIntervals) && !(tickIntervals[i].dur > target) {
		i++
	}
	if math.IsNaN(target) {
		i = len(tickIntervals) // every comparison with NaN is false
	}
	if i == len(tickIntervals) {
		return z.Year().Every(TickStep(start/(365*msPerDay), stop/(365*msPerDay), count))
	}
	if i == 0 {
		return z.Millisecond().Every(math.Max(TickStep(start, stop, count), 1))
	}
	pick := i
	if target/tickIntervals[i-1].dur < tickIntervals[i].dur/target {
		pick = i - 1
	}
	t := tickIntervals[pick]
	var base Interval
	switch t.unit {
	case kSecond:
		base = z.Second()
	case kMinute:
		base = z.Minute()
	case kHour:
		base = z.Hour()
	case kDay:
		if z.IsUTC() {
			base = z.UnixDay() // d3's UTC ticker counts whole days from the epoch
		} else {
			base = z.Day()
		}
	case kWeek:
		base = z.Week(0)
	case kMonth:
		base = z.Month()
	default:
		base = z.Year()
	}
	return base.Every(t.step)
}

// Ticks is d3.timeTicks / d3.utcTicks(start, stop, count): the interval
// boundaries from start to stop inclusive.
func (z Zone) Ticks(ctx context.Context, start, stop, count float64) ([]float64, error) {
	reverse := stop < start
	if reverse {
		start, stop = stop, start
	}
	iv, ok := z.TickInterval(start, stop, count)
	return z.ticksWith(ctx, iv, ok, start, stop, reverse)
}

// TicksWith is Ticks with an explicit interval instead of a tick count.
func TicksWith(ctx context.Context, iv Interval, start, stop float64) ([]float64, error) {
	reverse := stop < start
	if reverse {
		start, stop = stop, start
	}
	return iv.z.ticksWith(ctx, iv, true, start, stop, reverse)
}

func (z Zone) ticksWith(ctx context.Context, iv Interval, ok bool, start, stop float64, reverse bool) ([]float64, error) {
	if !ok {
		return nil, nil
	}
	out, err := iv.Range(ctx, start, stop+1, 1) // inclusive stop
	if err != nil {
		return nil, err
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}
