package vega

import (
	"math"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
)

// This file reads a transform's parameters the way upstream's transforms read
// `_.name`: with plain JavaScript, whatever the specification gave. A
// parameter's value is what the runtime marshalled (dataflow.go): nil for
// undefined, a jsval.Value (a literal, a signal's value), a []any (a literal
// array, its elements resolved one by one), a transforms.Field or a
// []transforms.Field (a field reference, a Field operator's value), or another
// operator value. A field accessor is a function upstream: truthy, with a
// length of 1 (its one parameter) and no elements.

// native is a constant the parse wrote as a Go value (a string, a number, a
// boolean: scope.go's P) as the value it stands for; anything else as it is.
func native(x any) any {
	switch v := x.(type) {
	case string:
		return jsval.Str(v)
	case float64:
		return jsval.Num(v)
	case int:
		return jsval.Int(v)
	case bool:
		return jsval.Bool(v)
	}
	return x
}

// paramTruthy is the truthiness `_.name || default` tests.
func paramTruthy(x any) bool {
	switch v := native(x).(type) {
	case nil:
		return false
	case jsval.Value:
		return v.IsTruthy()
	case transforms.Field:
		return !v.IsNil()
	}
	return true
}

// paramArray is vega-util's array(_.name): no elements for undefined and
// null, the elements of an array, and the value itself otherwise.
func paramArray(x any) []any {
	switch v := native(x).(type) {
	case nil:
		return nil
	case []any:
		return v
	case []transforms.Field:
		out := make([]any, len(v))
		for i, f := range v {
			out[i] = f
		}
		return out
	case jsval.Value:
		if v.IsNullish() {
			return nil
		}
		if v.IsArr() {
			items := v.Items()
			out := make([]any, len(items))
			for i, it := range items {
				out[i] = it
			}
			return out
		}
	case transforms.Field:
		if v.IsNil() {
			return nil
		}
	}
	return []any{x}
}

// paramLength is `x.length` of a value that is not null or undefined, as a
// loop bound `i < x.length` compares it: NaN when the length is undefined or
// not a number (no iteration), what ToNumber makes of it otherwise.
func paramLength(x any) float64 {
	switch v := native(x).(type) {
	case []any:
		return float64(len(v))
	case []transforms.Field:
		return float64(len(v))
	case transforms.Field, *boundExpr:
		return 1 // a function of one parameter
	case jsval.Value:
		if v.IsNullish() {
			return math.NaN()
		}
		return jsval.ToNumber(jsval.Member(v, "length"))
	}
	return math.NaN()
}

// paramLengthValue is `x.length` as a value, for a strict comparison of two
// lengths (`fields.length !== ops.length`): undefined where it is not a
// number.
func paramLengthValue(x any) jsval.Value {
	switch v := native(x).(type) {
	case []any, []transforms.Field, transforms.Field, *boundExpr:
		return jsval.Num(paramLength(x))
	case jsval.Value:
		if v.IsNullish() {
			return jsval.Undefined
		}
		return jsval.Member(v, "length")
	}
	return jsval.Undefined
}

// paramAt is `x[i]` of a value that is not null or undefined: the element of
// an array or array-like, a string's character, undefined for anything else.
func paramAt(x any, i int) any {
	switch v := native(x).(type) {
	case []any:
		if i < len(v) {
			return v[i]
		}
		return nil
	case []transforms.Field:
		if i < len(v) {
			return v[i]
		}
		return nil
	case jsval.Value:
		if v.IsNullish() {
			return nil
		}
		if v.IsArr() || v.IsStr() {
			a, _ := jsval.ToArrayLike(v)
			return a.At(int64(i))
		}
		if v.IsObj() {
			return jsval.Member(v, itoa(i))
		}
	}
	return nil
}

// paramValueOf is a parameter element as a value: nil is undefined, a null
// accessor null, and an operator value that is no JSON value (a function)
// reads as an object that names nothing.
func paramValueOf(x any) (jsval.Value, bool) {
	switch v := native(x).(type) {
	case nil:
		return jsval.Undefined, true
	case jsval.Value:
		return v, true
	case transforms.Field:
		if v.IsNil() {
			return jsval.Null, true
		}
	case []any:
		return paramJS(v), true
	}
	return jsval.Undefined, false
}

