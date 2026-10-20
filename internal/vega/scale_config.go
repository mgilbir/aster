package vega

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/transforms"
)

// The Scale operator's configuration (vega-encode's Scale transform:
// configureDomain, configureBins, configureRange and their helpers) reads
// its parameters as the JavaScript values upstream's code receives: a domain
// or a range a signal gives may be a number, a string, an object or an
// array, and upstream applies the same code to each, with the JavaScript
// meaning of every read, comparison and call on it (`domain.slice()`,
// `domain.length`, `_.range.every(isString)`, `count + 1`, ...). The scale's
// own setters then take the value as d3's do: Array.from, a for-of loop, a
// destructuring or an index read, by scale type.

// scaleArg is the Scale transform's `_[name]`: the parameter as the value
// upstream reads (a list of resolved parameters is an array).
func scaleArg(p *opParams, name string) jsval.Value { return toValue(p.Get(name)) }

// jsGet is `v[key]` of a value the Scale transform reads without checking
// its type: the TypeError for null or undefined, the member otherwise.
func jsGet(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		fail("TypeError: Cannot read properties of %s (reading '%s')", nullishName(v), key)
	}
	return jsval.Member(v, key)
}

// jsIndex is `v[i]` for an integer i (which may be negative or past the
// end, reading undefined).
func jsIndex(v jsval.Value, i float64) jsval.Value {
	if v.IsArr() {
		if i >= 0 && i < float64(v.Len()) {
			return v.Index(int(i))
		}
		return jsval.Undefined
	}
	return jsGet(v, jsval.JSNumberString(i))
}

// jsPeek is vega-util's peek: `array[array.length - 1]`.
func jsPeek(v jsval.Value) jsval.Value {
	return jsIndex(v, jsval.ToNumber(jsGet(v, "length"))-1)
}

// jsAdd is the + operator: a string, an array, an object or a date makes it
// a concatenation of their texts, anything else a sum of numbers.
func jsAdd(w jsval.Walker, a, b jsval.Value) jsval.Value {
	if concatenates(a) || concatenates(b) {
		return jsval.Str(w.String(a) + w.String(b))
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

// concatenates reports whether + reads v as text: its primitive (a date's
// with the default hint) is a string.
func concatenates(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

// jsTruthyNumber reports whether a number is truthy (not 0 or NaN).
func jsTruthyNumber(f float64) bool { return f != 0 && f == f }

// coerceNumber is vega-util's coerceNumber, `toNumber(x) ?? 0`: null,
// undefined and the empty string read as 0, anything else as +x.
func coerceNumber(v jsval.Value) float64 {
	if v.IsNullish() || (v.IsStr() && v.StrValue() == "") {
		return 0
	}
	return jsval.ToNumber(v)
}

// maxArrayLength is the largest length new Array(n) and Array.from accept
// (2^32 - 1); a longer one is a RangeError.
const maxArrayLength = 1<<32 - 1

// chargeList charges, before it is made, a list of n values the
// configuration builds from an array-like (whose length a signal can set to
// anything up to maxArrayLength).
func (g *flowGraph) chargeList(n int64) {
	g.work(int(n/16) + 1)
	if n > 256 {
		g.view.chargeRun(n * envItem)
	}
}

// codePoints is what iterating a string visits: its code points, each a
// string.
func codePoints(g *flowGraph, s string) []jsval.Value {
	g.chargeList(int64(utf8.RuneCountInString(s)))
	out := make([]jsval.Value, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		out = append(out, jsval.Str(string(r)))
	}
	return out
}

// arrayFrom is Array.from(v) (v not null or undefined): an array's
// elements, a string's code points, an object's array-like members (its
// `length`, then "0", "1", ...), nothing for any other value.
func arrayFrom(g *flowGraph, v jsval.Value) []jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		return v.Items()
	case jsval.KindStr:
		return codePoints(g, v.StrValue())
	case jsval.KindUndefined, jsval.KindNull:
		fail("TypeError: %s is not iterable (cannot read property Symbol(Symbol.iterator))", iterableName(v))
	}
	return arrayLike(g, v)
}

// arrayLike is the elements Array.prototype's methods visit of `this`: an
// array's, a string's UTF-16 code units, an object's members up to its
// `length`, none for any other value.
func arrayLike(g *flowGraph, v jsval.Value) []jsval.Value {
	if v.IsArr() {
		return v.Items()
	}
	a, ok := jsval.ToArrayLike(v)
	if !ok {
		fail("TypeError: Array.prototype.slice called on null or undefined")
	}
	n := a.Len()
	if n > maxArrayLength {
		fail("RangeError: Invalid array length")
	}
	g.chargeList(n)
	out := make([]jsval.Value, n)
	for i := range out {
		if i&(rowBatch-1) == 0 && i > 0 {
			g.work(rowBatch)
		}
		out[i] = a.At(int64(i))
	}
	return out
}

// iterated is what `for (const x of _)` visits: an array's elements, a
// string's code points, and the TypeError of anything else.
func iterated(g *flowGraph, v jsval.Value) []jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		return v.Items()
	case jsval.KindStr:
		return codePoints(g, v.StrValue())
	}
	fail("TypeError: _ is not iterable")
	return nil
}

