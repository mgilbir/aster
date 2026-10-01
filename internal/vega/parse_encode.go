package vega

import (
	"github.com/mgilbir/aster/internal/jsmath"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms"
)

// This file ports vega-parser's encode.js and encode/*: the value-reference
// language of mark encodings (value, signal, field, scale, band, offset, mult,
// exponent, round, colour and gradient references, production rules) and the
// application of config defaults and styles.
//
// Upstream turns each reference into JavaScript source and compiles it; here
// each becomes a Go closure with the same evaluation order and operator
// precedence, and the same JavaScript arithmetic.

// encEval is the state a value function reads: the context whose scales and
// signals apply, the item being encoded and its datum.
type encEval struct {
	ctx   *rtContext
	item  *scene.Item
	datum jsval.Value
}

type valueFn func(ev *encEval) jsval.Value

// channel is one encoded property.
type channel struct {
	name string
	fn   valueFn
}

// encodeSet is one encoding set (enter, update, exit, or a custom one).
type encodeSet struct {
	marktype string
	channels []channel
	output   []string
	// spatial flags for adjustSpatial.
	x, x2, xc, y, y2, yc bool
}

// encCompiler compiles value references and collects the operators they read
// (upstream merges the expressions' $params into the Encode operator's).
type encCompiler struct {
	scope  *Scope
	params *paramList
}

func (c *encCompiler) dep(name string, e *entry) {
	if !c.params.has(name) {
		c.params.set(name, e)
	}
}

func (c *encCompiler) depExpr(f *exprFn) {
	for _, d := range f.deps {
		c.dep(d.name, d.e)
	}
}

// scaleDeps registers the dependency on the named scale, or on every scale when
// the name is computed (scaleVisitor).
func (c *encCompiler) scaleDeps(literal jsval.Value, isLiteral bool) {
	if isLiteral {
		if e := c.scope.findScale(literal.AsString()); e != nil {
			c.dep("%"+literal.AsString(), e)
		}
		return
	}
	for s := c.scope; s != nil; s = s.parentScope {
		for name, e := range s.scales {
			c.dep("%"+name, e)
		}
	}
}

// parseEncode builds the parameters of an Encode operator from a mark's encode
// block: config defaults and styles are applied first.
func parseEncode(encode jsval.Value, typ, role string, style jsval.Value, scope *Scope, params *paramList) *paramList {
	if params == nil {
		params = newParamList()
	}
	encode = applyEncodeDefaults(encode, typ, role, style, scope.config)
	pe := pEncode{sets: map[string]*encodeSet{}}
	c := &encCompiler{scope: scope, params: params}
	if o := encode.ObjValue(); o != nil {
		for i := 0; i < o.Len(); i++ {
			name := o.KeyAt(i)
			pe.sets[name] = c.parseBlock(o.ValueAt(i), typ)
			pe.order = append(pe.order, name)
		}
	}
	params.set("encoders", pe)
	return params
}

func (c *encCompiler) parseBlock(block jsval.Value, typ string) *encodeSet {
	set := &encodeSet{marktype: typ}
	if block.IsNullish() {
		// the loop over its names does nothing, but listing the outputs does not
		perr("Cannot convert undefined or null to object")
	}
	o := block.ObjValue()
	if o == nil {
		return set
	}
	for i := 0; i < o.Len(); i++ {
		name := o.KeyAt(i)
		set.output = append(set.output, name)
		enc := o.ValueAt(i)
		if enc.IsNullish() {
			continue
		}
		var fn valueFn
		if enc.IsArr() {
			fn = c.rule(enc)
		} else {
			fn = c.entry(enc)
		}
		set.channels = append(set.channels, channel{name: name, fn: fn})
		switch name {
		case "x":
			set.x = true
		case "x2":
			set.x2 = true
		case "xc":
			set.xc = true
		case "y":
			set.y = true
		case "y2":
			set.y2 = true
		case "yc":
			set.yc = true
		}
	}
	return set
}

