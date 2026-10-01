package vega

import (
	"github.com/mgilbir/aster/internal/jsmath"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/transforms"
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

// domainList reads a domain parameter. Scales iterate the value they are given
// (`for (const v of domain)`, `Array.from(domain)`), so a string is a domain of
// its characters (code points); a non-iterable value is an error for the
// ordinal family and an empty domain for the rest.
func domainList(x any, typ string) ([]jsval.Value, bool) {
	if l, ok := valueList(x); ok {
		return l, true
	}
	v, ok := x.(jsval.Value)
	if !ok || !v.IsTruthy() {
		return nil, false
	}
	if v.IsStr() {
		var out []jsval.Value
		for _, r := range v.StrValue() {
			out = append(out, jsval.Str(string(r)))
		}
		return out, true
	}
	switch typ {
	case scale.TypeOrdinal, scale.TypeBand, scale.TypePoint, scale.TypeQuantile:
		fail("TypeError: %s is not iterable", v.String())
	}
	return nil, false
}

func scaleKey(p *opParams) string {
	t := p.Value("type").AsString()
	if t == scale.TypeSequential {
		return scale.TypeSequential + "-" + scale.TypeLinear
	}
	d := ""
	if isContinuousColor(p) {
		n := 0
		if dom, ok := domainList(p.Get("domain"), p.Value("type").AsString()); ok {
			n = len(dom)
			if !p.Value("domainMid").IsNullish() {
				n++
			}
		}
		switch n {
		case 2:
			d = scale.TypeSequential + "-"
		case 3:
			d = scale.TypeDiverging + "-"
		}
	}
	if d+t == "" {
		return scale.TypeLinear
	}
	return strings.ToLower(d + t)
}

func isContinuousColor(p *opParams) bool {
	t := p.Value("type").AsString()
	if !scale.IsContinuous(t) || t == scale.TypeTime || t == scale.TypeUTC {
		return false
	}
	if p.Value("scheme").IsTruthy() || p.Has("scheme") && p.Get("scheme") != nil {
		if s := p.Get("scheme"); s != nil {
			if v, ok := s.(jsval.Value); !ok || v.IsTruthy() {
				return true
			}
		}
	}
	if rng, ok := valueList(p.Get("range")); ok && len(rng) > 0 {
		for _, r := range rng {
			if !r.IsStr() {
				return false
			}
		}
		return true
	}
	return false
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
			s = ns
			n.value = s
		}
		for i := 0; i < p.count(); i++ {
			name := p.nameAt(i)
			if scaleSkip[name] {
				continue
			}
			if name == "padding" && includePad(s.Type()) {
				continue
			}
			if !scale.Set(s, name, toValue(p.Get(name))) {
				g.warn("Unsupported scale property: " + name)
			}
		}
		count := configureDomain(s, p, g)
		count = configureBins(s, p, count)
		configureRange(s, p, count)
		return barePulse(pulse)
	}), nil
}

