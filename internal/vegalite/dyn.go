package vegalite

import (
	"bytes"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The compiler manipulates specifications the way upstream does: as loosely
// typed, insertion-ordered objects. The output is compared byte for byte with
// upstream's JSON.stringify, so property order (including the position of a key
// whose value is later overwritten) is part of the contract. These helpers give
// the JavaScript idioms upstream relies on (object spread, omit, deep equality,
// `??`) a compact Go spelling.

type (
	// Value is a dynamic JavaScript-like value.
	Value = jsval.Value
	// Object is an insertion-ordered object.
	Object = jsval.Object
)

var undef = jsval.Undefined

// val converts common Go values to a Value.
func val(x any) Value {
	switch t := x.(type) {
	case nil:
		return jsval.Undefined
	case Value:
		return t
	case *Object:
		if t == nil {
			return jsval.Undefined
		}
		return jsval.Obj(t)
	case string:
		return jsval.Str(t)
	case bool:
		return jsval.Bool(t)
	case int:
		return jsval.Int(t)
	case float64:
		return jsval.Num(t)
	case []Value:
		return jsval.Arr(t)
	case []string:
		return strsVal(t)
	case []any:
		items := make([]Value, len(t))
		for i, e := range t {
			items[i] = val(e)
		}
		return jsval.Arr(items)
	}
	panic("vegalite: unsupported value type")
}

func strsVal(ss []string) Value {
	items := make([]Value, len(ss))
	for i, s := range ss {
		items[i] = jsval.Str(s)
	}
	return jsval.Arr(items)
}

// mk builds an object from alternating key/value pairs. Nil (undefined)
// values are kept as keys with an undefined value, as `{k: undefined}` is in
// JavaScript; they are dropped when the result is serialized.
func mk(kv ...any) *Object {
	o := jsval.NewObject(len(kv) / 2)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), val(kv[i+1]))
	}
	return o
}

// mkv is mk wrapped as a Value.
func mkv(kv ...any) Value { return jsval.Obj(mk(kv...)) }

func arr(items ...any) Value {
	out := make([]Value, len(items))
	for i, e := range items {
		out[i] = val(e)
	}
	return jsval.Arr(out)
}

func sig(expr string) Value { return mkv("signal", expr) }

func objVal(o *Object) Value {
	if o == nil {
		return undef
	}
	return jsval.Obj(o)
}

// isObject is vega-util's isObject: any non-null object, arrays included.
func isObject(v Value) bool { return v.IsObj() || v.IsArr() }

// hasProperty is upstream's hasProperty: v is an object whose key is defined.
func hasProperty(v Value, key string) bool {
	if !v.IsObj() {
		return false
	}
	x, ok := v.ObjValue().Get(key)
	return ok && !x.IsUndefined()
}

// isSignalRef is hasProperty(o, 'signal') in 6.4; 5.8 tests !!o?.signal,
// so a signal of "", 0, false or null is a plain value there.
func isSignalRef(cc *compileCtx, v Value) bool {
	if cc != nil && cc.v5 {
		return !v.IsNullish() && jsProp(v, "signal").IsTruthy()
	}
	return hasProperty(v, "signal")
}

// isExprRef is hasProperty(o, 'expr') in 6.4; 5.8 tests !!o?.expr, so
// an expr that is falsy is not one.
func isExprRef(cc *compileCtx, v Value) bool {
	if cc != nil && cc.v5 {
		return v.IsObj() && v.Get("expr").IsTruthy()
	}
	return hasProperty(v, "expr")
}

// cloneObj is `{...o}`.
// cloneObj is {...o}: o's own keys in a new plain object (whose prototype
// is Object.prototype, whatever o's is).
func cloneObj(o *Object) *Object {
	if o == nil {
		return jsval.NewObject(0)
	}
	c := o.Clone()
	scope := o.ConversionScope()
	c.SetPrototype(undef, nil)
	if scope != nil && c.Has("toString") {
		c.SetConversionScope(scope) // the copy holds o's toString too
	}
	return c
}

// spread copies the keys of src (an object; anything else is ignored) into dst,
// overwriting in place like JavaScript's spread. A new array-index key belongs
// among dst's leading index keys; rather than moving the keys after it on
// every insert (cubic over an object's keys, C4), the keys are appended and
// put in JavaScript's order once at the end, which gives the same order (see
// jsOrderKeys). It is linear in its input; spreadCC charges it.
func spread(dst *Object, srcs ...Value) *Object { return spreadCC(nil, dst, srcs...) }

