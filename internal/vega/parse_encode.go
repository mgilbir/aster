package vega

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/budget"
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
	set  scene.Setter // assigns the property of an item
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
		c.scope.work(1)
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
			c.scope.work(1)
			c.dep("%"+name, e)
		}
	}
}

// parseEncode builds the parameters of an Encode operator from a mark's encode
// block: config defaults and styles are applied first. Every key `for (key in
// encode)` enumerates is an encoding set, a string's or an array's indexes
// too.
func parseEncode(encode jsval.Value, typ string, typIsString bool, role string, style jsval.Value, scope *Scope, params *paramList) *paramList {
	if params == nil {
		params = newParamList()
	}
	encode = applyEncodeDefaults(scope.meter(), encode, typ, typIsString, role, style, scope.config)
	pe := pEncode{sets: map[string]*encodeSet{}}
	c := &encCompiler{scope: scope, params: params}
	forInEach(scope.meter(), encode, func(name string, block jsval.Value) {
		pe.sets[name] = c.parseBlock(block, typ)
		pe.order = append(pe.order, name)
	})
	params.set("encoders", pe)
	return params
}

// parseBlock is encode.js's parseBlock: a channel for each key `for (name
// in block)` enumerates whose value is not null, and the outputs
// Object.keys(block) lists.
func (c *encCompiler) parseBlock(block jsval.Value, typ string) *encodeSet {
	set := &encodeSet{marktype: typ}
	if block.IsObj() {
		set.channels = make([]channel, 0, block.Len())
	}
	forInEach(c.scope.meter(), block, func(name string, enc jsval.Value) {
		if enc.IsNullish() {
			return
		}
		fn := c.compile(enc) // compiled (its fields and parameters count) whatever it is stored as
		if name == "__proto__" {
			// `channels["__proto__"] = code` sets the object's prototype to a
			// string, which sets nothing: there is no such channel
			return
		}
		set.channels = append(set.channels, channel{name: name, fn: fn, set: scene.SetterFor(name)})
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
	})
	set.output = objectKeys(c.scope.meter(), block)
	return set
}

// compile compiles one channel's value reference (a production rule list or
// an entry). Its source, which upstream compiles, is parsed whatever the
// reference, so the reference fails where upstream's does; a reference of
// the documented shape is then evaluated by closures (canonicalEntry), any
// other by that source.
func (c *encCompiler) compile(enc jsval.Value) valueFn {
	if literalReference(enc) {
		return c.entry(enc)
	}
	var src string
	var rules []jsval.Value
	if enc.IsArr() {
		rules = enc.Items()
		src = ruleSource(c.scope.work, rules)
	} else {
		src = entrySource(enc).String()
	}
	// A reference that is only a string value has the string's literal for
	// its source, which parses whatever its length (the parser's length
	// bound is for programs): a path mark's path data given inline can be
	// longer, and is charged where it is parsed into a path.
	if !enc.IsArr() && len(src) > expr.MaxLength && canonicalEntry(enc, 0) && enc.IsObj() {
		if v := enc.Get("value"); v.IsStr() && src == specStringValue(v).String() {
			return c.entry(enc)
		}
	}
	f := c.scope.parseExpression(src)
	if enc.IsArr() && canonicalRules(rules) {
		return c.rule(rules)
	}
	if !enc.IsArr() && canonicalEntry(enc, 0) {
		return c.entry(enc)
	}
	c.depExpr(f)
	return func(ev *encEval) jsval.Value { return f.evalEnc(ev) }
}