// destructured is the first three values `[x0, x1, x2] = _` takes: an
// array's or a string's (its code points), and V8's TypeError for anything
// else.
func destructured(v jsval.Value) []jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		return v.Items()
	case jsval.KindStr:
		var out []jsval.Value
		for _, r := range v.StrValue() {
			if len(out) == 3 {
				break
			}
			out = append(out, jsval.Str(string(r)))
		}
		return out
	}
	fail("TypeError: %s is not iterable (cannot read property Symbol(Symbol.iterator))", iterableName(v))
	return nil
}

// iterableName is how V8 names a value that is not iterable.
func iterableName(v jsval.Value) string {
	switch v.Kind() {
	case jsval.KindUndefined:
		return "undefined"
	case jsval.KindNull:
		return "object null"
	case jsval.KindNum:
		return "number " + jsval.JSNumberString(v.NumValue())
	case jsval.KindBool:
		return "boolean " + strconv.FormatBool(v.BoolValue())
	}
	return "object"
}

// setScaleDomain is scale.domain(v), with the value taken as the scale
// type's d3 setter takes it.
func setScaleDomain(g *flowGraph, s scale.Scale, v jsval.Value) {
	switch s.(type) {
	case *scale.Sequential, *scale.Diverging, *scale.Quantize:
		s.SetDomain(destructured(v)) // [x0, x1(, x2)] = _
	case *scale.Ordinal, *scale.Band, *scale.Quantile:
		s.SetDomain(iterated(g, v)) // for (const value of _)
	case *scale.BinOrdinal:
		s.SetDomain(arrayLike(g, v)) // map.call(_, toNumber)
	default:
		s.SetDomain(arrayFrom(g, v)) // Array.from(_)
	}
}

// setScaleRange is scale.range(v), with the value taken as the scale type's
// setter takes it.
func setScaleRange(g *flowGraph, s scale.Scale, v jsval.Value) {
	s.SetRange(rangeValues(g, s, v))
}

// rangeValues is the range a scale's range (or rangeRound) setter makes of
// v.
func rangeValues(g *flowGraph, s scale.Scale, v jsval.Value) []jsval.Value {
	switch s.(type) {
	case *scale.Band:
		return []jsval.Value{jsGet(v, "0"), jsGet(v, "1")} // [+_[0], +_[1]]
	case *scale.Sequential, *scale.Diverging:
		return destructured(v) // [r0, r1] = _
	case *scale.BinOrdinal:
		return arrayLike(g, v) // slice.call(_)
	}
	return arrayFrom(g, v)
}

// flipValue is the Scale transform's flip: `reverse ? array.slice().reverse()
// : array`, which only an array survives.
func flipValue(v jsval.Value, reverse bool) jsval.Value {
	if !reverse {
		return v
	}
	switch v.Kind() {
	case jsval.KindArr:
		return jsval.Arr(flip(v.Items(), true))
	case jsval.KindStr:
		fail("TypeError: array.slice(...).reverse is not a function")
	case jsval.KindUndefined, jsval.KindNull:
		fail("TypeError: Cannot read properties of %s (reading 'slice')", nullishName(v))
	}
	fail("TypeError: array.slice is not a function")
	return v
}

// scaleKey is the Scale transform's scaleKey: the registry key of the scale
// the parameters ask for.
func scaleKey(p *opParams) string {
	t := scaleArg(p, "type")
	if strictIs(t, scale.TypeSequential) {
		return scale.TypeSequential + "-" + scale.TypeLinear
	}
	d := ""
	if isContinuousColor(p, t) {
		var n jsval.Value
		if raw := scaleArg(p, "rawDomain"); raw.IsTruthy() {
			n = jsGet(raw, "length")
		} else if dom := scaleArg(p, "domain"); dom.IsTruthy() {
			mid := 0.0
			if !scaleArg(p, "domainMid").IsNullish() {
				mid = 1
			}
			n = jsAdd(p.walk, jsGet(dom, "length"), jsval.Num(mid))
		} else {
			n = jsval.Num(0)
		}
		switch {
		case n.IsNum() && n.NumValue() == 2:
			d = scale.TypeSequential + "-"
		case n.IsNum() && n.NumValue() == 3:
			d = scale.TypeDiverging + "-"
		}
	}
	k := d + p.walk.String(t)
	if k == "" {
		return scale.TypeLinear
	}
	return jsval.JSLower(k)
}

