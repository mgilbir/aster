package scale

import (
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"

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

// Interpolate is vega-scale's interpolate(type, gamma): it looks up the d3
// interpolator d3['interpolate' + CamelCase(type)], a function of a range's
// two ends: "rgb", "hsl", "hsl-long", "lab", "hcl", "hcl-long",
// "cubehelix", "cubehelix-long", "number", "round", "string", "array",
// "number-array", "object", "date", "hue" and "zoom". gamma applies only to
// "rgb" and the cubehelix pair (which carry a gamma variant upstream) and is
// ignored by the others, as upstream does. ok is false for any other name:
// one d3 does not export (upstream's interpolator is then undefined, and the
// scale throws when used), and the names UnsupportedInterpolator explains.
func Interpolate(typ string, gamma float64, hasGamma bool) (Interpolator, bool) {
	switch jsval.JSLower(typ) { // 'interpolate' + type.toLowerCase()…
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
		return InterpolateNumberI, true
	case "round":
		return InterpolateRoundI, true
	case "string":
		return InterpolateString, true
	case "array":
		return InterpolateArray, true
	case "object":
		return InterpolateObject, true
	case "date":
		return InterpolateDateI, true
	case "hue":
		return InterpolateHue, true
	case "number-array":
		return InterpolateNumberArray, true
	case "zoom":
		return InterpolateZoom, true
	case "":
		return InterpolateValue, true // d3's `interpolate` itself
	}
	return nil, false
}

// UnsupportedInterpolator explains why a name d3 exports is not a scale's
// interpolator here: transform-css and transform-svg parse their ends with
// the browser's DOMMatrix and an SVG element, which node (where headless
// Vega runs) does not have either, so upstream throws as the scale is used;
// basis, basis-closed, discrete, rgb-basis and rgb-basis-closed take one
// list of values, not a range's two ends, so a scale built with them yields
// NaN. ok is false for every other name.
func UnsupportedInterpolator(typ string) (reason string, ok bool) {
	switch jsval.JSLower(typ) {
	case "transform-css", "transform-svg":
		return "it needs a browser's DOMMatrix or SVG DOM, which node and this engine do not have", true
	case "basis", "basis-closed", "discrete", "rgb-basis", "rgb-basis-closed":
		return "it interpolates a list of values, not a range's two ends", true
	}
	return "", false
}

// InterpolateNumberArray is d3.interpolateNumberArray: the first
// min(a.length, b.length) elements are numbers between a's and b's
// (a[i] * (1 - t) + b[i] * t), the rest of b as it is. A falsy b is []. Each
// call returns a fresh array, where upstream mutates one (as for
// InterpolateArray). A b that is not an array is upstream's TypeError
// (b.slice is not a function, or a string's units, read-only, assigned to);
// one with nothing to assign is returned as it is.
func InterpolateNumberArray(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	if !b.IsTruthy() {
		b = jsval.Arr(nil)
	}
	al, aok := jsval.ToArrayLike(a)
	n := 0
	if aok && a.IsTruthy() {
		if bl, ok := jsval.ToArrayLike(b); ok {
			n = int(min(al.Len(), bl.Len()))
		}
	}
	switch {
	case b.IsStr() && n == 0:
		return func(float64) jsval.Value { return b }
	case !b.IsArr():
		panic(&Thrown{Name: "TypeError", Msg: "interpolateNumberArray: the end of the range is not an array"})
	}
	bi := b.Items()
	x0, x1 := make([]float64, n), make([]float64, n)
	w.Work(int64(n))
	for i := range n {
		x0[i], x1[i] = jsval.ToNumber(al.At(int64(i))), jsval.ToNumber(bi[i])
	}
	return func(t float64) jsval.Value {
		w.Work(int64(len(bi)))
		w.Keep(int64(len(bi)) * valueBytes)
		c := make([]jsval.Value, len(bi))
		copy(c, bi)
		for i := range n {
			c[i] = jsval.Num(float64(x0[i]*(1-t)) + float64(x1[i]*t))
		}
		return jsval.Arr(c)
	}
}

// InterpolateZoom is d3.interpolateZoom of two views [ux, uy, w]: their
// members read as `p0[0]` reads them (a view that is null or undefined is
// upstream's TypeError), and the view written [ux0 + ox, uy0 + oy, w] with
// JavaScript's +, which joins a start that is a string, an array or an
// object as text.
func InterpolateZoom(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	if a.IsNullish() || b.IsNullish() {
		panic(&Thrown{Name: "TypeError", Msg: "interpolateZoom: a view is null or undefined"})
	}
	var p0, p1 [3]float64
	var u0 [2]jsval.Value
	for i := range 3 {
		v0, v1 := viewMember(a, i), viewMember(b, i)
		p0[i], p1[i] = jsval.ToNumber(v0), jsval.ToNumber(v1)
		if i < 2 {
			u0[i] = v0
		}
	}
	move, _ := zoomRho(math.Sqrt2, 2, 4)(p0, p1)
	plus := func(start jsval.Value, x0, d float64) jsval.Value {
		switch start.Kind() {
		case jsval.KindNum, jsval.KindBool, jsval.KindNull, jsval.KindUndefined:
			return jsval.Num(x0 + d)
		}
		return jsval.Str(w.String(start) + jsval.JSNumberString(d)) // ToPrimitive gives a string
	}
	return func(t float64) jsval.Value {
		m := move(t)
		w.Keep(3 * valueBytes)
		return jsval.ArrOf(plus(u0[0], p0[0], m[0]), plus(u0[1], p0[1], m[1]), jsval.Num(m[2]))
	}
}

// viewMember is v[i] for a value that is not null or undefined.
func viewMember(v jsval.Value, i int) jsval.Value {
	if v.IsObj() {
		return v.ObjValue().Lookup(jsval.JSNumberString(float64(i)))
	}
	a, _ := jsval.ToArrayLike(v)
	return a.At(int64(i))
}

// InterpolateColors is vega-scale's interpolateColors: a piecewise
// interpolator through the colours using the named interpolation space
// ("rgb" when typ is empty). w walks the colours (the call's Walker).
func InterpolateColors(w jsval.Walker, colors []jsval.Value, typ string, gamma float64, hasGamma bool) UnitInterpolator {
	if typ == "" {
		typ = "rgb"
	}
	interp, ok := Interpolate(typ, gamma, hasGamma)
	if !ok {
		interp = nil // d3's piecewise would call an undefined interpolator; use the default
	}
	return Piecewise(w, interp, colors)
}

// MaxSchemeSamples bounds the colours quantizeInterpolator samples from a
// scheme for a discrete scale (schemeCount, or the size of the domain).
const MaxSchemeSamples = 1 << 20

// QuantizeInterpolator is vega-scale's quantizeInterpolator: count samples
// at (i+1)/(count+1), i.e. excluding both ends of the ramp. A count above
// MaxSchemeSamples is an error wrapping budget.ErrLimit; a negative count is
// the caller's to refuse (upstream's new Array(count) throws).
func QuantizeInterpolator(interpolator UnitInterpolator, count int) ([]jsval.Value, error) {
	if count > MaxSchemeSamples {
		return nil, fmt.Errorf("%w: %d colours sampled from a scheme, more than %d", budget.ErrLimit, count, MaxSchemeSamples)
	}
	if count <= 0 {
		return nil, nil
	}
	samples := make([]jsval.Value, count)
	n := float64(count + 1)
	for i := 0; i < count; i++ {
		samples[i] = interpolator(float64(i+1) / n)
	}
	return samples, nil
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
		if r, ok := s.(*Radial); ok && name == "round" {
			return jsval.Bool(r.Round()), true
		}
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
