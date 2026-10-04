package scale

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of upstream's own tests (see internal/upstream): d3-interpolate and d3-scale.

// interpolators maps d3-interpolate's exports of the form `(a, b) => t => value`.
var interpolators = map[string]Interpolator{
	"interpolate":              InterpolateValue,
	"interpolateNumber":        InterpolateNumber,
	"interpolateRound":         InterpolateRound,
	"interpolateDate":          InterpolateDate,
	"interpolateString":        InterpolateString,
	"interpolateObject":        InterpolateObject,
	"interpolateArray":         InterpolateArray,
	"interpolateNumberArray":   InterpolateArray,
	"interpolateLab":           InterpolateLab,
	"interpolateHcl":           InterpolateHCL,
	"interpolateHclLong":       InterpolateHCLLong,
	"interpolateHsl":           InterpolateHSL,
	"interpolateHslLong":       InterpolateHSLLong,
	"interpolateRgb":           InterpolateRGB,
	"interpolateCubehelix":     InterpolateCubehelix,
	"interpolateCubehelixLong": InterpolateCubehelixLong,
	"interpolateHue":           InterpolateHue,
}

// interpolatorOf resolves a recorded interpolator function through its origin.
func interpolatorOf(v any) (Interpolator, bool) {
	origin, ok := upstream.IsFunction(v)
	if !ok || origin == nil {
		return nil, false
	}
	if name, ok := origin["export"].(string); ok {
		f, ok := interpolators[name]
		return f, ok
	}
	return interpolatorFromCall(origin["from"], origin["args"])
}

// interpolatorFromCall resolves `interpolateRgb.gamma(g)` and its kin.
func interpolatorFromCall(from, args any) (Interpolator, bool) {
	name, _ := from.(string)
	a, _ := args.([]any)
	switch name {
	case "interpolateRgb.gamma":
		if len(a) == 1 {
			return RGBGamma(upstream.Number(a[0])), true
		}
	case "interpolateCubehelix.gamma":
		if len(a) == 1 {
			return CubehelixGamma(upstream.Number(a[0]), false), true
		}
	case "interpolateCubehelixLong.gamma":
		if len(a) == 1 {
			return CubehelixGamma(upstream.Number(a[0]), true), true
		}
	}
	return nil, false
}