// isContinuousColor is the Scale transform's isContinuousColor: a
// continuous, non-temporal type with a scheme, or with a range of strings
// (`_.range && _.range.length && _.range.every(isString)`, which is a
// TypeError for a range that has a length and no every).
func isContinuousColor(p *opParams, t jsval.Value) bool {
	ts, ok := t.Strict()
	if !ok || !scale.IsContinuous(ts) || ts == scale.TypeTime || ts == scale.TypeUTC {
		return false
	}
	if scaleArg(p, "scheme").IsTruthy() {
		return true
	}
	rng := scaleArg(p, "range")
	if !rng.IsTruthy() || !jsGet(rng, "length").IsTruthy() {
		return false
	}
	if !rng.IsArr() {
		fail("TypeError: _.range.every is not a function")
	}
	for _, r := range rng.Items() {
		if !r.IsStr() {
			return false
		}
	}
	return true
}

// configureDomain is the Scale transform's configureDomain. It answers
// `domain.length`, the domain's cardinality as upstream reads it (any value,
// undefined for a domain that is not array-like).
func configureDomain(s scale.Scale, p *opParams, g *flowGraph) jsval.Value {
	w := g.view.walk
	typ := s.Type()
	// rawDomain: `if (raw > -1) return raw` of the raw domain's length,
	// which a raw domain that is not array-like has none of: the domain
	// is then configured as if there were no raw one.
	if raw := scaleArg(p, "domainRaw"); raw.IsTruthy() {
		domainCheck(typ, raw, g)
		setScaleDomain(g, s, raw)
		if n := jsGet(raw, "length"); transforms.Greater(w, n, jsval.Num(-1)) {
			return n
		}
	}
	domain := scaleArg(p, "domain")
	zv := scaleArg(p, "zero")
	zero := zv.IsTruthy() || (zv.IsUndefined() && includeZero(s))
	if !domain.IsTruthy() {
		return jsval.Num(0)
	}
	dmin, dmax, dmid := scaleArg(p, "domainMin"), scaleArg(p, "domainMax"), scaleArg(p, "domainMid")
	first, last := func() jsval.Value { return jsIndex(domain, 0) }, func() jsval.Value { return jsPeek(domain) }
	if zero || !dmin.IsNullish() || !dmax.IsNullish() || !dmid.IsNullish() {
		d := sliceDomain(domain)
		n := d.length() - 1
		if n == 0 {
			n = 1 // `(domain.length - 1) || 1`
		}
		if zero {
			if transforms.Greater(w, d.at(0), jsval.Num(0)) {
				d.set(0, jsval.Num(0))
			}
			if transforms.Less(w, d.at(n), jsval.Num(0)) {
				d.set(n, jsval.Num(0))
			}
		}
		if !dmin.IsNullish() {
			d.set(0, dmin)
		}
		if !dmax.IsNullish() {
			d.set(n, dmax)
		}
		if !dmid.IsNullish() {
			i := n
			if transforms.Greater(w, dmid, d.at(n)) {
				i = n + 1
			} else if transforms.Less(w, dmid, d.at(0)) {
				i = 0
			}
			if i != n {
				// df.warn(msg, mid): the logger prints the value after the
				// text, as logText does for an expression's warn().
				msg := []byte("Scale domainMid exceeds domain min or max. ")
				g.warn(string(g.walk.AppendStringPrefix(msg, dmid, MaxWarningText)))
			}
			d.splice(i, dmid)
		}
		domain = d.value()
		// peek reads `domain[-1]` of an empty array, which a write set.
		first, last = func() jsval.Value { return d.at(0) }, func() jsval.Value { return d.at(d.length() - 1) }
	}
	if includePad(typ) && scaleArg(p, "padding").IsTruthy() && !jsval.StrictEqual(first(), last()) {
		domain = padDomain(w, typ, domain, p)
	}
	domainCheck(typ, domain, g)
	setScaleDomain(g, s, domain)

	if typ == scale.TypeOrdinal {
		if o, ok := s.(*scale.Ordinal); ok {
			if scaleArg(p, "domainImplicit").IsTruthy() {
				o.SetImplicit()
			} else {
				o.SetUnknown(jsval.Undefined)
			}
		}
	}
	if nice := scaleArg(p, "nice"); nice.IsTruthy() {
		if nc, ok := s.(scale.Niceable); ok {
			// scale.nice((_.nice !== true && tickCount(scale, _.nice)) || null)
			tc := scale.TickCount{Null: true}
			if !(nice.IsBool() && nice.BoolValue()) {
				c, err := scale.TickCountFor(s, nice, jsval.Undefined)
				if err != nil {
					failErr(err)
				}
				if c.Interval != nil || c.Opaque || (c.HasN && jsTruthyNumber(c.N)) {
					tc = c
				}
			}
			nc.Nice(tc)
		}
	}
	return jsGet(domain, "length")
}

// jsDomain is the copy of a domain configureDomain adjusts (`domain.slice()`):
// an array, with the property "-1" that an empty array's `domain[n]` reads
// and writes, or a string, which every write fails on.
type jsDomain struct {
	str   jsval.Value // a string domain
	items []jsval.Value
	neg   jsval.Value // domain[-1]
}

