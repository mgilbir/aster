package vega

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// This file reproduces the JavaScript source vega-parser's encode/entry.js and
// encode/rule.js generate from a value reference, character for character,
// with the JavaScript semantics they apply to the specification's values:
// truthiness tests, `!= null` tests, template literals (String() of a value),
// unary plus, and the TypeError a property read of null raises. The parser
// compiles that source (vega-functions' parseExpression), so where the
// specification gives a value of an unexpected type upstream's result is the
// source's: `"mult": "x"` is the code `*x`, a reference to a signal named x;
// a rule list `["a", "b"]` is the code `nullnull`, a signal name too; a
// reference `null` in a rule list fails reading its `gradient`.
//
// encCompiler compiles a reference whose members have the types upstream's
// documentation gives them into Go closures (parse_encode.go), which evaluate
// as the source does; any other reference is compiled from this source. The
// source of every reference is parsed either way, so a reference fails where
// upstream's would.

// srcValue is the `value` entry.js builds: undefined, null, or source text.
// Upstream's value can also hold the number or boolean stringValue returns for
// one; every use of it is a String() of it, so it is kept as that text.
type srcValue struct {
	text        string
	null, undef bool
}

var (
	srcUndefined = srcValue{undef: true}
	srcNull      = srcValue{null: true}
)

func srcText(s string) srcValue { return srcValue{text: s} }

// String is the value in a template literal or a concatenation.
func (v srcValue) String() string {
	switch {
	case v.undef:
		return "undefined"
	case v.null:
		return "null"
	}
	return v.text
}

// joinText is the value as Array.prototype.join writes it: null and
// undefined as nothing.
func (v srcValue) joinText() string {
	if v.undef || v.null {
		return ""
	}
	return v.text
}

// specGet is `v[key]` of a specification value (jsval.Member), with the
// TypeError a read of null or undefined raises.
func specGet(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		perr("Cannot read properties of %s (reading '%s')", nullishName(v), key)
	}
	return jsval.Member(v, key)
}

func nullishName(v jsval.Value) string {
	if v.IsNull() {
		return "null"
	}
	return "undefined"
}

// specText is String(v) of a specification literal, as a template literal
// writes it.
func specText(v jsval.Value) string { return v.AsString() }

// specStringValue is vega-util's stringValue(v) as text: JSON for strings,
// objects and arrays (an array's elements by stringValue in turn), String(v)
// for any other value.
func specStringValue(v jsval.Value) srcValue {
	switch v.Kind() {
	case jsval.KindUndefined:
		return srcUndefined
	case jsval.KindNull:
		return srcNull
	}
	return srcText(stringValue(v))
}

// entrySource is entry.js's entry(enc).
func entrySource(enc jsval.Value) srcValue {
	if !specGet(enc, "gradient").IsNullish() {
		return srcText(gradientSource(enc))
	}
	value := srcUndefined
	switch {
	case specGet(enc, "signal").IsTruthy():
		value = srcText("(" + specText(specGet(enc, "signal")) + ")")
	case specGet(enc, "color").IsTruthy():
		value = colorSource(specGet(enc, "color"))
	case !specGet(enc, "field").IsNullish():
		value = srcText(fieldSource(specGet(enc, "field")))
	case !specGet(enc, "value").IsUndefined():
		value = specStringValue(specGet(enc, "value"))
	}
	if !specGet(enc, "scale").IsNullish() {
		value = scaleSource(enc, value)
	}
	if value.undef {
		value = srcNull
	}
	if e := specGet(enc, "exponent"); !e.IsNullish() {
		value = srcText("pow(" + value.String() + "," + propertySource(e) + ")")
	}
	if m := specGet(enc, "mult"); !m.IsNullish() {
		value = srcText(value.String() + "*" + propertySource(m))
	}
	if o := specGet(enc, "offset"); !o.IsNullish() {
		value = srcText(value.String() + "+" + propertySource(o))
	}
	if specGet(enc, "round").IsTruthy() {
		value = srcText("round(" + value.String() + ")")
	}
	return value
}

// ruleSource is rule.js's rule(enc): every rule's source, a tested one as
// `(test)?value:`, concatenated, and `null` after a trailing test.
func ruleSource(m func(int), rules []jsval.Value) string {
	var code strings.Builder
	for _, r := range rules {
		m(1)
		value := entrySource(r)
		if t := specGet(r, "test"); t.IsTruthy() {
			code.WriteString("(" + specText(t) + ")?" + value.String() + ":")
		} else {
			code.WriteString(value.String())
		}
	}
	s := code.String()
	if strings.HasSuffix(s, ":") {
		s += "null"
	}
	return s
}

// scaleRefSource is entry.js's scaleRef(scale).
func scaleRefSource(scale jsval.Value) string {
	switch {
	case scale.IsStr():
		return stringValue(scale)
	case specGet(scale, "signal").IsTruthy():
		return "(" + specText(specGet(scale, "signal")) + ")"
	}
	return fieldSource(scale)
}

// gradientSource is entry.js's gradient(enc).
func gradientSource(enc jsval.Value) string {
	var args []srcValue
	for _, k := range [...]string{"start", "stop", "count"} {
		v := specGet(enc, k)
		if v.IsNullish() {
			args = append(args, srcNull)
		} else {
			args = append(args, specStringValue(v))
		}
	}
	for len(args) > 0 && (args[len(args)-1].null || args[len(args)-1].undef) {
		args = args[:len(args)-1]
	}
	var b strings.Builder
	b.WriteString("gradient(")
	b.WriteString(scaleRefSource(specGet(enc, "gradient")))
	for _, a := range args {
		b.WriteByte(',')
		b.WriteString(a.joinText())
	}
	b.WriteByte(')')
	return b.String()
}