// spreadCC is spread charging cc a step per key, for the spreads of a
// specification-sized object (the config) that every view makes.
func spreadCC(cc *compileCtx, dst *Object, srcs ...Value) *Object {
	reorder := false
	put := func(k string, v Value) {
		cc.work(1)
		if !reorder && len(k) > 0 && k[0] >= '0' && k[0] <= '9' && !dst.Has(k) {
			if n, ok := arrayIndex(k); ok && !appendKeepsJSOrder(dst, n) {
				reorder = true
			}
		}
		dst.Set(k, v)
	}
	for _, s := range srcs {
		if s.IsStr() {
			i := 0
			for _, r := range s.StrValue() {
				if r >= 0x10000 {
					r -= 0x10000
					put(strconv.Itoa(i), jsval.Str(string(rune(0xD800+(r>>10)))))
					put(strconv.Itoa(i+1), jsval.Str(string(rune(0xDC00+(r&0x3FF)))))
					i += 2
					continue
				}
				put(strconv.Itoa(i), jsval.Str(string(r)))
				i++
			}
			continue
		}
		if s.IsArr() {
			for i, it := range s.Items() {
				put(strconv.Itoa(i), it)
			}
			continue
		}
		if s.IsObj() {
			so := s.ObjValue()
			for i := 0; i < so.Len(); i++ {
				put(so.KeyAt(i), so.ValueAt(i))
			}
		}
	}
	if reorder {
		jsOrderKeys(cc, dst)
	}
	return dst
}

// merged is `{...a, ...b, ...}`.
func merged(srcs ...Value) *Object { return mergedCC(nil, srcs...) }

// mergedCC is merged charging cc (spreadCC).
func mergedCC(cc *compileCtx, srcs ...Value) *Object {
	n := 0
	for _, s := range srcs {
		n += s.Len()
	}
	cc.alloc(objectBytes(n))
	return spreadCC(cc, jsval.NewObject(n), srcs...)
}

// omit is upstream's omit: a shallow copy without the given keys.
func omit(v Value, keys ...string) *Object {
	c := jsval.NewObject(v.Len())
	if !v.IsObj() {
		// {...v} of a string or array yields index keys.
		spread(c, v)
		for _, k := range keys {
			c.Delete(k)
		}
		return c
	}
	o := v.ObjValue()
	for i := 0; i < o.Len(); i++ {
		k := o.KeyAt(i)
		skip := false
		for _, x := range keys {
			if x == k {
				skip = true
				break
			}
		}
		if !skip {
			c.Set(k, o.ValueAt(i))
		}
	}
	return c
}

// pick is upstream's pick: only the own defined-or-not keys listed.
func pick(v Value, keys ...string) *Object {
	c := jsval.NewObject(len(keys))
	if !v.IsObj() {
		return c
	}
	o := v.ObjValue()
	for _, k := range keys {
		if x, ok := o.Get(k); ok {
			c.Set(k, x)
		}
	}
	return c
}