func configureDomain(s scale.Scale, p *opParams, g *flowGraph) int {
	typ := s.Type()
	if raw, ok := valueList(p.Get("domainRaw")); ok {
		domainCheck(typ, raw, g)
		s.SetDomain(raw)
		return len(raw)
	}
	domain, ok := domainList(p.Get("domain"), typ)
	if !ok {
		return 0
	}
	zv := p.Value("zero")
	zero := zv.IsTruthy() || (zv.IsUndefined() && includeZero(s))
	dmin, dmax, dmid := p.Value("domainMin"), p.Value("domainMax"), p.Value("domainMid")
	if zero || !dmin.IsNullish() || !dmax.IsNullish() || !dmid.IsNullish() {
		domain = append([]jsval.Value(nil), domain...)
		n := len(domain) - 1
		if n == 0 {
			n = 1 // `(length - 1) || 1`
		}
		at := func(i int) jsval.Value {
			if i >= 0 && i < len(domain) {
				return domain[i]
			}
			return jsval.Undefined
		}
		setAt := func(i int, v jsval.Value) {
			if i < 0 {
				return
			}
			for len(domain) <= i {
				domain = append(domain, jsval.Undefined)
			}
			domain[i] = v
		}
		if zero {
			if transforms.Greater(at(0), jsval.Num(0)) {
				setAt(0, jsval.Num(0))
			}
			if transforms.Less(at(n), jsval.Num(0)) {
				setAt(n, jsval.Num(0))
			}
		}
		if !dmin.IsNullish() {
			setAt(0, dmin)
		}
		if !dmax.IsNullish() {
			setAt(n, dmax)
		}
		if !dmid.IsNullish() {
			i := n
			if transforms.Greater(dmid, at(n)) {
				i = n + 1
			} else if transforms.Less(dmid, at(0)) {
				i = 0
			}
			if i != n {
				g.warn("Scale domainMid exceeds domain min or max.")
			}
			if i < 0 {
				i = 0
			}
			if i > len(domain) {
				i = len(domain)
			}
			domain = append(domain, jsval.Undefined)
			copy(domain[i+1:], domain[i:])
			domain[i] = dmid
		}
	}
	if includePad(typ) && p.Value("padding").IsTruthy() && len(domain) > 0 && !jsval.Equal(domain[0], domain[len(domain)-1]) {
		domain = padDomain(typ, domain, p)
	}
	domainCheck(typ, domain, g)
	s.SetDomain(domain)

	if typ == scale.TypeOrdinal {
		if o, ok := s.(*scale.Ordinal); ok {
			if p.Value("domainImplicit").IsTruthy() {
				o.SetImplicit()
			} else {
				o.SetUnknown(jsval.Undefined)
			}
		}
	}
	if nice := p.Value("nice"); nice.IsTruthy() {
		if nc, ok := s.(scale.Niceable); ok {
			var tc scale.TickCount
			if !(nice.IsBool() && nice.BoolValue()) {
				var err error
				tc, err = scale.TickCountFor(s, nice, nil)
				if err != nil {
					failErr(err)
				}
			}
			nc.Nice(tc)
		}
	}
	return len(domain)
}

func isFalsyParam(x any) bool {
	if v, ok := x.(jsval.Value); ok {
		return !v.IsTruthy()
	}
	return x == nil
}

func padDomain(typ string, domain []jsval.Value, p *opParams) []jsval.Value {
	rng, _ := valueList(p.Get("range"))
	if len(rng) == 0 {
		return domain
	}
	span := math.Abs(jsval.ToNumber(rng[len(rng)-1]) - jsval.ToNumber(rng[0]))
	pad := jsval.ToNumber(p.Value("padding"))
	frac := span / (span - 2*pad)
	lo, hi := jsval.ToNumber(domain[0]), jsval.ToNumber(domain[len(domain)-1])
	exp := jsval.ToNumber(p.Value("exponent"))
	if exp == 0 || math.IsNaN(exp) {
		exp = 1
	}
	constant := jsval.ToNumber(p.Value("constant"))
	if constant == 0 || math.IsNaN(constant) {
		constant = 1
	}
	var d [2]float64
	switch typ {
	case scale.TypeLog:
		d = zoomLog(lo, hi, frac)
	case scale.TypeSqrt:
		d = zoomPow(lo, hi, frac, 0.5)
	case scale.TypePow:
		d = zoomPow(lo, hi, frac, exp)
	case scale.TypeSymlog:
		d = zoomSymlog(lo, hi, frac, constant)
	default:
		d = zoomLinear(lo, hi, frac)
	}
	out := append([]jsval.Value(nil), domain...)
	out[0] = jsval.Num(d[0])
	out[len(out)-1] = jsval.Num(d[1])
	return out
}

func zoomLinear(d0, d1, k float64) [2]float64 {
	da := (d0 + d1) / 2
	return [2]float64{da + float64((d0-da)*k), da + float64((d1-da)*k)}
}

func zoomLog(lo, hi, k float64) [2]float64 {
	sign := jsSign(lo)
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
	lift := func(x float64) float64 { return jsSign(x) * jsmath.Log1p(math.Abs(x/c)) }
	ground := func(x float64) float64 { return float64(jsSign(x)*jsmath.Expm1(math.Abs(x))) * c }
	d0, d1 := lift(lo), lift(hi)
	da := (d0 + d1) / 2
	return [2]float64{ground(da + float64((d0-da)*k)), ground(da + float64((d1-da)*k))}
}

