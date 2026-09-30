package format

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Time unit names (vega-time).
const (
	UnitYear         = "year"
	UnitQuarter      = "quarter"
	UnitMonth        = "month"
	UnitWeek         = "week"
	UnitDate         = "date"
	UnitDay          = "day"
	UnitDayOfYear    = "dayofyear"
	UnitHours        = "hours"
	UnitMinutes      = "minutes"
	UnitSeconds      = "seconds"
	UnitMilliseconds = "milliseconds"
)

// TimeUnits is vega-time's TIME_UNITS, coarsest first.
var TimeUnits = []string{
	UnitYear, UnitQuarter, UnitMonth, UnitWeek, UnitDate, UnitDay, UnitDayOfYear,
	UnitHours, UnitMinutes, UnitSeconds, UnitMilliseconds,
}

func unitRank(u string) int { return slices.Index(TimeUnits, u) }

// NormalizeUnits is vega-time's timeUnits(units): it validates the names,
// rejects combinations that cannot be floored together (week/day with
// quarter/month/date, or dayofyear with either) and returns a copy sorted
// coarsest first.
func NormalizeUnits(units []string) ([]string, error) {
	if len(units) == 0 {
		return nil, fmt.Errorf("Missing time unit.")
	}
	has := map[string]bool{}
	for _, u := range units {
		if unitRank(u) < 0 {
			return nil, fmt.Errorf("Invalid time unit: %s.", u)
		}
		has[u] = true
	}
	n := 0
	if has[UnitWeek] || has[UnitDay] {
		n++
	}
	if has[UnitQuarter] || has[UnitMonth] || has[UnitDate] {
		n++
	}
	if has[UnitDayOfYear] {
		n++
	}
	if n > 1 {
		return nil, fmt.Errorf("Incompatible time units: %s", strings.Join(units, ","))
	}
	out := slices.Clone(units)
	slices.SortStableFunc(out, func(a, b string) int { return unitRank(a) - unitRank(b) })
	return out, nil
}

var defaultUnitSpecifiers = map[string]string{
	UnitYear:                   "%Y ",
	UnitQuarter:                "Q%q ",
	UnitMonth:                  "%b ",
	UnitDate:                   "%d ",
	UnitWeek:                   "W%U ",
	UnitDay:                    "%a ",
	UnitDayOfYear:              "%j ",
	UnitHours:                  "%H:00",
	UnitMinutes:                "00:%M",
	UnitSeconds:                ":%S",
	UnitMilliseconds:           ".%L",
	UnitYear + "-" + UnitMonth: "%Y-%m ",
	UnitYear + "-" + UnitMonth + "-" + UnitDate: "%Y-%m-%d ",
	UnitHours + "-" + UnitMinutes:               "%H:%M",
}

// UnitSpecifiers overrides entries of vega-time's default per-unit format
// pieces, keyed by a unit or a dash-joined run of units ("year-month"). A nil
// pointer is JavaScript null: it removes the default for that key.
type UnitSpecifiers map[string]*string

// UnitSpecifiersFromValue reads an object of specifiers; non-string members
// other than null are converted with String().
func UnitSpecifiersFromValue(v jsval.Value) UnitSpecifiers {
	o := v.ObjValue()
	if o == nil {
		return nil
	}
	out := make(UnitSpecifiers, o.Len())
	for i := 0; i < o.Len(); i++ {
		val := o.ValueAt(i)
		switch {
		case val.IsNullish():
			out[o.KeyAt(i)] = nil
		default:
			out[o.KeyAt(i)] = ptr(val.AsString())
		}
	}
	return out
}

// TimeUnitSpecifier is vega-time's timeUnitSpecifier: the time format
// specifier that labels a combination of time units, built from the longest
// runs of adjacent units that have a specifier.
func TimeUnitSpecifier(units []string, specifiers UnitSpecifiers) (string, error) {
	u, err := NormalizeUnits(units)
	if err != nil {
		return "", err
	}
	lookup := func(key string) (string, bool) {
		if p, ok := specifiers[key]; ok {
			if p == nil {
				return "", false
			}
			return *p, true
		}
		s, ok := defaultUnitSpecifiers[key]
		return s, ok
	}
	var b strings.Builder
	for start := 0; start < len(u); {
		found := false
		for end := len(u); end > start; end-- {
			if s, ok := lookup(strings.Join(u[start:end], "-")); ok {
				b.WriteString(s)
				start, found = end, true
				break
			}
		}
		if !found {
			break // upstream would loop forever when a unit's specifier is null
		}
	}
	return trimJS(b.String()), nil
}