// literalReference reports a reference made only of members whose types
// make its source parse: a literal value or a field named by a plain
// identifier, a scale named by a string, numbers for band, offset, mult and
// exponent, and round. Its source (`5`, `_scale("x", datum.a)+3`) cannot fail
// to parse and depends on nothing but what canonicalEntry registers, so
// parsing it would only repeat what the types already prove. It is most of a
// chart's references.
func literalReference(enc jsval.Value) bool {
	if !enc.IsObj() {
		return false
	}
	o := enc.ObjValue()
	n := 0 // value or field
	for i := 0; i < o.Len(); i++ {
		v := o.ValueAt(i)
		switch o.KeyAt(i) {
		case "value":
			switch v.Kind() {
			case jsval.KindNum, jsval.KindBool, jsval.KindNull, jsval.KindStr:
			default:
				return false
			}
			n++
		case "field":
			if !v.IsStr() || v.StrValue() == "" {
				return false
			}
			for _, r := range v.StrValue() {
				if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
					return false
				}
			}
			n++
		case "scale":
			if !v.IsStr() {
				return false
			}
		case "band":
			if !v.IsNum() && !v.IsBool() {
				return false
			}
		case "offset", "mult", "exponent":
			if !v.IsNum() {
				return false
			}
		case "round":
			if !v.IsBool() {
				return false
			}
		default:
			return false
		}
	}
	return n <= 1 && canonicalEntry(enc, 0)
}