// rule compiles a production rule list: the first entry whose test passes wins,
// an entry without a test ends the list, and no match is null.
func (c *encCompiler) rule(rules jsval.Value) valueFn {
	type branch struct {
		test *exprFn
		val  valueFn
	}
	var bs []branch
	items := rules.Items()
	if len(items) == 0 {
		// The generated source is empty, which does not parse.
		perr("Expression parse error: ")
	}
	for i, r := range items {
		if !r.Get("test").IsTruthy() && i < len(items)-1 {
			// Upstream concatenates the source of every rule, with no stop at
			// the unconditional one: what follows it is appended to its value
			// ("a""b" is not an expression).
			if fn, ok := c.concatenatedRules(items); ok {
				return fn
			}
		}
	}
	for _, r := range items {
		val := c.entry(r)
		if t := r.Get("test"); t.IsTruthy() {
			f := c.scope.parseExpression(t.AsString())
			c.depExpr(f)
			bs = append(bs, branch{test: f, val: val})
			continue
		}
		bs = append(bs, branch{val: val})
		break
	}
	return func(ev *encEval) jsval.Value {
		for _, b := range bs {
			if b.test == nil || b.test.evalEnc(ev).IsTruthy() {
				return b.val(ev)
			}
		}
		return jsval.Null
	}
}

// concatenatedRules builds rule.js's source for rules that follow an
// unconditional one and evaluates it as an expression. Only rules made of a
// constant, a signal or a field path can be rendered as source here; for any
// other it reports false and the caller keeps the rules up to the first
// unconditional one.
func (c *encCompiler) concatenatedRules(items []jsval.Value) (valueFn, bool) {
	var code strings.Builder
	for _, r := range items {
		var src string
		switch {
		case !r.IsObj() || !r.Get("gradient").IsNullish() || !r.Get("scale").IsNullish() || !r.Get("exponent").IsNullish() ||
			!r.Get("mult").IsNullish() || !r.Get("offset").IsNullish() || r.Get("round").IsTruthy() || r.Get("color").IsTruthy():
			return nil, false
		case r.Get("signal").IsTruthy():
			src = "(" + r.Get("signal").AsString() + ")"
		case !r.Get("field").IsNullish():
			f := r.Get("field")
			if !f.IsStr() {
				return nil, false
			}
			src = "datum"
			for _, seg := range jsval.ParseFieldPath(f.StrValue()) {
				src += "[" + stringValue(jsval.Str(seg)) + "]"
			}
		case !r.Get("value").IsUndefined():
			v := r.Get("value")
			if v.IsObj() || v.IsArr() {
				return nil, false
			}
			src = stringValue(v)
		default:
			src = "null"
		}
		if t := r.Get("test"); t.IsTruthy() {
			src = "(" + t.AsString() + ")?" + src + ":"
		}
		code.WriteString(src)
	}
	text := code.String()
	if strings.HasSuffix(text, ":") {
		text += "null"
	}
	f := c.scope.parseExpression(text)
	c.depExpr(f)
	return func(ev *encEval) jsval.Value { return f.evalEnc(ev) }, true
}

// evalEnc evaluates an expression with the encoder's datum and item.
func (f *exprFn) evalEnc(ev *encEval) jsval.Value {
	item := jsval.Undefined
	if f.usesItem {
		item = ev.ctx.view.itemAsValue(ev.item)
	}
	return f.eval(ev.ctx, ev.datum, item, jsval.Undefined, varDatum|varItem)
}

func constFn(v jsval.Value) valueFn { return func(*encEval) jsval.Value { return v } }

// term is an intermediate expression shape, kept so that `mult` and `offset`
// bind as they do in the generated source: a band scale reference is the sum
// `scale(v)+bandwidth*band`, and a following `*mult` applies to the bandwidth
// term only.
type term struct {
	a, b valueFn // b != nil: a + b
}

func (t term) fn() valueFn {
	if t.b == nil {
		return t.a
	}
	a, b := t.a, t.b
	return func(ev *encEval) jsval.Value { return jsAdd(a(ev), b(ev)) }
}

