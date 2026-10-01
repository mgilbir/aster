package transforms

import (
	"fmt"
	"github.com/mgilbir/aster/internal/jssort"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

// primitive is ToPrimitive with hint "number" as the relational operators use
// it: dates read as their epoch, arrays and objects as their string form.
func primitive(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindTimestamp:
		return jsval.Num(v.NumValue())
	case jsval.KindArr, jsval.KindObj, jsval.KindPattern:
		return jsval.Str(v.AsString())
	}
	return v
}

// Relate is JavaScript's abstract relational comparison of a and b: it
// returns -1, 0 or +1, and ok=false when either side coerces to NaN (every
// operator then answers false). Two strings compare by UTF-16 code units;
// anything else compares as numbers.
func Relate(a, b jsval.Value) (c int, ok bool) {
	// Fast path: the overwhelmingly common same-kind cases.
	if a.Kind() == jsval.KindNum && b.Kind() == jsval.KindNum {
		return cmpFloat(a.NumValue(), b.NumValue())
	}
	a, b = primitive(a), primitive(b)
	if a.Kind() == jsval.KindStr && b.Kind() == jsval.KindStr {
		return CompareUTF16(a.StrValue(), b.StrValue()), true
	}
	return cmpFloat(jsval.ToNumber(a), jsval.ToNumber(b))
}

func cmpFloat(x, y float64) (int, bool) {
	switch {
	case x < y:
		return -1, true
	case x > y:
		return 1, true
	case x == y:
		return 0, true
	}
	return 0, false
}

// Less is `a < b`.
func Less(a, b jsval.Value) bool { c, ok := Relate(a, b); return ok && c < 0 }

// Greater is `a > b`.
func Greater(a, b jsval.Value) bool { c, ok := Relate(a, b); return ok && c > 0 }

// GreaterEq is `a >= b`; it is false when either side is NaN.
func GreaterEq(a, b jsval.Value) bool { c, ok := Relate(a, b); return ok && c >= 0 }

// LessEq is `a <= b`.
func LessEq(a, b jsval.Value) bool { c, ok := Relate(a, b); return ok && c <= 0 }

// CompareUTF16 orders strings by UTF-16 code unit, as JavaScript does. It
// differs from byte order only when one string has a supplementary-plane
// character where the other has a BMP character above U+D7FF.
func CompareUTF16(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	if i == n {
		switch {
		case len(a) < len(b):
			return -1
		case len(a) > len(b):
			return 1
		}
		return 0
	}
	// Back up to the start of the rune that differs.
	for i > 0 && !utf8.RuneStart(a[i]) {
		i--
	}
	ra, _ := utf8.DecodeRuneInString(a[i:])
	rb, _ := utf8.DecodeRuneInString(b[i:])
	ua, ub := utf16Unit(ra), utf16Unit(rb)
	switch {
	case ua < ub:
		return -1
	case ua > ub:
		return 1
	}
	return 0
}

// utf16Unit is the first UTF-16 code unit of r.
func utf16Unit(r rune) rune {
	if r >= 0x10000 {
		return 0xD800 + ((r - 0x10000) >> 10)
	}
	return r
}

// Ascending is vega-util's ascending comparator. null and undefined sort
// before everything, NaN (and an Invalid Date) sort after them and before
// every valid value, and mixed types fall back to JavaScript's `<`/`>`.
func Ascending(u, v jsval.Value) int {
	// Fast paths for the two overwhelmingly common same-kind comparisons.
	switch {
	case u.Kind() == jsval.KindNum && v.Kind() == jsval.KindNum:
		a, b := u.NumValue(), v.NumValue()
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		case a == b:
			return 0
		case a != a && b == b:
			return -1
		case b != b && a == a:
			return 1
		}
		return 0
	case u.Kind() == jsval.KindStr && v.Kind() == jsval.KindStr:
		return CompareUTF16(u.StrValue(), v.StrValue())
	}
	un, vn := u.IsNullish(), v.IsNullish()
	if c, ok := Relate(u, v); ok {
		if c < 0 && !vn {
			return -1
		}
		if c > 0 && !un {
			return 1
		}
	}
	if un && !vn {
		return -1
	}
	if vn && !un {
		return 1
	}
	// Dates are compared as their epoch for the NaN checks below.
	uNaN, vNaN := isNaNValue(u), isNaNValue(v)
	if uNaN && !vNaN {
		return -1
	}
	if vNaN && !uNaN {
		return 1
	}
	return 0
}

func isNaNValue(v jsval.Value) bool {
	f, ok := v.NumberOrNull()
	return ok && math.IsNaN(f)
}

// Comparator orders two tuples.
type Comparator func(a, b jsval.Value) int

// Sort order constants for CompareBy.
const (
	Asc  = "ascending"
	Desc = "descending"
)

