package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/purego/internal/format"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// TimeUnitParams configures TimeUnit (vega-transforms' timeunit).
type TimeUnitParams struct {
	Field Field
	// NoInterval omits the upper bound field (upstream `interval: false`).
	NoInterval bool
	// Units names the time units to floor to (year, quarter, month, week, date,
	// day, dayofyear, hours, minutes, seconds, milliseconds). When empty, units
	// and step are chosen by timeBin from Extent (or the data's own extent) and
	// MaxBins.
	Units []string
	// Step is the multiple of the last unit to floor to; 0 means 1. It only
	// applies together with Units.
	Step    float64
	MaxBins float64 // 0 means 40
	// Extent optionally gives the [min, max] epoch milliseconds timeBin uses.
	Extent *[2]float64
	// InferUnits detects the coarsest units every date is aligned to, and
	// overrides Units, Step, MaxBins and Extent.
	InferUnits bool
	// Zone is the calendar: format.UTC (the zero Zone) or format.Local(loc).
	Zone format.Zone
	// As names the output fields; empty entries default to unit0 and unit1.
	As [2]string
}

// TimeUnitInfo describes the floor a TimeUnit run used.
type TimeUnitInfo struct {
	Units []string // normalised, coarsest first
	Unit  string   // the finest unit, the one Step applies to and offsets use
	Step  float64
	// Start and Stop bound every output value (unit0 and unit1); they are
	// +Inf and -Inf when no date was processed.
	Start, Stop float64
}

// TimeUnit discretises dates: it writes the start of the time unit containing
// each tuple's date to unit0 and, unless NoInterval, the start of the next
// interval to unit1, both as dates. A null field value writes null to both.
// Tuples are annotated in place and returned.
func TimeUnit(ctx context.Context, data []jsval.Value, p TimeUnitParams) ([]jsval.Value, TimeUnitInfo, error) {
	var info TimeUnitInfo
	var units []string
	var step float64
	switch {
	case p.InferUnits:
		dates := make([]float64, len(data))
		for i, t := range data {
			v := p.Field.Apply(t)
			f, ok := v.NumberOrNull()
			if !ok || !format.Valid(f) {
				// Upstream builds new Date(v) from strings too; parsing them is
				// the caller's business, so anything else is invalid here.
				return data, info, fmt.Errorf("Invalid date: %s", v.AsString())
			}
			dates[i] = f
		}
		bin, err := format.DetectUnits(dates, p.Zone)
		if err != nil {
			return data, info, err
		}
		units, step = bin.Units, bin.Step
	case len(p.Units) > 0:
		units, step = p.Units, p.Step
		if step == 0 || math.IsNaN(step) {
			step = 1
		}
	default:
		lo, hi := math.NaN(), math.NaN()
		if p.Extent != nil {
			lo, hi = p.Extent[0], p.Extent[1]
		} else if a, b, ok := Extent(len(data), func(i int) jsval.Value { return p.Field.Apply(data[i]) }); ok {
			lo, hi = jsval.ToNumber(a), jsval.ToNumber(b)
			if a.IsUndefined() {
				lo, hi = math.NaN(), math.NaN()
			}
		}
		bin := format.Bin(lo, hi, p.MaxBins)
		units, step = bin.Units, bin.Step
	}
	tunits, err := format.NormalizeUnits(units)
	if err != nil {
		return data, info, err
	}
	unit := tunits[len(tunits)-1]
	floor := format.NewFloor(p.Zone, tunits, step)
	info = TimeUnitInfo{Units: tunits, Unit: unit, Step: step, Start: math.Inf(1), Stop: math.Inf(-1)}

	u0, u1 := p.As[0], p.As[1]
	if u0 == "" {
		u0 = "unit0"
	}
	if u1 == "" {
		u1 = "unit1"
	}
	band := !p.NoInterval
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return data, info, err
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		v := p.Field.Apply(t)
		if v.IsNullish() {
			o.Set(u0, jsval.Null)
			if band {
				o.Set(u1, jsval.Null)
			}
			continue
		}
		a := floor.Floor(jsval.ToNumber(v))
		o.Set(u0, jsval.Timestamp(a))
		b := a
		if band {
			b = math.NaN()
			if off, ok := format.OffsetUnit(p.Zone, unit, a, step); ok {
				b = off
			}
			o.Set(u1, jsval.Timestamp(b))
		}
		if a < info.Start {
			info.Start = a
		}
		if b > info.Stop {
			info.Stop = b
		}
	}
	return data, info, nil
}
