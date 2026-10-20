package guides

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Thrown is an exception upstream's guide parser raises where a value of an
// unexpected type reaches it (a TypeError of a property read of undefined, a
// method missing from a value that is not a string): the guide fails as
// upstream's parse does (ADR 0013).
type Thrown struct{ msg string }

func (e *Thrown) Error() string { return e.msg }

// throw raises a Thrown, which the guide's entry point returns as its error.
func throw(format string, args ...any) { panic(&Thrown{msg: fmt.Sprintf(format, args...)}) }

// catch is deferred by the entry points: a Thrown becomes the error they
// return; any other panic (the call's budget stop) goes on.
func catch(err *error) {
	if r := recover(); r != nil {
		t, ok := r.(*Thrown)
		if !ok {
			panic(r)
		}
		*err = t
	}
}

// member is JavaScript's v[key] of a specification value of any type: an
// object's member (own, else Object.prototype's), an array's or a string's
// element, length or prototype method, a function's name, and the TypeError
// of a read of null or undefined.
func member(v Value, key string) Value {
	switch {
	case v.IsUndefined():
		throw("Cannot read properties of undefined (reading '%s')", key)
	case v.IsNull():
		throw("Cannot read properties of null (reading '%s')", key)
	case key == "name" && jsval.IsFunction(v):
		// the built-in methods a member read finds print as
		// "function NAME() { [native code] }"
		src := strings.TrimPrefix(v.AsString(), "function ")
		if i := strings.IndexByte(src, '('); i >= 0 {
			src = src[:i]
		}
		return jsval.Str(src)
	}
	return jsval.Member(v, key)
}

// forIn calls f with each key `for (const k in v)` enumerates and v[k]: an
// object's members, an array's or a string's indexes, nothing for any other
// value. Each key is a step of sc's work.
func forIn(sc Scope, v Value, f func(key string, val Value)) {
	switch v.Kind() {
	case jsval.KindObj:
		o := v.ObjValue()
		for i := 0; i < o.Len(); i++ {
			work(sc, 1)
			f(o.KeyAt(i), o.ValueAt(i))
		}
	case jsval.KindArr, jsval.KindStr:
		a, _ := jsval.ToArrayLike(v)
		for i := int64(0); i < a.Len(); i++ {
			work(sc, 1)
			f(strconv.FormatInt(i, 10), a.At(i))
		}
	}
}

type (
	// Value is the dynamic value model shared by the whole engine.
	Value = jsval.Value
	// Object is an insertion-ordered JavaScript object.
	Object = jsval.Object
)

// toValue converts the Go values the builders below accept. nil is undefined
// (the property is left out, like JSON.stringify leaves out undefined).
func toValue(x any) Value {
	switch v := x.(type) {
	case Value:
		return v
	case *Object:
		if v == nil {
			return jsval.Undefined
		}
		return jsval.Obj(v)
	case string:
		return jsval.Str(v)
	case int:
		return jsval.Int(v)
	case float64:
		return jsval.Num(v)
	case bool:
		return jsval.Bool(v)
	case []Value:
		return jsval.Arr(v)
	}
	return jsval.Undefined
}

// set assigns a property; assigning undefined removes it, which is what a JSON
// round trip of the JavaScript object would do.
func set(o *Object, key string, v Value) {
	if v.IsUndefined() {
		o.Delete(key)
		return
	}
	o.Set(key, v)
}

// obj builds an object from alternating string keys and values; undefined
// values are skipped.
func obj(pairs ...any) *Object {
	o := jsval.NewObject(len(pairs) / 2)
	for i := 0; i+1 < len(pairs); i += 2 {
		k, _ := pairs[i].(string)
		set(o, k, toValue(pairs[i+1]))
	}
	return o
}

// objv is obj as a Value.
func objv(pairs ...any) Value { return jsval.Obj(obj(pairs...)) }

// extend copies the properties of every source into dst (vega-util's
// extend: `for (k in src) dst[k] = src[k]`, so a string or an array source
// copies its indexes).
func extend(dst *Object, sources ...Value) *Object { return extendWork(nil, dst, sources...) }

// extendWork is extend charging each property it copies to sc (Scope.Work),
// for sources the specification or its config supply.
func extendWork(sc Scope, dst *Object, sources ...Value) *Object {
	var proto Value
	for _, s := range sources {
		forIn(sc, s, func(k string, v Value) {
			if k == "__proto__" {
				// dst.__proto__ = v (an object, or null): v's members
				// become readable through dst, and for-in lists them after
				// dst's own; any other value leaves the prototype as it is
				if v.IsObj() || v.IsArr() || v.IsNull() {
					proto = v
				}
				return
			}
			set(dst, k, v)
		})
	}
	forIn(sc, proto, func(k string, v Value) {
		if !dst.Has(k) {
			set(dst, k, v)
		}
	})
	return dst
}