// colorSource is entry.js's color(enc).
func colorSource(enc jsval.Value) srcValue {
	var typ string
	var names [3]string
	switch {
	case specGet(enc, "c").IsTruthy():
		typ, names = "hcl", [3]string{"h", "c", "l"}
	case specGet(enc, "h").IsTruthy() || specGet(enc, "s").IsTruthy():
		typ, names = "hsl", [3]string{"h", "s", "l"}
	case specGet(enc, "l").IsTruthy() || specGet(enc, "a").IsTruthy():
		typ, names = "lab", [3]string{"l", "a", "b"}
	case specGet(enc, "r").IsTruthy() || specGet(enc, "g").IsTruthy() || specGet(enc, "b").IsTruthy():
		typ, names = "rgb", [3]string{"r", "g", "b"}
	default:
		return srcNull
	}
	var b strings.Builder
	b.WriteString("(" + typ + "(")
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		// [x, y, z].map(entry).join(','): entry of a missing component reads
		// its gradient from undefined, and a null entry joins as nothing.
		b.WriteString(entrySource(specGet(enc, n)).joinText())
	}
	b.WriteString(")+'')")
	return srcText(b.String())
}

// propertySource is entry.js's property(p): an object (an array too) is a
// value reference in parentheses, anything else its String(), spliced into
// the source as it is.
func propertySource(p jsval.Value) string {
	if p.IsObj() || p.IsArr() {
		return "(" + entrySource(p).String() + ")"
	}
	return specText(p)
}

// fieldSource is entry.js's field(ref).
func fieldSource(ref jsval.Value) string {
	if ref.IsObj() || ref.IsArr() {
		return resolveFieldSource(ref, 0)
	}
	return resolveFieldSource(obj("datum", ref), 0)
}

// maxFieldNesting bounds the recursion of nested field references, which
// the specification's depth already bounds (jsval.MaxJSONDepth).
const maxFieldNesting = 1024

// resolveFieldSource is entry.js's resolveField(ref).
func resolveFieldSource(ref jsval.Value, depth int) string {
	if depth > maxFieldNesting {
		perrLimit("field reference nested too deep")
	}
	var object string
	var field jsval.Value
	sig := specGet(ref, "signal")
	switch {
	case sig.IsTruthy():
		object, field = "datum", sig
	case specGet(ref, "group").IsTruthy() || specGet(ref, "parent").IsTruthy():
		// Math.max(1, ref.level || 1): NaN for a level that is no number,
		// and then no group step at all.
		level := 1.0
		if l := specGet(ref, "level"); l.IsTruthy() {
			level = jsval.ToNumber(l)
			if level == level && level < 1 {
				level = 1
			}
		}
		if level > maxGroupLevel {
			perrLimit("field reference level %g too deep", level)
		}
		var b strings.Builder
		b.WriteString("item")
		for n := level; n > 0; n-- { // while (level-- > 0)
			b.WriteString(".mark.group")
		}
		if p := specGet(ref, "parent"); p.IsTruthy() {
			field = p
			b.WriteString(".datum")
		} else {
			field = specGet(ref, "group")
		}
		object = b.String()
	case specGet(ref, "datum").IsTruthy():
		object, field = "datum", specGet(ref, "datum")
	default:
		perr("Invalid field reference: %s", stringValue(ref))
	}
	var f string
	switch {
	case sig.IsTruthy():
		f = specText(field)
	case field.IsStr():
		segs, err := jsval.SplitFieldPath(field.StrValue())
		if err != nil {
			perr("%s", err.Error())
		}
		parts := make([]string, len(segs))
		for i, s := range segs {
			parts[i] = stringValue(jsval.Str(s))
		}
		f = strings.Join(parts, "][")
	default:
		f = resolveFieldSource(field, depth+1)
	}
	return object + "[" + f + "]"
}

// maxGroupLevel bounds a field reference's `level`: upstream writes
// `.mark.group` that many times, so a level past any real nesting is a
// string past any real chart (groups nest at most maxContextDepth deep).
const maxGroupLevel = 1 << 16

// scaleSource is entry.js's scale(enc, value).
func scaleSource(enc jsval.Value, value srcValue) srcValue {
	scale := scaleRefSource(specGet(enc, "scale"))
	if r := specGet(enc, "range"); !r.IsNullish() {
		return srcText("lerp(_range(" + scale + "), " + jsval.Num(jsval.ToNumber(r)).AsString() + ")")
	}
	if !value.undef {
		value = srcText("_scale(" + scale + ", " + value.String() + ")")
	}
	if band := specGet(enc, "band"); band.IsTruthy() {
		var b strings.Builder
		if !value.undef { // `value ? value + '+' : ''`: a defined value here is source text
			b.WriteString(value.text + "+")
		}
		b.WriteString("_bandwidth(" + scale + ")")
		if jsval.ToNumber(band) != 1 {
			b.WriteString("*" + propertySource(band))
		}
		value = srcText(b.String())
		if specGet(enc, "extra").IsTruthy() {
			value = srcText("(datum.extra ? _scale(" + scale + ", datum.extra.value) : " + value.text + ")")
		}
	}
	if value.undef || value.null {
		value = srcText("0")
	}
	return value
}