func itoa(i int) string {
	var buf [20]byte
	n := len(buf)
	if i == 0 {
		return "0"
	}
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

// paramBound is the bound of `for (i = 0; i < n; ++i)` over a length n read
// with paramLength: 0 for NaN. The loop charges each iteration to the run (an
// object's length can be anything, and upstream's loop may throw at its
// first element).
func paramBound(n float64) int {
	if !(n > 0) {
		return 0
	}
	return int(math.Min(math.Ceil(n), math.MaxInt32))
}

// paramNullish reports undefined and null (`x == null`): a null field
// accessor (an empty field name) is null too.
func paramNullish(x any) bool {
	switch v := native(x).(type) {
	case nil:
		return true
	case jsval.Value:
		return v.IsNullish()
	case transforms.Field:
		return v.IsNil()
	}
	return false
}

// nullishText is "undefined" or "null", as a TypeError names the value it
// read a property of.
func nullishText(x any) string {
	if v, ok := native(x).(jsval.Value); ok && v.IsNull() {
		return "null"
	}
	if f, ok := x.(transforms.Field); ok && f.IsNil() {
		return "null"
	}
	return "undefined"
}

// paramElem is `x[i]` of a value that is not null or undefined, as a value.
func paramElem(x any, i int) jsval.Value {
	v, _ := paramValueOf(paramAt(x, i))
	return v
}

// paramElems is the elements an index reads of a value that is not null or
// undefined, with its length as a loop bound compares it (NaN when it has
// none): the elements stop at the last one the value holds, as an index
// past them reads undefined. Each element read is charged to the run.
func paramElems(g *flowGraph, x any) ([]jsval.Value, float64) {
	n := paramLength(x)
	k := paramBound(n)
	if v, ok := native(x).(jsval.Value); ok && v.IsObj() {
		// an array-like object's elements are its own index keys
		k = min(k, v.ObjValue().Len())
	} else if l, ok := x.([]any); ok {
		k = min(k, len(l))
	} else if a, ok := native(x).(jsval.Value); ok && (a.IsArr() || a.IsStr()) {
		al, _ := jsval.ToArrayLike(a)
		k = min(k, int(al.Len()))
	} else {
		k = 0
	}
	out := make([]jsval.Value, k)
	for i := range out {
		g.work(1)
		out[i] = paramElem(x, i)
	}
	return out, n
}

// textOr is `_.name || def` used as a property key or a name: the
// parameter's text when it is truthy, def otherwise (0, false and "" too).
func (p *opParams) textOr(name, def string) string {
	x := p.Get(name)
	if !paramTruthy(x) {
		return def
	}
	if v, ok := paramValueOf(x); ok {
		return p.walk.String(v)
	}
	return def
}

// asPair is `as = _.as || [d0, d1]` read as as[0] and as[1], each the
// property key it names: String of the entry, "undefined" past the end
// (["x"] names the second output "undefined"), the empty string for "".
// A transform given it sets AsExact, so an empty name is not a default.
func (p *opParams) asPair(d0, d1 string) [2]string {
	x := p.Get("as")
	if !paramTruthy(x) {
		return [2]string{d0, d1}
	}
	var out [2]string
	for i := range out {
		if v, ok := paramValueOf(paramAt(x, i)); ok {
			out[i] = p.walk.String(v)
		} else {
			out[i] = "undefined" // an element that is no JSON value
		}
	}
	return out
}

// orValue is `_.a || _.b || ... || def`: the first truthy parameter, as a
// value.
func (p *opParams) orValue(def jsval.Value, names ...string) jsval.Value {
	for _, n := range names {
		if v, ok := paramValueOf(p.Get(n)); ok && v.IsTruthy() {
			return v
		}
	}
	return def
}

// stepsSpec is a density's or a kde's step bounds: `_.steps || _.minsteps
// || 25` and `_.steps || _.maxsteps || 200`.
func (p *opParams) stepsSpec() transforms.StepsSpec {
	return transforms.StepsSpec{
		Min: p.orValue(jsval.Num(25), "steps", "minsteps"),
		Max: p.orValue(jsval.Num(200), "steps", "maxsteps"),
	}
}

// extentValues is a domain parameter read as `domain[0]` and `domain[1]`
// when it is truthy (`_.extent || ...`), nil otherwise.
func (p *opParams) extentValues(name string) *[2]jsval.Value {
	x := p.Get(name)
	if !paramTruthy(x) {
		return nil
	}
	return &[2]jsval.Value{paramElem(x, 0), paramElem(x, 1)}
}

// densityMethod is `_.method || 'pdf'`, which must be the string "pdf" or
// "cdf" (`method !== 'pdf' && method !== 'cdf'` is an error).
func densityMethod(v jsval.Value) string {
	if !v.IsTruthy() {
		return "pdf"
	}
	if strictIs(v, "pdf") || strictIs(v, "cdf") {
		s, _ := v.Strict()
		return s
	}
	fail("Invalid density method: %s", jsval.StringPrefix(v, jsval.MessageBytes))
	return ""
}

// orNumber is `_.name || def` used as a number: def for a falsy value, the
// number of a truthy one (NaN for one that is no number).
func (p *opParams) orNumber(name string, def float64) float64 {
	v, ok := paramValueOf(p.Get(name))
	if !ok {
		return math.NaN() // a function: truthy, no number
	}
	if !v.IsTruthy() {
		return def
	}
	return jsval.ToNumber(v)
}

// strictFalse reports `x === false`.
func strictFalse(x any) bool {
	v, ok := paramValueOf(x)
	return ok && v.Kind() == jsval.KindBool && !v.BoolValue()
}

// paramIsArray is isArray(x).
func paramIsArray(x any) bool {
	switch v := native(x).(type) {
	case []any, []transforms.Field:
		return true
	case jsval.Value:
		return v.IsArr()
	}
	return false
}

// paramJS is a parameter as a value: a list resolved element by element is
// the array of its elements (toValue), a constant the value it is, and an
// operator value that is no JSON value undefined.
func paramJS(x any) jsval.Value {
	if l, ok := x.([]any); ok {
		out := make([]jsval.Value, len(l))
		for i, e := range l {
			out[i], _ = paramValueOf(e)
		}
		return jsval.Arr(out)
	}
	v, _ := paramValueOf(x)
	return v
}

// requireGroupByList fails as `(_.groupby || []).map(accessorName)` and
// partition's `groupby.map(get)` do for a group-by that is truthy but no
// list (a Field operator's single accessor, of a signal that is no array):
// a TypeError.
func (p *opParams) requireGroupByList() {
	if g := p.Get("groupby"); paramTruthy(g) && !paramIsArray(g) {
		fail("TypeError: groupby.map is not a function")
	}
}

// requirePartitionList fails as partition's `groupby.map(get)` does, once
// per tuple, for a group-by that is not null and no list (a signal's single
// accessor, whatever it answered): a TypeError when there are tuples.
func (p *opParams) requirePartitionList(rows int) {
	if g := p.Get("groupby"); rows > 0 && !paramNullish(g) && !paramIsArray(g) {
		fail("TypeError: groupby.map is not a function")
	}
}

// requireGroupKeyList fails as Window's groupkey(_.groupby) does for a
// group-by that is no list: its accessor has length 1, so the key function
// is its (missing) first element, called for each tuple.
func (p *opParams) requireGroupKeyList(rows int) {
	if g := p.Get("groupby"); rows > 0 && paramTruthy(g) && !paramIsArray(g) {
		fail("TypeError: key is not a function")
	}
}

// requireFieldsList fails as `fields.map(...)` does for a fields parameter
// that is no list (a signal's single accessor): fieldNames guards it with
// `if (!fields)` (guarded true: flatten, project), fold does not.
func (p *opParams) requireFieldsList(guarded bool) {
	g := p.Get("fields")
	if paramIsArray(g) || (guarded && !paramTruthy(g)) || (!guarded && paramNullish(g)) {
		return
	}
	fail("TypeError: fields.map is not a function")
}

// asList is `as = _.as || defaults` read as as[0] .. as[n-1] for the n
// defaults, each the property key it names ("undefined" past the end).
func (p *opParams) asList(defaults ...string) []string {
	x := p.Get("as")
	if !paramTruthy(x) {
		return defaults
	}
	out := make([]string, len(defaults))
	for i := range out {
		if v, ok := paramValueOf(paramAt(x, i)); ok {
			out[i] = p.walk.String(v)
		} else {
			out[i] = "undefined"
		}
	}
	return out
}

// strictText is the one of names v is strictly equal to (`v === name`),
// "" for none: a keyword upstream compares with ===, which ["center"] or a
// number never matches.
func strictText(v jsval.Value, names ...string) string {
	for _, n := range names {
		if strictIs(v, n) {
			return n
		}
	}
	return ""
}

// given is a parameter that is set (`p in _` for a value the
// specification gave, null included), as a value.
func (p *opParams) given(name string) (jsval.Value, bool) {
	x := p.Get(name)
	if x == nil {
		return jsval.Undefined, false
	}
	v, _ := paramValueOf(x)
	return v, true
}

// givenNum is a numeric parameter as a setter's +_ reads it: def when it
// is not given, +v otherwise (null is 0, undefined NaN).
func (p *opParams) givenNum(name string, def float64) float64 {
	v, ok := p.given(name)
	if !ok {
		return def
	}
	return jsval.ToNumber(v)
}

// member is v[key] as a setter reads it, a TypeError for null and undefined.
func member(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		fail("TypeError: Cannot read properties of %s (reading '%s')", nullishName(v), key)
	}
	return jsval.Member(v, key)
}