// CompareBy is vega-util's compare(fields, orders): tuples are ordered by each
// field in turn with Ascending, reversed for the fields whose order is
// "descending" (any other order string means ascending). Null fields are
// skipped along with their order. It returns nil when no field remains.
func CompareBy(fields []Field, orders []string) Comparator {
	get := make([]Accessor, 0, len(fields))
	ord := make([]int, 0, len(fields))
	for i, f := range fields {
		if f.IsNil() {
			continue
		}
		o := 1
		if i < len(orders) && orders[i] == Desc {
			o = -1
		}
		get = append(get, f.Get)
		ord = append(ord, o)
	}
	switch len(get) {
	case 0:
		return nil
	case 1:
		g, o := get[0], ord[0]
		return func(a, b jsval.Value) int { return Ascending(g(a), g(b)) * o }
	}
	return func(a, b jsval.Value) int {
		for i, g := range get {
			if c := Ascending(g(a), g(b)); c != 0 {
				return c * ord[i]
			}
		}
		return 0
	}
}

// SortTuples sorts data in place exactly as V8's Array.prototype.sort does
// (see package jssort): stable, and producing upstream's order even when the
// comparator is inconsistent, as vega-util's compare is across mixed types.
// A nil comparator leaves it untouched.
func SortTuples(data []jsval.Value, cmp Comparator) {
	if cmp == nil {
		return
	}
	if len(data) < 2 {
		return
	}
	// Sorting an index permutation moves 4 bytes per step instead of a Value.
	idx := make([]int32, len(data))
	for i := range idx {
		idx[i] = int32(i)
	}
	jssort.Sort(idx, func(a, b int32) int { return cmp(data[a], data[b]) })
	sorted := make([]jsval.Value, len(data))
	for i, j := range idx {
		sorted[i] = data[j]
	}
	copy(data, sorted)
}

// ExtentIndex is vega-util's extentIndex over the values f yields: the
// indices of the first minimum and first maximum, ignoring null, undefined and
// NaN; [-1,-1] when nothing is valid.
func ExtentIndex(n int, f func(i int) jsval.Value) (lo, hi int) {
	i := 0
	var a, c jsval.Value
	for ; i < n; i++ {
		b := f(i)
		if !b.IsNullish() && GreaterEq(b, b) {
			a, c = b, b
			break
		}
	}
	if i == n {
		return -1, -1
	}
	lo, hi = i, i
	for i++; i < n; i++ {
		b := f(i)
		if b.IsNullish() {
			continue
		}
		if Greater(a, b) {
			a, lo = b, i
		}
		if Less(c, b) {
			c, hi = b, i
		}
	}
	return lo, hi
}

// Extent is vega-util's extent over the values f yields: the minimum and
// maximum ignoring null, undefined and NaN. ok is false for empty input;
// with only invalid values upstream answers [NaN|null|undefined, same], and
// this returns that first invalid value with ok=true.
func Extent(n int, f func(i int) jsval.Value) (lo, hi jsval.Value, ok bool) {
	if n == 0 {
		return jsval.Undefined, jsval.Undefined, false
	}
	i := 0
	v := f(0)
	for i < n && (v.IsNullish() || isNaNValue(v)) {
		i++
		if i < n {
			v = f(i)
		}
	}
	if i == n {
		// Upstream leaves min = max = the undefined read one past the end.
		return jsval.Undefined, jsval.Undefined, true
	}
	lo, hi = v, v
	for ; i < n; i++ {
		v = f(i)
		if v.IsNullish() {
			continue
		}
		if Less(v, lo) {
			lo = v
		}
		if Greater(v, hi) {
			hi = v
		}
	}
	return lo, hi, true
}

// ExtentOf is vega-util's extent(tuples, field). Its search for a first valid
// value is `for (v = f(a[i]); i < n && invalid(v); v = f(a[++i]))`: when every
// value is invalid it applies the accessor once more, to a[n], which is
// undefined, and an accessor that reads a property of its argument (every
// field and expression accessor does) throws a TypeError. The null field has
// no accessor and reads undefined.
func ExtentOf(tuples []jsval.Value, f Field) (lo, hi jsval.Value, ok bool, err error) {
	lo, hi, ok = Extent(len(tuples), func(i int) jsval.Value { return f.Apply(tuples[i]) })
	if ok && lo.IsUndefined() && !f.IsNil() {
		prop := f.Name
		if len(f.Fields) > 0 {
			prop = f.Fields[0]
		}
		if segs := jsval.ParseFieldPath(prop); len(segs) > 0 {
			prop = segs[0]
		}
		return lo, hi, ok, fmt.Errorf("Cannot read properties of undefined (reading '%s')", prop)
	}
	return lo, hi, ok, nil
}

// NumExtent is Extent for numeric fields: the min and max of the finite-order
// numbers f yields (NaN skipped). ok is false when there are none.
func NumExtent(n int, f func(i int) float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := 0; i < n; i++ {
		v := f(i)
		if v != v {
			continue
		}
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
		ok = true
	}
	return lo, hi, ok
}

// OrderedKeys returns keys in JavaScript's own-property enumeration order:
// canonical array-index keys ascending first, then the rest in insertion order.
// Vega builds several lookup tables as plain objects and iterates them, so
// group order can depend on it.
func OrderedKeys(keys []string) []string {
	var idx []string
	var rest []string
	for _, k := range keys {
		if isArrayIndexKey(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	if len(idx) == 0 {
		return keys
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 64)
		b, _ := strconv.ParseUint(idx[j], 10, 64)
		return a < b
	})
	return append(idx, rest...)
}

func isArrayIndexKey(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < 4294967295
}
