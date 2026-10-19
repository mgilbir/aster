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
	UnitISOWeek      = "isoweek"
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
	UnitYear, UnitQuarter, UnitMonth, UnitWeek, UnitISOWeek, UnitDate, UnitDay, UnitDayOfYear,
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
	if has[UnitWeek] || has[UnitISOWeek] || has[UnitDay] {
		n++
	}
	if has[UnitQuarter] || has[UnitMonth] || has[UnitDate] {
		n++
	}
	if has[UnitDayOfYear] {
		n++
	}
	if n > 1 || (has[UnitWeek] && has[UnitISOWeek]) {
		return nil, fmt.Errorf("Incompatible time units: %s", strings.Join(units, ","))
	}
	out := slices.Clone(units)
	slices.SortStableFunc(out, func(a, b string) int { return unitRank(a) - unitRank(b) })
	return out, nil
}

var defaultUnitSpecifiers = map[string]string{
	UnitYear:                     "%Y ",
	UnitQuarter:                  "Q%q ",
	UnitMonth:                    "%b ",
	UnitDate:                     "%d ",
	UnitWeek:                     "W%U ",
	UnitISOWeek:                  "W%V ",
	UnitYear + "-" + UnitISOWeek: "%G W%V ",
	UnitDay:                      "%a ",
	UnitDayOfYear:                "%j ",
	UnitHours:                    "%H:00",
	UnitMinutes:                  "00:%M",
	UnitSeconds:                  ":%S",
	UnitMilliseconds:             ".%L",
	UnitYear + "-" + UnitMonth:   "%Y-%m ",
	UnitYear + "-" + UnitMonth + "-" + UnitDate: "%Y-%m-%d ",
	UnitHours + "-" + UnitMinutes:               "%H:%M",
}

// UnitSpecifiers overrides entries of vega-time's default per-unit format
// pieces, keyed by a unit or a dash-joined run of units ("year-month"). A nil
// pointer is JavaScript null: it removes the default for that key.
type UnitSpecifiers map[string]*string

