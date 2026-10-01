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
// operators whose answer is a set of tuples that the recording holds in full. The recording is one call
// of the operator's transform per vector: its parameters, the tuples of the pulse that went in and the
// ones that came out. An operator that is asked again with other parameters (sequence above 0) is
// handed the tuples it has already seen.
//
// An operator that runs again over its source (window, dotbin, lookup, impute, extent) is replayed on
// every call; one that keeps state between pulses (aggregate, collect, filter, sample) on its first
// call, and aggregate on the later ones by the cells it holds afterwards. The expressions of a filter
// or a formula are JavaScript closures that the recording holds only by the fields they read.
func TestUpstreamVegaTransforms(t *testing.T) {
	r := upstream.Start(t, "vega-transforms")
	loc, err := time.LoadLocation(r.File.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	local := format.Local(loc)
	ctx := context.Background()
	collected := map[int]*collectState{}

	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil && c.Op == "collect" {
			collected[c.Instance] = &collectState{lost: true}
		}
		if c.Oversized != nil || c.Op == "" {
			continue
		}
		in, _ := c.Input.(map[string]any)
		out, _ := c.Output.(map[string]any)
		params, _ := c.Params.(map[string]any)
		o := &opCall{Replay: r, ctx: ctx, c: c, params: params, in: in, out: out, collected: collected}
		expected, hasExpected := out["add"], out["add"] != nil
		if !hasExpected {
			expected, hasExpected = out["mod"], out["mod"] != nil
		}
		if upstream.Contains(c.Output, "truncated") || upstream.Contains(c.Input, "truncated") {
			r.Skip("pulses too large to record")
			if c.Op == "collect" {
				collected[c.Instance] = &collectState{lost: true}
			}
			continue
		}
		switch c.Op {
		case "timeunit":
			data, ok := pulseTuples(in, "add", "source")
			f, okField := fieldFrom(params["field"])
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
			data, ok := pulseTuples(in, "add")
			fields, okFields := fieldsFrom(params["fields"])
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
			data, ok := pulseTuples(in, "add")
			f, okField := fieldFrom(params["field"])
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
		case "aggregate":
			o.aggregate()
		case "window":
			o.window()
		case "bin":
			o.bin()
		case "project":
			o.project()
		case "extent":
			o.extent()
		case "impute":
			o.impute()
		case "lookup":
			o.lookup()
		case "dotbin":
			o.dotbin()
		case "kde":
			o.kde()
		case "filter":
			o.filter()
		case "sample":
			o.sample()
		case "collect":
			o.collect()
		case "formula":
			r.Skip("formulas, whose expression is a closure the recording holds only by the fields it reads")
		default:
			r.Skip(c.Op + ", which is " + plumbing[c.Op])
		}
	}
	r.Done(280)
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
