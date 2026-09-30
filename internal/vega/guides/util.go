package guides

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

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

// extend copies the properties of every source object into dst
// (vega-util's extend).
func extend(dst *Object, sources ...Value) *Object {
	for _, s := range sources {
		src := s.ObjValue()
		if src == nil {
			continue
		}
		for i := 0; i < src.Len(); i++ {
			set(dst, src.KeyAt(i), src.ValueAt(i))
		}
	}
	return dst
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
func isSignal(v Value) bool { return v.IsObj() && v.Get("signal").IsTruthy() }

// signalOf reads the expression of a signal reference.
func signalOf(v Value) string { return v.Get("signal").AsString() }

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
	if v.IsObj() {
		if s := v.Get("signal"); s.IsTruthy() {
			return s
		}
	}
	return v
}

// stringValue is vega-util's stringValue converted the way a template literal
// converts its result: strings and objects become JSON, anything else its
// JavaScript string form.
func stringValue(v Value) string {
	switch v.Kind() {
	case jsval.KindArr:
		var b strings.Builder
		b.WriteByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			if !it.IsNullish() {
				b.WriteString(stringValue(it))
			}
		}
		b.WriteByte(']')
		return b.String()
	case jsval.KindObj, jsval.KindStr:
		s := string(jsval.AppendJSON(nil, v))
		s = strings.Replace(s, " ", ` `, 1)
		return strings.Replace(s, " ", ` `, 1)
	}
	return v.AsString()
}

// hasKey reports whether o (a possibly non-object value) owns key.
func hasKey(o Value, key string) bool {
	ob := o.ObjValue()
	return ob != nil && ob.Has(key)
}

// lookup implements guide-util's lookup(): a property is looked up in the
// axis/legend/title spec first and in the matching config block second.
type lookup struct{ spec, config Value }

func (l lookup) get(name string) Value {
	return value(l.spec.Get(name), l.config.Get(name))
}

func (l lookup) getOr(name string, dflt Value) Value {
	return value(l.spec.Get(name), value(l.config.Get(name), dflt))
}

// isVertical is `_.isVertical(s)`: s selects the symbol-direction default
// rather than the gradient-direction one.
func (l lookup) isVertical(symbol bool) bool {
	fallback := l.config.Get("gradientDirection")
	if symbol {
		fallback = l.config.Get("symbolDirection")
	}
	dir := value(l.spec.Get("direction"), or(l.config.Get("direction"), fallback))
	return dir.IsStr() && dir.StrValue() == vertical
}

func (l lookup) gradientLength() Value {
	return value(l.spec.Get("gradientLength"), or(l.config.Get("gradientLength"), l.config.Get("gradientWidth")))
}

func (l lookup) gradientThickness() Value {
	return value(l.spec.Get("gradientThickness"), or(l.config.Get("gradientThickness"), l.config.Get("gradientHeight")))
}

// entryColumns is `_.entryColumns()`; the default is 1 column for vertical
// symbol legends and 0 (unbounded) otherwise.
func (l lookup) entryColumns() Value {
	d := 0
	if l.isVertical(true) {
		d = 1
	}
	return value(l.spec.Get("columns"), value(l.config.Get("columns"), jsval.Int(d)))
}