// sliceDomain is `domain.slice()`: a copy of an array, the string itself,
// and the TypeError of any other value, which has no slice.
func sliceDomain(v jsval.Value) *jsDomain {
	switch v.Kind() {
	case jsval.KindArr:
		return &jsDomain{items: append([]jsval.Value(nil), v.Items()...)}
	case jsval.KindStr:
		return &jsDomain{str: v}
	}
	fail("TypeError: domain.slice is not a function")
	return nil
}

func (d *jsDomain) length() int {
	if d.str.IsStr() {
		return int(jsval.ToNumber(jsval.Member(d.str, "length")))
	}
	return len(d.items)
}

func (d *jsDomain) at(i int) jsval.Value {
	switch {
	case d.str.IsStr():
		return jsval.Member(d.str, strconv.Itoa(i))
	case i == -1:
		return d.neg
	case i >= 0 && i < len(d.items):
		return d.items[i]
	}
	return jsval.Undefined
}

// set is `domain[i] = v`: a string's characters are read-only (the
// Scale transform is strict mode code).
func (d *jsDomain) set(i int, v jsval.Value) {
	if d.str.IsStr() {
		s := jsval.StringPrefix(d.str, jsval.MessageBytes)
		if i >= 0 && i < d.length() {
			fail("TypeError: Cannot assign to read only property '%d' of string '%s'", i, s)
		}
		fail("TypeError: Cannot create property '%d' on string '%s'", i, s)
	}
	if i == -1 {
		d.neg = v
		return
	}
	for len(d.items) <= i {
		d.items = append(d.items, jsval.Undefined)
	}
	d.items[i] = v
}

// splice is `domain.splice(i, 0, v)`, which a string does not have.
func (d *jsDomain) splice(i int, v jsval.Value) {
	if d.str.IsStr() {
		fail("TypeError: domain.splice is not a function")
	}
	if i < 0 {
		i = max(len(d.items)+i, 0)
	}
	i = min(i, len(d.items))
	d.items = append(d.items, jsval.Undefined)
	copy(d.items[i+1:], d.items[i:])
	d.items[i] = v
}

func (d *jsDomain) value() jsval.Value {
	if d.str.IsStr() {
		return d.str
	}
	return jsval.Arr(d.items)
}

// padDomain is the Scale transform's padDomain: the domain zoomed out so
// the range keeps `padding` pixels free at each end.
func padDomain(w jsval.Walker, typ string, domain jsval.Value, p *opParams) jsval.Value {
	rng := scaleArg(p, "range")
	span := math.Abs(jsval.ToNumber(jsPeek(rng)) - jsval.ToNumber(jsIndex(rng, 0)))
	frac := span / (span - float64(2*jsval.ToNumber(scaleArg(p, "padding"))))
	// vega-util's zoom: an empty domain is an error.
	if !transforms.Greater(w, jsGet(domain, "length"), jsval.Num(0)) {
		fail("Domain array must not be empty")
	}
	lo, hi := jsIndex(domain, 0), jsPeek(domain)
	var d [2]float64
	switch typ {
	case scale.TypeLog:
		d = zoomLog(jsval.ToNumber(lo), jsval.ToNumber(hi), frac)
	case scale.TypeSqrt:
		d = zoomPow(jsval.ToNumber(lo), jsval.ToNumber(hi), frac, 0.5)
	case scale.TypePow:
		d = zoomPow(jsval.ToNumber(lo), jsval.ToNumber(hi), frac, orOne(scaleArg(p, "exponent")))
	case scale.TypeSymlog:
		d = zoomSymlog(jsval.ToNumber(lo), jsval.ToNumber(hi), frac, orOne(scaleArg(p, "constant")))
	default:
		d = zoomLinear(coerceNumber(lo), coerceNumber(hi), frac)
	}
	// domain = domain.slice(); domain[0] = d[0]; domain[domain.length-1] = d[1]
	out := sliceDomain(domain)
	out.set(0, jsval.Num(d[0]))
	out.set(out.length()-1, jsval.Num(d[1]))
	return out.value()
}

// orOne is `x || 1` read as a number.
func orOne(v jsval.Value) float64 {
	if !v.IsTruthy() {
		return 1
	}
	return jsval.ToNumber(v)
}

// domainCheck is the Scale transform's domainCheck: a logarithmic scale
// warns of a domain that includes zero or values of both signs
// (`domain.reduce(...)`, which only an array has).
func domainCheck(typ string, domain jsval.Value, g *flowGraph) {
	if !scale.IsLogarithmic(typ) {
		return
	}
	if !domain.IsArr() {
		jsGet(domain, "reduce")
		fail("TypeError: domain.reduce is not a function")
	}
	items := domain.Items()
	s := 0
	for i, v := range items {
		g.rows(i, len(items), 1)
		switch {
		case transforms.Less(g.view.walk, v, jsval.Num(0)):
			s--
		case transforms.Greater(g.view.walk, v, jsval.Num(0)):
			s++
		}
	}
	if s < 0 {
		s = -s
	}
	if s != len(items) {
		// 'Log scale domain includes zero: ' + stringValue(domain), of which
		// the report keeps MaxWarningText bytes: only those are made (C151).
		msg := []byte("Log scale domain includes zero: ")
		g.warn(string(g.walk.AppendStringValuePrefix(msg, domain, MaxWarningText)))
	}
}

