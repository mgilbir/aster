package format

import (
	"math"
	"time"
)

// Zone selects the calendar that date arithmetic and formatting use: UTC, or
// the local time of a *time.Location. It is a small value; copy it freely.
//
// UTC and a Local zone whose location is time.UTC behave identically except
// in one place: vega-time's utcFloor of a year between 0 and 99 yields an
// Invalid Date (an upstream defect that timeFloor does not share), so the two
// are kept distinct.
//
// The zero Zone is UTC.
type Zone struct {
	loc *time.Location // nil for the UTC calendar
}

// UTC is the UTC calendar (d3's utcXxx family).
var UTC = Zone{}

// Local is the local calendar (d3's timeXxx family) of loc. A nil location
// means time.UTC. The engine never consults time.Local implicitly.
func Local(loc *time.Location) Zone {
	if loc == nil {
		loc = time.UTC
	}
	return Zone{loc: loc}
}

// IsUTC reports whether z is the UTC calendar.
func (z Zone) IsUTC() bool { return z.loc == nil }

// Location is the zone's location (time.UTC for UTC).
func (z Zone) Location() *time.Location {
	if z.loc == nil {
		return time.UTC
	}
	return z.loc
}

// fixed reports whether the zone has a constant zero offset.
func (z Zone) fixed() bool { return z.loc == nil || z.loc == time.UTC }

const (
	msPerSecond = 1000
	msPerMinute = 60 * msPerSecond
	msPerHour   = 60 * msPerMinute
	msPerDay    = 24 * msPerHour
	msPerWeek   = 7 * msPerDay

	maxTime = 8.64e15 // largest magnitude of a valid Date
)

// timeClip is ECMAScript TimeClip.
func timeClip(t float64) float64 {
	if math.IsNaN(t) || math.Abs(t) > maxTime {
		return math.NaN()
	}
	return math.Trunc(t) + 0 // + 0 turns -0 into +0
}

// Valid reports whether t is a valid (finite, in range) time value.
func Valid(t float64) bool { return !math.IsNaN(t) && math.Abs(t) <= maxTime }

func floorMod(a, b float64) float64 {
	m := math.Mod(a, b)
	if m < 0 {
		m += b
	}
	return m
}