func TestUpstreamD3Interpolate(t *testing.T) {
	r := upstream.Start(t, "d3-interpolate")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		fn := c.Fn
		if c.ArgsContain("$class") {
			r.Skip("colour instances as endpoints (the engine interpolates colour strings, as Vega passes them)")
			continue
		}
		if strings.HasPrefix(fn, "interpolateArray") && c.ArgsContain("object") {
			r.Skip("array-likes that are not arrays")
			continue
		}
		if c.ArgsContain("typed") || (!strings.HasPrefix(fn, "piecewise") && c.ArgsContain("function")) {
			r.Skip("typed arrays or functions as arguments (no such values in a specification)")
			continue
		}
		// A vector of the form `<export>()` asks the function the export returned.
		base, isCall := strings.CutSuffix(fn, "()")
		if !isCall {
			r.Skip("constructions (a function is the answer)")
			continue
		}
		args := c.ConstructedWith
		var unit func(float64) jsval.Value
		via := c.ViaSteps()
		switch {
		case base == "interpolateBasis" || base == "interpolateBasisClosed":
			xs := floatsOf(args[0])
			f := Basis
			if base == "interpolateBasisClosed" {
				f = BasisClosed
			}
			basis := f(xs)
			unit = func(t float64) jsval.Value { return jsval.Num(basis(t)) }
		case base == "interpolateDiscrete":
			unit = Discrete(upstream.ToValue(args[0]).Items())
		case base == "piecewise":
			interp, ok := interpolatorOf(args[0])
			if !ok {
				r.Skip("piecewise with an interpolator the recording cannot identify")
				continue
			}
			unit = Piecewise(interp, upstream.ToValue(args[1]).Items())
		case strings.HasSuffix(base, ".gamma"):
			// interpolateRgb.gamma(g)(a, b)(t)
			interp, ok := interpolatorFromCall(base, args)
			if !ok || len(via) != 1 || via[0].Method != "" || len(via[0].Args) != 2 {
				r.Skip("gamma interpolators not asked through (a, b)(t)")
				continue
			}
			unit = interp(upstream.ToValue(via[0].Args[0]), upstream.ToValue(via[0].Args[1]))
			via = nil
		case base == "interpolateZoom.rho":
			if len(via) != 1 || via[0].Method != "" || len(via[0].Args) != 2 {
				r.Skip("zoom with a different shape")
				continue
			}
			p0, ok0 := triple(via[0].Args[0])
			p1, ok1 := triple(via[0].Args[1])
			if !ok0 || !ok1 {
				r.Skip("zoom with a different shape")
				continue
			}
			interp, _ := Zoom(upstream.Number(args[0]))(p0, p1)
			unit = zoomValue(interp)
			via = nil
		case base == "interpolateZoom":
			p0, ok0 := triple(args[0])
			p1, ok1 := triple(args[1])
			if !ok0 || !ok1 {
				r.Skip("zoom with a different shape")
				continue
			}
			interp, _ := DefaultZoom()(p0, p1)
			unit = zoomValue(interp)
		default:
			f, ok := interpolators[base]
			if !ok || len(args) != 2 {
				r.Skip("unmapped " + fn)
				continue
			}
			unit = f(upstream.ToValue(args[0]), upstream.ToValue(args[1]))
		}
		if len(via) > 0 || len(c.Args) != 1 || c.Method != "" {
			r.Skip("not a plain call of the returned function")
			continue
		}
		r.Check(c, upstream.FromValue(unit(upstream.Number(c.Args[0]))), false)
	}
	r.Done(200)
}

func floatsOf(v any) []float64 {
	items, _ := v.([]any)
	out := make([]float64, len(items))
	for i, x := range items {
		out[i] = upstream.Number(x)
	}
	return out
}

func triple(v any) (p [3]float64, ok bool) {
	items, isArr := v.([]any)
	if !isArr || len(items) != 3 {
		return p, false
	}
	for i := range p {
		p[i] = upstream.Number(items[i])
	}
	return p, true
}

func zoomValue(f func(float64) [3]float64) func(float64) jsval.Value {
	return func(t float64) jsval.Value {
		v := f(t)
		return jsval.ArrOf(jsval.Num(v[0]), jsval.Num(v[1]), jsval.Num(v[2]))
	}
}

// scaleTypes maps d3-scale's constructors to the registry's type names. d3 has no scaleSequentialQuantile
// in Vega's registry; those vectors are not replayed. scaleRadial is not there either and is
// built directly.
var scaleTypes = map[string]string{
	"scaleLinear":           TypeLinear,
	"scaleLog":              TypeLog,
	"scalePow":              TypePow,
	"scaleSqrt":             TypeSqrt,
	"scaleSymlog":           TypeSymlog,
	"scaleIdentity":         TypeIdentity,
	"scaleTime":             TypeTime,
	"scaleUtc":              TypeUTC,
	"scaleSequential":       "sequential-linear",
	"scaleSequentialLog":    "sequential-log",
	"scaleSequentialPow":    "sequential-pow",
	"scaleSequentialSqrt":   "sequential-sqrt",
	"scaleSequentialSymlog": "sequential-symlog",
	"scaleDiverging":        "diverging-linear",
	"scaleDivergingLog":     "diverging-log",
	"scaleDivergingPow":     "diverging-pow",
	"scaleDivergingSqrt":    "diverging-sqrt",
	"scaleDivergingSymlog":  "diverging-symlog",
	"scaleQuantile":         TypeQuantile,
	"scaleQuantize":         TypeQuantize,
	"scaleThreshold":        TypeThreshold,
	"scaleOrdinal":          TypeOrdinal,
	"scaleBand":             TypeBand,
	"scalePoint":            TypePoint,
}