// trimJS is String.prototype.trim.
func trimJS(s string) string { return strings.TrimFunc(s, isJSSpaceRune) }

func isJSSpaceRune(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// IntervalFor is vega-time's timeInterval(unit) / utcInterval(unit): the
// d3 interval a time unit floors and steps by. ok is false for an unknown
// unit. Quarters are month.every(3); date, day and dayofyear are all days.
func IntervalFor(z Zone, unit string) (Interval, bool) {
	switch unit {
	case UnitYear:
		return z.Year(), true
	case UnitQuarter:
		return z.Month().Every(3)
	case UnitMonth:
		return z.Month(), true
	case UnitWeek:
		return z.Week(0), true
	case UnitDate, UnitDay, UnitDayOfYear:
		return z.Day(), true
	case UnitHours:
		return z.Hour(), true
	case UnitMinutes:
		return z.Minute(), true
	case UnitSeconds:
		return z.Second(), true
	case UnitMilliseconds:
		return z.Millisecond(), true
	}
	return Interval{}, false
}

// OffsetUnit is vega-time's timeOffset / utcOffset: t moved by step units of
// the given unit. ok is false for an unknown unit (undefined upstream).
func OffsetUnit(z Zone, unit string, t, step float64) (float64, bool) {
	iv, ok := IntervalFor(z, unit)
	if !ok {
		return 0, false
	}
	return iv.Offset(t, step), true
}

// SequenceUnit is vega-time's timeSequence / utcSequence: the unit boundaries
// in [start, stop) every step units. ok is false for an unknown unit.
func SequenceUnit(ctx context.Context, z Zone, unit string, start, stop, step float64) ([]float64, bool, error) {
	iv, ok := IntervalFor(z, unit)
	if !ok {
		return nil, false, nil
	}
	out, err := iv.Range(ctx, start, stop, step)
	return out, true, err
}

// ---- dayofyear / week ----

// jan1Days is the day number of 1 January of year y. In UTC, Date.UTC maps a
// year from 0 to 99 to 1900+y, which vega-time inherits for its UTC helpers;
// local time uses setFullYear, which does not.
func (z Zone) jan1Days(y int) int64 {
	if z.IsUTC() && y >= 0 && y <= 99 {
		y += 1900
	}
	return daysFromCivil(int64(y), 1)
}

func (z Zone) dayInfo(f Fields) dayInfo {
	return dayInfo{
		days:    daysFromCivil(int64(f.Year), f.Month+1) + int64(f.Day) - 1,
		jan1:    z.jan1Days(f.Year),
		weekday: f.Weekday,
	}
}

// DayOfYear is vega-time's dayofyear / utcdayofyear: 1 on 1 January.
func (z Zone) DayOfYear(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	di := z.dayInfo(z.Fields(t))
	return float64(di.days - di.jan1 + 1)
}

// WeekOfYear is vega-time's week / utcweek: the number of Sunday-based week
// boundaries since the year began (0 before the first Sunday).
func (z Zone) WeekOfYear(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	return float64(weekNumber(z.dayInfo(z.Fields(t)), 0))
}

// ---- floor ----

type getKind uint8

const (
	gYear getKind = iota
	gQuarter
	gMonth
	gDate
	gHours
	gMinutes
	gSeconds
	gMilliseconds
	gDayOfYear
	gWeek
	gWeekDay // WEEK+DAY: day of year of the weekday within the week number
	gDay
	gConst2012
	gZero
	gOne
)

type invKind uint8

const (
	invNone invKind = iota
	invQuarter
	invWeek
)

type floorPart struct {
	get   getKind
	step  float64 // steps only above 1
	phase float64
	inv   invKind
}

// Floorer is vega-time's timeFloor / utcFloor(units, step): it maps a date to
// the start of its enclosing unit combination, optionally rounding the last
// unit down to a multiple of step. Units that are not requested are pinned to
// their smallest value in the year 2012, so results are comparable dates.
type Floorer struct {
	z                   Zone
	y, m, d, H, M, S, L floorPart
}

// NewFloor builds a Floorer. units need not be sorted, but as in upstream
// the step applies to the last unit given and unknown names are ignored.
func NewFloor(z Zone, units []string, step float64) *Floorer {
	s := step
	if s == 0 || math.IsNaN(s) {
		s = 1
	}
	var b string
	if len(units) > 0 {
		b = units[len(units)-1]
	}
	u := map[string]bool{}
	for _, x := range units {
		u[x] = true
	}
	part := func(unit string, phase float64, get getKind, inv invKind) floorPart {
		p := floorPart{get: get, phase: phase, inv: inv}
		if unit == b {
			p.step = s
		}
		return p
	}
	f := &Floorer{z: z}
	switch {
	case u[UnitYear]:
		f.y = part(UnitYear, 0, gYear, invNone)
	default:
		f.y = floorPart{get: gConst2012}
	}
	switch {
	case u[UnitMonth]:
		f.m = part(UnitMonth, 0, gMonth, invNone)
	case u[UnitQuarter]:
		f.m = part(UnitQuarter, 0, gQuarter, invQuarter)
	default:
		f.m = floorPart{get: gZero}
	}
	switch {
	case u[UnitWeek] && u[UnitDay]:
		f.d = part(UnitDay, 1, gWeekDay, invNone)
	case u[UnitWeek]:
		f.d = part(UnitWeek, 1, gWeek, invWeek)
	case u[UnitDay]:
		f.d = part(UnitDay, 1, gDay, invNone)
	case u[UnitDate]:
		f.d = part(UnitDate, 1, gDate, invNone)
	case u[UnitDayOfYear]:
		f.d = part(UnitDayOfYear, 1, gDayOfYear, invNone)
	default:
		f.d = floorPart{get: gOne}
	}
	opt := func(unit string, get getKind) floorPart {
		if u[unit] {
			return part(unit, 0, get, invNone)
		}
		return floorPart{get: gZero}
	}
	f.H, f.M = opt(UnitHours, gHours), opt(UnitMinutes, gMinutes)
	f.S, f.L = opt(UnitSeconds, gSeconds), opt(UnitMilliseconds, gMilliseconds)
	return f
}

// floorState carries the per-call date decomposition.
type floorState struct {
	z  Zone
	f  Fields
	di dayInfo
}

func (st *floorState) get(k getKind, year float64) float64 {
	f := st.f
	switch k {
	case gYear:
		return float64(f.Year)
	case gQuarter:
		return math.Floor(float64(f.Month) / 3)
	case gMonth:
		return float64(f.Month)
	case gDate:
		return float64(f.Day)
	case gHours:
		return float64(f.Hour)
	case gMinutes:
		return float64(f.Minute)
	case gSeconds:
		return float64(f.Second)
	case gMilliseconds:
		return float64(f.Millisecond)
	case gDayOfYear:
		return float64(st.di.days - st.di.jan1 + 1)
	case gWeek:
		return float64(weekNumber(st.di, 0))
	case gWeekDay:
		return weekdayOfYear(float64(weekNumber(st.di, 0)), float64(f.Weekday), st.first(year))
	case gDay:
		return weekdayOfYear(1, float64(f.Weekday), st.first(year))
	}
	return math.NaN()
}

// weekdayOfYear is vega-time's weekday(): the day of the year, counting from
// the first weekday of the year, for a week number and day of week.
func weekdayOfYear(week, day, firstDay float64) float64 {
	return day + float64(week*7) - math.Mod(firstDay+6, 7)
}

// first is localFirst / utcFirst: the weekday of 1 January of year y, NaN when
// y is not a number.
func (st *floorState) first(y float64) float64 {
	if math.IsNaN(y) || math.IsInf(y, 0) || math.Abs(y) > 1e7 {
		return math.NaN()
	}
	yi := int(math.Trunc(y))
	return float64(weekdayOfDay(st.z.jan1Days(yi)))
}

func (p floorPart) eval(st *floorState, year float64) float64 {
	switch p.get {
	case gConst2012:
		return 2012
	case gZero:
		return 0
	case gOne:
		return 1
	}
	v := st.get(p.get, year)
	if p.step > 1 || math.IsNaN(p.step) {
		if p.phase != 0 {
			v = p.phase + float64(p.step*math.Floor((v-p.phase)/p.step))
		} else {
			v = float64(p.step * math.Floor(v/p.step))
		}
	}
	switch p.inv {
	case invQuarter:
		v *= 3
	case invWeek:
		v = weekdayOfYear(v, 0, st.first(year))
	}
	return v
}

// Floor maps t to the start of its unit combination. An invalid date maps to
// NaN. In UTC, a year between 0 and 99 yields NaN: vega-time's UTC date
// builder for such years reads a field of the wrong variable and produces an
// Invalid Date; the local builder is correct.
func (f *Floorer) Floor(t float64) float64 {
	if !Valid(t) {
		return math.NaN()
	}
	st := floorState{z: f.z, f: f.z.Fields(t)}
	st.di = f.z.dayInfo(st.f)
	year := f.y.eval(&st, 0)
	m := f.m.eval(&st, year)
	d := f.d.eval(&st, year)
	H, M := f.H.eval(&st, year), f.M.eval(&st, year)
	S, L := f.S.eval(&st, year), f.L.eval(&st, year)
	if f.z.IsUTC() && year >= 0 && year < 100 {
		return math.NaN()
	}
	return f.z.FullYearDate(year, m, d, H, M, S, L)
}

// ---- bin ----

// BinResult is the time units and step timeBin chooses.
type BinResult struct {
	Units []string
	Step  float64
}

var (
	binMilli   = []string{UnitYear, UnitMonth, UnitDate, UnitHours, UnitMinutes, UnitSeconds, UnitMilliseconds}
	binSeconds = binMilli[:6]
	binMinutes = binMilli[:5]
	binHours   = binMilli[:4]
	binDay     = binMilli[:3]
	binWeek    = []string{UnitYear, UnitWeek}
	binMonth   = []string{UnitYear, UnitMonth}
	binYear    = []string{UnitYear}
)

const (
	durSecond = 1000.0
	durMinute = durSecond * 60
	durHour   = durMinute * 60
	durDay    = durHour * 24
	durWeek   = durDay * 7
	durMonth  = durDay * 30
	durYear   = durDay * 365
)

var binIntervals = [...]struct {
	units []string
	step  float64
	dur   float64
}{
	{binSeconds, 1, durSecond}, {binSeconds, 5, 5 * durSecond}, {binSeconds, 15, 15 * durSecond}, {binSeconds, 30, 30 * durSecond},
	{binMinutes, 1, durMinute}, {binMinutes, 5, 5 * durMinute}, {binMinutes, 15, 15 * durMinute}, {binMinutes, 30, 30 * durMinute},
	{binHours, 1, durHour}, {binHours, 3, 3 * durHour}, {binHours, 6, 6 * durHour}, {binHours, 12, 12 * durHour},
	{binDay, 1, durDay}, {binWeek, 1, durWeek}, {binMonth, 1, durMonth}, {binMonth, 3, 3 * durMonth}, {binYear, 1, durYear},
}

// Bin is vega-time's timeBin: pick the time units and step that give about
// maxbins bins over the extent [lo, hi]. maxbins of 0 or NaN means 40. The
// returned Units slice is shared and must not be modified.
func Bin(lo, hi, maxbins float64) BinResult {
	max := maxbins
	if max == 0 || math.IsNaN(max) {
		max = 40
	}
	span := hi - lo
	if math.IsNaN(span) {
		span = 0 // vega-util span(): NaN || 0
	}
	target := math.Abs(span) / max

	i := 0
	for i < len(binIntervals) && !(binIntervals[i].dur > target) {
		i++
	}
	if math.IsNaN(target) {
		i = len(binIntervals)
	}
	switch {
	case i == len(binIntervals):
		return BinResult{binYear, TickStep(lo/durYear, hi/durYear, max)}
	case i > 0:
		pick := i
		if target/binIntervals[i-1].dur < binIntervals[i].dur/target {
			pick = i - 1
		}
		return BinResult{binIntervals[pick].units, binIntervals[pick].step}
	}
	return BinResult{binMilli, math.Max(TickStep(lo, hi, max), 1)}
}

// ---- detectTimeUnits ----

type grain struct {
	units     []string
	step      float64
	skippable bool
	aligned   func(dates []float64, z Zone) bool
}

func allDates(dates []float64, z Zone, pred func(Fields) bool) bool {
	for _, t := range dates {
		if !pred(z.Fields(t)) {
			return false
		}
	}
	return true
}

var grains = [...]grain{
	{binMilli, 1, false, func([]float64, Zone) bool { return true }},
	{binSeconds, 1, false, func(d []float64, z Zone) bool {
		return allDates(d, z, func(f Fields) bool { return f.Millisecond == 0 })
	}},
	{binMinutes, 1, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Second == 0 }) }},
	{binMinutes, 5, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Minute%5 == 0 }) }},
	{binMinutes, 10, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Minute%10 == 0 }) }},
	{binHours, 1, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Minute == 0 }) }},
	{binDay, 1, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Hour == 0 }) }},
	{binWeek, 1, true, func(d []float64, z Zone) bool {
		seen := map[int]bool{}
		for _, t := range d {
			seen[z.Fields(t).Weekday] = true
		}
		return len(seen) == 1
	}},
	{binMonth, 1, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Day == 1 }) }},
	{binMonth, 3, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Month%3 == 0 }) }},
	{binYear, 1, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Month == 0 }) }},
	{binYear, 10, false, func(d []float64, z Zone) bool { return allDates(d, z, func(f Fields) bool { return f.Year%10 == 0 }) }},
	{nil, 0, false, func([]float64, Zone) bool { return false }},
}

