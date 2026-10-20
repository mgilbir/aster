package vega

import (
	"math"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

func init() {
	transformFactories["scale"] = facScale
	transformFactories["projection"] = facProjection
}

// scaleSkip are the parameters the Scale operator does not pass to the scale
// as property setters: they are consumed by the configuration steps below.
var scaleSkip = map[string]bool{
	"set": true, "modified": true, "clear": true, "type": true, "scheme": true,
	"schemeExtent": true, "schemeCount": true, "domain": true, "domainMin": true,
	"domainMid": true, "domainMax": true, "domainRaw": true, "domainImplicit": true,
	"nice": true, "zero": true, "bins": true, "range": true, "rangeStep": true,
	"round": true, "reverse": true, "interpolate": true, "interpolateGamma": true,
}

func includeZero(s scale.Scale) bool {
	if t, ok := s.(scale.Typed); ok && t.Bins() != nil {
		return false
	}
	switch s.Type() {
	case scale.TypeLinear, scale.TypePow, scale.TypeSqrt:
		return true
	}
	return false
}

func includePad(typ string) bool { return scale.IsContinuous(typ) && typ != scale.TypeSequential }

// valueList reads a parameter that holds a list of values: an array value or
// a list of resolved parameters.
func valueList(x any) ([]jsval.Value, bool) {
	switch v := x.(type) {
	case []any:
		out := make([]jsval.Value, len(v))
		for i, e := range v {
			out[i] = toValue(e)
		}
		return out, true
	case jsval.Value:
		if v.IsArr() {
			return v.Items(), true
		}
	}
	return nil, false
}

func facScale(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	n.modified = true // always treated as modified
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		g := n.g
		s, _ := n.value.(scale.Scale)
		key := scaleKey(p)
		if s == nil || key != s.Type() {
			ns, ok := scale.NewIn(key, c.view.zone)
			if !ok {
				fail("unrecognized scale type: %s", key)
			}
			// The scale reads values as text and interpolates them
			// element by element: walks the call bounds (ADR 0018).
			if w, ok := ns.(interface{ SetWalker(jsval.Walker) }); ok {
				w.SetWalker(g.view.walk)
			}
			// The loops of an ordinal, band or point scale over its domain
			// (indexing each value, placing each band) charge the graph:
			// two steps a value.
			if w, ok := ns.(interface{ SetWork(func(int)) }); ok {
				w.SetWork(func(k int) { g.work(2 * k) })
			}
			s = ns
			n.value = s
		}
		for i := 0; i < p.count(); i++ {
			name := p.nameAt(i)
			if scaleSkipped(name) {
				continue
			}
			if name == "padding" && includePad(s.Type()) {
				continue
			}
			if !scaleProperty(g, s, name, scaleArg(p, name)) {
				g.warn("Unsupported scale property: " + name)
			}
		}
		count := configureDomain(s, p, g)
		count = configureBins(s, p, count, g)
		configureRange(g, s, p, count)
		return barePulse(pulse)
	}), nil
}

func zoomLinear(d0, d1, k float64) [2]float64 {
	da := (d0 + d1) / 2
	return [2]float64{da + float64((d0-da)*k), da + float64((d1-da)*k)}
}

func zoomLog(lo, hi, k float64) [2]float64 {
	sign := jsmath.Sign(lo)
	lift := func(x float64) float64 { return jsmath.Log(sign * x) }
	ground := func(x float64) float64 { return sign * jsmath.Exp(x) }
	d0, d1 := lift(lo), lift(hi)
	da := (d0 + d1) / 2
	return [2]float64{ground(da + float64((d0-da)*k)), ground(da + float64((d1-da)*k))}
}

func powT(exponent float64) func(float64) float64 {
	return func(x float64) float64 {
		if x < 0 {
			return -jsmath.Pow(-x, exponent)
		}
		return jsmath.Pow(x, exponent)
	}
}

func zoomPow(lo, hi, k, exponent float64) [2]float64 {
	lift, ground := powT(exponent), powT(1/exponent)
	d0, d1 := lift(lo), lift(hi)
	da := (d0 + d1) / 2
	return [2]float64{ground(da + float64((d0-da)*k)), ground(da + float64((d1-da)*k))}
}

func zoomSymlog(lo, hi, k, c float64) [2]float64 {
	lift := func(x float64) float64 { return jsmath.Sign(x) * jsmath.Log1p(math.Abs(x/c)) }
	ground := func(x float64) float64 { return float64(jsmath.Sign(x)*jsmath.Expm1(math.Abs(x))) * c }
	d0, d1 := lift(lo), lift(hi)
	da := (d0 + d1) / 2
	return [2]float64{ground(da + float64((d0-da)*k)), ground(da + float64((d1-da)*k))}
}