// keysOf is Object.keys for objects and index strings for arrays.
func keysOf(v Value) []string {
	if v.IsObj() {
		return v.ObjValue().Keys()
	}
	if v.IsArr() {
		out := make([]string, v.Len())
		for i := range out {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	return nil
}

// isEmptyObj is upstream's isEmpty: no own keys (undefined-valued keys count).
func isEmptyObj(v Value) bool { return len(keysOf(v)) == 0 }

// firstDefined is upstream's getFirstDefined.
func firstDefined(vs ...Value) Value {
	for _, v := range vs {
		if !v.IsUndefined() {
			return v
		}
	}
	return undef
}

// coalesce is `a ?? b ?? ...`.
func coalesce(vs ...Value) Value {
	for _, v := range vs {
		if !v.IsNullish() {
			return v
		}
	}
	if len(vs) > 0 {
		return vs[len(vs)-1]
	}
	return undef
}

// or is JavaScript's `||` chain.
func or(vs ...Value) Value {
	for _, v := range vs {
		if v.IsTruthy() {
			return v
		}
	}
	if len(vs) > 0 {
		return vs[len(vs)-1]
	}
	return undef
}

// deepEqual is upstream's deepEqual (key order is irrelevant). Lazy signals
// compare by their current expression.
//
// Each value visited is a step of cc's work, so a comparison of large values
// polls the deadline. The values may share their parts (the compiler reuses
// a config's or a definition's objects), and a walk of a shared part is
// repeated wherever it is reached, exponentially in the depth of sharing:
// past deepEqualMemoAfter containers, a pair of containers found equal is
// remembered and not walked again, which bounds the walk by the number of
// distinct pairs. The answer is the same.
func deepEqual(cc *compileCtx, a, b Value) bool {
	d := deepEqualWalk{cc: cc}
	defer func() { cc.free(d.charged) }() // the memo is scratch
	return d.eq(a, b)
}

// deepEqualMemoAfter is how many containers a comparison visits before it
// remembers the pairs it found equal: a small value is compared without
// allocating.
const deepEqualMemoAfter = 64

type deepEqualWalk struct {
	cc    *compileCtx
	seen  int
	equal map[[2]jsval.InternKey]bool
	// charged is what the memo's entries were charged (deepEqual gives it
	// back).
	charged int64
}

// scratchEntryBytes is what one entry of a scratch map keyed by a value's
// identity costs: its key (two for a pair), its value and the map's share
// of a bucket.
const scratchEntryBytes = 48

func (d *deepEqualWalk) eq(a, b Value) bool {
	d.cc.work(1)
	if a.Kind() != b.Kind() {
		return false
	}
	var key [2]jsval.InternKey
	switch a.Kind() {
	case jsval.KindArr, jsval.KindObj:
		if jsval.SameRef(a, b) {
			return true
		}
		d.seen++
		if d.seen > deepEqualMemoAfter {
			key = [2]jsval.InternKey{a.MapKey(), b.MapKey()}
			if d.equal[key] {
				return true
			}
		}
	}
	switch a.Kind() {
	case jsval.KindArr:
		// Upstream compares the elements from the last.
		x, y := a.Items(), b.Items()
		if len(x) != len(y) {
			return false
		}
		for i := len(x) - 1; i >= 0; i-- {
			if !d.eq(x[i], y[i]) {
				return false
			}
		}
	case jsval.KindObj:
		x, y := a.ObjValue(), b.ObjValue()
		_, lx := lazyFn(x)
		_, ly := lazyFn(y)
		if !lx && !ly {
			if same, decided := d.objectMethods(a, b); decided {
				return same
			}
		}
		if fx, ok := lazyFn(x); ok {
			a = mkv("signal", fx())
			x = a.ObjValue()
		}
		if fy, ok := lazyFn(y); ok {
			b = mkv("signal", fy())
			y = b.ObjValue()
		}
		if x.Len() != y.Len() {
			return false
		}
		// Every key of a in b, then the values from the last key, as
		// upstream's loops go.
		for i := 0; i < x.Len(); i++ {
			if !y.Has(x.KeyAt(i)) {
				return false
			}
		}
		for i := x.Len() - 1; i >= 0; i-- {
			if !d.eq(x.ValueAt(i), y.Lookup(x.KeyAt(i))) {
				return false
			}
		}
	default:
		return jsval.Equal(a, b)
	}
	if d.seen > deepEqualMemoAfter {
		if d.equal == nil {
			d.equal = map[[2]jsval.InternKey]bool{}
		}
		d.cc.alloc(2 * scratchEntryBytes)
		d.charged += 2 * scratchEntryBytes
		d.equal[key] = true
	}
	return true
}

// objectMethods is what upstream's deepEqual (fast-deep-equal's, adapted in
// 6.4) decides from two objects' methods before their keys: their
// constructors (6.4 compares their names, reading a.constructor.name, so a
// constructor of null or undefined, as a null prototype leaves, is a
// TypeError; 5.8 compares them with ===), then a valueOf or a toString that
// is not Object.prototype's, which it calls: held as data (or missing, under
// a null prototype) it is no function, a TypeError. decided is false when
// the comparison goes on to the keys.
func (d *deepEqualWalk) objectMethods(a, b Value) (same, decided bool) {
	if plainMethods(a.ObjValue()) && plainMethods(b.ObjValue()) {
		return false, false // Object.prototype's: the keys decide
	}
	ca, cb := jsProp(a, "constructor"), jsProp(b, "constructor")
	if d.cc != nil && d.cc.v5 {
		if !strictEq(ca, cb) {
			return false, true
		}
	} else if !strictEq(constructorName(ca), constructorName(cb)) {
		return false, true
	}
	for _, m := range [...]string{"valueOf", "toString"} {
		f := jsProp(a, m)
		if own, _ := jsval.Inherited(m); jsval.SameRef(f, own) {
			continue
		}
		if !jsval.IsFunction(f) {
			throw("a.%s is not a function", m)
		}
		// Another of Object.prototype's functions under that name (a
		// value no specification holds): upstream would call it; the
		// keys decide here.
		return false, false
	}
	return false, false
}

// plainMethods says whether o's constructor, valueOf and toString are
// Object.prototype's: no prototype of its own, and none of them held.
func plainMethods(o *Object) bool {
	return o.Prototype().IsUndefined() && !o.Has("constructor") && !o.Has("valueOf") && !o.Has("toString")
}

// constructorName is c.name for a constructor read from an object.
func constructorName(c Value) Value {
	if c.IsNullish() {
		throw("Cannot read properties of %s (reading 'name')", nullishName(c))
	}
	if jsval.IsFunction(c) {
		s := c.AsString() // "function Object() { [native code] }"
		if i := strings.IndexByte(s, '('); strings.HasPrefix(s, "function ") && i > 0 {
			return jsval.Str(s[len("function "):i])
		}
	}
	return jsProp(c, "name")
}

// deepClone is structuredClone for JSON-like values. The clone's keys are in
// JavaScript's order (array-index keys first, ascending), which a parsed
// document's need not be: they are ordered once per object.
func deepClone(cc *compileCtx, v Value) Value {
	cc.work(1)
	switch v.Kind() {
	case jsval.KindArr:
		items := v.Items()
		cc.alloc(arrayBytes(len(items)))
		out := make([]Value, len(items))
		for i, it := range items {
			out[i] = deepClone(cc, it)
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		o := v.ObjValue()
		cc.alloc(objectBytes(o.Len()))
		c := jsval.NewObject(o.Len())
		digit := false // a key that may be an array index
		for i := 0; i < o.Len(); i++ {
			k := o.KeyAt(i)
			digit = digit || (len(k) > 0 && k[0] >= '0' && k[0] <= '9')
			c.Set(k, deepClone(cc, o.ValueAt(i)))
		}
		if digit {
			jsOrderKeys(cc, c)
		}
		if cc != nil && c.Has("toString") {
			// String of it is a TypeError in JavaScript: its toString is
			// no function.
			c.SetConversionScope(cc.conv)
		}
		return jsval.Obj(c)
	}
	return v
}

// stringify is upstream's util.stringify: JSON with sorted keys, used as a
// hash key for structural comparison.
func stringify(cc *compileCtx, v Value) string {
	b := newSbuf(cc)
	stringifyTo(cc, &b, v)
	return b.String()
}

// stringifyText is `${stringify(v)}`: stringify's text, or "undefined"
// for a value JSON has no text for.
func stringifyText(cc *compileCtx, v Value) string {
	b := newSbuf(cc)
	if !stringifyTo(cc, &b, v) {
		return "undefined"
	}
	return b.String()
}

func stringifyTo(cc *compileCtx, b *sbuf, v Value) bool {
	cc.work(1)
	switch v.Kind() {
	case jsval.KindUndefined:
		return false
	case jsval.KindNull:
		b.WriteString("null")
	case jsval.KindNum, jsval.KindTimestamp:
		f := v.NumValue()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsval.JSNumberString(f))
		}
	case jsval.KindArr:
		b.PutByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.PutByte(',')
			}
			if !stringifyTo(cc, b, it) {
				b.WriteString("null")
			}
		}
		b.PutByte(']')
	case jsval.KindObj:
		if jsval.IsFunction(v) {
			return false // typeof 'function': JSON.stringify gives undefined
		}
		o := v.ObjValue()
		if fn, ok := lazyFn(o); ok {
			b.WriteString(`{"signal":` + string(jsval.AppendJSON(nil, jsval.Str(fn()))) + "}")
			return true
		}
		ks := append([]string(nil), o.Keys()...)
		cc.work(int64(len(ks))) // the sort, past a step a key
		sort.Strings(ks)
		b.PutByte('{')
		first := true
		for _, k := range ks {
			x := o.Lookup(k)
			if x.IsUndefined() {
				continue
			}
			if !first {
				b.PutByte(',')
			}
			first = false
			b.PutBytes(jsval.AppendJSON(nil, jsval.Str(k)))
			b.PutByte(':')
			stringifyTo(cc, b, x)
		}
		b.PutByte('}')
	default:
		if v.IsStr() {
			cc.work(int64(len(v.StrValue())) / 16) // a string is escaped a byte at a time
		}
		b.PutBytes(jsval.AppendJSON(nil, v))
	}
	return true
}

