package vegalite

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
func isExprRef(v Value) bool   { return hasProperty(v, "expr") }

// cloneObj is `{...o}`.
func cloneObj(o *Object) *Object {
	if o == nil {
		return jsval.NewObject(0)
	}
	return o.Clone()
}

// spread copies the keys of src (an object; anything else is ignored) into dst,
// overwriting in place like JavaScript's spread.
func spread(dst *Object, srcs ...Value) *Object {
	for _, s := range srcs {
		if s.IsStr() {
			for i, u := range utf16Len(s.StrValue()) {
				jsSet(dst, strconv.Itoa(i), jsval.Str(string(rune(u))))
			}
			continue
		}
		if s.IsArr() {
			for i, it := range s.Items() {
				jsSet(dst, strconv.Itoa(i), it)
			}
			continue
		}
		if s.IsObj() {
			so := s.ObjValue()
			for i := 0; i < so.Len(); i++ {
				jsSet(dst, so.KeyAt(i), so.ValueAt(i))
			}
		}
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
	return stringify(v)
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

// deepClone is structuredClone for JSON-like values.
func deepClone(v Value) Value {
	switch v.Kind() {
	case jsval.KindArr:
		items := v.Items()
		out := make([]Value, len(items))
		for i, it := range items {
			out[i] = deepClone(it)
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		o := v.ObjValue()
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			jsSet(c, o.KeyAt(i), deepClone(o.ValueAt(i)))
		}
		return jsval.Obj(c)
	}
	return v
}

// stringify is upstream's util.stringify: JSON with sorted keys, used as a
// hash key for structural comparison.
func stringify(v Value) string {
	var b strings.Builder
	stringifyTo(&b, v)
	return b.String()
}

func stringifyTo(b *strings.Builder, v Value) bool {
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
			if !stringifyTo(b, it) {
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
			stringifyTo(b, x)
		}
		b.WriteByte('}')
	default:
		b.Write(jsval.AppendJSON(nil, v))
	}
	return true
}

// stringValue is vega-util's stringValue: a JS literal for use in expressions.
// Arrays print as [a,b] with recursively converted elements; strings and
// objects are JSON with U+2028/2029 escaped; anything else is String(x).
func stringValue(v Value) string {
	switch v.Kind() {
	case jsval.KindArr:
		items := v.Items()
		parts := make([]string, len(items))
		for i, it := range items {
			switch {
			case it.IsNull():
				parts[i] = "null"
			case it.IsUndefined():
				parts[i] = ""
			default:
				parts[i] = stringValue(it)
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	case jsval.KindStr, jsval.KindObj:
		s := string(jsval.AppendJSON(nil, v))
		s = strings.ReplaceAll(s, "\u2028", "\\u2028")
		s = strings.ReplaceAll(s, "\u2029", "\\u2029")
		return s
	}
	return v.AsString()
}

func jsonString(s string) string { return stringValue(jsval.Str(s)) }

// hash is upstream's hash(): short strings are their own hash.
func hashOf(v Value) string {
	if v.IsNum() {
		return jsval.JSNumberString(v.NumValue())
	}
	s := ""
	if v.IsStr() {
		s = v.StrValue()
	} else {
		s = stringify(v)
	}
	if len(utf16Len(s)) < 250 {
		return s
	}
	var h int32
	for _, u := range utf16Len(s) {
		h = (h << 5) - h + int32(u)
	}
	return strconv.Itoa(int(h))
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

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
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
// is placed among the leading index keys in ascending order.
func jsSet(o *Object, k string, v Value) {
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
	pos := len(keys)
	for i, e := range keys {
		if m, isIdx := arrayIndex(e); !isIdx || m > n {
			pos = i
			break
		}
	}
	if pos == len(keys) {
		o.Set(k, v)
		return
	}
	type kv struct {
		k string
		v Value
	}
	var tail []kv
	for _, e := range append([]string(nil), keys[pos:]...) {
		tail = append(tail, kv{e, o.Lookup(e)})
		o.Delete(e)
	}
	o.Set(k, v)
	for _, e := range tail {
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
