package vegalite

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

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

func isSignalRef(v Value) bool { return hasProperty(v, "signal") }

// isExprRef is hasProperty(o, 'expr') in 6.4; 5.8 tests !!o?.expr, so
// an expr that is falsy is not one.
func isExprRef(cc *compileCtx, v Value) bool {
	if cc != nil && cc.v5 {
		return v.IsObj() && v.Get("expr").IsTruthy()
	}
	return hasProperty(v, "expr")
}

// cloneObj is `{...o}`.
func cloneObj(o *Object) *Object {
	if o == nil {
		return jsval.NewObject(0)
	}
	return o.Clone()
}

// spread copies the keys of src (an object; anything else is ignored) into dst,
// overwriting in place like JavaScript's spread. A new array-index key belongs
// among dst's leading index keys; rather than moving the keys after it on
// every insert (cubic over an object's keys, C4), the keys are appended and
// put in JavaScript's order once at the end, which gives the same order (see
// jsOrderKeys). It is linear in its input, so it needs no meter.
func spread(dst *Object, srcs ...Value) *Object {
	reorder := false
	put := func(k string, v Value) {
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
		jsOrderKeys(nil, dst)
	}
	return dst
}

// merged is `{...a, ...b, ...}`.
func merged(srcs ...Value) *Object {
	n := 0
	for _, s := range srcs {
		n += s.Len()
	}
	return spread(jsval.NewObject(n), srcs...)
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

// stringifyJS is util.stringify as a template literal renders it: undefined
// prints as "undefined".
func stringifyJS(v Value) string {
	if v.IsUndefined() {
		return "undefined"
	}
	return stringify(nil, v)
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
func deepEqual(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsval.KindArr:
		x, y := a.Items(), b.Items()
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !deepEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case jsval.KindObj:
		x, y := a.ObjValue(), b.ObjValue()
		if x == y {
			return true
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
		for i := 0; i < x.Len(); i++ {
			bv, ok := y.Get(x.KeyAt(i))
			if !ok || !deepEqual(x.ValueAt(i), bv) {
				return false
			}
		}
		return true
	}
	return jsval.Equal(a, b)
}

// deepClone is structuredClone for JSON-like values. The clone's keys are in
// JavaScript's order (array-index keys first, ascending), which a parsed
// document's need not be: they are ordered once per object.
func deepClone(cc *compileCtx, v Value) Value {
	cc.work(1)
	switch v.Kind() {
	case jsval.KindArr:
		items := v.Items()
		out := make([]Value, len(items))
		for i, it := range items {
			out[i] = deepClone(cc, it)
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		o := v.ObjValue()
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			c.Set(o.KeyAt(i), deepClone(cc, o.ValueAt(i)))
		}
		jsOrderKeys(cc, c)
		return jsval.Obj(c)
	}
	return v
}

// stringify is upstream's util.stringify: JSON with sorted keys, used as a
// hash key for structural comparison.
func stringify(cc *compileCtx, v Value) string {
	var b strings.Builder
	stringifyTo(cc, &b, v)
	return b.String()
}

// stringifyText is `${stringify(v)}`: stringify's text, or "undefined"
// for a value JSON has no text for.
func stringifyText(cc *compileCtx, v Value) string {
	var b strings.Builder
	if !stringifyTo(cc, &b, v) {
		return "undefined"
	}
	return b.String()
}

func stringifyTo(cc *compileCtx, b *strings.Builder, v Value) bool {
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
		b.WriteByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			if !stringifyTo(cc, b, it) {
				b.WriteString("null")
			}
		}
		b.WriteByte(']')
	case jsval.KindObj:
		o := v.ObjValue()
		if fn, ok := lazyFn(o); ok {
			b.WriteString(`{"signal":` + string(jsval.AppendJSON(nil, jsval.Str(fn()))) + "}")
			return true
		}
		ks := append([]string(nil), o.Keys()...)
		sort.Strings(ks)
		b.WriteByte('{')
		first := true
		for _, k := range ks {
			x := o.Lookup(k)
			if x.IsUndefined() {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.Write(jsval.AppendJSON(nil, jsval.Str(k)))
			b.WriteByte(':')
			stringifyTo(cc, b, x)
		}
		b.WriteByte('}')
	default:
		b.Write(jsval.AppendJSON(nil, v))
	}
	return true
}

// stringValue is vega-util's stringValue: a JS literal for use in
// expressions (see jsval.StringValue).
func stringValue(v Value) string { return jsval.StringValue(v) }

func jsonString(s string) string { return stringValue(jsval.Str(s)) }

// hash is upstream's hash(): short strings are their own hash.
func hashOf(cc *compileCtx, v Value) string {
	if v.IsNum() {
		return jsval.JSNumberString(v.NumValue())
	}
	s := ""
	if v.IsStr() {
		s = v.StrValue()
	} else {
		s = stringify(cc, v)
	}
	// The code units are walked in place (as utf16Len would produce them),
	// so a long key costs no copy and the walk is charged.
	if len(s) < 250 || utf16Count(cc, s) < 250 {
		return s
	}
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
	var b strings.Builder
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
func titleCase(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || r >= 0x10000 {
		return s
	}
	return jsUpperCase(s[:n]) + s[n:]
}

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

// jsUpperCase is String.prototype.toUpperCase: full Unicode case mapping.
func jsUpperCase(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return cases.Upper(language.Und).String(s)
		}
	}
	return strings.ToUpper(s)
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

func isNumericString(s string) bool {
	f := jsval.StringToNumber(s)
	return !math.IsNaN(f) && !math.IsNaN(parseFloatPrefix(s))
}

func parseFloatPrefix(s string) float64 {
	s = strings.TrimSpace(s)
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
	// Out of range, ParseFloat answers ±Inf (or 0) with an error; parseFloat
	// answers the same values ("1e999" is Infinity).
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
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
	cc.work(int64(n))
	type idxEntry struct {
		n uint32
		jsEntry
	}
	var idx []idxEntry
	rest := make([]jsEntry, 0, n)
	for i := 0; i < n; i++ {
		k, v := o.KeyAt(i), o.ValueAt(i)
		if m, ok := arrayIndex(k); ok {
			idx = append(idx, idxEntry{m, jsEntry{k: k, v: v}})
		} else {
			rest = append(rest, jsEntry{k: k, v: v})
		}
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].n < idx[j].n })
	for i := n - 1; i >= 0; i-- {
		o.Delete(o.KeyAt(i))
	}
	for _, e := range idx {
		o.Set(e.k, e.v)
	}
	for _, e := range rest {
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
		u := utf16Len(v.StrValue())
		if i >= 0 && i < len(u) {
			return jsval.Str(string(rune(u[i])))
		}
		return undef
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
func jsReplaceAll(s, find, repl string) string {
	if find == "" || !strings.Contains(s, find) {
		return s
	}
	if !strings.Contains(repl, "$") {
		return strings.ReplaceAll(s, find, repl)
	}
	var b strings.Builder
	rest := 0
	for {
		i := strings.Index(s[rest:], find)
		if i < 0 {
			break
		}
		start := rest + i
		end := start + len(find)
		b.WriteString(s[rest:start])
		for j := 0; j < len(repl); j++ {
			if repl[j] != '$' || j+1 == len(repl) {
				b.WriteByte(repl[j])
				continue
			}
			switch repl[j+1] {
			case '$':
				b.WriteByte('$')
			case '&':
				b.WriteString(find)
			case '`':
				b.WriteString(s[:start])
			case '\'':
				b.WriteString(s[end:])
			default:
				b.WriteByte('$')
				continue
			}
			j++
		}
		rest = end
	}
	b.WriteString(s[rest:])
	return b.String()
}
