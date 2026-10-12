package vegalite

import (
	"reflect"

	"github.com/mgilbir/aster/internal/jsval"
)

// Upstream keeps much of its state in plain JavaScript objects ({}) used as
// maps keyed by names a specification chooses: a parse keyed by field, an
// aggregate's measures, the fields a filter checks, a selection's projections.
// A plain object is not a map. It inherits from Object.prototype, so a field
// named after one of its members ("constructor", "toString", …) reads as
// present (`field in parse`, `meas[field] ??= {}` finds the function and keeps
// it), and assigning to "__proto__" replaces the object's prototype (an object
// value) or does nothing (a string), so that key never becomes the object's
// own. Writing through such an inherited value writes on the built-in itself:
// `meas.__proto__.mean = set` adds "mean" to Object.prototype, which every
// object then inherits. In node that change outlives the compilation; here it
// does not (ADR 0013).
//
// jsRealm is that model, one per compilation: Object.prototype, the functions
// it holds, the properties upstream wrote on them, and the prototypes
// assigned to plain-object maps. omap (a plain object keyed by name) and the
// object-valued maps of jsval go through it for the operations whose answer
// differs from a map's: assign, in and read.
type jsRealm struct {
	hosts map[string]*jsHost // "" is Object.prototype; else the member of that name
	// protos are the prototypes assigned to jsval objects used as maps.
	protos map[*Object]jsProto
}

// jsHost is a built-in object a plain object reaches through its prototype:
// Object.prototype itself, or a function it holds (Object, for
// "constructor"). props are what upstream wrote on it in this compilation.
type jsHost struct {
	name  string
	props *omap[any]
}

// jsProto is a plain object's prototype once "__proto__" was assigned an
// object or null; the zero value is Object.prototype.
type jsProto struct {
	set  bool
	null bool
	obj  any // the object assigned: a jsval Value, or a compiler component
}

// jsRef is what a property read o[key] answers when o does not hold key
// itself: a built-in (host), or a value (val) found on the prototype chain.
type jsRef struct {
	found bool
	host  *jsHost
	val   any
}

func (c *compileCtx) realm() *jsRealm {
	if c.jsr == nil {
		c.jsr = &jsRealm{hosts: map[string]*jsHost{}}
	}
	return c.jsr
}

func (r *jsRealm) host(name string) *jsHost {
	h := r.hosts[name]
	if h == nil {
		h = &jsHost{name: name, props: newOmap[any]()}
		r.hosts[name] = h
	}
	return h
}

// isJSObjectValue says whether x, assigned to "__proto__", is an object
// (which becomes the prototype), null (likewise), or a primitive (ignored).
func isJSObjectValue(x any) (object, null bool) {
	switch v := x.(type) {
	case Value:
		switch {
		case v.IsNull():
			return false, true
		case v.IsObj(), v.IsArr():
			return true, false
		}
		return false, false
	case string, float64, int, bool:
		return false, false
	case nil:
		return false, false
	}
	return true, false
}

// protoAssigned is the prototype after o.__proto__ = x, from p.
func protoAssigned(p jsProto, x any) jsProto {
	if obj, null := isJSObjectValue(x); obj {
		return jsProto{set: true, obj: x}
	} else if null {
		return jsProto{set: true, null: true}
	}
	return p
}

// inherited is the read of key through prototype p (the object itself does
// not hold key).
func (r *jsRealm) inherited(p jsProto, key string) jsRef {
	if p.set {
		if p.null {
			return jsRef{}
		}
		if key == "__proto__" {
			return jsRef{found: true, val: p.obj}
		}
		switch v := p.obj.(type) {
		case Value:
			if v.IsObj() {
				if x, ok := v.ObjValue().Get(key); ok {
					return jsRef{found: true, val: x}
				}
			}
		case *omap[any]:
			if x, ok := v.get(key); ok {
				return jsRef{found: true, val: x}
			}
		}
		// The assigned object's own prototype is Object.prototype.
	}
	if key == "__proto__" {
		return jsRef{found: true, host: r.host("")}
	}
	if _, ok := jsval.Inherited(key); ok {
		return jsRef{found: true, host: r.host(key)}
	}
	if x, ok := r.host("").props.get(key); ok {
		return jsRef{found: true, val: x}
	}
	return jsRef{}
}

// value is the read as a jsval Value: a built-in function reads as
// jsval.Inherited's function, Object.prototype as an empty object.
func (ref jsRef) value() Value {
	switch {
	case !ref.found:
		return undef
	case ref.host != nil && ref.host.name == "":
		return jsval.Obj(jsval.NewObject(0))
	case ref.host != nil:
		v, _ := jsval.Inherited(ref.host.name)
		return v
	}
	if v, ok := ref.val.(Value); ok {
		return v
	}
	return jsval.Obj(jsval.NewObject(0))
}

