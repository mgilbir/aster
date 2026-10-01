package scale

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale/color"
)

// Interpolator is d3's `interpolate(a, b)` shape: given a start and end value
// it returns a function of t (normally in [0, 1]) that yields the value in
// between. Colour interpolators produce colour strings ("rgb(...)"), exactly
// as d3 does, because that is what reaches the scenegraph.
type Interpolator func(a, b jsval.Value) func(t float64) jsval.Value

// channelKind selects how one colour channel is blended.
type channelKind uint8

const (
	chConst channelKind = iota
	chLinear
	chExp
)

// channel is d3-interpolate's linear / exponential / constant channel helper as
// a value type, so evaluating a colour interpolator allocates nothing but the
// resulting string.
type channel struct {
	kind channelKind
	a, d float64 // linear: a + t*d ; const: a ; exp: a (pow'd), d = pow(b)-a
	y    float64 // exp: 1/gamma
}

func (c channel) at(t float64) float64 {
	switch c.kind {
	case chLinear:
		return c.a + float64(t*c.d)
	case chExp:
		return jsPow(c.a+float64(t*c.d), c.y)
	}
	return c.a
}

func constantChannel(a, b float64) channel {
	if math.IsNaN(a) {
		return channel{kind: chConst, a: b}
	}
	return channel{kind: chConst, a: a}
}

// linearChannel is d3's `nogamma`.
func linearChannel(a, b float64) channel {
	d := b - a
	if d != 0 && !math.IsNaN(d) {
		return channel{kind: chLinear, a: a, d: d}
	}
	return constantChannel(a, b)
}

// hueChannel is d3's `hue`: interpolate along the shorter arc.
func hueChannel(a, b float64) channel {
	d := b - a
	if d != 0 && !math.IsNaN(d) {
		if d > 180 || d < -180 {
			d -= float64(360 * jsRound(d/360))
		}
		return channel{kind: chLinear, a: a, d: d}
	}
	return constantChannel(a, b)
}

// gammaChannel is d3's `gamma(y)(a, b)`.
func gammaChannel(y, a, b float64) channel {
	if y == 1 {
		return linearChannel(a, b)
	}
	if d := b - a; d != 0 && !math.IsNaN(d) {
		pa := jsPow(a, y)
		return channel{kind: chExp, a: pa, d: jsPow(b, y) - pa, y: 1 / y}
	}
	return constantChannel(a, b)
}

// InterpolateNumber is d3.interpolateNumber; both ends go through Number().
func InterpolateNumber(a, b jsval.Value) func(float64) jsval.Value {
	x, y := jsval.ToNumber(a), jsval.ToNumber(b)
	return func(t float64) jsval.Value { return jsval.Num(lerp(x, y, t)) }
}

// lerp is a*(1-t) + b*t, written so the compiler cannot fuse the products.
func lerp(a, b, t float64) float64 { return float64(a*(1-t)) + float64(b*t) }

// InterpolateRound is d3.interpolateRound.
func InterpolateRound(a, b jsval.Value) func(float64) jsval.Value {
	x, y := jsval.ToNumber(a), jsval.ToNumber(b)
	return func(t float64) jsval.Value { return jsval.Num(jsRound(lerp(x, y, t))) }
}

// InterpolateDate is d3.interpolateDate. Like Date.prototype.setTime it applies
// TimeClip, so results outside +-8.64e15 ms or NaN become an invalid date.
func InterpolateDate(a, b jsval.Value) func(float64) jsval.Value {
	x, y := jsval.ToNumber(a), jsval.ToNumber(b)
	return func(t float64) jsval.Value { return jsval.Timestamp(timeClip(lerp(x, y, t))) }
}

func timeClip(x float64) float64 {
	if math.IsNaN(x) || math.Abs(x) > 8.64e15 {
		return math.NaN()
	}
	return math.Trunc(x) + 0 // +0 turns -0 into +0
}