// UnitSpecifiersFromValue reads an object of specifiers; non-string members
// other than null are converted with String().
func UnitSpecifiersFromValue(w jsval.Walker, v jsval.Value) UnitSpecifiers {
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
			out[o.KeyAt(i)] = ptr(w.String(val))
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
	return jsval.JSTrim(b.String()), nil
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
	case UnitISOWeek:
		return z.Week(1), true
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
// the given unit. ok is false for an unknown unit (undefined upstream). A
// quarter moves one quarter at a time, as d3 does, its work charged to the
// ledger in ctx; err is ctx's error once the call is over.
func OffsetUnit(ctx context.Context, z Zone, unit string, t, step float64) (float64, bool, error) {
	iv, ok := IntervalFor(z, unit)
	if !ok {
		return 0, false, nil
	}
	off, err := iv.Offset(ctx, t, step)
	return off, true, err
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

// The day and week numbers of vega-time's util.js, on the zone's d3
// intervals, as vega-time computes them: through interval counts, which
// correct a span by the change in the zone's offset truncated to whole
// minutes (C120 of the 2026-10-18 audit), and from the start of the year as
// vega-time finds it. Every 0-99 year quirk of vega-time is reproduced
// (C161): its UTC helpers find a year's start with Date.UTC(y, 0, 1), which
// reads a year from 0 to 99 as 1900 plus it; its local helpers use
// setFullYear, which does not; and its UTC date builder makes an Invalid
// Date for those years (Floorer.Floor).

// vtYearStart is localYear(y), local midnight of 1 January with the year
// taken literally (setFullYear), or in UTC Date.UTC(y, 0, 1).
func (z Zone) vtYearStart(y float64) float64 {
	if z.IsUTC() {
		return UTC.Date(y, 0, 1, 0, 0, 0, 0)
	}
	return z.make(y, 0, 1, 0, 0, 0, 0)
}

// vtDayOffset is timeDay.offset(t, n) / utcDay.offset(t, n).
func (z Zone) vtDayOffset(t, n float64) float64 {
	return z.Day().baseOffset(timeClip(t), n)
}

// vtDayOfYear is localDayOfYear / utcDayOfYear: day.count(yearStart - 1, d).
func (z Zone) vtDayOfYear(t float64, fl Fields) float64 {
	return z.count(kDay, 0, z.vtYearStart(float64(fl.Year))-1, t)
}

// vtWeekNum is localWeekNum / utcWeekNum: sunday.count(yearStart - 1, d).
func (z Zone) vtWeekNum(t float64, fl Fields) float64 {
	return z.count(kWeek, 0, z.vtYearStart(float64(fl.Year))-1, t)
}

// vtISOWeekYear is localISOWeekYear / utcISOWeekYear: the year of the
// Thursday of the date's Monday-based week.
func (z Zone) vtISOWeekYear(t float64) float64 {
	return z.yearOf(z.vtDayOffset(z.Week(1).Floor(t), 3))
}

// vtISOWeekOne is localISOWeekOne / utcISOWeekOne: the Monday on which week
// 1 of the week-numbering year y begins.
func (z Zone) vtISOWeekOne(y float64) float64 {
	return z.Week(1).Floor(z.vtDayOffset(z.vtYearStart(y), 3))
}

// vtISOWeekNum is localISOWeekNum / utcISOWeekNum.
func (z Zone) vtISOWeekNum(t float64) float64 {
	return 1 + z.count(kWeek, 1, z.vtISOWeekOne(z.vtISOWeekYear(t)), t)
}

// vtISOWeekOneDate is localISOWeekOneDate / utcISOWeekOneDate: the day of
// January on which week 1 of year y begins; zero or less is in December.
func (z Zone) vtISOWeekOneDate(y float64) float64 {
	d := z.vtISOWeekOne(y)
	if !Valid(d) {
		return math.NaN()
	}
	f := z.Fields(d)
	if f.Month != 0 {
		return float64(f.Day - 31)
	}
	return float64(f.Day)
}

// vtFirst is localFirst / utcFirst: the weekday of the start of year y.
func (z Zone) vtFirst(y float64) float64 {
	t := z.vtYearStart(y)
	if !Valid(t) {
		return math.NaN()
	}
	return float64(z.Fields(t).Weekday)
}

// DayOfYear is vega-time's dayofyear / utcdayofyear: 1 on 1 January.
func (z Zone) DayOfYear(t float64) float64 {
	if t = timeClip(t); !Valid(t) {
		return math.NaN()
	}
	return z.vtDayOfYear(t, z.Fields(t))
}

// WeekOfYear is vega-time's week / utcweek: the number of Sunday-based week
// boundaries since the year began (0 before the first Sunday).
func (z Zone) WeekOfYear(t float64) float64 {
	if t = timeClip(t); !Valid(t) {
		return math.NaN()
	}
	return z.vtWeekNum(t, z.Fields(t))
}

// ISOWeekOfYear is vega-time's isoweek / utcisoweek: the ISO 8601 week number.
func (z Zone) ISOWeekOfYear(t float64) float64 {
	if t = timeClip(t); !Valid(t) {
		return math.NaN()
	}
	return z.vtISOWeekNum(t)
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
	gISOWeek     // the ISO 8601 week number
	gISOWeekYear // YEAR+ISOWEEK: the year the ISO week belongs to
	gISOWeekDay  // ISOWEEK+DAY: day of year of the weekday within the ISO week number
	gConst2012
	gConst2015
	gZero
	gOne
)

type invKind uint8

const (
	invNone invKind = iota
	invQuarter
	invWeek
	invISOWeek
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
	// Without a year unit, the reference year is 2012 (a leap year that begins on a Sunday), or 2015
	// for ISO weeks: it has 53, so every week number is a real week (vega-time's floor.js).
	switch {
	case u[UnitYear] && u[UnitISOWeek]:
		f.y = part(UnitYear, 0, gISOWeekYear, invNone)
	case u[UnitYear]:
		f.y = part(UnitYear, 0, gYear, invNone)
	case u[UnitISOWeek]:
		f.y = floorPart{get: gConst2015}
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
	case u[UnitISOWeek] && u[UnitDay]:
		f.d = part(UnitDay, 1, gISOWeekDay, invNone)
	case u[UnitWeek]:
		f.d = part(UnitWeek, 1, gWeek, invWeek)
	case u[UnitISOWeek]:
		f.d = part(UnitISOWeek, 1, gISOWeek, invISOWeek)
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
	z Zone
	t float64
	f Fields
}

func (st *floorState) get(k getKind, year float64) float64 {
	f, z := st.f, st.z
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
		return z.vtDayOfYear(st.t, f)
	case gWeek:
		return z.vtWeekNum(st.t, f)
	case gWeekDay:
		return weekdayOfYear(z.vtWeekNum(st.t, f), float64(f.Weekday), z.vtFirst(year))
	case gDay:
		return weekdayOfYear(1, float64(f.Weekday), z.vtFirst(year))
	case gISOWeek:
		return z.vtISOWeekNum(st.t)
	case gISOWeekYear:
		return z.vtISOWeekYear(st.t)
	case gISOWeekDay:
		return isoWeekday(z.vtISOWeekOneDate(year), z.vtISOWeekNum(st.t), float64((f.Weekday+6)%7))
	}
	return math.NaN()
}

// isoWeekday is vega-time's isoWeekday(): the day of the year of a day (Monday 0 to Sunday 6) of an ISO
// week, where weekOneDate is the day of January on which week 1 begins.
func isoWeekday(weekOneDate, week, day float64) float64 {
	return weekOneDate + float64((week-1)*7) + day
}

// weekdayOfYear is vega-time's weekday(): the day of the year, counting from
// the first weekday of the year, for a week number and day of week.
func weekdayOfYear(week, day, firstDay float64) float64 {
	return day + float64(week*7) - math.Mod(firstDay+6, 7)
}

func (p floorPart) eval(st *floorState, year float64) float64 {
	switch p.get {
	case gConst2012:
		return 2012
	case gConst2015:
		return 2015
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
		v = weekdayOfYear(v, 0, st.z.vtFirst(year))
	case invISOWeek:
		v = isoWeekday(st.z.vtISOWeekOneDate(year), v, 0)
	}
	return v
}

// Floor maps t to the start of its unit combination. An invalid date maps to
// NaN. In UTC, a year between 0 and 99 yields NaN: vega-time's UTC date
// builder for such years reads a field of the wrong variable and produces an
// Invalid Date; the local builder is correct.
func (f *Floorer) Floor(t float64) float64 {
	if t = timeClip(t); !Valid(t) { // t.setTime(+v)
		return math.NaN()
	}
	st := floorState{z: f.z, t: t, f: f.z.Fields(t)}
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