// configureBins is the Scale transform's configureBins. count is the
// domain's cardinality configureDomain answered; a bin-ordinal scale whose
// bins make its domain answers theirs.
func configureBins(s scale.Scale, p *opParams, count jsval.Value, g *flowGraph) jsval.Value {
	typed, _ := s.(scale.Typed)
	bins := scaleArg(p, "bins")
	var list []float64
	if bins.IsTruthy() && !bins.IsArr() {
		list = binsFromObject(g, s, bins)
	} else if bins.IsArr() {
		list = numbers(bins.Items())
	}
	if typed != nil {
		if bins.IsTruthy() {
			typed.SetBins(list)
		} else {
			typed.SetBins(nil)
		}
	}
	if s.Type() == scale.TypeBinOrdinal && typed != nil {
		if !bins.IsTruthy() {
			typed.SetBins(numbers(s.Domain()))
		} else if !scaleArg(p, "domain").IsTruthy() && !scaleArg(p, "domainRaw").IsTruthy() {
			s.SetDomain(numsToValues(list))
			count = jsval.Int(len(list))
		}
	}
	return count
}

// binsFromObject generates the bin boundaries of a bins parameter that is
// not an array, read as an object (a string or a number has no step).
func binsFromObject(g *flowGraph, s scale.Scale, b jsval.Value) []float64 {
	w := g.view.walk
	domain := s.Domain()
	lo, hi := jsval.Undefined, jsval.Undefined
	if len(domain) > 0 {
		lo, hi = domain[0], domain[len(domain)-1]
	}
	step := jsval.Member(b, "step")
	start, stop := lo, hi
	if v := jsval.Member(b, "start"); !v.IsNullish() {
		start = v
	}
	if v := jsval.Member(b, "stop"); !v.IsNullish() {
		stop = v
	}
	if !step.IsTruthy() {
		fail("Scale bins parameter missing step property.")
	}
	st := jsval.ToNumber(step)
	if transforms.Less(w, start, lo) {
		start = jsval.Num(float64(st * math.Ceil(jsval.ToNumber(lo)/st)))
	}
	if transforms.Greater(w, stop, hi) {
		stop = jsval.Num(float64(st * math.Floor(jsval.ToNumber(hi)/st)))
	}
	// sequence(start, stop + step / 2, step): a date's stop concatenates.
	return d3Range(jsval.ToNumber(start), jsval.ToNumber(jsAdd(w, stop, jsval.Num(st/2))), st)
}

// configureRange is the Scale transform's configureRange.
func configureRange(g *flowGraph, s scale.Scale, p *opParams, count jsval.Value) {
	typ := s.Type()
	round := scaleArg(p, "round").IsTruthy()
	reverse := scaleArg(p, "reverse").IsTruthy()
	rng := scaleArg(p, "range")

	if !scaleArg(p, "rangeStep").IsNullish() {
		rng = configureRangeStep(typ, p, count)
	} else if scaleArg(p, "scheme").IsTruthy() {
		var fn scale.UnitInterpolator
		rng, fn = configureScheme(g, typ, p, count)
		if fn != nil {
			if in, ok := s.(interface{ SetInterpolator(scale.UnitInterpolator) }); ok {
				in.SetInterpolator(fn)
				return
			}
			fail("Scale type %s does not support interpolating color schemes.", typ)
		}
	}

	if rng.IsTruthy() && scale.IsInterpolating(typ) {
		in, ok := s.(interface{ SetInterpolator(scale.UnitInterpolator) })
		if !ok {
			fail("Scale type %s does not support interpolating color schemes.", typ)
		}
		in.SetInterpolator(interpolateColorsOf(g, flipValue(rng, reverse), scaleArg(p, "interpolate"), scaleArg(p, "interpolateGamma")))
		return
	}

	ip, hasInterpolate := s.(scale.Interpolating)
	interp := scaleArg(p, "interpolate")
	switch {
	case rng.IsTruthy() && interp.IsTruthy() && hasInterpolate:
		ip.SetInterpolate(interpolatorNamed(interp, scaleArg(p, "interpolateGamma")))
	default:
		switch x := s.(type) {
		case scale.Bander: // scale.round
			x.SetRound(round)
		case *scale.Sequential, *scale.Diverging:
			// They have a rangeRound but no interpolate.
			fail("TypeError: scale.interpolate is not a function")
		default:
			if hasInterpolate { // scale.rangeRound
				if round {
					ip.SetInterpolate(scale.InterpolateRoundI)
				} else {
					ip.SetInterpolate(scale.InterpolateValue)
				}
			}
		}
	}
	if rng.IsTruthy() {
		setScaleRange(g, s, flipValue(rng, reverse))
	}
}