// work charges n steps to sc (Scope.Work); a nil sc charges nothing.
func work(sc Scope, n int) {
	if sc != nil {
		sc.Work(n)
	}
}

// isObject is vega-util's isObject: arrays, dates and patterns count.
func isObject(v Value) bool {
	switch v.Kind() {
	case jsval.KindObj, jsval.KindArr, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

// isSignal is vega-parser's `_ && _.signal`.
func isSignal(v Value) bool { return v.IsTruthy() && member(v, "signal").IsTruthy() }

// signalOf is the text of `${v.signal}`, the expression of a signal
// reference as a template literal prints it.
func signalOf(v Value) string { return member(v, "signal").AsString() }

// value is vega-parser's value(): the default replaces null and undefined.
func value(v, dflt Value) Value {
	if v.IsNullish() {
		return dflt
	}
	return v
}

// or is JavaScript's `a || b`.
func or(a, b Value) Value {
	if a.IsTruthy() {
		return a
	}
	return b
}

// deref is vega-parser's deref: the signal expression of a signal reference,
// the value itself otherwise.
func deref(v Value) Value {
	if v.IsTruthy() {
		if s := member(v, "signal"); s.IsTruthy() {
			return s
		}
	}
	return v
}

// stringValue is vega-util's stringValue (see jsval.StringValue).
func stringValue(v Value) string { return jsval.StringValue(v) }

// hasKey is vega-util's hasOwnProperty(o, key) for a possibly non-object
// value: an object's own member, an array's or a string's index or length;
// null and undefined are the TypeError of their conversion to an object.
func hasKey(o Value, key string) bool {
	switch o.Kind() {
	case jsval.KindObj:
		return o.ObjValue().Has(key)
	case jsval.KindArr, jsval.KindStr:
		if key == "length" {
			return true
		}
		a, _ := jsval.ToArrayLike(o)
		i, err := strconv.ParseInt(key, 10, 64)
		return err == nil && strconv.FormatInt(i, 10) == key && i >= 0 && i < a.Len()
	case jsval.KindUndefined, jsval.KindNull:
		throw("Cannot convert undefined or null to object")
	}
	return false
}

// lookup implements guide-util's lookup(): a property is looked up in the
// axis/legend/title spec first and in the matching config block second. sc
// is the guide's Scope, which the builders charge their copies of the
// specification's encode blocks to (nil: none).
type lookup struct {
	spec, config Value
	sc           Scope
}

func (l lookup) get(name string) Value {
	return value(member(l.spec, name), member(l.config, name))
}

func (l lookup) getOr(name string, dflt Value) Value {
	return value(member(l.spec, name), value(member(l.config, name), dflt))
}

// cfg is `config[name]` of the guide's config block (which a specification
// may have replaced by any value).
func (l lookup) cfg(name string) Value { return member(l.config, name) }

// sp is `spec[name]` of the guide's specification.
func (l lookup) sp(name string) Value { return member(l.spec, name) }

// isVertical is `_.isVertical(s)`: s selects the symbol-direction default
// rather than the gradient-direction one.
func (l lookup) isVertical(symbol bool) bool {
	sv := l.sp("direction")
	dir := l.cfg("direction")
	if !dir.IsTruthy() {
		if symbol {
			dir = l.cfg("symbolDirection")
		} else {
			dir = l.cfg("gradientDirection")
		}
	}
	dir = value(sv, dir)
	return dir.IsStr() && dir.StrValue() == vertical
}

func (l lookup) gradientLength() Value {
	sv := l.sp("gradientLength")
	c := l.cfg("gradientLength")
	if !c.IsTruthy() {
		c = l.cfg("gradientWidth")
	}
	return value(sv, c)
}

func (l lookup) gradientThickness() Value {
	sv := l.sp("gradientThickness")
	c := l.cfg("gradientThickness")
	if !c.IsTruthy() {
		c = l.cfg("gradientHeight")
	}
	return value(sv, c)
}

// entryColumns is `_.entryColumns()`; the default is 1 column for vertical
// symbol legends and 0 (unbounded) otherwise.
func (l lookup) entryColumns() Value {
	d := 0
	if l.isVertical(true) {
		d = 1
	}
	sv, c := l.sp("columns"), l.cfg("columns")
	return value(sv, value(c, jsval.Int(d)))
}