// givenPair is [+_[0], +_[1]] of a parameter that is given, nil otherwise.
func (p *opParams) givenPair(name string) *[2]float64 {
	v, ok := p.given(name)
	if !ok {
		return nil
	}
	return &[2]float64{jsval.ToNumber(member(v, "0")), jsval.ToNumber(member(v, "1"))}
}

// givenPairs is [[+_[0][0], +_[0][1]], [+_[1][0], +_[1][1]]] of a
// parameter that is given, nil otherwise.
func (p *opParams) givenPairs(name string) *[2][2]float64 {
	v, ok := p.given(name)
	if !ok {
		return nil
	}
	a, b := member(v, "0"), member(v, "1")
	return &[2][2]float64{
		{jsval.ToNumber(member(a, "0")), jsval.ToNumber(member(a, "1"))},
		{jsval.ToNumber(member(b, "0")), jsval.ToNumber(member(b, "1"))},
	}
}

// keyOf is a parameter used as a property key as it is (`t[_.as]`): its
// text, "null" or "undefined" for those.
func (p *opParams) keyOf(name string) string {
	v, _ := paramValueOf(p.Get(name))
	return p.walk.String(v)
}

// truthyTextsUpTo is `_.as || []` read as fieldNames does, `as[i] ||
// accessorName(f)`: each entry's text when it is truthy, "" (the accessor's
// name) otherwise, for the first n entries only (all for n < 0):
// fieldNames(fields, as) reads as[i] for each field, and no further entry
// is ever converted.
func (p *opParams) truthyTextsUpTo(name string, n int) []string {
	l := paramArray(p.Get(name))
	if n >= 0 && len(l) > n {
		l = l[:n]
	}
	out := make([]string, len(l))
	for i, e := range l {
		if v, ok := paramValueOf(e); ok && v.IsTruthy() {
			out[i] = p.walk.String(v)
		}
	}
	return out
}

