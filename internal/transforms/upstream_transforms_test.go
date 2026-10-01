package transforms

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamVegaTransforms replays vega-transforms' own tests (see internal/upstream) for the
// operators whose answer is a set of tuples that the recording holds in full: timeunit, flatten, fold,
// sequence and quantile. The recording is one call of the operator's transform per vector: its
// parameters, the tuples of the pulse that went in and the ones that came out. An operator that is
// asked again with other parameters (sequence above 0) is handed the tuples it has already seen.
func TestUpstreamVegaTransforms(t *testing.T) {
	r := upstream.Start(t, "vega-transforms")
	loc, err := time.LoadLocation(r.File.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	local := format.Local(loc)
	ctx := context.Background()

	field := func(v any) (Field, bool) {
		m, ok := v.(map[string]any)
		if !ok || m["$"] != "accessor" {
			return Field{}, false
		}
		fields, _ := m["fields"].([]any)
		if len(fields) != 1 {
			return Field{}, false
		}
		path, _ := fields[0].(string)
		f := FieldOf(path)
		if name, ok := m["name"].(string); ok {
			f.Name = name
		}
		return f, true
	}
	fieldList := func(v any) ([]Field, bool) {
		items, ok := v.([]any)
		if !ok {
			return nil, false
		}
		out := make([]Field, len(items))
		for i, it := range items {
			f, ok := field(it)
			if !ok {
				return nil, false
			}
			out[i] = f
		}
		return out, true
	}

	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Op == "" {
			continue
		}
		in, _ := c.Input.(map[string]any)
		out, _ := c.Output.(map[string]any)
		params, _ := c.Params.(map[string]any)
		pulse := func(m map[string]any, keys ...string) ([]jsval.Value, bool) {
			for _, k := range keys {
				if items, ok := m[k].([]any); ok {
					vals := make([]jsval.Value, len(items))
					for j, it := range items {
						vals[j] = upstream.ToValue(it)
					}
					return vals, true
				}
			}
			return nil, false
		}
		expected, hasExpected := out["add"], out["add"] != nil
		if !hasExpected {
			expected, hasExpected = out["mod"], out["mod"] != nil
		}
		if upstream.Contains(c.Output, "truncated") || upstream.Contains(c.Input, "truncated") {
			r.Skip("pulses too large to record")
			continue
		}
		switch c.Op {
		case "timeunit":
			data, ok := pulse(in, "add", "source")
			f, okField := field(params["field"])
			if !ok || !okField || !hasExpected {
				r.Skip("timeunit pulses of another shape")
				continue
			}
			p := TimeUnitParams{Field: f, Zone: local}
			if tz, _ := params["timezone"].(string); tz == "utc" {
				p.Zone = format.UTC
			}
			if units, ok := params["units"].([]any); ok {
				for _, u := range units {
					s, _ := u.(string)
					p.Units = append(p.Units, s)
				}
			}
			if v, ok := params["step"]; ok {
				p.Step = upstream.Number(v)
			}
			if v, ok := params["maxbins"]; ok {
				p.MaxBins = upstream.Number(v)
			}
			if ext, ok := params["extent"].([]any); ok && len(ext) == 2 {
				e := [2]float64{dateOrNumber(ext[0]), dateOrNumber(ext[1])}
				p.Extent = &e
			}
			p.InferUnits = upstream.ToValue(params["inferUnits"]).IsTruthy()
			if v, ok := params["interval"]; ok {
				p.NoInterval = !upstream.ToValue(v).IsTruthy()
			}
			res, _, err := TimeUnit(ctx, data, p)
			if err != nil {
				r.CheckAgainst(c, expected, nil, true)
				continue
			}
			r.CheckAgainst(c, expected, tupleList(res), false)
		case "flatten", "fold":
			data, ok := pulse(in, "add")
			fields, okFields := fieldList(params["fields"])
			if !ok || !okFields || !hasExpected {
				r.Skip(c.Op + " pulses of another shape")
				continue
			}
			var res []jsval.Value
			var err error
			if c.Op == "flatten" {
				p := FlattenParams{Fields: fields}
				if s, ok := params["index"].(string); ok {
					p.Index = s
				}
				res, err = Flatten(ctx, data, p)
			} else {
				res, err = Fold(ctx, data, FoldParams{Fields: fields})
			}
			if err != nil {
				r.CheckAgainst(c, expected, nil, true)
				continue
			}
			r.CheckAgainst(c, expected, tupleList(res), false)
		case "sequence":
			if !hasExpected {
				r.Skip("sequence pulses of another shape")
				continue
			}
			p := SequenceParams{Start: upstream.Number(params["start"]), Stop: upstream.Number(params["stop"])}
			if params["step"] != nil {
				p.Step = upstream.Number(params["step"])
			}
			if s, ok := params["as"].(string); ok {
				p.As = s
			}
			res, err := Sequence(ctx, p)
			if err != nil {
				r.CheckAgainst(c, expected, nil, true)
				continue
			}
			r.CheckAgainst(c, expected, tupleList(res), false)
		case "quantile":
			data, ok := pulse(in, "add")
			f, okField := field(params["field"])
			if !ok || !okField || !hasExpected {
				r.Skip("quantile pulses of another shape")
				continue
			}
			p := QuantileParams{Field: f}
			if probs, ok := params["probs"].([]any); ok {
				for _, pr := range probs {
					p.Probs = append(p.Probs, upstream.Number(pr))
				}
			}
			if v, ok := params["step"]; ok {
				p.Step = upstream.Number(v)
			}
			res, err := Quantile(ctx, data, p)
			if err != nil {
				r.CheckAgainst(c, expected, nil, true)
				continue
			}
			r.CheckAgainst(c, expected, tupleList(res), false)
		default:
			r.Skip("operators the adapter does not map (" + c.Op + ")")
		}
	}
	r.Done(50)
}

func dateOrNumber(v any) float64 {
	if d, ok := upstream.Date(v); ok {
		return d
	}
	f := upstream.Number(v)
	if math.IsNaN(f) {
		return math.NaN()
	}
	return f
}

// tupleList encodes tuples as the recorder encodes a pulse's tuples.
func tupleList(ts []jsval.Value) []any {
	out := make([]any, len(ts))
	for i, t := range ts {
		out[i] = upstream.FromValue(t)
	}
	return out
}
