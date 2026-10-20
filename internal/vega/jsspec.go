package vega

import (
	"strconv"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// How vega-parser's JavaScript reads the values of a specification, where a
// value of another type than the one documented reaches it (ADR 0013, which
// holds Vega to upstream's answer as it does Vega-Lite): `for (k in v)` and
// vega-util's extend enumerate a string's or an array's indexes, `v.forEach`
// and `v.map` throw a TypeError on anything that is no array, and a property
// read of null throws one too.

// forInEach calls f with each key `for (const k in v)` enumerates and v[k]:
// an object's members, an array's or a string's indexes (a string's UTF-16
// code units, each a string of one unit), nothing for any other value. Each
// key is a step of m's work.
func forInEach(m *budget.Meter, v jsval.Value, f func(key string, val jsval.Value)) {
	switch v.Kind() {
	case jsval.KindObj:
		o := v.ObjValue()
		for i := 0; i < o.Len(); i++ {
			charge(m, 1)
			f(o.KeyAt(i), o.ValueAt(i))
		}
	case jsval.KindArr, jsval.KindStr:
		a, _ := jsval.ToArrayLike(v)
		for i := int64(0); i < a.Len(); i++ {
			charge(m, 1)
			f(strconv.FormatInt(i, 10), a.At(i))
		}
	}
}

// objectKeys is Object.keys(v): what forInEach enumerates, and the TypeError
// of null or undefined.
func objectKeys(m *budget.Meter, v jsval.Value) []string {
	if v.IsNullish() {
		perr("Cannot convert undefined or null to object")
	}
	var keys []string
	forInEach(m, v, func(k string, _ jsval.Value) { keys = append(keys, k) })
	return keys
}

// listOf is `v.forEach(...)` (method "forEach") or `v.map(...)` ("map") of a
// specification value upstream calls the method on directly: an array's
// elements, and for any other value the TypeError V8 raises, which names
// the expression (what, as upstream's source spells it).
func listOf(v jsval.Value, what, method string) []jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		return v.Items()
	case jsval.KindUndefined, jsval.KindNull:
		perr("Cannot read properties of %s (reading '%s')", nullishName(v), method)
	}
	perr("%s.%s is not a function", what, method)
	return nil
}

// strictIs is `v === s`: only a string equal to s.
func strictIs(v jsval.Value, s string) bool {
	t, ok := v.Strict()
	return ok && t == s
}