func colorString(v jsval.Value) string {
	if v.IsStr() {
		return v.StrValue()
	}
	return v.AsString()
}

// RGBGamma returns d3.interpolateRgb.gamma(y): rgb interpolation with a gamma
// correction applied to the colour channels (never to opacity).
func RGBGamma(y float64) Interpolator {
	return func(a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseRGB(colorString(a))
		end := color.ParseRGB(colorString(b))
		r := gammaChannel(y, start.R, end.R)
		g := gammaChannel(y, start.G, end.G)
		bl := gammaChannel(y, start.B, end.B)
		o := linearChannel(start.Opacity, end.Opacity)
		return func(t float64) jsval.Value {
			return jsval.Str(color.RGB{R: r.at(t), G: g.at(t), B: bl.at(t), Opacity: o.at(t)}.FormatRgb())
		}
	}
}

// InterpolateRGB is d3.interpolateRgb.
func InterpolateRGB(a, b jsval.Value) func(float64) jsval.Value { return RGBGamma(1)(a, b) }

func hslInterp(long bool) Interpolator {
	return func(a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseHSL(colorString(a))
		end := color.ParseHSL(colorString(b))
		var h channel
		if long {
			h = linearChannel(start.H, end.H)
		} else {
			h = hueChannel(start.H, end.H)
		}
		s := linearChannel(start.S, end.S)
		l := linearChannel(start.L, end.L)
		o := linearChannel(start.Opacity, end.Opacity)
		return func(t float64) jsval.Value {
			return jsval.Str(color.HSL{H: h.at(t), S: s.at(t), L: l.at(t), Opacity: o.at(t)}.String())
		}
	}
}

// InterpolateHSL is d3.interpolateHsl (shortest hue arc); InterpolateHSLLong is
// d3.interpolateHslLong (hue interpolated linearly).
var (
	InterpolateHSL     Interpolator = hslInterp(false)
	InterpolateHSLLong Interpolator = hslInterp(true)
)

// InterpolateLab is d3.interpolateLab.
func InterpolateLab(a, b jsval.Value) func(float64) jsval.Value {
	start := color.ParseLab(colorString(a))
	end := color.ParseLab(colorString(b))
	l := linearChannel(start.L, end.L)
	ca := linearChannel(start.A, end.A)
	cb := linearChannel(start.B, end.B)
	o := linearChannel(start.Opacity, end.Opacity)
	return func(t float64) jsval.Value {
		return jsval.Str(color.Lab{L: l.at(t), A: ca.at(t), B: cb.at(t), Opacity: o.at(t)}.String())
	}
}

func hclInterp(long bool) Interpolator {
	return func(a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseHCL(colorString(a))
		end := color.ParseHCL(colorString(b))
		var h channel
		if long {
			h = linearChannel(start.H, end.H)
		} else {
			h = hueChannel(start.H, end.H)
		}
		c := linearChannel(start.C, end.C)
		l := linearChannel(start.L, end.L)
		o := linearChannel(start.Opacity, end.Opacity)
		return func(t float64) jsval.Value {
			return jsval.Str(color.HCL{H: h.at(t), C: c.at(t), L: l.at(t), Opacity: o.at(t)}.String())
		}
	}
}

// InterpolateHCL is d3.interpolateHcl; InterpolateHCLLong is interpolateHclLong.
var (
	InterpolateHCL     Interpolator = hclInterp(false)
	InterpolateHCLLong Interpolator = hclInterp(true)
)

// CubehelixGamma returns d3.interpolateCubehelix.gamma(y) (or the Long variant):
// the gamma bends only the lightness channel.
func CubehelixGamma(y float64, long bool) Interpolator {
	return func(a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseCubehelix(colorString(a))
		end := color.ParseCubehelix(colorString(b))
		var h channel
		if long {
			h = linearChannel(start.H, end.H)
		} else {
			h = hueChannel(start.H, end.H)
		}
		s := linearChannel(start.S, end.S)
		l := linearChannel(start.L, end.L)
		o := linearChannel(start.Opacity, end.Opacity)
		return func(t float64) jsval.Value {
			return jsval.Str(color.Cubehelix{H: h.at(t), S: s.at(t), L: l.at(jsPow(t, y)), Opacity: o.at(t)}.String())
		}
	}
}