// write is ref[key] = x: on a built-in, recorded for the rest of the
// compilation; on an object reached through an assigned prototype, on that
// object (a jsval object; a compiler component has no such properties).
func (r *jsRealm) write(ref jsRef, key string, x any) {
	switch {
	case ref.host != nil:
		if key == "__proto__" {
			if ref.host.name == "" {
				if obj, null := isJSObjectValue(x); obj || null {
					throw("Immutable prototype object 'Object.prototype' cannot have their prototype set")
				}
			}
			return
		}
		ref.host.props.set(key, x)
	default:
		if v, ok := ref.val.(Value); ok && v.IsObj() {
			if xv, ok := x.(Value); ok {
				jsAssign(r, v.ObjValue(), key, xv)
			}
		}
	}
}

// jsAssign is o[key] = v on a jsval object upstream uses as a plain object.
// An own "__proto__" (JSON.parse makes one) is an ordinary property.
func jsAssign(r *jsRealm, o *Object, key string, v Value) {
	if key == "__proto__" && !o.Has(key) {
		if r.protos == nil {
			r.protos = map[*Object]jsProto{}
		}
		r.protos[o] = protoAssigned(r.protos[o], v)
		return
	}
	o.Set(key, v)
}

// jsHasKey is `key in o` on a jsval object upstream uses as a plain object.
func jsHasKey(r *jsRealm, o *Object, key string) bool {
	if o.Has(key) {
		return true
	}
	return r.inherited(r.protos[o], key).found
}

// assign is o[k] = v, JavaScript's assignment to a plain object.
func (o *omap[V]) assign(k string, v V) {
	if k == "__proto__" && !o.has(k) {
		o.proto = protoAssigned(o.proto, v)
		return
	}
	o.set(k, v)
}

// in is `k in o`.
func (o *omap[V]) in(r *jsRealm, k string) bool {
	if o.has(k) {
		return true
	}
	return r.inherited(o.proto, k).found
}

// read is o[k]: the own value (own true), else what the prototype chain
// answers.
func (o *omap[V]) read(r *jsRealm, k string) (v V, own bool, ref jsRef) {
	if v, ok := o.get(k); ok {
		return v, true, jsRef{}
	}
	return v, false, r.inherited(o.proto, k)
}

// objectStatics are the functions Object holds (Object.keys, …), which a
// read through "constructor" finds.
var objectStatics = strSet([]string{
	"assign", "create", "defineProperties", "defineProperty", "entries", "freeze", "fromEntries",
	"getOwnPropertyDescriptor", "getOwnPropertyDescriptors", "getOwnPropertyNames", "getOwnPropertySymbols",
	"getPrototypeOf", "groupBy", "hasOwn", "is", "isExtensible", "isFrozen", "isSealed", "keys",
	"preventExtensions", "seal", "setPrototypeOf", "values",
})

// functionProtoMembers are Function.prototype's methods, which every
// function inherits.
var functionProtoMembers = strSet([]string{"apply", "bind", "call", "toString"})

// protoFuncLength is each Object.prototype function's length.
var protoFuncLength = map[string]int{
	"constructor": 1, "hasOwnProperty": 1, "isPrototypeOf": 1, "propertyIsEnumerable": 1,
	"__defineGetter__": 2, "__defineSetter__": 2, "__lookupGetter__": 1, "__lookupSetter__": 1,
}

// hostGet is h[key]: what upstream wrote on the built-in in this
// compilation, else its own members and those it inherits.
func (r *jsRealm) hostGet(h *jsHost, key string) (any, bool) {
	if x, ok := h.props.get(key); ok {
		return x, true
	}
	if h.name == "" { // Object.prototype
		if key == "__proto__" {
			return jsval.Null, true
		}
		if _, ok := jsval.Inherited(key); ok {
			return r.host(key), true
		}
		return nil, false
	}
	name := h.name
	if name == "constructor" {
		name = "Object"
	}
	switch {
	case key == "name":
		return jsval.Str(name), true
	case key == "length":
		return jsval.Int(protoFuncLength[h.name]), true
	case h.name == "constructor" && key == "prototype":
		return r.host(""), true
	case h.name == "constructor" && objectStatics[key]:
		return jsval.NativeFunction(key), true
	case functionProtoMembers[key]:
		return jsval.NativeFunction(key), true
	case key == "constructor":
		return jsval.NativeFunction("Function"), true
	case key == "__proto__":
		return jsval.NativeFunction(""), true // Function.prototype
	}
	if _, ok := jsval.Inherited(key); ok {
		return r.host(key), true
	}
	if x, ok := r.host("").props.get(key); ok {
		return x, true
	}
	return nil, false
}