// stringValue is vega-util's stringValue: a JS literal for use in
// expressions (see jsval.StringValue). Vega-Lite 5.8 is built on vega-util
// 1.17, whose stringValue differs (stringValue117).
func stringValue(cc *compileCtx, v Value) string {
	if cc != nil && cc.v5 {
		b := newSbuf(cc)
		stringValue117(cc, &b, v, 0)
		return b.String()
	}
	if v.IsStr() {
		// A string's literal is its text quoted, and escaped where it must
		// be: charged for the quoted text before it is made, and for the
		// escapes once it is (an escape is at most six bytes).
		n := int64(len(v.StrValue()) + 2)
		cc.alloc(n)
		s := jsval.StringValue(v)
		cc.alloc(int64(len(s)) - n)
		return s
	}
	s := jsval.StringValue(v)
	cc.alloc(int64(len(s)))
	return s
}

func jsonString(cc *compileCtx, s string) string { return stringValue(cc, jsval.Str(s)) }

// stringValue117 is vega-util 1.17's stringValue, as Vega-Lite 5.8 calls it
// (C83 of the 2026-10-18 audit): `'[' + x.map($) + ']'`, so a null or
// undefined element is written as nothing where 2.x writes null, and
// JSON.stringify(x).replace('\u2028', ...).replace('\u2029', ...), which
// escapes only the first of each separator; anything else as its String.
// The text is written through b, which charges the call's ledger.
func stringValue117(cc *compileCtx, b *sbuf, v Value, depth int) {
	cc.work(1)
	switch v.Kind() {
	case jsval.KindArr:
		if depth > jsval.MaxValueDepth {
			throw("Maximum call stack size exceeded")
		}
		b.PutByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.PutByte(',')
			}
			if !it.IsNullish() {
				stringValue117(cc, b, it, depth+1)
			}
		}
		b.PutByte(']')
	case jsval.KindStr, jsval.KindObj:
		js := cc.walker().AppendJSON(nil, v)
		// The JSON text is held while it is copied into b.
		cc.alloc(int64(len(js)))
		cc.work(int64(len(js)) / 16)
		putFirstSeparatorsEscaped(b, js)
	default:
		b.WriteString(v.AsString())
	}
}