func numbers(v []jsval.Value) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = jsval.ToNumber(x)
	}
	return out
}

func numsToValues(f []float64) []jsval.Value {
	out := make([]jsval.Value, len(f))
	for i, x := range f {
		out[i] = jsval.Num(x)
	}
	return out
}

// maxBinValues bounds the bin boundaries a scale may generate; real charts
// use a few dozen.
const maxBinValues = 1_000_000

// d3Range is d3.range(start, stop, step). Its length is
// `Math.max(0, Math.ceil((stop - start) / step)) | 0`: wrapped to 32 bits, so
// a count that wraps negative is `new Array(n)`'s RangeError, while a count
// that wraps to a small positive one yields that many values.
func d3Range(start, stop, step float64) []float64 {
	n := float64(jsmath.ToInt32(math.Max(0, math.Ceil((stop-start)/step))))
	if n < 0 {
		fail("Invalid array length")
	}
	if n > maxBinValues {
		failLimit("scale bins exceed %d values", maxBinValues)
	}
	out := make([]float64, int(n))
	for i := range out {
		out[i] = start + float64(float64(i)*step)
	}
	return out
}

func flip(a []jsval.Value, reverse bool) []jsval.Value {
	if !reverse {
		return a
	}
	out := make([]jsval.Value, len(a))
	for i, x := range a {
		out[len(a)-1-i] = x
	}
	return out
}

// -- projection ----------------------------------------------------------------

func facProjection(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	n.modified = true
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		// projections['constructor'] is Object, and Object() makes a plain
		// object: no projection (not a registered scale, no path), which only a
		// pointRadius or a fit with a size or extent can fault on.
		if tv, _ := paramValueOf(p.Get("type")); tv.IsStr() && jsval.JSLower(tv.StrValue()) == "constructor" {
			if r := p.Value("pointRadius"); !r.IsNullish() {
				fail("TypeError: Cannot read properties of undefined (reading 'pointRadius')")
			}
			if p.Value("fit").IsTruthy() && (p.Value("extent").IsTruthy() || p.Value("size").IsTruthy()) {
				fail("TypeError: proj.fitExtent is not a function")
			}
			n.value = nil
			return barePulse(pulse)
		}
		params := jsval.NewObject(p.vals.len())
		for i := 0; i < p.count(); i++ {
			name := p.nameAt(i)
			params.Set(name, projectionParam(p.Get(name)))
		}
		cur, _ := n.value.(geo.Projection)
		proj, err := geo.ConfigureProjection(n.g.ctx, c.view.projections, cur, params, func(name string) bool { return p.Modified(name) })
		if err != nil {
			failErr(err)
		}
		n.value = proj
		return barePulse(pulse)
	}), nil
}

// projectionParam converts a resolved parameter to the value the geo package
// reads: lists of references become arrays.
func projectionParam(x any) jsval.Value {
	switch v := x.(type) {
	case jsval.Value:
		return v
	case []any:
		out := make([]jsval.Value, len(v))
		for i, e := range v {
			out[i] = projectionParam(e)
		}
		return jsval.Arr(out)
	}
	return jsval.Undefined
}

// -- scale access for expressions and encoders ----------------------------------

// scaleOf resolves a scale reference (a name) to the scale in this context.
func (c *rtContext) scaleOf(ref jsval.Value) scale.Scale {
	if !ref.IsStr() {
		return nil
	}
	n := c.scaleNode(ref.StrValue())
	if n == nil {
		return nil
	}
	s, _ := n.value.(scale.Scale)
	return s
}

func (c *rtContext) applyScale(ref, v jsval.Value) jsval.Value {
	defer func() {
		// an exception of the scale is an exception of the expression
		if r := recover(); r != nil {
			if e, ok := r.(*scale.Thrown); ok {
				panic(&expr.Error{Name: e.Name, Msg: e.Msg})
			}
			panic(r)
		}
	}()
	s := c.scaleOf(ref)
	if s == nil {
		if p, ok := c.projectionOf(ref); ok {
			return projectPoint(p, v)
		}
		return jsval.Undefined
	}
	return s.Apply(v)
}

// projectPoint applies a projection to a [longitude, latitude] pair; a position
// the projection has no location for gives undefined.
func projectPoint(p geo.Projection, v jsval.Value) jsval.Value {
	x, y, ok := p.Forward(jsval.ToNumber(v.Index(0)), jsval.ToNumber(v.Index(1)))
	if !ok {
		return jsval.Undefined
	}
	return jsval.ArrOf(jsval.Num(x), jsval.Num(y))
}

