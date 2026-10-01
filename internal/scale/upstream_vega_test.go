package scale

import (
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replay of vega-scale's own tests (see internal/upstream).

// vegaScaleOf rebuilds a scale a test made with `scale(type)()` and went on configuring, from the
// recording of the call or of the function it was passed on as.
func vegaScaleOf(typ string, viaChain []upstream.Step, local format.Zone) (Scale, bool) {
	s, ok := NewIn(typ, local)
	if !ok {
		return nil, false
	}
	for _, step := range viaChain {
		if !configure(s, step, local) {
			return nil, false
		}
	}
	return s, true
}

func stepsOf(raw any) []upstream.Step {
	items, _ := raw.([]any)
	out := make([]upstream.Step, 0, len(items))
	for _, item := range items {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		m, _ := pair[0].(string)
		a, _ := pair[1].([]any)
		out = append(out, upstream.Step{Method: m, Args: a})
	}
	return out
}

func TestUpstreamVegaScale(t *testing.T) {
	r := upstream.Start(t, "vega-scale")
	loc, err := time.LoadLocation(r.File.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	local := format.Local(loc)
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		switch c.Fn {
		case "scale()":
			typ, _ := argOf(c.ConstructedWith, 0).(string)
			via := c.ViaSteps()
			if len(via) != 1 || via[0].Method != "" || c.Method == "" {
				r.Skip("constructions (a scale is the answer)")
				continue
			}
			s, ok := vegaScaleOf(typ, c.ViaChainSteps(), local)
			if !ok {
				r.Skip("configuration the engine cannot express")
				continue
			}
			switch c.Method {
			case "invert":
				inv, ok := s.(Inverter)
				if !ok {
					r.Skip("scales without invert")
					continue
				}
				r.Check(c, upstream.FromValue(inv.Invert(upstream.ToValue(argOf(c.Args, 0)))), false)
			case "invertRange":
				rng := upstream.ToValue(argOf(c.Args, 0))
				v, ok := InvertRange(s, rng.Index(0), rng.Index(1))
				if !ok {
					r.Check(c, upstream.Undefined(), false)
					continue
				}
				r.Check(c, upstream.FromValue(v), false)
			default:
				r.Skip("unmapped method " + c.Method)
			}
		case "tickCount", "tickValues":
			origin, isFn := upstream.IsFunction(c.Arg(0))
			if !isFn || origin == nil || origin["from"] != "scale()" {
				r.Skip("scales that are the test's own functions")
				continue
			}
			cw, _ := origin["constructedWith"].([]any)
			typ, _ := argOf(cw, 0).(string)
			s, ok := vegaScaleOf(typ, stepsOfChain(origin["viaChain"]), local)
			if !ok {
				r.Skip("configuration the engine cannot express")
				continue
			}
			countArg := upstream.ToValue(c.Arg(1))
			var minStep *float64
			if c.Fn == "tickCount" && len(c.Args) > 2 && c.Args[2] != nil && !upstream.IsUndefined(c.Args[2]) {
				m := upstream.Number(c.Args[2])
				minStep = &m
			}
			if c.Fn == "tickCount" {
				tc, err := TickCountFor(s, countArg, minStep)
				if err != nil {
					r.Check(c, nil, true)
					continue
				}
				if tc.Interval != nil {
					r.Skip("interval tick counts")
					continue
				}
				r.Check(c, upstream.Enc(tc.N), false)
				continue
			}
			tc, err := TickCountFor(s, countArg, nil)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, valuesOf(TickValues(s, tc)), false)
		default:
			r.Skip("unmapped " + c.Fn)
		}
	}
	r.Done(60)
}

// stepsOfChain reads an origin's recorded chain: a list of [method, args].
func stepsOfChain(raw any) []upstream.Step { return stepsOf(raw) }