// configureRangeStep is the Scale transform's configureRangeStep: the
// range [0, rangeStep * bandSpace(count, inner, outer)].
func configureRangeStep(typ string, p *opParams, count jsval.Value) jsval.Value {
	if typ != scale.TypeBand && typ != scale.TypePoint {
		fail("Only band and point scales support rangeStep.")
	}
	padding := scaleArg(p, "padding")
	outer := scaleArg(p, "paddingOuter")
	if outer.IsNullish() {
		outer = padding
	}
	inner := jsval.Num(1)
	if typ != scale.TypePoint {
		inner = scaleArg(p, "paddingInner")
		if inner.IsNullish() {
			inner = padding
		}
	}
	// (... || 0), then bandSpace's `count - paddingInner + paddingOuter * 2`
	// and `count ? ... : 0`.
	space := 0.0
	if count.IsTruthy() {
		space = jsval.ToNumber(count) - orZero(inner) + float64(orZero(outer)*2)
		if !(space > 0) {
			space = 1
		}
	}
	return jsval.ArrOf(jsval.Num(0), jsval.Num(float64(jsval.ToNumber(scaleArg(p, "rangeStep"))*space)))
}

// orZero is `x || 0` read as a number.
func orZero(v jsval.Value) float64 {
	if !v.IsTruthy() {
		return 0
	}
	return jsval.ToNumber(v)
}

// The Object.prototype members a scheme name finds in vega-scale's scheme
// registry, a plain object: the names are lower-cased first, so only these
// two of them.
const (
	schemeObjectFunction  = "constructor" // Object
	schemeObjectPrototype = "__proto__"   // Object.prototype
)

// configureScheme is the Scale transform's configureScheme: the range the
// scheme gives (a value), or an interpolator over [0, 1] (fn).
func configureScheme(g *flowGraph, typ string, p *opParams, count jsval.Value) (rng jsval.Value, fn scale.UnitInterpolator) {
	w := g.view.walk
	extent := scaleArg(p, "schemeExtent")
	reverse := scaleArg(p, "reverse").IsTruthy()
	sch := scaleArg(p, "scheme")

	var scheme jsval.Value // a scheme that is not a function
	if sch.IsArr() {
		fn = interpolateColorsOf(g, sch, scaleArg(p, "interpolate"), scaleArg(p, "interpolateGamma"))
	} else {
		if !sch.IsStr() {
			fail("TypeError: _.scheme.toLowerCase is not a function")
		}
		name := jsval.JSLower(sch.StrValue())
		switch name {
		case schemeObjectFunction:
			// Object(t): a Number object, read here as the number.
			fn = func(t float64) jsval.Value { return jsval.Num(t) }
		case schemeObjectPrototype:
			scheme = jsval.Obj(jsval.NewObject(0))
		default:
			sc, ok := scale.LookupScheme(name)
			if !ok {
				fail("Unrecognized scheme name: %s", sch.StrValue())
			}
			if sc.IsDiscrete() {
				colors := make([]jsval.Value, len(sc.Colors))
				for i, c := range sc.Colors {
					colors[i] = jsval.Str(c)
				}
				scheme = jsval.Arr(colors)
			} else {
				fn = sc.Interpolator
			}
		}
	}

	switch typ {
	case scale.TypeThreshold:
		count = jsAdd(w, count, jsval.Num(1))
	case scale.TypeBinOrdinal:
		count = jsval.Num(jsval.ToNumber(count) - 1)
	case scale.TypeOrdinal:
		// +_.schemeCount || count || DEFAULT_COUNT
		if c := jsval.ToNumber(scaleArg(p, "schemeCount")); jsTruthyNumber(c) {
			count = jsval.Num(c)
		} else if !count.IsTruthy() {
			count = jsval.Num(5)
		}
	case scale.TypeQuantile, scale.TypeQuantize:
		c := jsval.ToNumber(scaleArg(p, "schemeCount"))
		if !jsTruthyNumber(c) {
			c = 5
		}
		count = jsval.Num(c)
	}

	// adjustScheme: a function scheme with an extent (or reversed) is
	// rescaled to it.
	adjust := func(f scale.UnitInterpolator, rev bool) scale.UnitInterpolator {
		if extent.IsTruthy() || rev {
			ext := extent
			if !ext.IsTruthy() {
				ext = jsval.ArrOf(jsval.Num(0), jsval.Num(1))
			}
			return interpolateRangeOf(w, f, flipValue(ext, rev))
		}
		return f
	}
	if scale.IsInterpolating(typ) {
		if fn != nil {
			return jsval.Undefined, adjust(fn, reverse)
		}
		return scheme, nil
	}
	if fn != nil {
		return quantizeInterpolator(g, adjust(fn, false), count), nil
	}
	if typ == scale.TypeOrdinal {
		return scheme, nil
	}
	if !scheme.IsArr() {
		fail("TypeError: scheme.slice is not a function")
	}
	return jsval.Arr(sliceTo(scheme.Items(), count)), nil
}