// rule compiles a production rule list whose rules canonicalRules accepts:
// the first entry whose test passes wins, the last may have none, and no
// match is null.
func (c *encCompiler) rule(items []jsval.Value) valueFn {
	type branch struct {
		test *exprFn
		val  valueFn
	}
	bs := make([]branch, 0, len(items))
	for _, r := range items {
		c.scope.work(1)
		val := c.entry(r)
		if t := r.Get("test"); t.IsTruthy() {
			f := c.scope.parseExpression(t.StrValue())
			c.depExpr(f)
			bs = append(bs, branch{test: f, val: val})
			continue
		}
		bs = append(bs, branch{val: val})
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

// canonicalRules reports whether rule.js's source for rules evaluates as
// rule() does: every rule a canonical entry tested by an expression string,
// and only the last untested (rule.js concatenates every rule's source, so
// what follows an untested rule is appended to its value).
func canonicalRules(rules []jsval.Value) bool {
	if len(rules) == 0 {
		return false
	}
	for i, r := range rules {
		if !canonicalEntry(r, 0) {
			return false
		}
		switch t := r.Get("test"); {
		case t.IsStr() && t.StrValue() != "":
		case !t.IsTruthy() && i == len(rules)-1:
		default:
			return false
		}
	}
	return true
}

// canonicalEntry reports whether enc is a value reference whose members
// have the types upstream documents, which entry() compiles to closures that
// evaluate as entry.js's source does. depth bounds the nesting of the
// references a property holds.
func canonicalEntry(enc jsval.Value, depth int) bool {
	if !enc.IsObj() || depth > 8 {
		return false
	}
	if !enc.Get("gradient").IsNullish() || enc.Get("color").IsTruthy() {
		return false
	}
	switch {
	case enc.Get("signal").IsTruthy():
		if !enc.Get("signal").IsStr() {
			return false
		}
	case !enc.Get("field").IsNullish():
		if !canonicalField(enc.Get("field")) {
			return false
		}
	case !enc.Get("value").IsUndefined():
		switch enc.Get("value").Kind() {
		case jsval.KindStr, jsval.KindNum, jsval.KindBool, jsval.KindNull:
		default:
			return false
		}
	}
	if s := enc.Get("scale"); !s.IsNullish() {
		// A scale reference is a name, a signal, or a field reference whose
		// value names the scale (entry.js's scaleRef: `field(scale)`).
		if !(s.IsStr() || s.IsObj() && s.Get("signal").IsStr() && s.Get("signal").IsTruthy() ||
			s.IsObj() && !s.Get("signal").IsTruthy() && canonicalFieldAt(s, depth+1)) {
			return false
		}
		if r := enc.Get("range"); !r.IsNullish() && !r.IsNum() {
			return false
		}
		if b := enc.Get("band"); b.IsTruthy() && !b.IsNum() && !b.IsBool() && !canonicalEntry(b, depth+1) {
			return false
		}
	}
	for _, k := range [...]string{"exponent", "mult", "offset"} {
		p := enc.Get(k)
		switch {
		case p.IsNullish(), p.IsNum(), p.IsBool():
		case p.IsObj():
			if !canonicalEntry(p, depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// canonicalField reports whether a field reference is a path, or a signal,
// datum, group or parent reference to a path or to a canonical reference
// (field.js's resolveField of a nested one, `object[inner]`), which fieldObj
// compiles as entry.js's source reads it.
func canonicalField(f jsval.Value) bool { return canonicalFieldAt(f, 0) }

func canonicalFieldAt(f jsval.Value, depth int) bool {
	if f.IsStr() {
		return f.StrValue() != ""
	}
	if !f.IsObj() || depth > 8 {
		return false
	}
	key := func(k jsval.Value) bool { return k.IsStr() || k.IsObj() && canonicalFieldAt(k, depth+1) }
	switch {
	case f.Get("signal").IsTruthy():
		return f.Get("signal").IsStr()
	case f.Get("group").IsTruthy() || f.Get("parent").IsTruthy():
		k := f.Get("parent")
		if !k.IsTruthy() {
			k = f.Get("group")
		}
		l := f.Get("level")
		return key(k) && (l.IsUndefined() || l.IsNum())
	case f.Get("datum").IsTruthy():
		return key(f.Get("datum"))
	}
	return false
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
	return func(ev *encEval) jsval.Value { return ev.ctx.view.jsAdd(a(ev), b(ev)) }
}

func (c *encCompiler) entry(enc jsval.Value) valueFn {
	c.scope.work(1)
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
	}
	if cur.a == nil && cur.b == nil {
		cur = term{a: constFn(jsval.Null)}
	}

	if !enc.Get("exponent").IsNullish() {
		inner, exp := cur.fn(), c.property(enc.Get("exponent"))
		cur = term{a: func(ev *encEval) jsval.Value {
			return jsval.Num(jsmath.Pow(jsval.ToNumber(inner(ev)), jsval.ToNumber(exp(ev))))
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
			return jsval.Num(jsmath.Round(jsval.ToNumber(inner(ev))))
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
		// The reference is the source `(signal)` as the first argument of
		// _scale(...): parentheses leave no node, so a signal that is a
		// literal (`null`, `'x'`, `2`) is the Literal scaleVisitor registers
		// by name, a scale named String(value) (none for null), not the
		// indirect lookup that depends on every scale.
		if ast := f.prog.AST(); ast != nil && ast.Kind == expr.KindLiteral && ast.Regex == nil {
			c.scaleDeps(ast.Value, true)
		} else {
			c.scaleDeps(jsval.Undefined, false)
		}
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
			return lerp(ev.ctx.view, rng, f)
		}}
	}
	var cur term
	if haveValue {
		cur = term{a: func(ev *encEval) jsval.Value { return ev.ctx.encScale(scale(ev), value(ev)) }}
	}
	// `if (enc.band)`: a band of 0 adds no bandwidth term (C94).
	if band := enc.Get("band"); band.IsTruthy() {
		bw := func(ev *encEval) jsval.Value { return jsval.Num(ev.ctx.scaleBandwidth(ev.ctx.encName(scale(ev)))) }
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
		// the helper is a function of datum alone
		v, err := ev.ctx.evalProgram(prog, jsval.Obj(d), jsval.Undefined, jsval.Undefined, varDatum)
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
	c.scope.work(1)
	if !ref.IsObj() {
		perr("Invalid field reference: %s", quote(ref))
	}
	switch {
	case ref.Get("signal").IsTruthy():
		f := c.scope.parseExpression(ref.Get("signal").AsString())
		c.depExpr(f)
		return func(ev *encEval) jsval.Value {
			d := ev.datum
			if d.IsNullish() {
				perrThrow("Cannot read properties of " + nullishName(d) + " (reading '" + ev.ctx.view.walk.String(f.evalEnc(ev)) + "')")
			}
			return propOf(ev.ctx.view.walk, d, f.evalEnc(ev))
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
			// item.mark.group.mark.group…: past the root group, whose mark
			// has no group, the next read is of undefined
			g := ev.item
			for i := 0; i < level; i++ {
				if g == nil {
					perrThrow("Cannot read properties of undefined (reading 'mark')")
				}
				if g.Mark == nil {
					g = nil
					continue
				}
				g = g.Mark.Group
			}
			p := path(ev)
			if g == nil {
				read := "datum"
				if !parent && len(p) > 0 {
					read = ev.ctx.view.jsString(p[0])
				}
				perrThrow("Cannot read properties of undefined (reading '" + read + "')")
			}
			if parent {
				return walkPath(ev.ctx.view.walk, g.Datum, p)
			}
			if len(p) == 1 {
				if k := p[0].AsString(); k != "mark" && k != "items" {
					return getItemProp(g, k)
				}
			}
			return walkPath(ev.ctx.view.walk, ev.ctx.view.itemAsValue(g), p)
		}
	case ref.Get("datum").IsTruthy():
		// A constant path is the common case: compile it once.
		if d := ref.Get("datum"); d.IsStr() {
			if _, err := jsval.SplitFieldPath(d.StrValue()); err != nil {
				perr("%s", err.Error())
			}
			if !hasQuirkySegment(d.StrValue()) {
				// datum["a"]["b"]: a missing step is a TypeError, as the
				// generated source's member chain raises
				get := transforms.FieldOfStrict(d.StrValue()).Get
				return func(ev *encEval) jsval.Value { return get(ev.datum) }
			}
		}
		path := c.fieldPath(ref.Get("datum"))
		return func(ev *encEval) jsval.Value { return walkPath(ev.ctx.view.walk, ev.datum, path(ev)) }
	}
	perr("Invalid field reference: %s", quote(ref))
	return nil
}

// fieldPath resolves the field part of a reference to path segments at
// evaluation time: a literal path is split once, a nested reference is
// evaluated and used as a single key.
func (c *encCompiler) fieldPath(key jsval.Value) func(ev *encEval) []jsval.Value {
	if key.IsStr() {
		segs, err := jsval.SplitFieldPath(key.StrValue())
		if err != nil {
			perr("%s", err.Error())
		}
		vals := make([]jsval.Value, len(segs))
		fns := make([]valueFn, len(segs))
		quirky := false
		for i, s := range segs {
			c.scope.work(1)
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

// walkPath reads a chain of keys from a value as the generated member chain
// `v["a"]["b"]` does: a step of null or undefined is a TypeError.
func walkPath(w jsval.Walker, v jsval.Value, path []jsval.Value) jsval.Value {
	for _, k := range path {
		if v.IsNullish() {
			perrThrow("Cannot read properties of " + nullishName(v) + " (reading '" + w.String(k) + "')")
		}
		v = propOf(w, v, k)
	}
	return v
}

// perrThrow raises a TypeError of the evaluated encoder, as the generated
// source's throws: the run's error, which upstream's dataflow logs.
func perrThrow(msg string) { panic(&jsval.Thrown{Name: "TypeError", Msg: msg}) }

// propOf is `v[key]` of a value that is not null or undefined
// (jsval.Member: an object's own or inherited member, an array's or a
// string's length and elements, the prototype methods of any value), the
// key's String() made with the call's Walker w.
func propOf(w jsval.Walker, v, key jsval.Value) jsval.Value {
	if v.IsNullish() {
		return jsval.Undefined
	}
	if v.IsArr() && key.IsNum() {
		// in range as a float: converting a huge index or Infinity to an
		// int is platform-dependent
		f := key.NumValue()
		if f >= 0 && f == math.Trunc(f) && f < float64(v.Len()) {
			return v.Index(int(f))
		}
	}
	k, ok := key.Strict()
	if !ok {
		k = w.String(key)
	}
	return jsval.Member(v, k)
}

// propOfString is propOf for a key that is a string.
func propOfString(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		return jsval.Undefined
	}
	return jsval.Member(v, key)
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

// -- JavaScript arithmetic ---------------------------------------------------

// jsAdd is the `+` operator of the generated encoder source: string
// concatenation when either side's primitive is a string (a string, an
// array, an object, and a date, whose default primitive is its text), with
// String() as the view writes it (the call's Walker, its zone); numeric
// addition otherwise.
func (v *runView) jsAdd(a, b jsval.Value) jsval.Value {
	if a.IsNum() && b.IsNum() {
		return jsval.Num(a.NumValue() + b.NumValue())
	}
	concat := func(x jsval.Value) bool {
		switch x.Kind() {
		case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
			return true
		}
		return false
	}
	if concat(a) || concat(b) {
		return jsval.Str(v.jsString(a) + v.jsString(b))
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

// lerp is the generated `lerp(_range(scale), +enc.range)`, vega-util's lerp:
// `lo + f * (hi - lo)`, whose `+` concatenates a string range's first value
// (C93: "red" and NaN make "redNaN"), as v.jsAdd does.
func lerp(v *runView, rng jsval.Value, frac float64) jsval.Value {
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
	return v.jsAdd(lo, jsval.Num(float64(frac*(h-l))))
}

// -- defaults ----------------------------------------------------------------

// hasEncodeKey is util.js's has(key, encode): `encode && ((encode.enter &&
// encode.enter[key]) || (encode.update && encode.update[key]))`.
func hasEncodeKey(key string, encode jsval.Value) bool {
	if !encode.IsTruthy() {
		return false
	}
	for _, set := range [...]string{"enter", "update"} {
		if b := specIndex(encode, set); b.IsTruthy() && specIndex(b, key).IsTruthy() {
			return true
		}
	}
	return false
}

// applyEncodeDefaults is encode/defaults.js: it adds config-derived defaults
// to an encode block: the group config for the root frame, mark config and
// per-type config for plain marks, and the config.style entries named by
// the mark's `style`, in that order of increasing precedence. A default that
// is a signal goes to `update` (it may change), everything else to `enter`.
// The result is a copy of encode (`extend({}, encode)`, which enumerates a
// string's or an array's indexes) with those sets extended.
func applyEncodeDefaults(m *budget.Meter, encode jsval.Value, typ string, typIsString bool, role string, style jsval.Value, config jsval.Value) jsval.Value {
	defaults := jsval.NewObject(8)
	setDefault := func(key string, v jsval.Value) {
		if v.IsTruthy() && specIndex(v, "signal").IsTruthy() {
			defaults.Set(key, obj("signal", specIndex(v, "signal")))
		} else {
			defaults.Set(key, obj("value", v))
		}
	}

	if typIsString && typ == "text" && !specIndex(config, "lineBreak").IsNullish() && !hasEncodeKey("lineBreak", encode) {
		setDefault("lineBreak", specIndex(config, "lineBreak"))
	}

	if role == "legend" || strings.HasPrefix(role, "axis") {
		role = ""
	}

	var props jsval.Value
	switch role {
	case "frame":
		props = specIndex(config, "group")
	case "mark":
		merged := jsval.NewObject(8)
		extendObj(m, merged, specIndex(config, "mark"))
		extendObj(m, merged, specIndex(config, typ))
		props = jsval.Obj(merged)
	}
	forInEach(m, props, func(key string, v jsval.Value) {
		skip := hasEncodeKey(key, encode) ||
			((key == "fill" || key == "stroke") && (hasEncodeKey("fill", encode) || hasEncodeKey("stroke", encode)))
		if !skip {
			setDefault(key, v)
		}
	})

	styles := specIndex(config, "style")
	for _, name := range arrayOf(style) {
		charge(m, 1)
		if !styles.IsTruthy() {
			continue
		}
		key, ok := name.PropertyKey(jsval.MaxStringLength)
		if !ok {
			continue
		}
		forInEach(m, specIndex(styles, key), func(key string, v jsval.Value) {
			if !hasEncodeKey(key, encode) {
				setDefault(key, v)
			}
		})
	}

	out := jsval.NewObject(4)
	extendObj(m, out, encode)
	enter := jsval.NewObject(defaults.Len())
	var update *jsval.Object
	for i := 0; i < defaults.Len(); i++ {
		charge(m, 1)
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
	extendObj(m, enter, out.Lookup("enter"))
	out.Set("enter", jsval.Obj(enter))
	if update != nil {
		extendObj(m, update, out.Lookup("update"))
		out.Set("update", jsval.Obj(update))
	}
	return jsval.Obj(out)
}

// specIndex is `v[key]` of a specification value v that is not null or
// undefined (jsval.Member), and undefined for those.
func specIndex(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		return jsval.Undefined
	}
	return jsval.Member(v, key)
}

func hasQuirkySegment(path string) bool {
	for _, seg := range jsval.ParseFieldPath(path) {
		if quirkyLiteral(seg) {
			return true
		}
	}
	return false
}
