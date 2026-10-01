package upstream

import (
	"sort"

	"github.com/mgilbir/aster/internal/jsval"
)

// ToValue decodes a recorded argument into the engine's JavaScript-like value. A function marker
// becomes undefined (an adapter handles function arguments itself). Object keys come out sorted: the
// decoded JSON does not keep their order.
func ToValue(v any) jsval.Value {
	switch x := v.(type) {
	case nil:
		return jsval.Null
	case bool:
		return jsval.Bool(x)
	case float64:
		return jsval.Num(x)
	case string:
		return jsval.Str(x)
	case []any:
		items := make([]jsval.Value, len(x))
		for i, e := range x {
			items[i] = ToValue(e)
		}
		return jsval.Arr(items)
	case map[string]any:
		if f, ok := Num(x); ok {
			return jsval.Num(f)
		}
		if t, ok := Date(x); ok {
			return jsval.Timestamp(t)
		}
		if _, ok := IsMarker(x); ok {
			return jsval.Undefined
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := jsval.NewObject(len(keys))
		for _, k := range keys {
			o.Set(k, ToValue(x[k]))
		}
		return jsval.Obj(o)
	}
	return jsval.Undefined
}

// FromValue encodes an engine value the way the recorder encodes a JavaScript value.
func FromValue(v jsval.Value) any {
	switch v.Kind() {
	case jsval.KindUndefined:
		return Undefined()
	case jsval.KindNull:
		return nil
	case jsval.KindBool:
		return v.BoolValue()
	case jsval.KindNum:
		return Enc(v.NumValue())
	case jsval.KindStr:
		return v.StrValue()
	case jsval.KindTimestamp:
		return EncDate(v.NumValue())
	case jsval.KindArr:
		items := v.Items()
		out := make([]any, len(items))
		for i, e := range items {
			out[i] = FromValue(e)
		}
		return out
	case jsval.KindObj:
		out := map[string]any{}
		o := v.ObjValue()
		for _, k := range o.Keys() {
			out[k] = FromValue(o.Lookup(k))
		}
		return out
	}
	return Undefined()
}

// Arg returns the i-th argument, or the undefined marker when the call had fewer.
func (c *Call) Arg(i int) any {
	if i < len(c.Args) {
		return c.Args[i]
	}
	return Undefined()
}

// IsUndefined reports whether v is the recorder's undefined marker.
func IsUndefined(v any) bool {
	k, ok := IsMarker(v)
	return ok && k == "undefined"
}

// IsFunction reports whether v is a recorded function and, when it carries one, its origin: the
// export it is, or the call that returned it.
func IsFunction(v any) (origin map[string]any, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap || m["$"] != "function" {
		return nil, false
	}
	o, _ := m["origin"].(map[string]any)
	return o, true
}

// Number is JavaScript's `+v` for a recorded value.
func Number(v any) float64 { return jsval.ToNumber(ToValue(v)) }

// String is JavaScript's `v + ""` for a recorded value.
func String(v any) string { return ToValue(v).AsString() }

// Contains reports whether a recorded value holds, at any depth, a marker of one of the kinds (for
// example "function" or "typed"), an instance of a class when "$class" is listed, or a plain object
// when "object" is listed.
func Contains(v any, kinds ...string) bool {
	has := func(k string) bool {
		for _, want := range kinds {
			if want == k {
				return true
			}
		}
		return false
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if Contains(e, kinds...) {
				return true
			}
		}
	case map[string]any:
		if k, ok := x["$"].(string); ok && has(k) {
			return true
		}
		if _, ok := x["$class"]; ok && has("$class") {
			return true
		}
		if _, marker := x["$"]; !marker && has("object") {
			return true
		}
		for _, e := range x {
			if Contains(e, kinds...) {
				return true
			}
		}
	}
	return false
}

// ArgsContain reports whether any argument of the call, in its construction, chain or via steps or the
// call itself, holds a marker of one of the kinds (see Contains).
func (c *Call) ArgsContain(kinds ...string) bool {
	if Contains(c.Args, kinds...) || Contains(c.ConstructedWith, kinds...) {
		return true
	}
	for _, s := range c.ChainSteps() {
		if Contains(s.Args, kinds...) {
			return true
		}
	}
	for _, s := range c.ViaSteps() {
		if Contains(s.Args, kinds...) {
			return true
		}
	}
	return false
}

// Typed encodes values as the recorder encodes a typed array of the given constructor name.
func Typed(typ string, values []float64) any {
	return map[string]any{"$": "typed", "type": typ, "values": Floats(values)}
}