// sliceTo is `items.slice(0, end)`: an undefined end is the length, a
// negative one counts from the end, NaN is 0.
func sliceTo(items []jsval.Value, end jsval.Value) []jsval.Value {
	if end.IsUndefined() {
		return items
	}
	e := math.Trunc(jsval.ToNumber(end))
	switch {
	case e != e:
		e = 0
	case e < 0:
		e = math.Max(float64(len(items))+e, 0)
	}
	return items[:int(math.Min(e, float64(len(items))))]
}

// quantizeInterpolator is vega-scale's quantizeInterpolator:
//
//	const samples = new Array(count), n = count + 1;
//	for (let i = 0; i < count;) samples[i] = interpolator(++i / n);
//
// with count as given: a count that is not a number makes the one-element
// array [count] (and is compared and added to as JavaScript does), and a
// number that is not a length is a RangeError.
func quantizeInterpolator(g *flowGraph, interp scale.UnitInterpolator, count jsval.Value) jsval.Value {
	var samples []jsval.Value
	if count.IsNum() {
		c := count.NumValue()
		if c != c || c < 0 || c != math.Trunc(c) || c > maxArrayLength {
			fail("RangeError: Invalid array length")
		}
	} else {
		samples = []jsval.Value{count}
	}
	steps := math.Ceil(jsval.ToNumber(count)) // the loop's `i < count`
	if !(steps > 0) {
		steps = 0
	}
	if steps > scale.MaxSchemeSamples {
		failLimit("%v colours sampled from a scheme, more than %d", steps, scale.MaxSchemeSamples)
	}
	k := int(steps)
	n := jsval.ToNumber(jsAdd(g.view.walk, count, jsval.Num(1)))
	g.chargeList(int64(k))
	if len(samples) < k {
		samples = append(samples, make([]jsval.Value, k-len(samples))...)
	}
	for i := 0; i < k; i++ {
		samples[i] = interp(float64(i+1) / n)
	}
	return jsval.Arr(samples)
}

// interpolateRangeOf is vega-scale's interpolateRange: `start = range[0]`,
// `span = peek(range) - start`, and `interpolator(start + i * span)`, where
// a start that is a string concatenates.
func interpolateRangeOf(w jsval.Walker, f scale.UnitInterpolator, rng jsval.Value) scale.UnitInterpolator {
	start := jsIndex(rng, 0)
	span := jsval.ToNumber(jsPeek(rng)) - jsval.ToNumber(start)
	if concatenates(start) {
		text := w.String(start)
		return func(i float64) jsval.Value {
			return f(jsval.StringToNumber(text + jsval.JSNumberString(float64(i*span))))
		}
	}
	x := jsval.ToNumber(start)
	return func(i float64) jsval.Value { return f(x + float64(i*span)) }
}

// interpolateColorsOf is vega-scale's interpolateColors(colors, type,
// gamma): d3's piecewise through the colours (read as an array-like) with
// the interpolator interpolate(type || 'rgb', gamma) names.
func interpolateColorsOf(g *flowGraph, colors, typ, gamma jsval.Value) scale.UnitInterpolator {
	if !typ.IsTruthy() {
		typ = jsval.Str("rgb")
	}
	interp := interpolatorNamed(typ, gamma)
	return scale.Piecewise(g.view.walk, interp, pieceValues(g, colors))
}

// pieceValues are the values d3's piecewise reads: `n = values.length - 1`,
// `new Array(n < 0 ? 0 : n)` (a RangeError for a length that is not one),
// then values[0] to values[n].
func pieceValues(g *flowGraph, v jsval.Value) []jsval.Value {
	if v.IsArr() {
		return v.Items()
	}
	n := jsval.ToNumber(jsGet(v, "length")) - 1
	if n < 0 {
		return nil
	}
	if n != n || n != math.Trunc(n) || n > maxArrayLength {
		fail("RangeError: Invalid array length")
	}
	g.chargeList(int64(n) + 1)
	out := make([]jsval.Value, int(n)+1)
	for i := range out {
		if i&(rowBatch-1) == 0 && i > 0 {
			g.work(rowBatch)
		}
		out[i] = jsval.Member(v, strconv.Itoa(i))
	}
	return out
}

// undefinedInterpolator stands for the undefined interpolate(type) answers
// for a name d3-interpolate does not export: calling it is the TypeError.
func undefinedInterpolator(jsval.Walker, jsval.Value, jsval.Value) func(float64) jsval.Value {
	panic(&scale.Thrown{Name: "TypeError", Msg: "interpolate is not a function"})
}