// upstreamTickCount is the count argument of ticks, nice and tickFormat: a number, or a d3-time interval.
func upstreamTickCount(v any, local format.Zone) (TickCount, bool) {
	if iv, ok := intervalOfFunction(v, local); ok {
		return IntervalCount(iv), true
	}
	if upstream.IsUndefined(v) || v == nil {
		return TickCount{}, true
	}
	if upstream.Contains(v, "function") {
		return TickCount{}, false
	}
	return Count(upstream.Number(v)), true
}

// intervalOfFunction resolves an interval d3-time exported, through its recorded origin.
func intervalOfFunction(v any, local format.Zone) (format.Interval, bool) {
	origin, ok := upstream.IsFunction(v)
	if !ok || origin == nil {
		return format.Interval{}, false
	}
	name, _ := origin["export"].(string)
	if pkg, _ := origin["package"].(string); pkg != "d3-time" {
		return format.Interval{}, false
	}
	z := format.UTC
	var unit string
	switch {
	case strings.HasPrefix(name, "time"):
		z, unit = local, name[len("time"):]
	case strings.HasPrefix(name, "utc"):
		unit = name[len("utc"):]
	case name == "unixDay":
		return format.UTC.UnixDay(), true
	default:
		return format.Interval{}, false
	}
	switch unit {
	case "Millisecond":
		return z.Millisecond(), true
	case "Second":
		return z.Second(), true
	case "Minute":
		return z.Minute(), true
	case "Hour":
		return z.Hour(), true
	case "Day":
		return z.Day(), true
	case "Week", "Sunday":
		return z.Week(0), true
	case "Monday":
		return z.Week(1), true
	case "Month":
		return z.Month(), true
	case "Year":
		return z.Year(), true
	}
	return format.Interval{}, false
}

// configure applies one recorded configuration step. ok is false for a step the engine has no
// counterpart for, or one that takes a function the recording cannot identify.
func configure(s Scale, step upstream.Step, local format.Zone) bool {
	if upstream.Contains(step.Args, "function") && step.Method != "interpolate" && step.Method != "nice" {
		return false
	}
	arg := func(i int) any {
		if i < len(step.Args) {
			return step.Args[i]
		}
		return upstream.Undefined()
	}
	switch step.Method {
	case "":
		// asking an implicit ordinal scale adds the value to its domain
		s.Apply(upstream.ToValue(arg(0)))
		return true
	case "domain", "range", "rangeRound":
		items, isArr := arg(0).([]any)
		if !isArr {
			return false
		}
		vals := make([]jsval.Value, len(items))
		for i, e := range items {
			vals[i] = upstream.ToValue(e)
		}
		return Set(s, step.Method, jsval.Arr(vals))
	case "clamp", "unknown", "round", "base", "exponent", "constant", "padding", "paddingInner", "paddingOuter", "align":
		return Set(s, step.Method, upstream.ToValue(arg(0)))
	case "interpolate":
		ip, ok := s.(Interpolating)
		if !ok {
			return false
		}
		origin, isFn := upstream.IsFunction(arg(0))
		if !isFn || origin == nil || origin["package"] != "d3-interpolate" {
			return false
		}
		f, ok := interpolators[origin["export"].(string)]
		if !ok {
			return false
		}
		ip.SetInterpolate(f)
		return true
	case "nice":
		n, ok := s.(Niceable)
		if !ok || len(step.Args) > 1 {
			return false
		}
		count, ok := upstreamTickCount(arg(0), local)
		if !ok {
			return false
		}
		n.Nice(count)
		return true
	}
	return false
}

