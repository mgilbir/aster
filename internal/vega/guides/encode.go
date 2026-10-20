package guides

import "github.com/mgilbir/aster/internal/jsval"

// Ready-made encoders. Each call returns a fresh object because callers
// attach further properties (mult, offset) to the objects they receive.
func zero() Value { return objv("value", 0) }
func one() Value  { return objv("value", 1) }

// encoder is vega-parser's encoder(): a plain object is copied, any other
// value (an array too) becomes {value: v}.
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
// block (enter/update/exit/...), skipping the mark-level keys:
// `for (name in extra) encode[name] = extend(encode[name] || {}, extra[name])`,
// so the blocks of a string or an array are its indexes, and a block's own
// members are read as for-in reads them. The user's blocks are charged to
// sc, key by key (Scope.Work).
func extendEncode(sc Scope, encode *Object, extra Value) *Object {
	forIn(sc, extra, func(name string, val Value) {
		if skipKey(name) {
			return
		}
		if name == "__proto__" {
			// encode.__proto__ is Object.prototype: extend writes the
			// block's members there, onto every object of the process
			n := 0
			forIn(sc, val, func(string, Value) { n++ })
			if n > 0 {
				throw("guide: an encode block named __proto__ writes to Object.prototype, which is not modelled")
			}
			return
		}
		block := encode.Lookup(name).ObjValue()
		if block == nil {
			block = jsval.NewObject(0)
		}
		set(encode, name, jsval.Obj(extendWork(sc, block, val)))
	})
	return encode
}

// hasEncoder is `has(key, encode)`: the user encode sets key in enter or
// update (`encode && (encode.enter && encode.enter[key] || ...)`).
func hasEncoder(key string, encode Value) bool {
	if !encode.IsTruthy() {
		return false
	}
	if e := member(encode, "enter"); e.IsTruthy() && member(e, key).IsTruthy() {
		return true
	}
	u := member(encode, "update")
	return u.IsTruthy() && member(u, key).IsTruthy()
}

// guideMark finishes a guide mark definition with the user's per-mark extras
// (name, style, interactive and extra encoders), charged to l's Scope.
func guideMark(l lookup, mark *Object, extras Value) Value {
	if extras.IsTruthy() {
		set(mark, "name", member(extras, "name"))
		st := member(extras, "style")
		if !st.IsTruthy() {
			st = mark.Lookup("style")
		}
		set(mark, "style", st)
		mark.Set("interactive", jsval.Bool(member(extras, "interactive").IsTruthy()))
		set(mark, "encode", jsval.Obj(extendEncode(l.sc, mark.Lookup("encode").ObjValue(), extras)))
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
