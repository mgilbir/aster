package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
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
	// UnitsGiven is set when the units parameter is present, even as an empty
	// array: `_.units ? ... : timeBin(...)` takes an empty list as given, and
	// timeUnits then rejects it.
	UnitsGiven bool
	// UnitsStrict, when set, reports for each of Units whether it is the
	// string itself: `unit === peek(units)` in vega-time's floor fails for
	// a unit that is any other value naming one (["month"]), and then the
	// step applies to no part.
	UnitsStrict []bool
	// Step is the multiple of the last unit to floor to; 0 means 1. It only
	// applies together with Units.
	Step float64
	// StepRaw, with StepRawGiven, is the step as given: `_.step || 1` keeps a
	// truthy value of any type (["x"], an object) whose number may be NaN or 0,
	// and shows it as it is in the operator's value.
	StepRaw      jsval.Value
	StepRawGiven bool
	MaxBins      float64 // 0 means 40
	// MaxBinsExact: MaxBins is already `maxbins || 40` (see format.BinExact).
	MaxBinsExact bool
	// Extent optionally gives the [min, max] epoch milliseconds timeBin uses.
	Extent *[2]float64
	// InferUnits detects the coarsest units every date is aligned to, and
	// overrides Units, Step, MaxBins and Extent.
	InferUnits bool
	// Zone is the calendar: format.UTC (the zero Zone) or format.Local(loc).
	Zone format.Zone
	// As names the output fields; empty entries default to unit0 and unit1.
	As [2]string
	// AsExact reports that As are the names as the runtime resolved them
	// (`_.as || defaults`, then each entry's text): an empty one is the
	// empty name, not the default.
	AsExact bool
}

// TimeUnitInfo describes the floor a TimeUnit run used.
type TimeUnitInfo struct {
	Units []string // normalised, coarsest first
	Unit  string   // the finest unit, the one Step applies to and offsets use
	Step  float64
	// StepValue, when set (not undefined), is the step as upstream holds it.
	StepValue jsval.Value
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
	var stepValue jsval.Value
	stepExact := false
	switch {
	case p.InferUnits:
		dates := make([]float64, len(data))
		for i, t := range data {
			v := p.Field.Apply(t)
			f, ok := v.NumberOrNull()
			if !ok || !format.Valid(f) {
				// Upstream builds new Date(v) from strings too; parsing them is
				// the caller's business, so anything else is invalid here.
				return data, info, fmt.Errorf("Invalid date: %s", jsval.StringPrefix(v, jsval.MessageBytes))
			}
			dates[i] = f
		}
		bin, err := format.DetectUnits(dates, p.Zone)
		if err != nil {
			return data, info, err
		}
		units, step = bin.Units, bin.Step
	case p.UnitsGiven || len(p.Units) > 0:
		units, step = p.Units, p.Step
		if p.StepRawGiven {
			stepExact = true
			if p.StepRaw.IsTruthy() {
				stepValue, step = p.StepRaw, jsval.ToNumber(p.StepRaw)
			} else {
				stepValue, step = jsval.Num(1), 1
			}
		} else if step == 0 || math.IsNaN(step) {
			step = 1
		}
	default:
		lo, hi := math.NaN(), math.NaN()
		if p.Extent != nil {
			lo, hi = p.Extent[0], p.Extent[1]
		} else {
			a, b, ok, err := ExtentOf(jsval.WalkerFor(ctx), data, p.Field)
			if err != nil {
				return data, info, err
			}
			if ok {
				lo, hi = jsval.ToNumber(a), jsval.ToNumber(b)
				if a.IsUndefined() {
					lo, hi = math.NaN(), math.NaN()
				}
			}
		}
		var bin format.BinResult
		if p.MaxBinsExact {
			bin = format.BinExact(lo, hi, p.MaxBins)
		} else {
			bin = format.Bin(lo, hi, p.MaxBins)
		}
		units, step = bin.Units, bin.Step
	}
	tunits, err := format.NormalizeUnits(units)
	if err != nil {
		return data, info, err
	}
	unit := tunits[len(tunits)-1]
	floor := format.NewFloor(p.Zone, tunits, step)
	if stepExact {
		floorStep := step
		if len(p.UnitsStrict) == len(p.Units) {
			for i := len(p.Units) - 1; i >= 0; i-- {
				if p.Units[i] == unit {
					if !p.UnitsStrict[i] {
						floorStep = 1 // `unit === b && s` is false
					}
					break
				}
			}
		}
		floor = format.NewFloorExact(p.Zone, tunits, floorStep)
	}
	info = TimeUnitInfo{Units: tunits, Unit: unit, Step: step, StepValue: stepValue, Start: math.Inf(1), Stop: math.Inf(-1)}

	u0, u1 := p.As[0], p.As[1]
	if u0 == "" && !p.AsExact {
		u0 = "unit0"
	}
	if u1 == "" && !p.AsExact {
		u1 = "unit1"
	}
	band := !p.NoInterval
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return data, info, err
		}
		v := p.Field.Apply(t)
		o := jsval.FieldsFor(t, u0)
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
			off, ok, err := format.OffsetUnit(ctx, p.Zone, unit, a, step)
			if err != nil {
				return data, info, err
			}
			if ok {
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
