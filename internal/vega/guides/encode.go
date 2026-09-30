package guides

import "github.com/mgilbir/aster/internal/jsval"

// Ready-made encoders. Each call returns a fresh object because callers
// attach further properties (mult, offset) to the objects they receive.
func zero() Value { return objv("value", 0) }
func one() Value  { return objv("value", 1) }

// encoder is vega-parser's encoder(): a plain object is copied, any other
// value becomes {value: v}.
func encoder(v Value) *Object {
	if v.IsObj() {
		return extend(jsval.NewObject(v.Len()), v)
	}
	return obj("value", v)
}

// kv is one property of an addEncoders group.
type kv struct {
	name string
	v    Value
}

// addEncode is vega-parser's addEncode: encoder objects (and non-empty arrays
// of encoder rules) always go to the update block so that signals re-evaluate;
// plain values go to `set` (enter by default) as {value}.
func addEncode(encode *Object, name string, v Value, set_ string) {
	if v.IsNullish() {
		return
	}
	isEnc := v.IsObj() || (v.IsArr() && v.Len() > 0 && isObject(v.Index(0)))
	if isEnc {
		encode.Lookup("update").ObjValue().Set(name, v)
		return
	}
	encode.Lookup(set_).ObjValue().Set(name, objv("value", v))
}

// addEncoders adds the enter group and then the group that needs `update`.
func addEncoders(encode *Object, enter []kv, update []kv) {
	for _, p := range enter {
		addEncode(encode, p.name, p.v, "enter")
	}
	for _, p := range update {
		addEncode(encode, p.name, p.v, "update")
	}
}

// extendEncode merges a user encode block into a generated one, block by
// block (enter/update/exit/...), skipping the mark-level keys.
func extendEncode(encode *Object, extra Value) *Object {
	eo := extra.ObjValue()
	if eo == nil {
		return encode
	}
	for i := 0; i < eo.Len(); i++ {
		name := eo.KeyAt(i)
		if skipKey(name) {
			continue
		}
		block := encode.Lookup(name).ObjValue()
		if block == nil {
			block = jsval.NewObject(0)
		}
		set(encode, name, jsval.Obj(extend(block, eo.ValueAt(i))))
	}
	return encode
}

// hasEncoder is `has(key, encode)`: the user encode sets key in enter or update.
func hasEncoder(key string, encode Value) bool {
	return encode.Get("enter").Get(key).IsTruthy() || encode.Get("update").Get(key).IsTruthy()
}

// guideMark finishes a guide mark definition with the user's per-mark extras
// (name, style, interactive and extra encoders).
func guideMark(mark *Object, extras Value) Value {
	if extras.IsObj() {
		set(mark, "name", extras.Get("name"))
		st := extras.Get("style")
		if !st.IsTruthy() {
			st = mark.Lookup("style")
		}
		set(mark, "style", st)
		mark.Set("interactive", jsval.Bool(extras.Get("interactive").IsTruthy()))
		set(mark, "encode", jsval.Obj(extendEncode(mark.Lookup("encode").ObjValue(), extras)))
	} else {
		mark.Set("interactive", jsval.False)
	}
	return jsval.Obj(mark)
}

// guideGroup marks a definition as a non-interactive-by-default group.
func guideGroup(mark *Object) Value {
	mark.Set("type", jsval.Str("group"))
	if !mark.Lookup("interactive").IsTruthy() {
		mark.Set("interactive", jsval.False)
	}
	return jsval.Obj(mark)
}