func (c *encCompiler) entry(enc jsval.Value) valueFn {
	if !enc.Get("gradient").IsNullish() {
		return c.gradient(enc)
	}

	var value valueFn
	haveValue := false
	switch {
	case enc.Get("signal").IsTruthy():
		f := c.scope.parseExpression(enc.Get("signal").AsString())
		c.depExpr(f)
		value = func(ev *encEval) jsval.Value { return f.evalEnc(ev) }
		haveValue = true
	case enc.Get("color").IsTruthy():
		value = c.color(enc.Get("color"))
		haveValue = true
	case !enc.Get("field").IsNullish():
		value = c.field(enc.Get("field"))
		haveValue = true
	case !enc.Get("value").IsUndefined():
		value = c.literal(enc.Get("value"))
		haveValue = true
	}

	cur := term{a: value}
	if !enc.Get("scale").IsNullish() {
		cur = c.scaleTerm(enc, value, haveValue)
		haveValue = true
	}
	if cur.a == nil && cur.b == nil {
		cur = term{a: constFn(jsval.Null)}
	}

	if !enc.Get("exponent").IsNullish() {
		inner, exp := cur.fn(), c.property(enc.Get("exponent"))
		cur = term{a: func(ev *encEval) jsval.Value {
			return jsval.Num(jsPow(jsval.ToNumber(inner(ev)), jsval.ToNumber(exp(ev))))
		}}
	}
	if !enc.Get("mult").IsNullish() {
		m := c.property(enc.Get("mult"))
		mul := func(x valueFn) valueFn {
			return func(ev *encEval) jsval.Value {
				return jsval.Num(jsval.ToNumber(x(ev)) * jsval.ToNumber(m(ev)))
			}
		}
		if cur.b != nil {
			cur.b = mul(cur.b)
		} else {
			cur.a = mul(cur.a)
		}
	}
	if !enc.Get("offset").IsNullish() {
		o := c.property(enc.Get("offset"))
		cur = term{a: cur.fn(), b: o}
	}
	fn := cur.fn()
	if enc.Get("round").IsTruthy() {
		inner := fn
		fn = func(ev *encEval) jsval.Value {
			return jsval.Num(jsRound(jsval.ToNumber(inner(ev))))
		}
	}
	return fn
}

// property is a numeric property that may itself be a value reference.
// quirkyLiteral reports whether upstream's expression parser reads the string
// literal s as an identifier. It tests `legalKeywords[lookahead.value]` on a
// plain object for every token, so the literal "if" and every name an object
// inherits ("constructor", "toString", ...) parse as identifiers, whatever
// their quotes.
func quirkyLiteral(s string) bool { return s == "if" || expr.IsObjectPrototypeName(s) }

// literal is the value a reference holds as a constant. Upstream emits it as
// source text and parses that: a quirky string becomes a signal reference.
func (c *encCompiler) literal(v jsval.Value) valueFn {
	if v.IsStr() && quirkyLiteral(v.StrValue()) {
		return c.identifierFn(v.StrValue())
	}
	return constFn(v)
}

// identifierFn evaluates the identifier name as the expression compiler does,
// failing the parse where upstream's would (no such signal).
func (c *encCompiler) identifierFn(name string) valueFn {
	f := c.scope.parseExpression(name)
	c.depExpr(f)
	return func(ev *encEval) jsval.Value { return f.evalEnc(ev) }
}

func (c *encCompiler) property(p jsval.Value) valueFn {
	if p.IsObj() {
		return c.entry(p)
	}
	return constFn(p)
}

// scaleRefFn evaluates a scale reference: a name literal, a signal expression
// or a field reference.
func (c *encCompiler) scaleRefFn(s jsval.Value) valueFn {
	switch {
	case s.IsStr() && quirkyLiteral(s.StrValue()):
		c.scaleDeps(jsval.Undefined, false)
		return c.identifierFn(s.StrValue())
	case s.IsStr():
		c.scaleDeps(s, true)
		return constFn(s)
	case s.IsObj() && s.Get("signal").IsTruthy():
		f := c.scope.parseExpression(s.Get("signal").AsString())
		c.depExpr(f)
		c.scaleDeps(jsval.Undefined, false)
		return func(ev *encEval) jsval.Value { return f.evalEnc(ev) }
	}
	c.scaleDeps(jsval.Undefined, false)
	return c.field(s)
}