// DetectUnits is vega-time's detectTimeUnits: the coarsest time units and step
// that every date is aligned to (a set of month starts detects as month, a
// set of Mondays as week, ...). It fails on an invalid date.
func DetectUnits(dates []float64, z Zone) (BinResult, error) {
	for _, t := range dates {
		if !Valid(t) {
			return BinResult{}, fmt.Errorf("Invalid date: %s", jsval.JSNumberString(t))
		}
	}
	mismatch, required := -1, -1
	for i, g := range grains {
		if g.aligned(dates, z) {
			continue
		}
		if mismatch < 0 {
			mismatch = i
		}
		if required < 0 && !g.skippable {
			required = i
		}
		if mismatch >= 0 && required >= 0 {
			break
		}
	}
	index := mismatch
	if required > mismatch+1 {
		index = required
	}
	g := grains[index-1]
	return BinResult{g.units, g.step}, nil
}

// ---- datetime constructors ----

// DatetimeArgs is the JavaScript constructor `new Date(...args)` for one or
// more numeric arguments: a single argument is a time value, two or more are
// year, month[, day[, hours[, minutes[, seconds[, milliseconds]]]]] read in
// the zone (day defaults to 1, the rest to 0). Extra arguments are ignored.
func (z Zone) DatetimeArgs(args ...float64) float64 {
	switch {
	case len(args) == 0:
		return math.NaN()
	case len(args) == 1:
		return timeClip(args[0])
	}
	a := [7]float64{0, 0, 1, 0, 0, 0, 0}
	copy(a[:], args)
	return z.Date(a[0], a[1], a[2], a[3], a[4], a[5], a[6])
}

// UTCArgs is Date.UTC(...args): like DatetimeArgs in UTC, except that a single
// argument is a year (month 0, day 1) and a missing month is January.
func UTCArgs(args ...float64) float64 {
	if len(args) == 0 {
		return math.NaN()
	}
	a := [7]float64{0, 0, 1, 0, 0, 0, 0}
	copy(a[:], args)
	return UTC.Date(a[0], a[1], a[2], a[3], a[4], a[5], a[6])
}