// hasScaleRef reports whether ref names a scale or a projection the context
// can see. The encoders' scale parameters exist only for scales known when the
// specification was parsed, and the generated code calls them unguarded.
func (c *rtContext) hasScaleRef(ref jsval.Value) bool {
	if !ref.IsStr() {
		return false
	}
	if c.scaleNode(ref.StrValue()) != nil {
		return true
	}
	_, ok := c.projectionOf(ref)
	return ok
}

// encName is the key `_["%" + ref]` looks a scale up by (scales.js writes the
// reference as that concatenation): the text of any value, not only a
// string's. A scale is found by it whatever the type of the reference (a scale
// named "1" by the number 1).
func (c *rtContext) encName(ref jsval.Value) jsval.Value {
	if ref.IsStr() {
		return ref
	}
	return jsval.Str(c.view.jsString(ref))
}

// encScale is the encoders' _scale(name, value), `_["%name"](value)`: with no
// such scale that is a call of undefined.
func (c *rtContext) encScale(ref, v jsval.Value) jsval.Value {
	ref = c.encName(ref)
	if !c.hasScaleRef(ref) {
		fail("_.%%%s is not a function", jsval.StringPrefix(ref, jsval.MessageBytes))
	}
	return c.applyScale(ref, v)
}

// encScaleRange is the encoders' _range(name), `_["%name"].range()`.
func (c *rtContext) encScaleRange(ref jsval.Value) jsval.Value {
	ref = c.encName(ref)
	if !c.hasScaleRef(ref) {
		fail("Cannot read properties of undefined (reading 'range')")
	}
	if c.scaleOf(ref) == nil {
		// a projection, which has no range method
		fail("TypeError: _.%%%s.range is not a function", jsval.StringPrefix(ref, jsval.MessageBytes))
	}
	return c.scaleRange(ref)
}

// EncodeScale and EncodeRange implement expr.EncodeScaleProvider: the encoders'
// helpers as an expression compiled from a value reference calls them.
func (c *rtContext) EncodeScale(ref, v jsval.Value) jsval.Value { return c.encScale(ref, v) }

func (c *rtContext) EncodeRange(ref jsval.Value) jsval.Value { return c.encScaleRange(ref) }

func (c *rtContext) EncodeBandwidth(ref jsval.Value) jsval.Value {
	return jsval.Num(c.scaleBandwidth(c.encName(ref)))
}

func (c *rtContext) scaleRange(ref jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		return jsval.Arr(nil)
	}
	// A copy, as d3's range() is, charged as domain()'s is.
	r := s.Range()
	c.view.chargeItems(len(r))
	return jsval.Arr(r)
}

func (c *rtContext) scaleBandwidth(ref jsval.Value) float64 {
	s := c.scaleOf(ref)
	if b, ok := s.(scale.Bander); ok {
		return b.Bandwidth()
	}
	return 0
}

// Scale implements expr.ScaleProvider.
func (c *rtContext) Scale(ref, v, group jsval.Value) jsval.Value {
	return c.applyScale(ref, v)
}

func (c *rtContext) Invert(ref, v, group jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		if p, ok := c.projectionOf(ref); ok {
			lon, lat, ok := p.Invert(jsval.ToNumber(v.Index(0)), jsval.ToNumber(v.Index(1)))
			if !ok {
				return jsval.Undefined
			}
			return jsval.ArrOf(jsval.Num(lon), jsval.Num(lat))
		}
		return jsval.Undefined
	}
	// `isArray(range) ? (s.invertRange || s.invert)(range)
	// : (s.invert || s.invertExtent)(range)`: a scale with neither (a
	// sequential, diverging, ordinal or bin-ordinal one) is a call of
	// undefined.
	typ := s.Type()
	if v.IsArr() {
		if !scaleHasMethod(typ, "invertRange") && !scaleHasMethod(typ, "invert") {
			panic(&expr.Error{Name: "TypeError", Msg: "(s.invertRange || s.invert) is not a function"})
		}
		r, ok := scale.InvertRange(s, v.Index(0), v.Index(1))
		if !ok {
			return jsval.Undefined
		}
		c.view.chargeItems(r.Len())
		return r
	}
	if !scaleHasMethod(typ, "invert") && !scaleHasMethod(typ, "invertExtent") {
		panic(&expr.Error{Name: "TypeError", Msg: "(s.invert || s.invertExtent) is not a function"})
	}
	if inv, ok := s.(scale.Inverter); ok {
		return inv.Invert(v)
	}
	if ei, ok := s.(scale.ExtentInverter); ok {
		e := ei.InvertExtent(v)
		return jsval.ArrOf(e[0], e[1])
	}
	return jsval.Undefined
}