// interpolatorNamed is vega-scale's interpolate(type, gamma):
// d3-interpolate's export named 'interpolate' followed by the lower-cased
// type's dash-separated parts, each capitalised, with the gamma applied
// where the interpolator takes one. A name d3-interpolate does not export
// answers undefined, which fails once called.
func interpolatorNamed(typ, gamma jsval.Value) scale.Interpolator {
	if typ.IsNullish() {
		fail("TypeError: Cannot read properties of %s (reading 'toLowerCase')", nullishName(typ))
	}
	if !typ.IsStr() {
		fail("TypeError: type.toLowerCase is not a function")
	}
	var b strings.Builder
	b.WriteString("interpolate")
	for _, part := range strings.Split(jsval.JSLower(typ.StrValue()), "-") {
		if part == "" {
			fail("TypeError: Cannot read properties of undefined (reading 'toUpperCase')")
		}
		r, size := utf8.DecodeRuneInString(part)
		if r >= 0x10000 {
			// s[0] is half of the character: no export has it.
			return undefinedInterpolator
		}
		b.WriteString(jsval.JSUpper(part[:size]))
		b.WriteString(part[size:])
	}
	name, ok := d3InterpolateExports[b.String()]
	if !ok {
		return undefinedInterpolator
	}
	f, _ := scale.Interpolate(name, jsval.ToNumber(gamma), !gamma.IsNullish())
	return f
}

// d3InterpolateExports maps d3-interpolate's exports of the interpolate*
// form to the name scale.Interpolate knows them by.
var d3InterpolateExports = map[string]string{
	"interpolateRgb":            "rgb",
	"interpolateHsl":            "hsl",
	"interpolateHslLong":        "hsl-long",
	"interpolateLab":            "lab",
	"interpolateHcl":            "hcl",
	"interpolateHclLong":        "hcl-long",
	"interpolateCubehelix":      "cubehelix",
	"interpolateCubehelixLong":  "cubehelix-long",
	"interpolateNumber":         "number",
	"interpolateRound":          "round",
	"interpolateString":         "string",
	"interpolateArray":          "array",
	"interpolateObject":         "object",
	"interpolateDate":           "date",
	"interpolateHue":            "hue",
	"interpolateBasis":          "basis",
	"interpolateBasisClosed":    "basis-closed",
	"interpolateDiscrete":       "discrete",
	"interpolateNumberArray":    "number-array",
	"interpolateTransformCss":   "transform-css",
	"interpolateTransformSvg":   "transform-svg",
	"interpolateZoom":           "zoom",
	"interpolateRgbBasis":       "rgb-basis",
	"interpolateRgbBasisClosed": "rgb-basis-closed",
}

// scaleSkipped is the Scale transform's `SKIP[key]`: the parameters the
// configuration consumes, and, SKIP being a plain object, every name it
// inherits from Object.prototype.
func scaleSkipped(name string) bool {
	if scaleSkip[name] || name == "__proto__" {
		return true
	}
	_, inherited := jsval.Inherited(name)
	return inherited
}

// scaleProperty is the Scale transform's `scale[key](_[key])` for a
// parameter the configuration does not consume, and reports whether the
// scale has such a function (upstream warns of one it has not). Besides
// its own setters a scale is a function, with Function.prototype's methods:
// apply and call call the scale (an ordinal scale with an implicit domain
// grows by undefined), and a strict function's arguments and caller cannot
// be read.
func scaleProperty(g *flowGraph, s scale.Scale, name string, v jsval.Value) bool {
	if name == "rangeRound" {
		if r, ok := s.(scale.Rounder); ok {
			r.SetRangeRound(rangeValues(g, s, v))
			return true
		}
		return false
	}
	if scale.Set(s, name, v) {
		return true
	}
	switch name {
	case "apply", "call":
		s.Apply(jsval.Undefined)
		return true
	case "bind":
		return true
	case "arguments", "caller":
		fail("TypeError: 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them")
	}
	return scaleHasMethod(s.Type(), name)
}

// scaleHasMethod reports the methods of a scale of type typ that do nothing
// the configuration can see when called with one argument (getters, copy,
// ticks): d3's and vega-scale's, by type.
func scaleHasMethod(typ, name string) bool {
	switch name {
	case "copy":
		return true
	case "ticks", "tickFormat":
		return scale.IsContinuous(typ) || typ == scale.TypeIdentity || typ == scale.TypeQuantize ||
			(name == "tickFormat" && typ == scale.TypeBinOrdinal)
	case "invert":
		return (scale.IsContinuous(typ) && !scale.IsInterpolating(typ)) || typ == scale.TypeIdentity ||
			typ == scale.TypeBand || typ == scale.TypePoint
	case "invertRange":
		return !scale.IsInterpolating(typ) && typ != scale.TypeOrdinal && typ != scale.TypeBinOrdinal
	case "invertExtent":
		return typ == scale.TypeQuantile || typ == scale.TypeQuantize || typ == scale.TypeThreshold
	case "quantiles":
		return typ == scale.TypeQuantile
	case "thresholds":
		return typ == scale.TypeQuantize
	case "bandwidth", "step":
		return typ == scale.TypeBand || typ == scale.TypePoint
	case "interpolator":
		// Replaced by configureRange's, or the configuration fails first.
		return scale.IsInterpolating(typ)
	}
	return false
}