func (c *encCompiler) scaleTerm(enc jsval.Value, value valueFn, haveValue bool) term {
	scale := c.scaleRefFn(enc.Get("scale"))
	if r := enc.Get("range"); !r.IsNullish() {
		f := jsval.ToNumber(r)
		return term{a: func(ev *encEval) jsval.Value {
			rng := ev.ctx.encScaleRange(scale(ev))
			return lerp(rng, f)
		}}
	}
	var cur term
	if haveValue {
		cur = term{a: func(ev *encEval) jsval.Value { return ev.ctx.encScale(scale(ev), value(ev)) }}
	}
	if band := enc.Get("band"); !band.IsNullish() {
		bw := func(ev *encEval) jsval.Value { return jsval.Num(ev.ctx.scaleBandwidth(scale(ev))) }
		b := bw
		if jsval.ToNumber(band) != 1 {
			factor := c.property(band)
			b = func(ev *encEval) jsval.Value {
				return jsval.Num(jsval.ToNumber(bw(ev)) * jsval.ToNumber(factor(ev)))
			}
		}
		if haveValue {
			cur = term{a: cur.a, b: b}
		} else {
			cur = term{a: b}
		}
		if enc.Get("extra").IsTruthy() {
			sum := cur.fn()
			cur = term{a: func(ev *encEval) jsval.Value {
				if ex := ev.datum.Get("extra"); ex.IsTruthy() {
					return ev.ctx.encScale(scale(ev), ex.Get("value"))
				}
				return sum(ev)
			}}
		}
	}
	if cur.a == nil && cur.b == nil {
		cur = term{a: constFn(jsval.Num(0))}
	}
	return cur
}

var colorArgs = map[string][3]string{
	"hcl": {"h", "c", "l"}, "hsl": {"h", "s", "l"}, "lab": {"l", "a", "b"}, "rgb": {"r", "g", "b"},
}

var colorProgs = map[string]*expr.Program{}

func init() {
	for name := range colorArgs {
		p, err := expr.Compile("(" + name + "(datum.x,datum.y,datum.z)+'')")
		if err != nil {
			panic("vega: cannot compile colour helper: " + err.Error())
		}
		colorProgs[name] = p
	}
}

func (c *encCompiler) color(enc jsval.Value) valueFn {
	var typ string
	switch {
	case enc.Get("c").IsTruthy():
		typ = "hcl"
	case enc.Get("h").IsTruthy() || enc.Get("s").IsTruthy():
		typ = "hsl"
	case enc.Get("l").IsTruthy() || enc.Get("a").IsTruthy():
		typ = "lab"
	case enc.Get("r").IsTruthy() || enc.Get("g").IsTruthy() || enc.Get("b").IsTruthy():
		typ = "rgb"
	default:
		return constFn(jsval.Null)
	}
	names := colorArgs[typ]
	var comps [3]valueFn
	for i, n := range names {
		comps[i] = c.entry(enc.Get(n))
	}
	prog := colorProgs[typ]
	return func(ev *encEval) jsval.Value {
		d := jsval.NewObject(3)
		d.Set("x", comps[0](ev))
		d.Set("y", comps[1](ev))
		d.Set("z", comps[2](ev))
		s := ev.ctx.scope()
		s.Datum = jsval.Obj(d)
		v, err := prog.Eval(s)
		if err != nil {
			failErr(err)
		}
		return v
	}
}

func (c *encCompiler) gradient(enc jsval.Value) valueFn {
	scale := c.scaleRefFn(enc.Get("gradient"))
	arg := func(v jsval.Value) jsval.Value {
		if v.IsNullish() {
			return jsval.Null
		}
		return v
	}
	p0, p1, cnt := arg(enc.Get("start")), arg(enc.Get("stop")), arg(enc.Get("count"))
	return func(ev *encEval) jsval.Value {
		return ev.ctx.Gradient(scale(ev), p0, p1, cnt, jsval.Undefined)
	}
}

// field compiles a field reference: a datum path, a path in the group item
// (`group`), or in a group item's datum (`parent`), `level` groups up.
func (c *encCompiler) field(ref jsval.Value) valueFn {
	if !ref.IsObj() {
		ref = obj("datum", ref)
	}
	return c.fieldObj(ref)
}