func jsSign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x
}

func domainCheck(typ string, domain []jsval.Value, g *flowGraph) {
	if !scale.IsLogarithmic(typ) {
		return
	}
	s := 0
	for _, v := range domain {
		f := jsval.ToNumber(v)
		switch {
		case f < 0:
			s--
		case f > 0:
			s++
		}
	}
	if s < 0 {
		s = -s
	}
	if s != len(domain) {
		g.warn("Log scale domain includes zero: " + jsval.Arr(domain).String())
	}
}

func configureBins(s scale.Scale, p *opParams, count int) int {
	typed, _ := s.(scale.Typed)
	var bins []float64
	have := false
	switch b := p.Get("bins").(type) {
	case jsval.Value:
		switch {
		case b.IsArr():
			bins, have = numbers(b.Items()), true
		case b.IsObj():
			bins, have = binsFromObject(s, b), true
		}
	case []any:
		bins = make([]float64, len(b))
		for i, x := range b {
			bins[i] = jsval.ToNumber(toValue(x))
		}
		have = true
	}
	if typed != nil {
		if have {
			typed.SetBins(bins)
		} else {
			typed.SetBins(nil)
		}
	}
	if s.Type() == scale.TypeBinOrdinal && typed != nil {
		if !have {
			typed.SetBins(numbers(s.Domain()))
		} else if !p.Value("domain").IsTruthy() && p.Get("domain") == nil && !p.Value("domainRaw").IsTruthy() {
			s.SetDomain(numsToValues(bins))
			count = len(bins)
		}
	}
	return count
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

func binsFromObject(s scale.Scale, b jsval.Value) []float64 {
	// lo and hi are undefined (NaN) for an empty domain; an explicit start or
	// stop does not need them.
	lo, hi := math.NaN(), math.NaN()
	if domain := s.Domain(); len(domain) > 0 {
		lo, hi = jsval.ToNumber(domain[0]), jsval.ToNumber(domain[len(domain)-1])
	}
	step := jsval.ToNumber(b.Get("step"))
	if step == 0 || math.IsNaN(step) {
		fail("Scale bins parameter missing step property.")
	}
	start, stop := lo, hi
	if v := b.Get("start"); !v.IsNullish() {
		start = jsval.ToNumber(v)
	}
	if v := b.Get("stop"); !v.IsNullish() {
		stop = jsval.ToNumber(v)
	}
	if start < lo {
		start = step * math.Ceil(lo/step)
	}
	if stop > hi {
		stop = step * math.Floor(hi/step)
	}
	return d3Range(start, stop+step/2, step)
}

// d3Range is d3.range(start, stop, step).
func d3Range(start, stop, step float64) []float64 {
	n := math.Ceil((stop - start) / step)
	if !(n > 0) || n > 1e6 {
		return []float64{}
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

// rangeSpec is the range as configureRange builds it: a list of values or, for
// colour schemes, an interpolator of [0, 1].
type rangeSpec struct {
	values []jsval.Value
	interp scale.UnitInterpolator
}

func configureRange(s scale.Scale, p *opParams, count int) {
	g := s
	typ := g.Type()
	round := p.Value("round").IsTruthy()
	reverse := p.Value("reverse").IsTruthy()
	var rng rangeSpec
	haveRange := false
	if r, ok := valueList(p.Get("range")); ok {
		rng, haveRange = rangeSpec{values: r}, true
	}

	if v := p.Value("rangeStep"); !v.IsNullish() && p.Has("rangeStep") {
		rng, haveRange = configureRangeStep(typ, p, count), true
	} else if sch := p.Get("scheme"); sch != nil && !isFalsyParam(sch) {
		rng = configureScheme(typ, p, count)
		haveRange = true
		if rng.interp != nil {
			if in, ok := s.(interface{ SetInterpolator(scale.UnitInterpolator) }); ok {
				in.SetInterpolator(rng.interp)
				return
			}
			fail("Scale type %s does not support interpolating color schemes.", typ)
		}
	}

	gamma, hasGamma := 0.0, false
	if gv := p.Value("interpolateGamma"); !gv.IsNullish() {
		gamma, hasGamma = jsval.ToNumber(gv), true
	}
	interpName := p.Value("interpolate")

	if haveRange && scale.IsInterpolating(typ) {
		in, ok := s.(interface{ SetInterpolator(scale.UnitInterpolator) })
		if !ok {
			fail("Scale type %s does not support interpolating color schemes.", typ)
		}
		var colors []jsval.Value
		if rng.interp != nil {
			// a function range on an interpolating scale is applied above
			in.SetInterpolator(rng.interp)
			return
		}
		colors = flip(rng.values, reverse)
		name := ""
		if interpName.IsTruthy() {
			name = interpName.AsString()
		}
		in.SetInterpolator(scale.InterpolateColors(colors, name, gamma, hasGamma))
		return
	}

	ip, isInterp := s.(scale.Interpolating)
	switch {
	case haveRange && interpName.IsTruthy() && isInterp:
		fn, ok := scale.Interpolate(interpName.AsString(), gamma, hasGamma)
		if !ok {
			fail("unrecognized interpolator: %s", interpName.AsString())
		}
		ip.SetInterpolate(fn)
	default:
		if b, ok := s.(scale.Bander); ok {
			b.SetRound(round)
		} else if isInterp {
			if round {
				ip.SetInterpolate(scale.InterpolateRound)
			} else {
				ip.SetInterpolate(scale.InterpolateValue)
			}
		}
	}
	if haveRange {
		if rng.values == nil {
			rng.values = []jsval.Value{}
		}
		s.SetRange(flip(rng.values, reverse))
	}
}

func configureRangeStep(typ string, p *opParams, count int) rangeSpec {
	if typ != scale.TypeBand && typ != scale.TypePoint {
		fail("Only band and point scales support rangeStep.")
	}
	padding := jsval.ToNumber(orZeroV(p.Value("padding")))
	outer := padding
	if v := p.Value("paddingOuter"); !v.IsNullish() {
		outer = jsval.ToNumber(orZeroV(v))
	} else {
		outer = numOr0f(padding)
	}
	inner := 1.0
	if typ != scale.TypePoint {
		if v := p.Value("paddingInner"); !v.IsNullish() {
			inner = numOr0f(jsval.ToNumber(v))
		} else {
			inner = numOr0f(padding)
		}
	}
	step := jsval.ToNumber(p.Value("rangeStep"))
	return rangeSpec{values: []jsval.Value{jsval.Num(0), jsval.Num(step * scale.BandSpace(float64(count), inner, numOr0f(outer)))}}
}

func orZeroV(v jsval.Value) jsval.Value {
	if v.IsTruthy() {
		return v
	}
	return jsval.Num(0)
}

func numOr0f(f float64) float64 {
	if f == 0 || math.IsNaN(f) {
		return 0
	}
	return f
}

func configureScheme(typ string, p *opParams, count int) rangeSpec {
	extent, hasExtent := valueList(p.Get("schemeExtent"))
	reverse := p.Value("reverse").IsTruthy()
	var interp scale.UnitInterpolator
	var colors []jsval.Value
	var discrete bool
	var countFrac bool // the scheme count is not an integer

	gamma, hasGamma := 0.0, false
	if gv := p.Value("interpolateGamma"); !gv.IsNullish() {
		gamma, hasGamma = jsval.ToNumber(gv), true
	}
	iname := ""
	if iv := p.Value("interpolate"); iv.IsTruthy() {
		iname = iv.AsString()
	}

	if arr, ok := valueList(p.Get("scheme")); ok {
		interp = scale.InterpolateColors(arr, iname, gamma, hasGamma)
	} else {
		name := strings.ToLower(p.Value("scheme").AsString())
		sch, ok := scale.LookupScheme(name)
		if !ok {
			fail("Unrecognized scheme name: %s", p.Value("scheme").AsString())
		}
		if sch.IsDiscrete() {
			discrete = true
			colors = make([]jsval.Value, len(sch.Colors))
			for i, c := range sch.Colors {
				colors[i] = jsval.Str(c)
			}
		} else {
			interp = sch.Interpolator
		}
	}

	switch typ {
	case scale.TypeThreshold:
		count++
	case scale.TypeBinOrdinal:
		count--
	case scale.TypeOrdinal:
		// (+_.schemeCount || count || DEFAULT_COUNT): the count of an
		// interpolating scheme's samples; a discrete scheme is used whole.
		if c := jsval.ToNumber(p.Value("schemeCount")); c != 0 && !math.IsNaN(c) {
			count = int(c)
		} else if count == 0 {
			count = 5
		}
	case scale.TypeQuantile, scale.TypeQuantize:
		c := jsval.ToNumber(p.Value("schemeCount"))
		if c == 0 || math.IsNaN(c) {
			c = 5
		}
		count = int(c)
		countFrac = c != math.Trunc(c)
	}

	adjust := func(f scale.UnitInterpolator, withReverse bool) scale.UnitInterpolator {
		rv := withReverse && reverse
		if f != nil && (hasExtent || rv) {
			ext := []jsval.Value{jsval.Num(0), jsval.Num(1)}
			if hasExtent {
				ext = extent
			}
			return scale.InterpolateRange(f, numbers(flip(ext, rv)))
		}
		return f
	}

	if scale.IsInterpolating(typ) {
		if discrete {
			return rangeSpec{values: colors}
		}
		return rangeSpec{interp: adjust(interp, true)}
	}
	if !discrete && interp != nil {
		// quantizeInterpolator builds new Array(count): a negative count (a
		// bin-ordinal scale over an empty domain) or a fractional one (a
		// fractional schemeCount) is a RangeError, which ends the scale
		// operator's evaluation.
		if count < 0 || countFrac {
			fail("Invalid array length")
		}
		return rangeSpec{values: scale.QuantizeInterpolator(adjust(interp, false), count)}
	}
	if typ == scale.TypeOrdinal {
		return rangeSpec{values: colors}
	}
	// scheme.slice(0, count): a negative end counts from the end.
	if count < 0 {
		count = max(len(colors)+count, 0)
	}
	if count > len(colors) {
		count = len(colors)
	}
	return rangeSpec{values: colors[:count]}
}

// -- projection ----------------------------------------------------------------

func facProjection(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	n.modified = true
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		params := jsval.NewObject(p.vals.len())
		for i := 0; i < p.count(); i++ {
			name := p.nameAt(i)
			params.Set(name, projectionParam(p.Get(name)))
		}
		cur, _ := n.value.(geo.Projection)
		proj, err := geo.ConfigureProjection(cur, params, func(name string) bool { return p.Modified(name) })
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
			if e, ok := r.(*scale.Error); ok {
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

func (c *rtContext) scaleRange(ref jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		return jsval.Arr(nil)
	}
	return jsval.Arr(s.Range())
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
	if v.IsArr() {
		r, ok := scale.InvertRange(s, v.Index(0), v.Index(1))
		if !ok {
			return jsval.Undefined
		}
		return r
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
		return jsval.Arr(nil)
	}
	return jsval.Arr(s.Domain())
}

func (c *rtContext) Range(ref, group jsval.Value) jsval.Value { return c.scaleRange(ref) }

func (c *rtContext) Bandwidth(ref, group jsval.Value) jsval.Value {
	return jsval.Num(c.scaleBandwidth(ref))
}

func (c *rtContext) Copy(ref, group jsval.Value) jsval.Value { return jsval.Undefined }

var _ = format.UTC

// Gradient implements expr.ScaleProvider's gradient(scale, p0, p1, count):
// a linear gradient object sampling the scale's colours along (p0, p1).
func (c *rtContext) Gradient(ref, p0, p1, count, group jsval.Value) jsval.Value {
	s := c.scaleOf(ref)
	if s == nil {
		return jsval.Undefined
	}
	return scaleGradient(s, p0, p1, count)
}
