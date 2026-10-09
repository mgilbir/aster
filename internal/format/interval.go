package format

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
)

// MaxRangeLen bounds the number of dates Interval.Range and Ticks return; a
// specification chooses the extent and the step, so the work must be bounded.
const MaxRangeLen = 1 << 22

// ErrRangeTooLarge is returned when a range would exceed MaxRangeLen dates.
var ErrRangeTooLarge = fmt.Errorf("format: interval range too large: %w", budget.ErrLimit)

type unitKind uint8

const (
	kMillisecond unitKind = iota
	kSecond
	kMinute
	kHour
	kDay
	kWeek
	kMonth
	kYear
	kUnixDay // UTC days counted from the epoch; differs from kDay only in every()
)

// Interval is a d3-time interval: a calendar unit in a Zone with floor, ceil,
// round, offset, range, count and every. Dates are epoch milliseconds; NaN in
// gives NaN out. Interval is a small comparable value.
type Interval struct {
	z       Zone
	kind    unitKind
	weekday int // kWeek: the day the week starts on, 0 = Sunday
	// k > 0 marks the optimised millisecond.every(k) / year.every(k)
	// intervals: they floor to multiples of k and step by k units.
	k int64
	// filter > 0 is interval.every(filter): the parent interval restricted to
	// the boundaries that pass a divisibility test. Such an interval has no
	// count or every of its own.
	filter int64
}

// Millisecond is d3.timeMillisecond (identical in every zone).
func (z Zone) Millisecond() Interval { return Interval{z: z, kind: kMillisecond} }

// Second is d3.timeSecond.
func (z Zone) Second() Interval { return Interval{z: z, kind: kSecond} }

// Minute is d3.timeMinute / d3.utcMinute.
func (z Zone) Minute() Interval { return Interval{z: z, kind: kMinute} }

// Hour is d3.timeHour / d3.utcHour.
func (z Zone) Hour() Interval { return Interval{z: z, kind: kHour} }

// Day is d3.timeDay / d3.utcDay.
func (z Zone) Day() Interval { return Interval{z: z, kind: kDay} }

// UnixDay is d3.unixDay: like UTC Day, but Every counts days from the epoch
// instead of from the start of the month. d3's UTC tick intervals use it.
func (z Zone) UnixDay() Interval { return Interval{z: UTC, kind: kUnixDay} }

// Week is the weekly interval starting on weekday (0 = Sunday ... 6 =
// Saturday): d3.timeSunday ... d3.timeSaturday and their UTC twins.
func (z Zone) Week(weekday int) Interval {
	return Interval{z: z, kind: kWeek, weekday: ((weekday % 7) + 7) % 7}
}

// Month is d3.timeMonth / d3.utcMonth.
func (z Zone) Month() Interval { return Interval{z: z, kind: kMonth} }

// Year is d3.timeYear / d3.utcYear.
func (z Zone) Year() Interval { return Interval{z: z, kind: kYear} }

// Zone returns the interval's calendar.
func (iv Interval) Zone() Zone { return iv.z }

func (iv Interval) local() bool { return !iv.z.IsUTC() }

// ---- base operations (floori / offseti / count of d3.timeInterval) ----