// fieldObj compiles a field reference object. A nested reference is not
// wrapped as a datum path: a non-object there is an invalid reference.
func (c *encCompiler) fieldObj(ref jsval.Value) valueFn {
	if !ref.IsObj() {
		perr("Invalid field reference: %s", quote(ref))
	}
	switch {
	case ref.Get("signal").IsTruthy():
		f := c.scope.parseExpression(ref.Get("signal").AsString())
		c.depExpr(f)
		return func(ev *encEval) jsval.Value {
			return propOf(ev.datum, f.evalEnc(ev))
		}
	case ref.Get("group").IsTruthy() || ref.Get("parent").IsTruthy():
		level := 1
		if l := jsval.ToNumber(ref.Get("level")); l > 1 {
			level = int(math.Min(l, 1000))
		}
		parent := ref.Get("parent").IsTruthy()
		var key jsval.Value
		if parent {
			key = ref.Get("parent")
		} else {
			key = ref.Get("group")
		}
		path := c.fieldPath(key)
		return func(ev *encEval) jsval.Value {
			g := ev.item
			for i := 0; i < level && g != nil; i++ {
				if g.Mark == nil {
					g = nil
					break
				}
				g = g.Mark.Group
			}
			if g == nil {
				return jsval.Undefined
			}
			p := path(ev)
			if parent {
				return walkPath(g.Datum, p)
			}
			if len(p) == 1 {
				if k := p[0].AsString(); k != "mark" && k != "items" {
					return getItemProp(g, k)
				}
			}
			return walkPath(ev.ctx.view.itemAsValue(g), p)
		}
	case ref.Get("datum").IsTruthy():
		// A constant path is the common case: compile it once.
		if d := ref.Get("datum"); d.IsStr() && !hasQuirkySegment(d.StrValue()) {
			get := transforms.FieldOf(d.StrValue()).Get
			return func(ev *encEval) jsval.Value { return get(ev.datum) }
		}
		path := c.fieldPath(ref.Get("datum"))
		return func(ev *encEval) jsval.Value { return walkPath(ev.datum, path(ev)) }
	}
	perr("Invalid field reference: %s", quote(ref))
	return nil
}

// fieldPath resolves the field part of a reference to path segments at
// evaluation time: a literal path is split once, a nested reference is
// evaluated and used as a single key.
func (c *encCompiler) fieldPath(key jsval.Value) func(ev *encEval) []jsval.Value {
	if key.IsStr() {
		segs := jsval.ParseFieldPath(key.StrValue())
		vals := make([]jsval.Value, len(segs))
		fns := make([]valueFn, len(segs))
		quirky := false
		for i, s := range segs {
			vals[i] = jsval.Str(s)
			if quirkyLiteral(s) {
				// A quoted segment of the generated datum access is parsed
				// as an identifier too.
				fns[i], quirky = c.identifierFn(s), true
			}
		}
		if quirky {
			return func(ev *encEval) []jsval.Value {
				out := make([]jsval.Value, len(vals))
				for i, v := range vals {
					out[i] = v
					if fns[i] != nil {
						out[i] = fns[i](ev)
					}
				}
				return out
			}
		}
		return func(*encEval) []jsval.Value { return vals }
	}
	inner := c.fieldObj(key)
	return func(ev *encEval) []jsval.Value { return []jsval.Value{inner(ev)} }
}

// walkPath reads a chain of keys from a value; a missing step reads undefined.
func walkPath(v jsval.Value, path []jsval.Value) jsval.Value {
	for _, k := range path {
		v = propOf(v, k)
	}
	return v
}

// propOf is `v[key]`.
func propOf(v, key jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindObj:
		return v.ObjValue().Lookup(key.AsString())
	case jsval.KindArr:
		if key.IsNum() {
			f := key.NumValue()
			if f >= 0 && f == math.Trunc(f) {
				return v.Index(int(f))
			}
			return jsval.Undefined
		}
		s := key.AsString()
		if s == "length" {
			return jsval.Int(v.Len())
		}
		if i, ok := parseArrayIndex(s); ok {
			return v.Index(i)
		}
	case jsval.KindStr:
		if key.AsString() == "length" {
			return jsval.Int(utf16Len(v.StrValue()))
		}
	}
	return jsval.Undefined
}