func (c *rtContext) Domain(ref, group jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		// A projection is a scale to getScale, without a domain.
		if _, ok := c.projectionOf(ref); ok {
			panic(&expr.Error{Name: "TypeError", Msg: "s.domain is not a function"})
		}
		return jsval.Arr(nil)
	}
	// A copy, as d3's domain() is: charged once made (the scale holds as
	// much, so the charge comes one copy late at most).
	d := s.Domain()
	c.view.chargeItems(len(d))
	return jsval.Arr(d)
}

func (c *rtContext) Range(ref, group jsval.Value) jsval.Value { return c.scaleRange(ref) }

func (c *rtContext) Bandwidth(ref, group jsval.Value) jsval.Value {
	return jsval.Num(c.scaleBandwidth(ref))
}

// copiedScale is the scale a copy() function stands for.
type copiedScale struct{ s scale.Scale }

// Copy is copy(name): `s ? s.copy() : undefined`, a function. It prints as
// the source of d3's scale function of that family of scales (what V8's
// Function.prototype.toString answers for the pinned d3 build); vega-scale's
// isRegisteredScale is false for it, so no scale function accepts it.
func (c *rtContext) Copy(ref, group jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		return jsval.Undefined
	}
	// the copy has the domain and the range of the scale: charged as
	// domain() and range() charge theirs
	c.view.chargeItems(len(s.Domain()) + len(s.Range()))
	typ := s.Type()
	switch s.(type) {
	case *scale.Sequential: // sequential-linear, -log...: Type() is the transform's
		typ = scale.TypeSequential
	case *scale.Diverging:
		typ = scale.TypeDiverging
	}
	return jsval.HostFunction(scaleFunctionSource(typ), copiedScale{s.Copy()})
}

func scaleFunctionSource(typ string) string {
	const nl = "\n    "
	const end = "\n  }"
	switch typ {
	case scale.TypeIdentity:
		return "function scale(x) {" + nl + "return x == null || isNaN(x = +x) ? unknown : x;" + end
	case scale.TypeSequential:
		return "function scale(x) {" + nl + "return x == null || isNaN(x = +x) ? unknown : interpolator(k10 === 0 ? 0.5 : (x = (transform(x) - t0) * k10, clamp ? Math.max(0, Math.min(1, x)) : x));" + end
	case scale.TypeDiverging:
		return "function scale(x) {" + nl + "return isNaN(x = +x) ? unknown : (x = 0.5 + ((x = +transform(x)) - t1) * (s * x < s * t1 ? k10 : k21), interpolator(clamp ? Math.max(0, Math.min(1, x)) : x));" + end
	case scale.TypeOrdinal, scale.TypeBand, scale.TypePoint:
		return "function scale(d) {\n    let i = index.get(d);\n    if (i === undefined) {\n      if (unknown !== implicit) return unknown;\n      index.set(d, i = domain.push(d) - 1);\n    }\n    return range[i % range.length];" + end
	case scale.TypeQuantile:
		return "function scale(x) {" + nl + "return x == null || isNaN(x = +x) ? unknown : range[bisect(thresholds, x)];" + end
	case scale.TypeQuantize, scale.TypeThreshold:
		return "function scale(x) {" + nl + "return x != null && x <= x ? range[bisect(domain, x, 0, n)] : unknown;" + end
	case scale.TypeBinOrdinal:
		return "function scale(x) {" + nl + "return x == null || x !== x ? undefined : range[(bisect(domain, x) - 1) % range.length];" + end
	}
	return "function scale(x) {" + nl + "return x == null || isNaN(x = +x) ? unknown : (output || (output = piecewise(domain.map(transform), range, interpolate)))(transform(clamp(x)));" + end
}

var _ = format.UTC

// Gradient implements expr.ScaleProvider's gradient(scale, p0, p1, count):
// a linear gradient object sampling the scale's colours along (p0, p1).
func (c *rtContext) Gradient(ref, p0, p1, count, group jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		// `scale.domain()` of what getScale answers: a projection has no
		// domain, and an unknown name is undefined.
		if _, ok := c.projectionOf(ref); ok {
			panic(&expr.Error{Name: "TypeError", Msg: "scale.domain is not a function"})
		}
		panic(&expr.Error{Name: "TypeError", Msg: "Cannot read properties of undefined (reading 'domain')"})
	}
	return scaleGradient(c.view, s, p0, p1, count)
}

// orZeroV is `v || 0`.
func orZeroV(v jsval.Value) jsval.Value {
	if v.IsTruthy() {
		return v
	}
	return jsval.Num(0)
}

// numOr0f is `x || 0` of a number.
func numOr0f(f float64) float64 {
	if f == 0 || math.IsNaN(f) {
		return 0
	}
	return f
}
