package scale

import (
	"context"

	"github.com/mgilbir/aster/purego/internal/format"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// LocalZone is the calendar used by "time" scales (d3's local time). The engine
// is deterministic, so it defaults to UTC; set it once at start-up to render in
// another zone.
var LocalZone = format.UTC

// Time is d3.scaleTime / d3.scaleUtc: a continuous scale over dates whose
// ticks, niceing and default labels follow a calendar.
type Time struct {
	*Continuous
	zone format.Zone
}

// NewTime is d3.scaleTime in the given zone (domain: 2000-01-01 to 2000-01-02
// local).
func NewTime(zone format.Zone) *Time {
	lo := zone.Date(2000, 0, 1, 0, 0, 0, 0)
	hi := zone.Date(2000, 0, 2, 0, 0, 0, 0)
	return &Time{Continuous: newContinuous(kindLinear, []float64{lo, hi}), zone: zone}
}

// NewUTC is d3.scaleUtc.
func NewUTC() *Time { return NewTime(format.UTC) }

// Zone returns the calendar of the scale.
func (s *Time) Zone() format.Zone { return s.zone }

// dateNumber is time.js's `number`: a Date is its epoch value, anything else
// goes through Number() and then `new Date(n)` (which applies TimeClip).
func dateNumber(v jsval.Value) float64 {
	if v.IsTimestamp() {
		return v.NumValue()
	}
	return timeClip(jsval.ToNumber(v))
}

// Domain returns the domain as dates.
func (s *Time) Domain() []jsval.Value { return numsToTimestamps(s.domain) }

// SetDomain is scale.domain(d): dates, or numbers taken as epoch milliseconds.
func (s *Time) SetDomain(d []jsval.Value) {
	nums := make([]float64, len(d))
	for i, v := range d {
		nums[i] = dateNumber(v)
	}
	s.Continuous.SetDomainNumbers(nums)
}

// Invert is scale.invert(y): a date.
func (s *Time) Invert(y jsval.Value) jsval.Value {
	return jsval.Timestamp(timeClip(s.Continuous.invert(y)))
}

// InvertRange is vega-scale's invertRange over dates.
func (s *Time) InvertRange(lo, hi jsval.Value) (jsval.Value, bool) {
	if hi.IsNum() && lo.IsNum() && hi.NumValue() < lo.NumValue() {
		lo, hi = hi, lo
	}
	return jsval.ArrOf(s.Invert(lo), s.Invert(hi)), true
}

// maxTimeTicks bounds tick generation for absurd extents; the format package
// enforces its own limit and reports an error beyond it.
func (s *Time) tickValues(count TickCount) []float64 {
	start, stop := endpoints(s.domain)
	var (
		out []float64
		err error
	)
	if count.Interval != nil {
		out, err = format.TicksWith(context.Background(), *count.Interval, start, stop)
	} else {
		out, err = s.zone.Ticks(context.Background(), start, stop, count.countOrDefault())
	}
	if err != nil {
		return nil
	}
	return out
}

// Ticks is scale.ticks(count | interval): the calendar boundaries in the domain.
func (s *Time) Ticks(count TickCount) []float64 { return s.tickValues(count) }

// Nice is scale.nice(count | interval): floors and ceils the domain end points
// to the interval. A count picks the interval d3 would tick with; when no
// interval fits (d3 answers null) the domain is left unchanged.
func (s *Time) Nice(count TickCount) {
	if len(s.domain) == 0 {
		return
	}
	d0, dn := endpoints(s.domain)
	var iv format.Interval
	if count.Interval != nil {
		iv = *count.Interval
	} else {
		var ok bool
		iv, ok = s.zone.TickInterval(d0, dn, count.countOrDefault())
		if !ok {
			return
		}
	}
	d := append([]float64(nil), s.domain...)
	i0, i1 := 0, len(d)-1
	x0, x1 := d[i0], d[i1]
	if x1 < x0 {
		i0, i1 = i1, i0
		x0, x1 = x1, x0
	}
	d[i0] = iv.Floor(x0)
	d[i1] = iv.Ceil(x1)
	s.Continuous.SetDomainNumbers(d)
}

// Copy is calendar()'s copy: domain, range, interpolate, clamp and unknown.
func (s *Time) Copy() Scale {
	return &Time{Continuous: s.Continuous.copyContinuous(), zone: s.zone}
}

var (
	_ Scale         = (*Time)(nil)
	_ Ticker        = (*Time)(nil)
	_ Niceable      = (*Time)(nil)
	_ Inverter      = (*Time)(nil)
	_ RangeInverter = (*Time)(nil)
)