// putFirstSeparatorsEscaped writes js to b with the first U+2028 and the
// first U+2029 escaped, as the two single-replacement calls of vega-util
// 1.17 leave it.
func putFirstSeparatorsEscaped(b *sbuf, js []byte) {
	i := bytes.Index(js, []byte("\u2028"))
	j := bytes.Index(js, []byte("\u2029"))
	b.Grow(len(js) + 12)
	if i < 0 && j < 0 {
		b.PutBytes(js)
		return
	}
	if j >= 0 && (i < 0 || j < i) {
		i, j = j, i
	}
	// i is the first separator found, j the other (or -1).
	put := func(at int) {
		b.PutBytes(js[:at])
		b.WriteString("\\u202")
		b.PutByte(js[at+2] - 0xA8 + '8')
		js = js[at+3:]
	}
	put(i)
	if j >= 0 {
		put(j - (i + 3))
	}
	b.PutBytes(js)
}

// hash is upstream's hash(): short strings are their own hash.
func hashOf(cc *compileCtx, v Value) string {
	if v.IsUndefined() {
		// stringify(undefined) is undefined, and hash reads its length: a
		// domain that merges to nothing (every domain dropped by unique's
		// `in`, as "constructor" is) fails where its hash is taken.
		throw("Cannot read properties of undefined (reading 'length')")
	}
	if v.IsNum() {
		return jsval.JSNumberString(v.NumValue())
	}
	s := ""
	var b sbuf
	if v.IsStr() {
		s = v.StrValue()
	} else {
		b = newSbuf(cc)
		stringifyTo(cc, &b, v)
		s = b.String()
	}
	// The code units are walked in place (as utf16Len would produce them),
	// so a long key costs no copy and the walk is charged.
	if len(s) < 250 || utf16Count(cc, s) < 250 {
		return s
	}
	// A long text is only hashed: what its making was charged is given
	// back (a node is hashed once per pass of the optimizer).
	defer b.release()
	var h int32
	for _, r := range s {
		cc.work(1)
		if r >= 0x10000 {
			r -= 0x10000
			h = (h << 5) - h + int32(uint16(0xD800+(r>>10)))
			h = (h << 5) - h + int32(uint16(0xDC00+(r&0x3FF)))
		} else {
			h = (h << 5) - h + int32(uint16(r))
		}
	}
	return strconv.Itoa(int(h))
}

