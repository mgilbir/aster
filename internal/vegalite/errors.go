package vegalite

import (
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// compileError is the value thrown (as a panic) where upstream throws an
// Error. Compile recovers it and returns the message as an ordinary error, so
// nothing reachable from a specification escapes as a panic.
type compileError struct{ msg string }

func (e compileError) Error() string { return e.msg }

func throw(format string, args ...any) {
	panic(compileError{fmt.Sprintf(format, args...)})
}

// limitError is the value thrown where a specification exceeds one of the
// compiler's own limits, which upstream does not have. Compile returns it as
// an error wrapping budget.ErrLimit.
type limitError struct{ msg string }

func (e limitError) Error() string { return e.msg }
func (e limitError) Unwrap() error { return budget.ErrLimit }

func exceeded(format string, args ...any) {
	panic(limitError{fmt.Sprintf(format, args...)})
}

// depthMsg reports a specification that nests composition operators (layer,
// concat, facet, repeat) deeper than maxDepth.
const depthMsg = "vegalite: specification is nested too deeply"

// maxDepth bounds the nesting of composite specifications, far beyond any
// real chart.
const maxDepth = 64

// maxTransforms bounds the data transforms of one specification. Each becomes
// a node in the dataflow tree, in a chain with the transforms before it, and
// the optimizer and the assembler walk that tree recursively: the transform
// count is the recursion depth. Real charts have a few dozen; 10000 keeps the
// walk to a stack of a few MB.
const maxTransforms = 10000

// DefaultMaxViews is the views a compilation may create when
// Options.MaxViews is 0: what the default memory limit (2 GiB) admits at
// about 32 KiB a compiled view (24 KiB of a plain unit's output is retained).
const DefaultMaxViews = 65536

// Where upstream's compiler meets a value of the wrong type it mostly does not
// check: it throws the TypeError JavaScript raises on the operation (`.map` of
// an object, `in` on a string), and the compilation fails. The helpers below
// raise the same error at the same operation, with JavaScript's message,
// which names the property (ADR 0013).

// nullishName is how a TypeError names a null or undefined value.
func nullishName(v Value) string {
	if v.IsNull() {
		return "null"
	}
	return "undefined"
}

// listOf is the items of v where upstream calls expr.map(...) (or another
// array method) on it: a non-array is a TypeError.
func listOf(v Value, expr, method string) []Value {
	switch {
	case v.IsArr():
		return v.Items()
	case v.IsNullish():
		throw("Cannot read properties of %s (reading '%s')", nullishName(v), method)
	}
	throw("%s.%s is not a function", expr, method)
	return nil
}

// iterate is the values `for (const x of v)` visits: an array's items, a
// string's code points; anything else is not iterable.
func iterate(v Value, name string) []Value {
	switch {
	case v.IsArr():
		return v.Items()
	case v.IsStr():
		var out []Value
		for _, r := range v.StrValue() {
			out = append(out, jsval.Str(string(r)))
		}
		return out
	}
	throw("%s is not iterable", name)
	return nil
}

// jsIn is JavaScript's `key in v`, which throws on a value that is not an
// object. It sees inherited members too: `'filter' in []` and
// `'values' in []` are true (Array.prototype's methods), as is
// `'constructor' in {}`.
func jsIn(v Value, key string) bool {
	if !isObject(v) {
		throw("Cannot use 'in' operator to search for '%s' in %s", key, v.AsString())
	}
	if _, ok := jsval.Inherited(key); ok {
		return true
	}
	if v.IsArr() {
		if n, ok := arrayIndex(key); ok {
			return int64(n) < int64(v.Len())
		}
		return key == "length" || arrayProtoMembers[key]
	}
	return v.ObjValue().Has(key)
}

// arrayProtoMembers are the names Array.prototype holds.
var arrayProtoMembers = strSet([]string{
	"at", "concat", "copyWithin", "entries", "every", "fill", "filter", "find", "findIndex", "findLast",
	"findLastIndex", "flat", "flatMap", "forEach", "includes", "indexOf", "join", "keys", "lastIndexOf",
	"map", "pop", "push", "reduce", "reduceRight", "reverse", "shift", "slice", "some", "sort", "splice",
	"toReversed", "toSorted", "toSpliced", "unshift", "values", "with",
})

// varNameOf is varName of a value upstream passes as it is: varName calls
// s.replace, a TypeError on anything but a string.
func varNameOf(v Value) string {
	switch {
	case v.IsStr():
		return varName(v.StrValue())
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'replace')", nullishName(v))
	}
	throw("s.replace is not a function")
	return ""
}

// iterateNew is the values `new Set(v)` (or a spread) takes: none for null
// or undefined, else as iterate, with V8's message for a value that is not
// iterable.
func iterateNew(v Value) []Value {
	switch {
	case v.IsNullish():
		return nil
	case v.IsArr(), v.IsStr():
		return iterate(v, "")
	case v.IsObj():
		throw("object is not iterable (cannot read property Symbol(Symbol.iterator))")
	}
	what := "number"
	if v.IsBool() {
		what = "boolean"
	}
	throw("%s %s is not iterable (cannot read property Symbol(Symbol.iterator))", what, v.AsString())
	return nil
}
