package scale

import (
	"math"
	"strconv"
	"unsafe"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale/color"
)

// numeric adapts an interpolator of numbers or dates, which walks nothing,
// to the Interpolator shape.
func numeric(f func(a, b jsval.Value) func(float64) jsval.Value) Interpolator {
	return func(_ jsval.Walker, a, b jsval.Value) func(float64) jsval.Value { return f(a, b) }
}

// The interpolators of numbers and dates as Interpolators.
var (
	InterpolateNumberI = numeric(InterpolateNumber)
	InterpolateRoundI  = numeric(InterpolateRound)
	InterpolateDateI   = numeric(InterpolateDate)
)

// Interpolator is d3's `interpolate(a, b)` shape: given a start and end value
// it returns a function of t (normally in [0, 1]) that yields the value in
// between. Colour interpolators produce colour strings ("rgb(...)"), exactly
// as d3 does, because that is what reaches the scenegraph. w is the call's
// Walker: a colour read from an array is its String, and d3.interpolate of
// arrays and objects goes element by element, walks of values a run can
// have built, which w bounds (ADR 0018).
type Interpolator func(w jsval.Walker, a, b jsval.Value) func(t float64) jsval.Value

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
		return jsmath.Pow(c.a+float64(t*c.d), c.y)
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
			d -= float64(360 * jsmath.Round(d/360))
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
		pa := jsmath.Pow(a, y)
		return channel{kind: chExp, a: pa, d: jsmath.Pow(b, y) - pa, y: 1 / y}
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
	return func(t float64) jsval.Value { return jsval.Num(jsmath.Round(lerp(x, y, t))) }
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

// colorString is the text d3.color reads a value as: `format + ""`.
func colorString(w jsval.Walker, v jsval.Value) string {
	if v.IsStr() {
		return v.StrValue()
	}
	return w.String(v)
}