// utf16Count is len(utf16Len(s)) without building the code units.
func utf16Count(cc *compileCtx, s string) int {
	n := 0
	for _, r := range s {
		cc.work(1)
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

func utf16Len(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// varName is upstream's varName: non-word characters become '_' and a leading
// digit is prefixed.
func varName(s string) string {
	// A name of identifier characters only, not led by a digit, is its own
	// variable name (most are): return it as it is, allocating nothing.
	clean := len(s) == 0 || s[0] < '0' || s[0] > '9'
	for i := 0; clean && i < len(s); i++ {
		c := s[i]
		clean = c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 1)
	for _, r := range s {
		if r < 128 && (r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			b.WriteRune(r)
		} else if r >= 0x10000 {
			b.WriteString("__")
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		return "_" + out
	}
	return out
}

// titleCase is upstream's titleCase, s.charAt(0).toUpperCase() + s.substr(1):
// the first UTF-16 unit, upper-cased with JavaScript's full case mapping
// (ß becomes SS). A first character outside the BMP is a lone surrogate to
// charAt, which has no upper case, so the string is unchanged.
func titleCase(s string) string { return jsval.UpperFirst(s) }

// titleCaseV is titleCase of a spec value, which calls s.charAt.
func titleCaseV(v Value) string {
	switch {
	case v.IsStr():
		return titleCase(v.StrValue())
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'charAt')", nullishName(v))
	}
	throw("s.charAt is not a function")
	return ""
}

// appendVals is Array.prototype.concat over Value slices.
func appendVals(a []Value, more ...Value) []Value {
	out := make([]Value, 0, len(a)+len(more))
	out = append(out, a...)
	return append(out, more...)
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// isNumericV is upstream's isNumeric of any value: a number, or a value
// that isNaN (ToNumber) and parseFloat (of its text) both read as a number,
// so [[1]] and "1" are numeric and true and null are not.
func isNumericV(v Value) bool {
	if v.IsNum() {
		return true
	}
	return !math.IsNaN(jsval.ToNumber(v)) && !math.IsNaN(parseFloatPrefix(v.AsString()))
}

func parseFloatPrefix(s string) float64 {
	s = jsval.JSTrimStart(s)
	end := 0
	seenDot, seenDigit := false, false
	for i, c := range s {
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
			end = i + 1
		case (c == '+' || c == '-') && i == 0:
		case c == '.' && !seenDot:
			seenDot = true
		case (c == 'e' || c == 'E') && seenDigit:
			// exponent: take the longest valid suffix
			rest := s[i+1:]
			j := 0
			if j < len(rest) && (rest[j] == '+' || rest[j] == '-') {
				j++
			}
			k := j
			for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
				k++
			}
			if k > j {
				end = i + 1 + k
			}
			goto done
		default:
			goto done
		}
	}
done:
	if end == 0 {
		if strings.HasPrefix(s, "Infinity") || strings.HasPrefix(s, "+Infinity") {
			return math.Inf(1)
		}
		if strings.HasPrefix(s, "-Infinity") {
			return math.Inf(-1)
		}
		return math.NaN()
	}
	f, _ := jsval.ParseDecimal(s[:end])
	return f
}

// arrayIndex reports whether k is a canonical array index ("0", "17", not
// "01"), and its value. JavaScript orders such keys first, ascending, before all
// other keys of an object.
func arrayIndex(k string) (uint32, bool) {
	if k == "" || len(k) > 10 || k[0] < '0' || k[0] > '9' {
		return 0, false
	}
	if k[0] == '0' && len(k) > 1 {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	if n >= 1<<32-1 {
		return 0, false
	}
	return uint32(n), true
}

// jsSet is Object.Set with JavaScript's property order: a new array-index key
// is placed among the leading index keys in ascending order. o must be in
// JavaScript's order already (every object the compiler builds with jsSet,
// spread or deepClone is), so the place is found by binary search and only
// the keys after it move: building an object key by key in ascending or
// arbitrary non-index order costs O(log n) a key. A builder that inserts many
// index keys out of order appends them with Set and calls jsOrderKeys once
// instead (C4).
//
// It is JavaScript's assignment: to "__proto__" the object does not hold as
// its own, it replaces the object's prototype or does nothing (jsAssign).
func jsSet(cc *compileCtx, o *Object, k string, v Value) {
	if k == "__proto__" && !o.Has(k) && cc != nil {
		jsAssign(cc.realm(), o, k, v)
		return
	}
	if len(k) == 0 || k[0] < '0' || k[0] > '9' || o.Has(k) {
		o.Set(k, v)
		return
	}
	n, ok := arrayIndex(k)
	if !ok {
		o.Set(k, v)
		return
	}
	keys := o.Keys()
	pos := sort.Search(len(keys), func(i int) bool {
		m, isIdx := arrayIndex(keys[i])
		return !isIdx || m > n
	})
	if pos == len(keys) {
		o.Set(k, v)
		return
	}
	// Remove the tail from the end (each removal of the last key is O(1)),
	// then put the new key and the tail back.
	cc.work(int64(len(keys) - pos))
	tail := make([]jsEntry, len(keys)-pos)
	for i := len(keys) - 1; i >= pos; i-- {
		tail[i-pos] = jsEntry{k: keys[i], v: o.ValueAt(i)}
		o.Delete(keys[i])
	}
	o.Set(k, v)
	for _, e := range tail {
		o.Set(e.k, e.v)
	}
}

// appendKeepsJSOrder reports whether appending a new array-index key n to o,
// an object in JavaScript's order, leaves it in that order: o is empty, or
// its last key is an index below n (and so, by the order, all of them are).
func appendKeepsJSOrder(o *Object, n uint32) bool {
	if o.Len() == 0 {
		return true
	}
	m, ok := arrayIndex(o.KeyAt(o.Len() - 1))
	return ok && m < n
}

// jsOrderKeys puts o's keys in JavaScript's own-key order: array-index keys
// first, ascending, then the others in their present order. That is the
// order JavaScript gives an object whatever order its keys were added in, so
// adding keys with Set and ordering once is the same as adding each with
// jsSet, in O(n log n) instead of O(n) a key. An object already in order (any
// without index keys) costs one scan.
func jsOrderKeys(cc *compileCtx, o *Object) {
	n := o.Len()
	ordered, seenOther, last := true, false, int64(-1)
	for i := 0; i < n; i++ {
		k := o.KeyAt(i)
		if len(k) == 0 || k[0] < '0' || k[0] > '9' {
			seenOther = true
			continue
		}
		m, ok := arrayIndex(k)
		if !ok {
			seenOther = true
			continue
		}
		if seenOther || int64(m) <= last {
			ordered = false
			break
		}
		last = int64(m)
	}
	if ordered {
		return
	}
	// Each loop, and the sort's comparisons, charge a step an entry: an
	// object is as large as the specification. A compilation the charge
	// ends leaves o part reordered; nothing reads it after.
	type idxEntry struct {
		n uint32
		jsEntry
	}
	var idx []idxEntry
	rest := make([]jsEntry, 0, n)
	for i := 0; i < n; i++ {
		cc.work(1)
		k, v := o.KeyAt(i), o.ValueAt(i)
		if m, ok := arrayIndex(k); ok {
			idx = append(idx, idxEntry{m, jsEntry{k: k, v: v}})
		} else {
			rest = append(rest, jsEntry{k: k, v: v})
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		cc.work(1)
		return idx[i].n < idx[j].n
	})
	for i := n - 1; i >= 0; i-- {
		cc.work(1)
		o.Delete(o.KeyAt(i))
	}
	for _, e := range idx {
		cc.work(1)
		o.Set(e.k, e.v)
	}
	for _, e := range rest {
		cc.work(1)
		o.Set(e.k, e.v)
	}
}

// jsName renders a possibly-undefined name (represented as "") the way a
// JavaScript template literal renders undefined.
func jsName(s string) string {
	if s == "" {
		return "undefined"
	}
	return s
}

// jsIndex is v[i] for arrays and strings (a string yields one UTF-16 unit).
func jsIndex(v Value, i int) Value {
	if v.IsStr() {
		return unitAt(utf16Len(v.StrValue()), i)
	}
	return v.Index(i)
}

// jsGreater is JavaScript's a > b: strings compare by UTF-16 code units, other
// operands by their numeric value (NaN compares false).
func jsGreater(a, b Value) bool {
	if a.IsStr() && b.IsStr() {
		x, y := utf16Len(a.StrValue()), utf16Len(b.StrValue())
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				return x[i] > y[i]
			}
		}
		return len(x) > len(y)
	}
	return jsval.ToNumber(a) > jsval.ToNumber(b)
}

// strIs reports whether v is the string s.
func strIs(v Value, s string) bool { return v.IsStr() && v.StrValue() == s }

// setHas is set.has(v) of a Set of names: v must be one of them, compared
// strictly (a Set never coerces).
func setHas(set map[string]bool, v Value) bool {
	s, ok := v.Strict()
	return ok && set[s]
}

// looseEq is v == s, JavaScript's loose equality with a string literal (the
// few sites upstream writes == rather than ===): a string equals s, a number
// or boolean equals Number(s), an array or object compares its String
// (ToPrimitive), and null and undefined equal no string.
func looseEq(v Value, s string) bool {
	switch v.Kind() {
	case jsval.KindStr:
		return v.StrValue() == s
	case jsval.KindNum, jsval.KindBool:
		return jsval.ToNumber(v) == jsval.ToNumber(jsval.Str(s))
	case jsval.KindArr, jsval.KindObj:
		return v.StringIs(s)
	}
	return false
}

// undefinedKey is the property key upstream reads with an undefined name
// (obj[undefined] is obj["undefined"]).
const undefinedKey = "undefined"

// propKey is v read as a property key (obj[v], `v in obj`): String(v), so
// ["bar"] names "bar" and undefined "undefined". A key compared with a name
// is a property lookup; a keyword compared with one is strIs.
func propKey(v Value) string { return v.AsString() }

// strIn is contains(names, v) (indexOf, includes, Set.has): v is one of
// names, compared strictly, as strIs does.
func strIn(v Value, names ...string) bool {
	if !v.IsStr() {
		return false
	}
	s := v.StrValue()
	for _, n := range names {
		if s == n {
			return true
		}
	}
	return false
}

// jsIndexOrGet is v[key] for an object or an array (whose keys are indices).
func jsIndexOrGet(v Value, key string) Value {
	if v.IsArr() {
		i, err := strconv.Atoi(key)
		if err != nil {
			return undef
		}
		return v.Index(i)
	}
	return v.Get(key)
}

// jsReplaceAll is upstream's util.replaceAll: string.replace with a global
// regular expression of find taken literally, so the replacement's $
// patterns apply: $$ is "$", $& the match, $` the text before it and $' the
// text after it (there are no groups, so $1 and $<name> stay as they are).
//
// The result can be quadratic in the input ($` and $' copy up to the whole
// string once per match, C6: audit 2026-10-18), as upstream's is: its length
// is worked out first, a step per match, and charged to cc before it is
// built, and each match charges its bytes as work, so the compilation
// fails at the call's budget or deadline instead of building it unseen.
func jsReplaceAll(cc *compileCtx, s, find, repl string) string {
	if find == "" || !strings.Contains(s, find) {
		return s
	}
	// The replacement's pieces, once: literal text, or a pattern.
	type piece struct {
		lit  string
		kind byte // 0: lit; '&', '`', '\'': the match, the text before, after
	}
	var pieces []piece
	lit := 0 // where the current literal run starts
	for j := 0; j < len(repl); j++ {
		if repl[j] != '$' || j+1 == len(repl) {
			continue
		}
		switch c := repl[j+1]; c {
		case '$', '&', '`', '\'':
			pieces = append(pieces, piece{lit: repl[lit:j]})
			if c == '$' {
				pieces = append(pieces, piece{lit: "$"})
			} else {
				pieces = append(pieces, piece{kind: c})
			}
			j++
			lit = j + 1
		}
	}
	pieces = append(pieces, piece{lit: repl[lit:]})
	// The length of the result: each match's replacement depends on where
	// the match is ($` and $' copy what is before and after it).
	size := 0
	for rest := 0; ; {
		cc.work(1)
		i := strings.Index(s[rest:], find)
		if i < 0 {
			size += len(s) - rest
			break
		}
		start := rest + i
		end := start + len(find)
		size += start - rest
		for _, p := range pieces {
			switch p.kind {
			case 0:
				size += len(p.lit)
			case '&':
				size += len(find)
			case '`':
				size += start
			case '\'':
				size += len(s) - end
			}
		}
		rest = end
	}
	b := newSbuf(cc)
	b.Grow(size)
	for rest := 0; ; {
		i := strings.Index(s[rest:], find)
		if i < 0 {
			b.WriteString(s[rest:])
			break
		}
		start := rest + i
		end := start + len(find)
		n := b.Len()
		b.WriteString(s[rest:start])
		for _, p := range pieces {
			switch p.kind {
			case 0:
				b.WriteString(p.lit)
			case '&':
				b.WriteString(find)
			case '`':
				b.WriteString(s[:start])
			case '\'':
				b.WriteString(s[end:])
			}
		}
		cc.work(1 + int64(b.Len()-n)>>10)
		rest = end
	}
	return b.String()
}