func TestUpstreamD3Scale(t *testing.T) {
	r := upstream.Start(t, "d3-scale")
	loc, err := time.LoadLocation(r.File.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	local := format.Local(loc)
	locale, err := format.NewLocale(jsval.Undefined, jsval.Undefined, local)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		base, isCall := strings.CutSuffix(c.Fn, "()")
		if !isCall {
			r.Skip("constructions and tickFormat results (a function is the answer)")
			continue
		}
		if base == "tickFormat" {
			// tickFormat(start, stop, count, specifier)(value)
			ca := c.ConstructedWith
			if len(c.Args) != 1 || len(c.ViaSteps()) != 0 || c.Method != "" || c.ArgsContain("function") {
				r.Skip("tickFormat asked through another shape")
				continue
			}
			spec := upstream.String(argOf(ca, 3))
			if len(ca) < 4 || ca[3] == nil || upstream.IsUndefined(ca[3]) {
				spec = ",f"
			}
			f, err := locale.FormatSpan(upstream.Number(argOf(ca, 0)), upstream.Number(argOf(ca, 1)), upstream.Number(argOf(ca, 2)), spec)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, f(upstream.Number(c.Args[0])), false)
			continue
		}
		if base == "scaleBand" || base == "scalePoint" {
			r.Skip("d3's band and point scales (Vega registers its own, replayed from vega-scale)")
			continue
		}
		typ, ok := scaleTypes[base]
		if !ok && base != "scaleRadial" {
			r.Skip("scales the engine does not have (" + base + ")")
			continue
		}
		var s Scale = NewRadial()
		if ok {
			s, _ = NewIn(typ, local)
		}
		if ts, isTime := s.(*Time); isTime && typ == TypeTime {
			_ = ts
		}
		configured := true
		steps := append(constructorSteps(base, c.ConstructedWith), c.ChainSteps()...)
		for _, step := range steps {
			if !configure(s, step, local) {
				configured = false
				break
			}
		}
		if !configured {
			r.Skip("configuration the recording or the engine cannot express")
			continue
		}
		if c.ArgsContain("function") && c.Method != "nice" {
			r.Skip("function arguments")
			continue
		}
		if c.ArgsContain("$class", "object") {
			r.Skip("objects, which d3 compares by identity, and wrapper objects")
			continue
		}
		arg := c.Arg
		via := c.ViaSteps()
		if len(via) == 1 && via[0].Method == "tickFormat" && c.Method == "" {
			// scale.tickFormat(count, specifier)(value)
			tf, ok := s.(interface {
				TickFormat(*format.Locale, TickCount, jsval.Value) (func(float64) string, error)
			})
			if !ok {
				r.Skip("scales without tickFormat")
				continue
			}
			count, ok := upstreamTickCount(argOf(via[0].Args, 0), local)
			if !ok {
				r.Skip("tickFormat count the recording cannot identify")
				continue
			}
			f, err := tf.TickFormat(locale, count, upstream.ToValue(argOf(via[0].Args, 1)))
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, f(upstream.Number(arg(0))), false)
			continue
		}
		if len(via) > 0 {
			r.Skip("derived functions of another shape")
			continue
		}
		switch c.Method {
		case "":
			r.Check(c, upstream.FromValue(s.Apply(upstream.ToValue(arg(0)))), false)
		case "invert":
			inv, ok := s.(Inverter)
			if !ok {
				r.Skip("scales without invert")
				continue
			}
			r.Check(c, upstream.FromValue(inv.Invert(upstream.ToValue(arg(0)))), false)
		case "ticks":
			tk, ok := s.(Ticker)
			if !ok {
				r.Skip("scales without ticks")
				continue
			}
			count, ok := upstreamTickCount(arg(0), local)
			if !ok {
				r.Skip("tick count the recording cannot identify")
				continue
			}
			ticks := tk.Ticks(count)
			if _, isTime := s.(*Time); isTime {
				out := make([]any, len(ticks))
				for j, v := range ticks {
					out[j] = upstream.EncDate(v)
				}
				r.Check(c, out, false)
			} else {
				r.Check(c, upstream.Floats(ticks), false)
			}
		case "domain":
			r.Check(c, valuesOf(s.Domain()), false)
		case "range":
			r.Check(c, valuesOf(s.Range()), false)
		case "copy":
			r.Skip("copies (a function is the answer)")
		case "quantiles":
			if q, ok := s.(*Quantile); ok {
				r.Check(c, upstream.Floats(q.Quantiles()), false)
			} else {
				r.Skip("unmapped quantiles")
			}
		case "thresholds":
			if q, ok := s.(*Quantize); ok {
				r.Check(c, upstream.Floats(q.Thresholds()), false)
			} else {
				r.Skip("unmapped thresholds")
			}
		case "invertExtent":
			ei, ok := s.(ExtentInverter)
			if !ok {
				r.Skip("scales without invertExtent")
				continue
			}
			ext := ei.InvertExtent(upstream.ToValue(arg(0)))
			r.Check(c, []any{upstream.FromValue(ext[0]), upstream.FromValue(ext[1])}, false)
		case "bandwidth", "step", "paddingInner", "paddingOuter", "padding", "align", "round", "unknown", "clamp", "exponent", "base", "constant":
			if len(c.Args) != 0 {
				r.Skip("unmapped " + c.Method + " with arguments")
				continue
			}
			v, ok := Get(s, c.Method)
			if !ok {
				r.Skip("unmapped getter " + c.Method)
				continue
			}
			if o, isOrdinal := s.(*Ordinal); isOrdinal && c.Method == "unknown" && o.Implicit() {
				// d3's implicit marker is a private symbol
				r.Check(c, map[string]any{"$": "symbol"}, false)
				continue
			}
			r.Check(c, upstream.FromValue(v), false)
		case "tickFormat":
			r.Skip("tickFormat results (a function is the answer)")
		default:
			r.Skip("unmapped method " + c.Method)
		}
	}
	r.Done(836)
}