// imputeMethod is Impute's `m = _.method || 'value'`, looked up as
// Methods[m] by its text.
func imputeMethod(p *opParams) (method string, notStrict bool, proto string) {
	mv := p.orValue(jsval.Str("value"), "method")
	m, _ := mv.PropertyKey(64)
	switch m {
	case "value":
		// `m === Methods.value` holds only for the string itself
		return m, !strictIs(mv, "value"), ""
	case "mean", "median", "max", "min":
		return m, false, ""
	}
	if expr.IsObjectPrototypeName(m) {
		// Methods[m] finds the inherited member, so getValue accepts it
		return "proto", false, m
	}
	fail("Unrecognized imputation method: %s", m)
	return "", false, ""
}

// pivotOp is Pivot's `(_.op === 'count' ? '__count__' : _.op) || 'sum'`,
// the aggregate op its text names.
func pivotOp(p *opParams) string {
	v := p.orValue(jsval.Str("sum"), "op")
	if strictIs(v, "count") { // (_.op === 'count' ? '__count__' : _.op)
		return "__count__"
	}
	k, _ := v.PropertyKey(64)
	return k
}

// orPlusOne is `(_.name || def) + 1` as JavaScript adds it: a string, an
// array or an object concatenates ("3" + 1 is "31"), read as a number.
func (p *opParams) orPlusOne(name string, def float64) float64 {
	v := p.orValue(jsval.Num(def), name)
	switch v.Kind() {
	case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
		return jsval.StringToNumber(p.walk.String(v) + "1")
	}
	return jsval.ToNumber(v) + 1
}