// InterpolateCubehelix is d3.interpolateCubehelix (gamma 1, shortest hue arc);
// InterpolateCubehelixLong is interpolateCubehelixLong.
var (
	InterpolateCubehelix     = CubehelixGamma(1, false)
	InterpolateCubehelixLong = CubehelixGamma(1, true)
)

// InterpolateHue is d3.interpolateHue: the hue in [0, 360).
func InterpolateHue(a, b jsval.Value) func(float64) jsval.Value {
	c := hueChannel(jsval.ToNumber(a), jsval.ToNumber(b))
	return func(t float64) jsval.Value {
		x := c.at(t)
		return jsval.Num(x - float64(360*math.Floor(x/360)))
	}
}

// InterpolateArray is d3.interpolateArray for generic arrays: pairs of elements
// are interpolated with InterpolateValue, and extra elements of b are copied.
// Unlike upstream, each call returns a fresh array instead of a shared,
// mutated one.
func InterpolateArray(a, b jsval.Value) func(float64) jsval.Value {
	bi := b.Items()
	nb := len(bi)
	na := 0
	if a.IsArr() {
		na = min(nb, a.Len())
	}
	x := make([]func(float64) jsval.Value, na)
	for i := 0; i < na; i++ {
		x[i] = InterpolateValue(a.Index(i), bi[i])
	}
	return func(t float64) jsval.Value {
		c := make([]jsval.Value, nb)
		copy(c, bi)
		for i := 0; i < na; i++ {
			c[i] = x[i](t)
		}
		return jsval.Arr(c)
	}
}

// InterpolateObject is d3.interpolateObject. Keys of b absent from a keep b's
// value and come first in the result, followed by the interpolated keys (the
// order d3 builds its result object in).
func InterpolateObject(a, b jsval.Value) func(float64) jsval.Value {
	ao, bo := a.ObjValue(), b.ObjValue()
	type entry struct {
		key string
		f   func(float64) jsval.Value
	}
	var interp []entry
	consts := jsval.NewObject(0)
	if bo != nil {
		for i := 0; i < bo.Len(); i++ {
			k := bo.KeyAt(i)
			if ao != nil && ao.Has(k) {
				interp = append(interp, entry{k, InterpolateValue(ao.Lookup(k), bo.ValueAt(i))})
			} else {
				consts.Set(k, bo.ValueAt(i))
			}
		}
	}
	return func(t float64) jsval.Value {
		c := consts.Clone()
		for _, e := range interp {
			c.Set(e.key, e.f(t))
		}
		return jsval.Obj(c)
	}
}

// InterpolateValue is d3.interpolate: it picks an interpolator from the type of
// the end value b.
func InterpolateValue(a, b jsval.Value) func(float64) jsval.Value {
	switch b.Kind() {
	case jsval.KindNull, jsval.KindUndefined, jsval.KindBool:
		return func(float64) jsval.Value { return b }
	case jsval.KindNum:
		return InterpolateNumber(a, b)
	case jsval.KindStr:
		if _, ok := color.Parse(b.StrValue()); ok {
			return InterpolateRGB(a, b)
		}
		return InterpolateString(a, b)
	case jsval.KindTimestamp:
		return InterpolateDate(a, b)
	case jsval.KindArr:
		return InterpolateArray(a, b)
	}
	return InterpolateObject(a, b)
}