func (iv Interval) baseFloor(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	z := iv.z
	switch iv.kind {
	case kMillisecond:
		if iv.k > 0 {
			k := float64(iv.k)
			return math.Floor(t/k) * k
		}
		return t
	case kSecond:
		return t - floorMod(t, msPerSecond)
	case kMinute:
		if iv.local() {
			f := z.Fields(t)
			return t - float64(f.Millisecond) - float64(float64(f.Second)*msPerSecond)
		}
		return t - floorMod(t, msPerMinute)
	case kHour:
		if iv.local() {
			f := z.Fields(t)
			return t - float64(f.Millisecond) - float64(float64(f.Second)*msPerSecond) - float64(float64(f.Minute)*msPerMinute)
		}
		return t - floorMod(t, msPerHour)
	case kDay, kUnixDay:
		if iv.local() {
			f := z.Fields(t)
			return z.make(float64(f.Year), float64(f.Month), float64(f.Day), 0, 0, 0, 0)
		}
		return t - floorMod(t, msPerDay)
	case kWeek:
		f := z.Fields(t)
		back := float64((f.Weekday + 7 - iv.weekday) % 7)
		if iv.local() {
			t2 := z.make(float64(f.Year), float64(f.Month), float64(f.Day)-back, float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
			if !Valid(t2) {
				return math.NaN()
			}
			f = z.Fields(t2)
			return z.make(float64(f.Year), float64(f.Month), float64(f.Day), 0, 0, 0, 0)
		}
		return timeClip(t - float64(back*msPerDay) - floorMod(t, msPerDay))
	case kMonth:
		f := z.Fields(t)
		return z.make(float64(f.Year), float64(f.Month), 1, 0, 0, 0, 0)
	case kYear:
		f := z.Fields(t)
		y := float64(f.Year)
		if iv.k > 0 {
			k := float64(iv.k)
			y = math.Floor(y/k) * k
		}
		return z.make(y, 0, 1, 0, 0, 0, 0)
	}
	return math.NaN()
}

// baseOffset moves t by step (already floored) base units; a NaN or infinite
// step gives an Invalid Date.
func (iv Interval) baseOffset(t, step float64) float64 {
	if !Valid(t) || math.IsNaN(step) || math.IsInf(step, 0) {
		return math.NaN()
	}
	z := iv.z
	switch iv.kind {
	case kMillisecond:
		if iv.k > 0 {
			step *= float64(iv.k)
		}
		return timeClip(t + step)
	case kSecond:
		return timeClip(t + float64(step*msPerSecond))
	case kMinute:
		return timeClip(t + float64(step*msPerMinute))
	case kHour:
		return timeClip(t + float64(step*msPerHour))
	case kDay, kUnixDay:
		if iv.local() {
			f := z.Fields(t)
			return z.make(float64(f.Year), float64(f.Month), float64(f.Day)+step, float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
		}
		return timeClip(t + float64(step*msPerDay))
	case kWeek:
		if iv.local() {
			f := z.Fields(t)
			return z.make(float64(f.Year), float64(f.Month), float64(f.Day)+float64(step*7), float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
		}
		return timeClip(t + float64(step*msPerWeek))
	case kMonth:
		f := z.Fields(t)
		return z.make(float64(f.Year), float64(f.Month)+step, float64(f.Day), float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
	case kYear:
		if iv.k > 0 {
			step *= float64(iv.k)
		}
		f := z.Fields(t)
		return z.make(float64(f.Year)+step, float64(f.Month), float64(f.Day), float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
	}
	return math.NaN()
}

// baseCount is d3's count(start, end) for two dates already floored.
func (iv Interval) baseCount(start, end float64) float64 {
	if !Valid(start) || !Valid(end) {
		return math.NaN()
	}
	switch iv.kind {
	case kMillisecond:
		if iv.k > 0 {
			return (end - start) / float64(iv.k)
		}
		return end - start
	case kSecond:
		return (end - start) / msPerSecond
	case kMinute:
		return (end - start) / msPerMinute
	case kHour:
		return (end - start) / msPerHour
	case kDay, kUnixDay, kWeek:
		unit := float64(msPerDay)
		if iv.kind == kWeek {
			unit = msPerWeek
		}
		// A local day is not always 24 hours: correct by the change in the
		// (minute-truncated) time zone offset between the two dates.
		tzo := float64(float64(iv.z.TimezoneOffset(end)-iv.z.TimezoneOffset(start)) * msPerMinute)
		return (end - start - tzo) / unit
	case kMonth:
		a, b := iv.z.Fields(start), iv.z.Fields(end)
		return float64(b.Month - a.Month + (b.Year-a.Year)*12)
	case kYear:
		return float64(iv.z.Fields(end).Year - iv.z.Fields(start).Year)
	}
	return math.NaN()
}

// fieldValue is d3's `field` argument for the intervals whose every() tests
// a calendar field: seconds, minutes, hours, day of month and month.
func (iv Interval) fieldValue(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	if iv.kind == kSecond {
		return math.Floor(floorMod(t, msPerMinute) / msPerSecond) // getUTCSeconds, even for local time
	}
	f := iv.z.Fields(t)
	switch iv.kind {
	case kMinute:
		return float64(f.Minute)
	case kHour:
		return float64(f.Hour)
	case kDay:
		return float64(f.Day - 1)
	case kMonth:
		return float64(f.Month)
	}
	return math.NaN()
}

// countBased reports whether the every() test of the (unfiltered) parent is
// "count since the epoch is a multiple of step" and can therefore be solved
// arithmetically.
func (iv Interval) countBased() bool {
	return iv.kind == kWeek || iv.kind == kMillisecond || iv.kind == kUnixDay
}

// arithmetic reports whether a count-based filter can be solved in one step.
// Local weeks cannot: d3 moves one week at a time, and the wall-clock time of
// day drifts when a step lands in a daylight-saving gap.
func (iv Interval) arithmetic() bool {
	return iv.kind != kWeek || iv.z.fixed()
}

// epochCount is the number every() tests: interval.count(0, t) for weeks and
// milliseconds, the number of days since the epoch for unixDay.
func (iv Interval) epochCount(t float64) float64 {
	if iv.kind == kUnixDay {
		return math.Floor(t / msPerDay)
	}
	base := iv
	base.filter = 0
	return math.Floor(base.baseCount(base.baseFloor(0), base.baseFloor(t)))
}

func floorDiv(a, b float64) float64 { return math.Floor(a / b) }

func ceilDiv(a, b float64) float64 { return math.Ceil(a / b) }

// maxFilterIterations bounds the search loops of filtered intervals. A
// filtered interval only ever needs to walk back to the previous multiple of
// a calendar field (at most a year of months, a month of days, ...), so the
// bound is never reached by valid input; it exists so no input can spin.
const maxFilterIterations = 1 << 16

func (iv Interval) passes(t float64) bool {
	if iv.countBased() {
		return math.Mod(iv.epochCount(t), float64(iv.filter)) == 0
	}
	return math.Mod(iv.fieldValue(t), float64(iv.filter)) == 0
}

// ---- public operations ----

// Floor is interval.floor(t): the latest boundary at or before t.
func (iv Interval) Floor(t float64) float64 {
	if iv.filter == 0 {
		return iv.baseFloor(t)
	}
	if !Valid(t) {
		return math.NaN()
	}
	parent := iv
	parent.filter = 0
	d := parent.baseFloor(t)
	step := float64(iv.filter)
	if iv.countBased() && iv.arithmetic() {
		c := iv.epochCount(d)
		n := c - float64(floorDiv(c, step)*step)
		if n == 0 {
			return d
		}
		return parent.baseFloor(parent.baseOffset(d, -n))
	}
	for i := 0; i < maxFilterIterations; i++ {
		if iv.passes(d) {
			return d
		}
		d = parent.baseFloor(d - 1)
		if math.IsNaN(d) {
			break
		}
	}
	return math.NaN()
}

// Ceil is interval.ceil(t): the earliest boundary at or after t.
func (iv Interval) Ceil(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	d := iv.Floor(t - 1)
	d = iv.offset(d, 1)
	return iv.Floor(d)
}

// Round is interval.round(t): the nearer of Floor and Ceil (Ceil on a tie).
func (iv Interval) Round(t float64) float64 {
	d0, d1 := iv.Floor(t), iv.Ceil(t)
	if t-d0 < d1-t {
		return d0
	}
	return d1
}

// Offset is interval.offset(t, step): t moved by floor(step) intervals.
// (d3's default step of 1 is the caller's to pass.) A NaN step on a filtered
// interval returns t unchanged, as d3 does.
func (iv Interval) Offset(t, step float64) float64 {
	return iv.offset(t, math.Floor(step))
}

func (iv Interval) offset(t, step float64) float64 {
	if iv.filter == 0 {
		return iv.baseOffset(t, step)
	}
	if !(t == t) || math.IsNaN(step) {
		return t
	}
	if !Valid(t) {
		return math.NaN()
	}
	parent := iv
	parent.filter = 0
	k := float64(iv.filter)
	if iv.countBased() && iv.arithmetic() {
		if math.IsInf(step, 0) {
			return math.NaN()
		}
		c := iv.epochCount(t)
		var target float64
		switch {
		case step > 0:
			target = (floorDiv(c, k) + step) * k
		case step < 0:
			target = (ceilDiv(c, k) + step) * k
		default:
			return t
		}
		return parent.baseOffset(t, target-c)
	}
	d := t
	budget := maxFilterIterations
	move := func(dir float64) bool {
		for {
			d = parent.baseOffset(d, dir)
			if math.IsNaN(d) {
				return false
			}
			if iv.passes(d) {
				return true
			}
			if budget--; budget < 0 {
				d = math.NaN()
				return false
			}
		}
	}
	if step < 0 {
		for n := step; n < 0; n++ {
			if !move(-1) {
				return math.NaN()
			}
		}
	} else {
		for n := step; n > 0; n-- {
			if !move(1) {
				return math.NaN()
			}
		}
	}
	return d
}

// Range is interval.range(start, stop, step): the boundaries in
// [ceil(start), stop) at every floor(step)-th interval. It fails with
// ErrRangeTooLarge beyond MaxRangeLen dates or with the context's error.
func (iv Interval) Range(ctx context.Context, start, stop, step float64) ([]float64, error) {
	step = math.Floor(step)
	cur := iv.Ceil(start)
	if !(cur < stop) || !(step > 0) { // also handles Invalid Date
		return nil, nil
	}
	if iv.certainlyTooLong(stop-cur, step) {
		return nil, ErrRangeTooLarge
	}
	var out []float64
	for {
		prev := cur
		out = append(out, prev)
		if len(out) > MaxRangeLen {
			return nil, ErrRangeTooLarge
		}
		if len(out)&1023 == 0 && ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		cur = iv.Floor(iv.offset(cur, step))
		if !(prev < cur && cur < stop) {
			return out, nil
		}
	}
}

// certainlyTooLong reports whether a span of ms milliseconds holds more than
// MaxRangeLen boundaries step units apart, so Range can fail before stepping
// through millions of dates. No calendar unit is longer than twice its nominal
// length, so the test never rejects a range that fits. Filtered intervals keep
// only some boundaries and are left to the loop.
func (iv Interval) certainlyTooLong(ms, step float64) bool {
	if iv.filter > 0 {
		return false
	}
	var unit float64
	switch iv.kind {
	case kMillisecond:
		unit = 1
	case kSecond:
		unit = 1e3
	case kMinute:
		unit = 6e4
	case kHour:
		unit = 36e5
	case kDay, kUnixDay:
		unit = 864e5
	case kWeek:
		unit = 6048e5
	case kMonth:
		unit = 2592e6 // 30 days
	case kYear:
		unit = 31536e6 // 365 days
	default:
		return false
	}
	if iv.k > 0 {
		unit *= float64(iv.k)
	}
	return ms/(2*unit*step) > MaxRangeLen+1
}

// Count is interval.count(start, end): the number of boundaries after start
// up to and including end. It reports ok=false for a filtered interval (the
// result of Every), which has no count in d3.
func (iv Interval) Count(start, end float64) (n float64, ok bool) {
	if iv.filter > 0 || (iv.kind == kYear && iv.k > 0) {
		return math.NaN(), false
	}
	return math.Floor(iv.baseCount(iv.baseFloor(start), iv.baseFloor(end))), true
}

// Every is interval.every(step). ok is false where d3 returns null (step not
// a finite number greater than 0) and for intervals that have no every of
// their own (the results of Every). every(1) is the interval itself, except
// on a year, where d3 builds a fresh year.every(1) without count or every.
func (iv Interval) Every(step float64) (Interval, bool) {
	step = math.Floor(step)
	if math.IsNaN(step) || math.IsInf(step, 0) || !(step > 0) {
		return Interval{}, false
	}
	if iv.filter > 0 || (iv.kind == kYear && iv.k > 0) {
		return Interval{}, false
	}
	switch iv.kind {
	case kMillisecond:
		if iv.k == 0 { // the optimised millisecond.every
			if !(step > 1) {
				return iv, true
			}
			return Interval{z: iv.z, kind: kMillisecond, k: clampStep(step)}, true
		}
	case kYear:
		// year.every(k) is always its own interval, even for k = 1.
		return Interval{z: iv.z, kind: kYear, k: clampStep(step)}, true
	}
	if !(step > 1) {
		return iv, true
	}
	out := iv
	out.filter = clampStep(step)
	return out, true
}

// clampStep is int64 so that a step is the same on 32-bit platforms.
func clampStep(f float64) int64 {
	if f > 1<<40 {
		return 1 << 40
	}
	return int64(f)
}