// get is ref[key], for a value a plain object read answered.
func (r *jsRealm) get(ref jsRef, key string) (any, bool) {
	switch {
	case ref.host != nil:
		return r.hostGet(ref.host, key)
	case !ref.found:
		throw("Cannot read properties of undefined (reading '%s')", key)
	}
	switch v := ref.val.(type) {
	case Value:
		if v.IsNullish() {
			throw("Cannot read properties of %s (reading '%s')", nullishName(v), key)
		}
		if v.IsObj() {
			if x, ok := v.ObjValue().Get(key); ok {
				return x, true
			}
		}
		ref := r.inherited(jsProto{}, key)
		if ref.host != nil {
			return ref.host, true
		}
		return ref.val, ref.found
	case *sset:
		// A Set's own members are its methods, which upstream calls.
		if key == "add" || key == "has" || key == "forEach" || key == "size" {
			return jsval.NativeFunction(key), true
		}
	}
	x := r.inherited(jsProto{}, key)
	if x.host != nil {
		return x.host, true
	}
	return x.val, x.found
}

// isNullishAny says whether a read answered undefined or null.
func isNullishAny(x any, found bool) bool {
	if !found || x == nil {
		return true
	}
	if v, ok := x.(Value); ok {
		return v.IsNullish()
	}
	return false
}

// jsRefTruthy is the truthiness of what a read through the prototype chain
// answered: a built-in is an object, so true.
func jsRefTruthy(ref jsRef) bool {
	switch {
	case !ref.found:
		return false
	case ref.host != nil:
		return true
	}
	if v, ok := ref.val.(Value); ok {
		return v.IsTruthy()
	}
	return ref.val != nil
}

// truthy is `!!o[k]` for a map of objects (an own nil is a missing value).
func (o *omap[V]) truthy(r *jsRealm, k string) bool {
	v, own, ref := o.read(r, k)
	if own {
		if x, ok := any(v).(Value); ok {
			return x.IsTruthy()
		}
		rv := reflect.ValueOf(any(v))
		switch rv.Kind() {
		case reflect.Invalid:
			return false
		case reflect.Pointer, reflect.Map:
			return !rv.IsNil()
		}
		// A slice stands for an array, which is an object even when empty.
		return true
	}
	return jsRefTruthy(ref)
}

// isInheritedKey says whether `key in o` holds for every plain object o,
// whatever it holds: "__proto__" and Object.prototype's members. A test
// `key in TABLE` against one of upstream's constant tables passes for them.
func isInheritedKey(key string) bool {
	if key == "__proto__" {
		return true
	}
	_, ok := jsval.Inherited(key)
	return ok
}

// inheritedValue is what reading key from one of upstream's constant tables
// (a plain object literal) answers when the table does not hold it but every
// object inherits it: Object.prototype for "__proto__" (an empty object, as
// JSON prints it), else the member function.
func inheritedValue(key string) (Value, bool) {
	if key == "__proto__" {
		return jsval.Obj(jsval.NewObject(0)), true
	}
	return jsval.Inherited(key)
}

// jsSeen is `v in u` in upstream's util.unique (and the similar
// `!map[name]`), whose u is a plain object: a key it set, or one every object
// inherits, so a value whose key is "__proto__" or "toString" is dropped as
// already seen.
func jsSeen(cc *compileCtx, seen map[string]bool, k string) bool {
	if seen[k] || isInheritedKey(k) {
		return true
	}
	return cc != nil && cc.jsr != nil && cc.jsr.host("").props.has(k)
}

// skipsInClone58 says whether 5.8's duplicate (the clone package) leaves out
// an object's key: it copies by for…in but skips a key Object.prototype holds
// as a data property, its members ("toString", "constructor", …) and
// anything written there in this compilation; "__proto__" goes through the
// setter, so it never becomes the copy's own key either.
func skipsInClone58(cc *compileCtx, key string) bool {
	if isInheritedKey(key) {
		return true
	}
	return cc != nil && cc.jsr != nil && cc.jsr.host("").props.has(key)
}

// duplicate is upstream's duplicate: structuredClone in 6.4, the clone
// package in 5.8 (see skipsInClone58).
func duplicate(cc *compileCtx, v Value) Value {
	if cc == nil || !cc.v5 {
		return deepClone(cc, v)
	}
	cc.work(1)
	switch v.Kind() {
	case jsval.KindArr:
		items := v.Items()
		out := make([]Value, len(items))
		for i, it := range items {
			out[i] = duplicate(cc, it)
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		if jsval.IsFunction(v) {
			return v
		}
		o := v.ObjValue()
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			if k := o.KeyAt(i); !skipsInClone58(cc, k) {
				c.Set(k, duplicate(cc, o.ValueAt(i)))
			}
		}
		jsOrderKeys(cc, c)
		return jsval.Obj(c)
	}
	return v
}