// numberAt finds the leftmost match of d3-interpolate's number regexp
// /[-+]?(?:\d+\.?\d*|\.?\d+)(?:[eE][-+]?\d+)?/ in s at or after from.
func numberAt(s string, from int) (start, end int, ok bool) {
	isDigit := func(i int) bool { return i < len(s) && s[i] >= '0' && s[i] <= '9' }
	for i := from; i < len(s); i++ {
		j := i
		if s[j] == '+' || s[j] == '-' {
			j++
		}
		switch {
		case isDigit(j):
			for isDigit(j) {
				j++
			}
			if j < len(s) && s[j] == '.' {
				j++
				for isDigit(j) {
					j++
				}
			}
		case j < len(s) && s[j] == '.' && isDigit(j+1):
			j++
			for isDigit(j) {
				j++
			}
		default:
			continue
		}
		if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
			k := j + 1
			if k < len(s) && (s[k] == '+' || s[k] == '-') {
				k++
			}
			if isDigit(k) {
				for isDigit(k) {
					k++
				}
				j = k
			}
		}
		return i, j, true
	}
	return 0, 0, false
}

// InterpolateString is d3.interpolateString: numbers embedded in b are
// interpolated with the numbers found at the same positions in a; the
// non-numeric text comes from b.
func InterpolateString(a, b jsval.Value) func(float64) jsval.Value {
	as, bs := a.AsString(), b.AsString()
	type slot struct{ x, y float64 }
	var (
		parts []string // constants; "" placeholder for interpolated slots
		isNum []bool
		qs    []slot
		bi    int
		ai    int
	)
	appendConst := func(str string) {
		if n := len(parts); n > 0 && !isNum[n-1] && parts[n-1] != "" {
			parts[n-1] += str
			return
		}
		parts = append(parts, str)
		isNum = append(isNum, false)
	}
	for {
		as0, ae, ok := numberAt(as, ai)
		if !ok {
			break
		}
		bs0, be, ok := numberAt(bs, bi)
		if !ok {
			break
		}
		ai = ae
		if bs0 > bi {
			appendConst(bs[bi:bs0])
		}
		am, bm := as[as0:ae], bs[bs0:be]
		if am == bm {
			appendConst(bm)
		} else {
			parts = append(parts, "")
			isNum = append(isNum, true)
			qs = append(qs, slot{jsval.StringToNumber(am), jsval.StringToNumber(bm)})
		}
		bi = be
	}
	if bi < len(bs) {
		appendConst(bs[bi:])
	}
	if len(parts) < 2 {
		if len(qs) > 0 {
			q := qs[0]
			return func(t float64) jsval.Value { return jsval.Str(jsval.JSNumberString(lerp(q.x, q.y, t))) }
		}
		return func(float64) jsval.Value { return jsval.Str(bs) }
	}
	return func(t float64) jsval.Value {
		buf := make([]byte, 0, len(bs)+8*len(qs))
		qi := 0
		for i, p := range parts {
			if isNum[i] {
				q := qs[qi]
				qi++
				buf = jsval.AppendJSNumber(buf, lerp(q.x, q.y, t))
			} else {
				buf = append(buf, p...)
			}
		}
		return jsval.Str(string(buf))
	}
}

// Basis is d3.interpolateBasis: a uniform cubic B-spline through values.
func Basis(values []float64) func(t float64) float64 {
	n := len(values) - 1
	return func(t float64) float64 {
		if t != t || n < 0 {
			return math.NaN()
		}
		var i int
		switch {
		case t <= 0:
			t, i = 0, 0
		case t >= 1:
			t, i = 1, n-1
		default:
			i = int(math.Floor(t * float64(n)))
		}
		v1 := valueAt(values, i)
		v2 := valueAt(values, i+1)
		v0 := 2*v1 - v2
		if i > 0 {
			v0 = valueAt(values, i-1)
		}
		v3 := 2*v2 - v1
		if i < n-1 {
			v3 = valueAt(values, i+2)
		}
		return basisSegment((t-float64(i)/float64(n))*float64(n), v0, v1, v2, v3)
	}
}