func argOf(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}

func valuesOf(vs []jsval.Value) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = upstream.FromValue(v)
	}
	return out
}

// constructorSteps are what d3's constructors do with their arguments: initRange for the scales
// that have a range (`scaleLinear(range)`, `scaleLinear(domain, range)`) and initInterpolator for
// the sequential and diverging ones, whose last argument may be an interpolator function.
func constructorSteps(fn string, args []any) []upstream.Step {
	if len(args) == 0 {
		return nil
	}
	step := func(m string, a any) upstream.Step { return upstream.Step{Method: m, Args: []any{a}} }
	if strings.HasPrefix(fn, "scaleSequential") || strings.HasPrefix(fn, "scaleDiverging") {
		var out []upstream.Step
		last := args[len(args)-1]
		if len(args) > 1 {
			out = append(out, step("domain", args[0]))
		}
		if _, isFn := upstream.IsFunction(last); isFn {
			return append(out, step("interpolator", last))
		}
		return append(out, step("range", last))
	}
	if len(args) == 1 {
		return []upstream.Step{step("range", args[0])}
	}
	return []upstream.Step{step("domain", args[0]), step("range", args[1])}
}

// TestUpstreamD3ArrayTicks replays d3-array's tick functions, which d3-scale builds on.
func TestUpstreamD3ArrayTicks(t *testing.T) {
	r := upstream.Start(t, "d3-array")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			continue
		}
		n := func(i int) float64 { return upstream.Number(c.Arg(i)) }
		switch c.Fn {
		case "ticks":
			r.Check(c, upstream.Floats(Ticks(n(0), n(1), n(2))), false)
		case "tickStep":
			r.Check(c, upstream.Enc(TickStep(n(0), n(1), n(2))), false)
		case "tickIncrement":
			r.Check(c, upstream.Enc(TickIncrement(n(0), n(1), n(2))), false)
		case "nice":
			lo, hi := Nice(n(0), n(1), n(2))
			r.Check(c, upstream.Floats([]float64{lo, hi}), false)
		}
	}
	r.Done(250)
}
