package scale

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// InterpolateRange is vega-scale's interpolateRange: it rescales an
// interpolator over [0, 1] to the sub-interval [range[0], range[last]].
func InterpolateRange(interpolator UnitInterpolator, rng []float64) UnitInterpolator {
	if len(rng) == 0 {
		return func(float64) jsval.Value { return interpolator(math.NaN()) }
	}
	start := rng[0]
	span := rng[len(rng)-1] - start
	return func(i float64) jsval.Value { return interpolator(start + float64(i*span)) }
}

// Interpolate is vega-scale's interpolate(type, gamma): it looks up a d3
// two-argument interpolator by its vega name ("rgb", "hsl", "hcl-long",
// "cubehelix", "lab", "number", "round", "string", "array", "object", "date",
// "hue", "value"). gamma applies only to "rgb" and "cubehelix" (which carry a
// gamma variant upstream) and is ignored by the others, as upstream does. ok is
// false for a name with no matching d3 interpolator of this shape.
func Interpolate(typ string, gamma float64, hasGamma bool) (Interpolator, bool) {
	switch strings.ToLower(typ) {
	case "rgb":
		if hasGamma {
			return RGBGamma(gamma), true
		}
		return RGBGamma(1), true
	case "cubehelix":
		if hasGamma {
			return CubehelixGamma(gamma, false), true
		}
		return InterpolateCubehelix, true
	case "cubehelix-long":
		if hasGamma {
			return CubehelixGamma(gamma, true), true
		}
		return InterpolateCubehelixLong, true
	case "hsl":
		return InterpolateHSL, true
	case "hsl-long":
		return InterpolateHSLLong, true
	case "lab":
		return InterpolateLab, true
	case "hcl":
		return InterpolateHCL, true
	case "hcl-long":
		return InterpolateHCLLong, true
	case "number":
		return InterpolateNumber, true
	case "round":
		return InterpolateRound, true
	case "string":
		return InterpolateString, true
	case "array":
		return InterpolateArray, true
	case "object":
		return InterpolateObject, true
	case "date":
		return InterpolateDate, true
	case "hue":
		return InterpolateHue, true
	case "":
		return InterpolateValue, true // d3's `interpolate` itself
	}
	return nil, false
}

// InterpolateColors is vega-scale's interpolateColors: a piecewise
// interpolator through the colours using the named interpolation space
// ("rgb" when typ is empty).
func InterpolateColors(colors []jsval.Value, typ string, gamma float64, hasGamma bool) UnitInterpolator {
	if typ == "" {
		typ = "rgb"
	}
	interp, ok := Interpolate(typ, gamma, hasGamma)
	if !ok {
		interp = nil // d3's piecewise would call an undefined interpolator; use the default
	}
	return Piecewise(interp, colors)
}

// QuantizeInterpolator is vega-scale's quantizeInterpolator: count samples
// at (i+1)/(count+1), i.e. excluding both ends of the ramp.
func QuantizeInterpolator(interpolator UnitInterpolator, count int) []jsval.Value {
	if count < 0 || count > maxSamples {
		return nil
	}
	samples := make([]jsval.Value, count)
	n := float64(count + 1)
	for i := 0; i < count; i++ {
		samples[i] = interpolator(float64(i+1) / n)
	}
	return samples
}

// ScaleFraction is vega-scale's scaleFraction: a function that maps a value in
// [min, max] to its position in [0, 1] using the scale's own transform family
// (linear, log, pow, ...), so that a gradient legend can be laid out in the
// scale's spacing. A degenerate or infinite extent gives the constant 0.5.
func ScaleFraction(s Scale, min, max float64) func(x jsval.Value) jsval.Value {
	delta := max - min
	if delta == 0 || math.IsInf(delta, 0) || math.IsNaN(delta) {
		half := jsval.Num(0.5)
		return func(jsval.Value) jsval.Value { return half }
	}
	t := s.Type()
	if i := strings.IndexByte(t, '-'); i >= 0 {
		t = t[i+1:]
	}
	f, ok := New(t)
	if !ok {
		half := jsval.Num(0.5)
		return func(jsval.Value) jsval.Value { return half }
	}
	f.SetDomain([]jsval.Value{jsval.Num(min), jsval.Num(max)})
	f.SetRange([]jsval.Value{jsval.Num(0), jsval.Num(1)})
	for _, m := range [...]string{"clamp", "base", "constant", "exponent"} {
		if v, ok := Get(s, m); ok {
			Set(f, m, v)
		}
	}
	return f.Apply
}

// Get reads a property through its getter (the counterpart of Set), reporting
// whether this scale has one. Supported: clamp, round, base, exponent,
// constant, padding, paddingInner, paddingOuter, align, unknown, bandwidth,
// step.
func Get(s Scale, name string) (jsval.Value, bool) {
	switch name {
	case "clamp":
		if c, ok := s.(Clamper); ok {
			return jsval.Bool(c.Clamp()), true
		}
	case "unknown":
		if u, ok := s.(Unknowner); ok {
			return u.Unknown(), true
		}
	case "base":
		switch x := s.(type) {
		case *Continuous:
			v, ok := x.Base()
			return jsval.Num(v), ok
		case *Sequential:
			v, ok := x.Base()
			return jsval.Num(v), ok
		case *Diverging:
			v, ok := x.Base()
			return jsval.Num(v), ok
		}
	case "exponent":
		switch x := s.(type) {
		case *Continuous:
			v, ok := x.Exponent()
			return jsval.Num(v), ok
		case *Sequential:
			v, ok := x.Exponent()
			return jsval.Num(v), ok
		case *Diverging:
			v, ok := x.Exponent()
			return jsval.Num(v), ok
		}
	case "constant":
		switch x := s.(type) {
		case *Continuous:
			v, ok := x.Constant()
			return jsval.Num(v), ok
		case *Sequential:
			v, ok := x.Constant()
			return jsval.Num(v), ok
		case *Diverging:
			v, ok := x.Constant()
			return jsval.Num(v), ok
		}
	case "round", "padding", "paddingInner", "paddingOuter", "align", "bandwidth", "step":
		b, ok := s.(*Band)
		if !ok {
			return jsval.Undefined, false
		}
		switch name {
		case "round":
			return jsval.Bool(b.Round()), true
		case "padding":
			return jsval.Num(b.Padding()), true
		case "paddingInner":
			return jsval.Num(b.PaddingInner()), !b.point
		case "paddingOuter":
			return jsval.Num(b.PaddingOuter()), true
		case "align":
			return jsval.Num(b.Align()), true
		case "bandwidth":
			return jsval.Num(b.Bandwidth()), true
		default:
			return jsval.Num(b.Step()), true
		}
	}
	return jsval.Undefined, false
}