// valueAt is values[i] with JavaScript's out-of-range result (NaN here).
func valueAt(values []float64, i int) float64 {
	if i < 0 || i >= len(values) {
		return math.NaN()
	}
	return values[i]
}

func basisSegment(t1, v0, v1, v2, v3 float64) float64 {
	t2 := float64(t1 * t1)
	t3 := float64(t2 * t1)
	// every inner product is rounded before it is added, as in JavaScript
	return (float64((1-float64(3*t1)+float64(3*t2)-t3)*v0) +
		float64((4-float64(6*t2)+float64(3*t3))*v1) +
		float64((1+float64(3*t1)+float64(3*t2)-float64(3*t3))*v2) +
		float64(t3*v3)) / 6
}

// BasisClosed is d3.interpolateBasisClosed: a periodic cubic B-spline.
func BasisClosed(values []float64) func(t float64) float64 {
	n := len(values)
	return func(t float64) float64 {
		if n == 0 || t != t || math.IsInf(t, 0) {
			return math.NaN()
		}
		t = math.Mod(t, 1)
		if t < 0 {
			t++
		}
		i := int(math.Floor(t * float64(n)))
		v0 := values[(i+n-1)%n]
		v1 := values[i%n]
		v2 := values[(i+1)%n]
		v3 := values[(i+2)%n]
		return basisSegment((t-float64(i)/float64(n))*float64(n), v0, v1, v2, v3)
	}
}

func rgbSpline(spline func([]float64) func(float64) float64) func(colors []jsval.Value) func(float64) jsval.Value {
	return func(colors []jsval.Value) func(float64) jsval.Value {
		n := len(colors)
		r, g, b := make([]float64, n), make([]float64, n), make([]float64, n)
		for i, c := range colors {
			p := color.ParseRGB(colorString(c))
			r[i], g[i], b[i] = nz(p.R), nz(p.G), nz(p.B)
		}
		fr, fg, fb := spline(r), spline(g), spline(b)
		return func(t float64) jsval.Value {
			return jsval.Str(color.RGB{R: fr(t), G: fg(t), B: fb(t), Opacity: 1}.FormatRgb())
		}
	}
}

// nz is JavaScript's `x || 0`.
func nz(x float64) float64 {
	if x != x {
		return 0
	}
	return x
}

// RGBBasis is d3.interpolateRgbBasis; RGBBasisClosed is interpolateRgbBasisClosed.
var (
	RGBBasis       = rgbSpline(Basis)
	RGBBasisClosed = rgbSpline(BasisClosed)
)

// Discrete is d3.interpolateDiscrete: t in [0,1) picks one of the values.
func Discrete(values []jsval.Value) func(t float64) jsval.Value {
	n := len(values)
	return func(t float64) jsval.Value {
		if n == 0 {
			return jsval.Undefined
		}
		f := math.Floor(t * float64(n))
		if f != f {
			return jsval.Undefined // range[NaN]
		}
		i := int(math.Max(0, math.Min(float64(n-1), f)))
		return values[i]
	}
}

// Piecewise is d3.piecewise: interpolates through the values with one
// interpolator per consecutive pair. A nil interpolate means InterpolateValue.
func Piecewise(interpolate Interpolator, values []jsval.Value) func(t float64) jsval.Value {
	if interpolate == nil {
		interpolate = InterpolateValue
	}
	n := len(values) - 1
	if n < 0 {
		n = 0
	}
	segs := make([]func(float64) jsval.Value, n)
	for i := 0; i < n; i++ {
		segs[i] = interpolate(values[i], values[i+1])
	}
	return func(t float64) jsval.Value {
		if n == 0 {
			// d3 would call I[0], which is undefined: a TypeError. The nearest
			// harmless answer is the single value, or undefined.
			if len(values) == 1 {
				return values[0]
			}
			return jsval.Undefined
		}
		t = float64(t * float64(n)) // rounded here so t-i below cannot fuse into an FMA
		f := math.Floor(t)
		if f != f {
			return segs[0](math.NaN())
		}
		i := int(math.Max(0, math.Min(float64(n-1), f)))
		return segs[i](t - float64(i))
	}
}