// RGBGamma returns d3.interpolateRgb.gamma(y): rgb interpolation with a gamma
// correction applied to the colour channels (never to opacity).
func RGBGamma(y float64) Interpolator {
	return func(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseRGB(colorString(w, a))
		end := color.ParseRGB(colorString(w, b))
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
func InterpolateRGB(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	return RGBGamma(1)(w, a, b)
}

func hslInterp(long bool) Interpolator {
	return func(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseHSL(colorString(w, a))
		end := color.ParseHSL(colorString(w, b))
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
func InterpolateLab(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	start := color.ParseLab(colorString(w, a))
	end := color.ParseLab(colorString(w, b))
	l := linearChannel(start.L, end.L)
	ca := linearChannel(start.A, end.A)
	cb := linearChannel(start.B, end.B)
	o := linearChannel(start.Opacity, end.Opacity)
	return func(t float64) jsval.Value {
		return jsval.Str(color.Lab{L: l.at(t), A: ca.at(t), B: cb.at(t), Opacity: o.at(t)}.String())
	}
}

func hclInterp(long bool) Interpolator {
	return func(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseHCL(colorString(w, a))
		end := color.ParseHCL(colorString(w, b))
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
	return func(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
		start := color.ParseCubehelix(colorString(w, a))
		end := color.ParseCubehelix(colorString(w, b))
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
			return jsval.Str(color.Cubehelix{H: h.at(t), S: s.at(t), L: l.at(jsmath.Pow(t, y)), Opacity: o.at(t)}.String())
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
func InterpolateHue(_ jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	c := hueChannel(jsval.ToNumber(a), jsval.ToNumber(b))
	return func(t float64) jsval.Value {
		x := c.at(t)
		return jsval.Num(x - float64(360*math.Floor(x/360)))
	}
}

// InterpolateArray is d3.interpolateArray for generic arrays (genericArray):
// pairs of elements are interpolated with InterpolateValue, and the elements
// of b past a's are copied. Like upstream, every call answers the same array,
// updated in place: values taken from the scale earlier change with it.
func InterpolateArray(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	m := w.Meter()
	defer m.Done()
	return interpolateArray(w, &m, a, b, 0)
}

// The elementwise interpolators are built by a walk of the end values, a
// step a value (m), and each evaluation updates the array or object a level
// at a time, charging its steps to w: a range whose arrays share their parts
// stands for far more elements than it holds, and upstream updates them all
// on every call. What the result holds is charged once, when it is made.

// errArrayLength is the RangeError of new Array(n) for an n that is not a
// length.
func errArrayLength() *Thrown { return &Thrown{Name: "RangeError", Msg: "Invalid array length"} }

func interpolateArray(w jsval.Walker, m *jsval.WalkMeter, a, b jsval.Value, depth int) func(float64) jsval.Value {
	// d3's genericArray: nb = b ? b.length : 0, na = a ? Math.min(nb,
	// a.length) : 0, x = new Array(na), c = new Array(nb); the ends are read
	// as JavaScript reads them, a string by its characters, an object by its
	// length and indexes, a number as having no length.
	nb := jsval.Num(0)
	if b.IsTruthy() {
		nb = lengthValue(b)
	}
	na := 0.0
	if a.IsTruthy() {
		x, y := jsval.ToNumber(nb), jsval.ToNumber(lengthValue(a))
		if x != x || y != y {
			na = math.NaN()
		} else {
			na = math.Min(x, y)
		}
	}
	if !validArrayLength(na) {
		panic(errArrayLength())
	}
	// new Array(nb): a number is a length, anything else the one element.
	var c0 []jsval.Value
	if nb.IsNum() {
		if !validArrayLength(nb.NumValue()) {
			panic(errArrayLength())
		}
		w.Work(int64(nb.NumValue()))
		w.Keep(int64(nb.NumValue()) * valueBytes)
		c0 = make([]jsval.Value, int(nb.NumValue()))
	} else {
		c0 = []jsval.Value{nb}
	}
	n := int(na)
	w.Work(int64(n))
	w.Keep(int64(n) * funcBytes)
	x := make([]func(float64) jsval.Value, n)
	for i := 0; i < n; i++ {
		x[i] = interpolateValue(w, m, member(a, i), member(b, i), depth+1)
	}
	// for (; i < nb; ++i) c[i] = b[i]
	if hi := jsval.ToNumber(nb); hi > float64(n) {
		if top := math.Min(math.Ceil(hi), 1<<32-1); top > float64(len(c0)) {
			// c grows to hold b's elements: charged before it does
			w.Work(int64(top))
			w.Keep(int64(top) * valueBytes)
			c0 = setAt(c0, int(top)-1, jsval.Undefined)
		}
		for i := n; float64(i) < hi && i < len(c0); i++ {
			c0[i] = member(b, i)
		}
	}
	if len(c0) < n {
		// The first c[i] = x[i](t) would grow c to n: it does so now, so that
		// the one array every call answers keeps its place.
		w.Work(int64(n))
		w.Keep(int64(n-len(c0)) * valueBytes)
		c0 = setAt(c0, n-1, jsval.Undefined)
	}
	c := c0
	cv := jsval.Arr(c)
	return func(t float64) jsval.Value {
		w.Work(int64(len(c)))
		for i := 0; i < n; i++ {
			c[i] = x[i](t)
		}
		return cv
	}
}

// lengthValue is v.length as JavaScript reads it: a string's UTF-16 units,
// an array's length, an object's member "length" (any value), and undefined
// for the rest.
func lengthValue(v jsval.Value) jsval.Value {
	switch {
	case v.IsStr():
		return jsval.Num(float64(jsval.UTF16Len(v.StrValue())))
	case v.IsArr():
		return jsval.Num(float64(v.Len()))
	case v.IsObj():
		return v.ObjValue().Lookup("length")
	}
	return jsval.Undefined
}

// member is v[i]: an array's element, a string's i-th UTF-16 unit, an
// object's member "i", undefined for the rest.
func member(v jsval.Value, i int) jsval.Value {
	if v.IsObj() {
		return v.ObjValue().Lookup(strconv.Itoa(i))
	}
	al, _ := jsval.ToArrayLike(v)
	return al.At(int64(i))
}

// validArrayLength is whether new Array(n) accepts the number n.
func validArrayLength(n float64) bool { return n >= 0 && n <= 1<<32-1 && n == math.Trunc(n) }

// setAt is c[i] = v, growing c with holes (undefined) as JavaScript does.
func setAt(c []jsval.Value, i int, v jsval.Value) []jsval.Value {
	for len(c) <= i {
		c = append(c, jsval.Undefined)
	}
	c[i] = v
	return c
}

// valueBytes is what an element of an array holds.
const valueBytes = int64(unsafe.Sizeof(jsval.Value{}))

// funcBytes is what an interpolator held in a slice takes.
const funcBytes = int64(unsafe.Sizeof(func() {}))

// InterpolateObject is d3.interpolateObject. Keys of b that a does not have
// (as `k in a` reads it: its own and its inherited members) keep b's value
// and come first in the result, followed by the interpolated keys (the order
// d3 builds its result object in). Like upstream, every call answers the
// same object, updated in place.
func InterpolateObject(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	m := w.Meter()
	defer m.Done()
	return interpolateObject(w, &m, a, b, 0)
}

// objectPrototype stands for Object.prototype where a member read answers
// it (`a.__proto__`): an object of no keys of its own.
var objectPrototype = jsval.Obj(jsval.NewObject(0))

func interpolateObject(w jsval.Walker, m *jsval.WalkMeter, a, b jsval.Value, depth int) func(float64) jsval.Value {
	// `if (a === null || typeof a !== "object") a = {}`: a function is not
	// an object either.
	if !isObjectValue(a) {
		a = jsval.Undefined
	}
	type entry struct {
		key string
		f   func(float64) jsval.Value
	}
	var interp []entry
	c := jsval.NewObject(0)
	// for (k in b): an object's own keys, an array's indexes.
	var bo *jsval.Object
	var items []jsval.Value
	switch {
	case b.IsObj():
		bo = b.ObjValue()
	case b.IsArr():
		items = b.Items()
	}
	nb := len(items)
	if bo != nil {
		nb = bo.Len()
	}
	for i := 0; i < nb; i++ {
		var k string
		var v jsval.Value
		if bo != nil {
			k, v = bo.KeyAt(i), bo.ValueAt(i)
		} else {
			k, v = strconv.Itoa(i), items[i]
		}
		if !inObject(a, k) {
			c.Set(k, v)
			continue
		}
		f := interpolateValue(w, m, memberOf(a, k), v, depth+1)
		// `i.__proto__ = f` sets i's prototype rather than a key, so
		// no __proto__ reaches the result.
		if k != "__proto__" {
			interp = append(interp, entry{k, f})
		}
	}
	n := int64(c.Len() + len(interp))
	w.Keep(n * memberBytes)
	cv := jsval.Obj(c)
	return func(t float64) jsval.Value {
		w.Work(n)
		for _, e := range interp {
			c.Set(e.key, e.f(t))
		}
		return cv
	}
}

// isObjectValue is `v !== null && typeof v === "object"`.
func isObjectValue(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindArr, jsval.KindTimestamp, jsval.KindPattern:
		return true
	case jsval.KindObj:
		return !jsval.IsFunction(v)
	}
	return false
}

// inObject is `k in a` for an object a, or for {} when a is undefined: its
// own keys, an array's indexes and length, and what it inherits (the
// prototype's methods, __proto__).
func inObject(a jsval.Value, k string) bool {
	if k == "__proto__" {
		return true
	}
	switch a.Kind() {
	case jsval.KindObj:
		if a.ObjValue().Has(k) {
			return true
		}
	case jsval.KindArr:
		if i, ok := jsval.ArrayIndex(k); ok {
			return int64(i) < int64(a.Len())
		}
		return !jsval.Member(a, k).IsUndefined()
	case jsval.KindUndefined:
		_, ok := jsval.Inherited(k)
		return ok
	}
	return !jsval.Member(a, k).IsUndefined()
}

// memberOf is a[k] for inObject's a.
func memberOf(a jsval.Value, k string) jsval.Value {
	if k == "__proto__" {
		return objectPrototype
	}
	if a.IsUndefined() {
		v, _ := jsval.Inherited(k)
		return v
	}
	return jsval.Member(a, k)
}

// memberBytes is what a member of an object holds: its key and its value.
const memberBytes = int64(unsafe.Sizeof("")) + valueBytes

// InterpolateValue is d3.interpolate: it picks an interpolator from the type of
// the end value b.
func InterpolateValue(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	m := w.Meter()
	defer m.Done()
	return interpolateValue(w, &m, a, b, 0)
}

// interpolateValue is InterpolateValue for values nested depth levels deep. A
// range can come from a signal whose value was built at run time, nested as
// deep as the specification is long: past jsval.MaxValueDepth the walk, and
// the call, stop with ErrLimit, as every walk of a value does.
func interpolateValue(w jsval.Walker, m *jsval.WalkMeter, a, b jsval.Value, depth int) func(float64) jsval.Value {
	m.Step(depth)
	switch b.Kind() {
	case jsval.KindNull, jsval.KindUndefined, jsval.KindBool:
		return func(float64) jsval.Value { return b }
	case jsval.KindNum:
		return InterpolateNumber(a, b)
	case jsval.KindStr:
		if _, ok := color.Parse(b.StrValue()); ok {
			return InterpolateRGB(w, a, b)
		}
		return InterpolateString(w, a, b)
	case jsval.KindTimestamp:
		return InterpolateDate(a, b)
	case jsval.KindArr:
		return interpolateArray(w, m, a, b, depth)
	case jsval.KindObj:
		// `typeof b.valueOf !== "function" && typeof b.toString !==
		// "function" || isNaN(b)`: isNaN reads b as a number, which takes
		// its toString when its valueOf answers an object, and an own
		// toString that is not a function cannot.
		if o := b.ObjValue(); o.Has("toString") && !jsval.IsFunction(o.Lookup("toString")) &&
			(!o.Has("valueOf") || jsval.IsFunction(o.Lookup("valueOf"))) {
			panic(&Thrown{Name: "TypeError", Msg: "Cannot convert object to primitive value"})
		}
	}
	return interpolateObject(w, m, a, b, depth)
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
func InterpolateString(w jsval.Walker, a, b jsval.Value) func(float64) jsval.Value {
	as, bs := colorString(w, a), colorString(w, b) // a + "", b + ""
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
	return basisOver(int64(len(values)), func(i int64) float64 { return valueAt(values, int(i)) })
}

// basisOver is d3.interpolateBasis over n values that at reads (NaN for an
// index outside them, as an undefined element is NaN to d3's arithmetic).
func basisOver(n1 int64, at func(int64) float64) func(t float64) float64 {
	n := n1 - 1
	return func(t float64) float64 {
		if t != t || n < 0 {
			return math.NaN()
		}
		var i int64
		switch {
		case t <= 0:
			t, i = 0, 0
		case t >= 1:
			t, i = 1, n-1
		default:
			i = int64(math.Floor(t * float64(n)))
		}
		v1 := at(i)
		v2 := at(i + 1)
		v0 := 2*v1 - v2
		if i > 0 {
			v0 = at(i - 1)
		}
		v3 := 2*v2 - v1
		if i < n-1 {
			v3 = at(i + 2)
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
	return basisClosedOver(int64(len(values)), func(i int64) float64 { return values[i] })
}

// basisClosedOver is d3.interpolateBasisClosed over n values that at reads.
func basisClosedOver(n int64, at func(int64) float64) func(t float64) float64 {
	return func(t float64) float64 {
		if n == 0 || t != t || math.IsInf(t, 0) {
			return math.NaN()
		}
		t = math.Mod(t, 1)
		if t < 0 {
			t++
		}
		i := int64(math.Floor(t * float64(n)))
		v0 := at((i + n - 1) % n)
		v1 := at(i % n)
		v2 := at((i + 1) % n)
		v3 := at((i + 2) % n)
		return basisSegment((t-float64(i)/float64(n))*float64(n), v0, v1, v2, v3)
	}
}

func rgbSpline(spline func([]float64) func(float64) float64) func(w jsval.Walker, colors []jsval.Value) func(float64) jsval.Value {
	return func(w jsval.Walker, colors []jsval.Value) func(float64) jsval.Value {
		n := len(colors)
		r, g, b := make([]float64, n), make([]float64, n), make([]float64, n)
		for i, c := range colors {
			p := color.ParseRGB(colorString(w, c))
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

// discreteOver is d3.interpolateDiscrete over n values that at reads.
func discreteOver(n int64, at func(int64) jsval.Value) func(t float64) jsval.Value {
	return func(t float64) jsval.Value {
		if n == 0 {
			return jsval.Undefined
		}
		f := math.Floor(t * float64(n))
		if f != f {
			return jsval.Undefined // range[NaN]
		}
		return at(int64(math.Max(0, math.Min(float64(n-1), f))))
	}
}

// Thrown is the panic value of a JavaScript exception thrown inside a scale
// function, whose Apply methods cannot return an error (see jsval.Thrown).
type Thrown = jsval.Thrown

// Piecewise is d3.piecewise: interpolates through the values with one
// interpolator per consecutive pair. A nil interpolate means InterpolateValue.
func Piecewise(w jsval.Walker, interpolate Interpolator, values []jsval.Value) func(t float64) jsval.Value {
	if interpolate == nil {
		interpolate = InterpolateValue
	}
	n := len(values) - 1
	if n < 0 {
		n = 0
	}
	segs := make([]func(float64) jsval.Value, n)
	for i := 0; i < n; i++ {
		segs[i] = interpolate(w, values[i], values[i+1])
	}
	return func(t float64) jsval.Value {
		// `I[Math.max(0, Math.min(n - 1, Math.floor(t *= n)))](t - i)`: a NaN
		// position indexes I[NaN], and a piecewise through fewer than two
		// values has no I[0]; either way the call is on undefined.
		t = float64(t * float64(n)) // rounded here so t-i below cannot fuse into an FMA
		f := math.Floor(t)
		if n == 0 || f != f {
			panic(&Thrown{Name: "TypeError", Msg: "I[i] is not a function"})
		}
		i := int(math.Max(0, math.Min(float64(n-1), f)))
		return segs[i](t - float64(i))
	}
}

// Zoom is d3.interpolateZoom.rho(rho): it interpolates between two views
// [cx, cy, width] and also reports the suggested duration in milliseconds.
func Zoom(rho float64) func(p0, p1 [3]float64) (interp func(t float64) [3]float64, duration float64) {
	rho = math.Max(1e-3, rho)
	rho2 := rho * rho
	return absolute(zoomRho(rho, rho2, rho2*rho2))
}

// zoomFunc returns, for t, the move from the start view [ox, oy] and the
// width: d3 writes the view as [ux0 + ox, uy0 + oy, w], and the + is
// JavaScript's (InterpolateZoom keeps it for starts that are not numbers).
type zoomFunc func(p0, p1 [3]float64) (move func(t float64) [3]float64, duration float64)

// absolute adds the start back as numbers.
func absolute(z zoomFunc) func(p0, p1 [3]float64) (func(float64) [3]float64, float64) {
	return func(p0, p1 [3]float64) (func(float64) [3]float64, float64) {
		f, d := z(p0, p1)
		return func(t float64) [3]float64 {
			m := f(t)
			return [3]float64{p0[0] + m[0], p0[1] + m[1], m[2]}
		}, d
	}
}

func zoomRho(rho, rho2, rho4 float64) zoomFunc {
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
				return [3]float64{float64(t * dx), float64(t * dy), w0 * jsmath.Exp(float64(rho*t)*S)}
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
				return [3]float64{float64(u * dx), float64(u * dy), w0 * coshr0 / cosh(float64(rho*s)+r0)}
			}
		}
		return f, S * 1000 * rho / math.Sqrt2
	}
}
