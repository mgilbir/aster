package format

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of Vega's own tests (see internal/upstream): vega-time and vega-format.

// unitsOf reads a recorded time unit or list of units.
func unitsOf(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

func argAtCall(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}

func nullishArg(v any) bool { return v == nil || upstream.IsUndefined(v) }

func TestUpstreamVegaTime(t *testing.T) {
	r := upstream.Start(t, "vega-time")
	local := zoneOf(t, r)
	ctx := context.Background()
	zoneFor := func(fn string) Zone {
		if strings.HasPrefix(fn, "utc") {
			return UTC
		}
		return local
	}
	date := func(v any) float64 {
		if d, ok := upstream.Date(v); ok {
			return d
		}
		return upstream.Number(v)
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		base := strings.TrimSuffix(c.Fn, "()")
		switch base {
		case "timeFloor", "utcFloor":
			if c.Fn == base {
				r.Skip("constructions (a function is the answer)")
				continue
			}
			units, ok := unitsOf(argAtCall(c.ConstructedWith, 0))
			if !ok {
				r.Skip("units of another shape")
				continue
			}
			step := 0.0
			if len(c.ConstructedWith) > 1 {
				step = upstream.Number(c.ConstructedWith[1])
			}
			r.Check(c, upstream.EncDate(NewFloor(zoneFor(base), units, step).Floor(date(c.Arg(0)))), false)
		case "timeInterval", "utcInterval":
			if c.Fn == base {
				r.Skip("constructions (a function is the answer)")
				continue
			}
			unit, ok := argAtCall(c.ConstructedWith, 0).(string)
			if !ok {
				r.Skip("unit of another shape")
				continue
			}
			iv, ok := IntervalFor(zoneFor(base), unit)
			if !ok {
				r.Skip("unit the engine has no interval for")
				continue
			}
			r.Check(c, upstream.EncDate(iv.Floor(date(c.Arg(0)))), false)
		case "timeOffset", "utcOffset":
			unit, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("unit of another shape")
				continue
			}
			step := 1.0
			if !nullishArg(c.Arg(2)) {
				step = upstream.Number(c.Arg(2))
			}
			v, ok := OffsetUnit(zoneFor(base), unit, date(c.Arg(1)), step)
			if !ok {
				r.Check(c, upstream.Undefined(), false)
				continue
			}
			r.Check(c, upstream.EncDate(v), false)
		case "timeSequence", "utcSequence":
			unit, ok := c.Arg(0).(string)
			if !ok {
				r.Skip("unit of another shape")
				continue
			}
			step := 1.0
			if !nullishArg(c.Arg(3)) {
				step = upstream.Number(c.Arg(3))
			}
			out, ok, err := SequenceUnit(ctx, zoneFor(base), unit, date(c.Arg(1)), date(c.Arg(2)), step)
			if err != nil || !ok {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, dates(out), false)
		case "timeUnits":
			units, ok := unitsOf(c.Arg(0))
			if !ok {
				r.Skip("units of another shape")
				continue
			}
			out, err := NormalizeUnits(units)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			vals := make([]any, len(out))
			for j, u := range out {
				vals[j] = u
			}
			r.Check(c, vals, false)
		case "timeUnitSpecifier":
			units, ok := unitsOf(c.Arg(0))
			if !ok {
				r.Skip("units of another shape")
				continue
			}
			spec, err := TimeUnitSpecifier(units, UnitSpecifiersFromValue(upstream.ToValue(c.Arg(1))))
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, spec, false)
		case "dayofyear", "utcdayofyear":
			r.Check(c, upstream.Enc(zoneFor(base).DayOfYear(date(c.Arg(0)))), false)
		case "isoweek", "utcisoweek":
			r.Check(c, upstream.Enc(zoneFor(strings.TrimPrefix(base, "")).ISOWeekOfYear(date(c.Arg(0)))), false)
		case "week", "utcweek":
			r.Check(c, upstream.Enc(zoneFor(base).WeekOfYear(date(c.Arg(0)))), false)
		case "detectTimeUnits":
			// detectTimeUnits(data, accessor, utc): the tests' accessor reads the one field of a row
			items, ok := c.Arg(0).([]any)
			if !ok {
				r.Skip("data of another shape")
				continue
			}
			dates := make([]float64, len(items))
			for j, item := range items {
				if row, isRow := item.(map[string]any); isRow && len(row) == 1 {
					for _, v := range row {
						item = v
					}
				}
				dates[j] = date(item)
			}
			z := local
			if upstream.ToValue(c.Arg(2)).IsTruthy() {
				z = UTC
			}
			res, err := DetectUnits(dates, z)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			units := make([]any, len(res.Units))
			for j, u := range res.Units {
				units[j] = u
			}
			r.Check(c, map[string]any{"units": units, "step": upstream.Enc(res.Step)}, false)
		case "timeBin":
			opt, ok := c.Arg(0).(map[string]any)
			if !ok || len(opt) != 2 {
				r.Skip("timeBin options the adapter does not read")
				continue
			}
			ext, _ := opt["extent"].([]any)
			if len(ext) != 2 {
				r.Skip("timeBin options the adapter does not read")
				continue
			}
			res := Bin(date(ext[0]), date(ext[1]), upstream.Number(opt["maxbins"]))
			units := make([]any, len(res.Units))
			for j, u := range res.Units {
				units[j] = u
			}
			r.Check(c, map[string]any{"units": units, "step": upstream.Enc(res.Step)}, false)
		default:
			r.Skip("unmapped " + c.Fn)
		}
	}
	r.Done(450)
}