// daysFromCivil is the number of days since 1970-01-01 of the proleptic
// Gregorian date y-m-d (m in 1..12, d may be any day count).
func daysFromCivil(y int64, m int) int64 {
	if m <= 2 {
		y--
	}
	era := y
	if era < 0 {
		era -= 399
	}
	era /= 400
	yoe := y - era*400
	mp := int64(m+9) % 12
	doy := (153*mp + 2) / 5 // day of year for the 1st of the month
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the inverse: year, month (1..12) and day (1..31).
func civilFromDays(z int64) (y int64, m, d int) {
	z += 719468
	era := z
	if era < 0 {
		era -= 146096
	}
	era /= 146097
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = int(doy - (153*mp+2)/5 + 1)
	if mp < 10 {
		m = int(mp + 3)
	} else {
		m = int(mp - 9)
	}
	if m <= 2 {
		y++
	}
	return
}

// Fields are the calendar fields of a time value in a zone.
type Fields struct {
	Year        int
	Month       int // 0..11
	Day         int // day of month, 1..31
	Hour        int
	Minute      int
	Second      int
	Millisecond int
	Weekday     int // 0 = Sunday
}

// offsetMillis is the zone's UTC offset at the instant t (local = t + offset).
func (z Zone) offsetMillis(t float64) float64 {
	if z.fixed() {
		return 0
	}
	sec := math.Floor(t / 1000)
	_, off := time.Unix(int64(sec), 0).In(z.loc).Zone()
	return float64(off) * 1000
}

// TimezoneOffset is Date.prototype.getTimezoneOffset: minutes west of UTC at
// the instant t (positive behind UTC), NaN for an invalid date. V8 truncates
// the offset to whole minutes here even where the zone's own offset has
// seconds (pre-1900 local mean time), while the calendar fields keep them.
func (z Zone) TimezoneOffset(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	return math.Trunc(-z.offsetMillis(t)/msPerMinute) + 0
}

// Fields decomposes the valid time value t. For an invalid t every field is 0.
func (z Zone) Fields(t float64) Fields {
	if !Valid(t) {
		return Fields{}
	}
	local := t + z.offsetMillis(t)
	days := math.Floor(local / msPerDay)
	msOfDay := int(local - float64(days*msPerDay))
	y, m, d := civilFromDays(int64(days))
	wd := int((int64(days)%7 + 11) % 7) // 1970-01-01 was a Thursday
	return Fields{
		Year: int(y), Month: m - 1, Day: d,
		Hour: msOfDay / msPerHour, Minute: msOfDay / msPerMinute % 60,
		Second: msOfDay / 1000 % 60, Millisecond: msOfDay % 1000, Weekday: wd,
	}
}

// localToUTC is ECMAScript UTC(t): interpret the local clock reading l (a
// "time value" in local time) as an instant. An ambiguous reading (clocks
// turned back) resolves to the earlier instant; a skipped reading (clocks
// turned forward) is interpreted with the offset in force before the change.
func (z Zone) localToUTC(l float64) float64 {
	if z.fixed() || math.IsNaN(l) || math.IsInf(l, 0) {
		return l
	}
	if math.Abs(l) > maxTime+2*msPerDay {
		return math.NaN()
	}
	before := z.offsetMillis(l - msPerDay)
	after := z.offsetMillis(l + msPerDay)
	if before == after {
		return l - before
	}
	ub, ua := l-before, l-after
	okB := z.offsetMillis(ub) == before
	okA := z.offsetMillis(ua) == after
	switch {
	case okB && okA:
		return math.Min(ub, ua)
	case okB:
		return ub
	case okA:
		return ua
	}
	return ub
}

// V8 refuses field values beyond these before doing any arithmetic.
const (
	maxYearField  = 1000000
	maxMonthField = 10000000
)

// makeLocal is MakeDate(MakeDay(y, m, d), MakeTime(h, mi, s, ms)) read as a
// local clock time and converted to an instant (no TimeClip). All arguments
// are truncated toward zero; any non-finite argument gives NaN.
func (z Zone) makeLocal(y, mo, d, h, mi, s, ms float64) float64 {
	for _, v := range [...]float64{y, mo, d, h, mi, s, ms} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.NaN()
		}
	}
	y, mo, d = math.Trunc(y), math.Trunc(mo), math.Trunc(d)
	ym := y + math.Floor(mo/12)
	if math.Abs(y) > maxYearField || math.Abs(mo) > maxMonthField || math.Abs(ym) > maxYearField {
		return math.NaN()
	}
	mn := int(floorMod(mo, 12))
	days := float64(daysFromCivil(int64(ym), mn+1)) + d - 1
	// Explicit conversions keep arm64 from fusing the products into FMAs;
	// with in-range fields every product is an exact integer anyway.
	tm := float64(math.Trunc(h)*msPerHour) + float64(math.Trunc(mi)*msPerMinute) + float64(math.Trunc(s)*msPerSecond) + math.Trunc(ms)
	return z.localToUTC(float64(days*msPerDay) + tm)
}

// make is makeLocal with TimeClip: the instant for a local clock reading, the
// years 0..99 taken literally (as Date.prototype.setFullYear does).
func (z Zone) make(y, mo, d, h, mi, s, ms float64) float64 {
	return timeClip(z.makeLocal(y, mo, d, h, mi, s, ms))
}

// Date is the JavaScript constructor new Date(y, mo, d, h, mi, s, ms) (or
// Date.UTC in the UTC zone): field overflow rolls over into the next larger
// field, and a year from 0 to 99 means 1900 plus that.
func (z Zone) Date(y, mo, d, h, mi, s, ms float64) float64 {
	if yi := math.Trunc(y); yi >= 0 && yi <= 99 {
		y = 1900 + yi
	}
	return z.make(y, mo, d, h, mi, s, ms)
}

// FullYearDate builds a date whose year is taken literally even when it is
// between 0 and 99, the way d3-time-format's localDate/utcDate do it: construct
// in year -1, then setFullYear. The construction's own rollover is therefore
// discarded with year -1 (a month of 12 stays in the requested year).
func (z Zone) FullYearDate(y, mo, d, h, mi, s, ms float64) float64 {
	if !(y >= 0 && y < 100) {
		return z.Date(y, mo, d, h, mi, s, ms)
	}
	return z.setFullYear(z.make(-1, mo, d, h, mi, s, ms), y)
}

// setFullYear is Date.prototype.setFullYear(y) on the instant t: keep month,
// day and time of day, replace the year. An Invalid Date is treated as the
// epoch (local midnight of 1 January 1970) first.
func (z Zone) setFullYear(t, y float64) float64 {
	var f Fields
	if Valid(t) {
		f = z.Fields(t)
	} else {
		f = Fields{Year: 1970, Month: 0, Day: 1}
	}
	return z.make(y, float64(f.Month), float64(f.Day), float64(f.Hour), float64(f.Minute), float64(f.Second), float64(f.Millisecond))
}