func parseArrayIndex(s string) (int, bool) {
	if s == "" || len(s) > 9 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// -- JavaScript arithmetic ---------------------------------------------------

// jsAdd is the `+` operator: string concatenation when either side is a
// string, numeric addition otherwise.
func jsAdd(a, b jsval.Value) jsval.Value {
	if a.IsStr() || b.IsStr() || a.IsArr() || b.IsArr() || a.IsObj() || b.IsObj() {
		return jsval.Str(a.AsString() + b.AsString())
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && (x < 0 || math.Signbit(x)) {
		return math.Copysign(0, -1)
	}
	return r
}

func jsPow(x, y float64) float64 {
	if math.IsNaN(y) {
		return math.NaN()
	}
	if y == 0 {
		return 1
	}
	if (x == 1 || x == -1) && math.IsInf(y, 0) {
		return math.NaN()
	}
	return jsmath.Pow(x, y)
}

// lerp is vega-util's lerp over a range value.
func lerp(rng jsval.Value, frac float64) jsval.Value {
	items := rng.Items()
	if len(items) == 0 {
		return jsval.Undefined
	}
	lo := items[0]
	hi := items[len(items)-1]
	if hi.IsUndefined() {
		return lo
	}
	if frac == 0 || math.IsNaN(frac) {
		return lo
	}
	if frac == 1 {
		return hi
	}
	l, h := jsval.ToNumber(lo), jsval.ToNumber(hi)
	return jsval.Num(l + float64(frac*(h-l)))
}

// -- defaults ----------------------------------------------------------------

func hasEncodeKey(key string, encode jsval.Value) bool {
	if !encode.IsObj() {
		return false
	}
	return encode.Get("enter").Get(key).IsTruthy() || encode.Get("update").Get(key).IsTruthy()
}

// applyEncodeDefaults adds config-derived defaults to an encode block: the
// group config for the root frame, mark config and per-type config for plain
// marks, and the config.style entries named by the mark's `style`, in that
// order of increasing precedence. A default that is a signal goes to `update`
// (it may change), everything else to `enter`.
func applyEncodeDefaults(encode jsval.Value, typ, role string, style jsval.Value, config jsval.Value) jsval.Value {
	defaults := jsval.NewObject(8)
	setDefault := func(key string, v jsval.Value) {
		if isSignalObj(v) {
			defaults.Set(key, obj("signal", v.Get("signal")))
		} else {
			defaults.Set(key, obj("value", v))
		}
	}

	if typ == "text" && !config.Get("lineBreak").IsNullish() && !hasEncodeKey("lineBreak", encode) {
		setDefault("lineBreak", config.Get("lineBreak"))
	}

	if role == "legend" || (len(role) >= 4 && role[:4] == "axis") {
		role = ""
	}

	var props jsval.Value
	switch role {
	case "frame":
		props = config.Get("group")
	case "mark":
		merged := jsval.NewObject(8)
		extendObj(merged, config.Get("mark"))
		extendObj(merged, config.Get(typ))
		props = jsval.Obj(merged)
	}
	if po := props.ObjValue(); po != nil {
		for i := 0; i < po.Len(); i++ {
			key := po.KeyAt(i)
			skip := hasEncodeKey(key, encode) ||
				((key == "fill" || key == "stroke") && (hasEncodeKey("fill", encode) || hasEncodeKey("stroke", encode)))
			if !skip {
				setDefault(key, po.ValueAt(i))
			}
		}
	}

	for _, name := range arrayOf(style) {
		if !config.Get("style").IsObj() {
			break
		}
		sp := config.Get("style").Get(name.AsString()).ObjValue()
		if sp == nil {
			continue
		}
		for i := 0; i < sp.Len(); i++ {
			key := sp.KeyAt(i)
			if !hasEncodeKey(key, encode) {
				setDefault(key, sp.ValueAt(i))
			}
		}
	}

	out := jsval.NewObject(4)
	if encode.IsObj() {
		extendObj(out, encode)
	}
	enter := jsval.NewObject(defaults.Len())
	var update *jsval.Object
	for i := 0; i < defaults.Len(); i++ {
		key := defaults.KeyAt(i)
		p := defaults.ValueAt(i)
		if p.Get("signal").IsTruthy() {
			if update == nil {
				update = jsval.NewObject(2)
			}
			update.Set(key, p)
		} else {
			enter.Set(key, p)
		}
	}
	extendObj(enter, encode.Get("enter"))
	out.Set("enter", jsval.Obj(enter))
	if update != nil {
		extendObj(update, encode.Get("update"))
		out.Set("update", jsval.Obj(update))
	}
	return jsval.Obj(out)
}

func hasQuirkySegment(path string) bool {
	for _, seg := range jsval.ParseFieldPath(path) {
		if quirkyLiteral(seg) {
			return true
		}
	}
	return false
}