// QuantizeSamples is d3.quantize: n evenly spaced samples of interpolator over [0,1].
// n is bounded so a specification cannot request an unbounded sample.
func QuantizeSamples(interpolator func(float64) jsval.Value, n int) []jsval.Value {
	if n < 0 || n > maxSamples {
		return nil
	}
	samples := make([]jsval.Value, n)
	for i := range samples {
		samples[i] = interpolator(float64(i) / float64(n-1))
	}
	return samples
}

const maxSamples = 1 << 20

// Zoom is d3.interpolateZoom.rho(rho): it interpolates between two views
// [cx, cy, width] and also reports the suggested duration in milliseconds.
func Zoom(rho float64) func(p0, p1 [3]float64) (interp func(t float64) [3]float64, duration float64) {
	rho = math.Max(1e-3, rho)
	rho2 := rho * rho
	return zoomRho(rho, rho2, rho2*rho2)
}

// DefaultZoom is d3.interpolateZoom itself. It is not Zoom(math.Sqrt2): upstream builds it with the
// exact squares 2 and 4, where rho(Math.SQRT2) squares the rounded root (2.0000000000000004).
func DefaultZoom() func(p0, p1 [3]float64) (interp func(t float64) [3]float64, duration float64) {
	return zoomRho(math.Sqrt2, 2, 4)
}

func zoomRho(rho, rho2, rho4 float64) func(p0, p1 [3]float64) (interp func(t float64) [3]float64, duration float64) {
	const epsilon2 = 1e-12
	cosh := func(x float64) float64 { x = jsmath.Exp(x); return (x + 1/x) / 2 }
	sinh := func(x float64) float64 { x = jsmath.Exp(x); return (x - 1/x) / 2 }
	tanh := func(x float64) float64 { x = jsmath.Exp(2 * x); return (x - 1) / (x + 1) }
	return func(p0, p1 [3]float64) (func(float64) [3]float64, float64) {
		ux0, uy0, w0 := p0[0], p0[1], p0[2]
		ux1, uy1, w1 := p1[0], p1[1], p1[2]
		dx, dy := ux1-ux0, uy1-uy0
		d2 := float64(dx*dx) + float64(dy*dy)
		var S float64
		var f func(float64) [3]float64
		if d2 < epsilon2 {
			S = jsmath.Log(w1/w0) / rho
			f = func(t float64) [3]float64 {
				return [3]float64{ux0 + float64(t*dx), uy0 + float64(t*dy), w0 * jsmath.Exp(float64(rho*t)*S)}
			}
		} else {
			d1 := math.Sqrt(d2)
			b0 := (float64(w1*w1) - float64(w0*w0) + float64(rho4*d2)) / (2 * w0 * rho2 * d1)
			b1 := (float64(w1*w1) - float64(w0*w0) - float64(rho4*d2)) / (2 * w1 * rho2 * d1)
			r0 := jsmath.Log(math.Sqrt(float64(b0*b0)+1) - b0)
			r1 := jsmath.Log(math.Sqrt(float64(b1*b1)+1) - b1)
			S = (r1 - r0) / rho
			f = func(t float64) [3]float64 {
				s := t * S
				coshr0 := cosh(r0)
				u := w0 / (rho2 * d1) * (float64(coshr0*tanh(float64(rho*s)+r0)) - sinh(r0))
				return [3]float64{ux0 + float64(u*dx), uy0 + float64(u*dy), w0 * coshr0 / cosh(float64(rho*s)+r0)}
			}
		}
		return f, S * 1000 * rho / math.Sqrt2
	}
}
