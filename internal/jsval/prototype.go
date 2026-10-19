package jsval

// Every JavaScript object inherits the members of Object.prototype, so a read
// of a name the object does not hold answers a function for these rather than
// undefined: datum.toString is a function whatever the row holds. The functions
// are modelled as inert objects that print as native code; the engine never
// calls them.

// protoFuncs maps an inherited member to the name of its function.
var protoFuncs = map[string]Value{}

func init() {
	for key, name := range map[string]string{
		"constructor":          "Object",
		"hasOwnProperty":       "hasOwnProperty",
		"isPrototypeOf":        "isPrototypeOf",
		"propertyIsEnumerable": "propertyIsEnumerable",
		"toString":             "toString",
		"toLocaleString":       "toLocaleString",
		"valueOf":              "valueOf",
		"__defineGetter__":     "__defineGetter__",
		"__defineSetter__":     "__defineSetter__",
		"__lookupGetter__":     "__lookupGetter__",
		"__lookupSetter__":     "__lookupSetter__",
	} {
		o := &Object{host: nativeFunction{}}
		src := "function " + name + "() { [native code] }"
		o.str = func(*Object) string { return src }
		protoFuncs[key] = Obj(o)
	}
}

// nativeFunction marks the objects that stand for a function of
// Object.prototype.
type nativeFunction struct{}

// NativeFunction is a function of a built-in prototype named name (an array's
// values, say), as inert as Inherited's: it prints as native code and JSON
// leaves it out.
func NativeFunction(name string) Value {
	src := "function " + name + "() { [native code] }"
	return Obj(&Object{host: nativeFunction{}, str: func(*Object) string { return src }})
}

// IsFunction reports whether v is one of the functions Inherited answers.
// JSON has no functions: JSON.stringify leaves such a member out of an object
// and writes null for it in an array.
func IsFunction(v Value) bool {
	if v.Kind() != KindObj {
		return false
	}
	_, ok := v.ObjValue().host.(nativeFunction)
	return ok
}

// Inherited answers the member of Object.prototype named key (the same value
// on every call), and false for a name that is not one. __proto__, which would
// answer Object.prototype itself, is not modelled.
func Inherited(key string) (Value, bool) {
	if len(key) < 7 || len(key) > 20 {
		return Undefined, false
	}
	v, ok := protoFuncs[key]
	return v, ok
}

// Prop is a JavaScript property read o[key]: the object's own value, else
// what its prototype answers (see SetPrototype), else the member of
// Object.prototype of that name, else undefined.
func (o *Object) Prop(key string) Value {
	if v, ok := o.Get(key); ok {
		return v
	}
	if v, ok := o.protoGet(key); ok {
		return v
	}
	v, _ := Inherited(key)
	return v
}

// protoHost is the host payload of an object whose prototype is another
// object or an array rather than Object.prototype.
type protoHost struct{ proto Value }

// SetPrototype makes p (an object or an array) the object's prototype: a
// property read the object does not answer itself (Value.Get, Prop,
// PropIn) is p's (an array's elements, its length and Array.prototype's
// members), as JavaScript reads through its prototype chain; its own keys
// (Keys, Has, Len, JSON) are unchanged. Only the Vega-Lite compiler sets
// one: upstream's code assigns a specification's key "__proto__" in places
// (vega-lite 5.8's clone, a copy made by assignment), and the assignment
// replaces the prototype. An object with a host payload of its own (a
// native function) takes no prototype. Anything but an object or an array
// gives the object Object.prototype back.
func (o *Object) SetPrototype(p Value) {
	switch {
	case p.Kind() != KindObj && p.Kind() != KindArr:
		if isProtoHost(o.host) {
			o.host = nil
		}
	case o.host == nil || isProtoHost(o.host):
		o.host = protoHost{p}
	}
}

// Prototype is the value SetPrototype set, undefined for Object.prototype.
func (o *Object) Prototype() Value {
	if ph, ok := o.host.(protoHost); ok {
		return ph.proto
	}
	return Undefined
}

func isProtoHost(h any) bool { _, ok := h.(protoHost); return ok }

// protoGet reads key through the object's prototype chain (not its own
// keys), at most MaxValueDepth prototypes deep: a chain only a
// specification's nesting builds.
func (o *Object) protoGet(key string) (Value, bool) {
	p := o.Prototype()
	for depth := 0; depth <= MaxValueDepth; depth++ {
		switch p.Kind() {
		case KindObj:
			if v, ok := p.obj().Get(key); ok {
				return v, true
			}
			p = p.obj().Prototype()
		case KindArr:
			items := p.arr().items
			if i, ok := ArrayIndex(key); ok && int64(i) < int64(len(items)) {
				return items[i], true
			}
			switch {
			case key == "length":
				return Int(len(items)), true
			case ArrayPrototypeMember(key):
				return NativeFunction(key), true
			}
			return Undefined, false
		default:
			return Undefined, false
		}
	}
	return Undefined, false
}

// ArrayPrototypeMember reports whether Array.prototype holds a member of
// that name (its methods).
func ArrayPrototypeMember(name string) bool { return arrayProtoMembers[name] }

var arrayProtoMembers = map[string]bool{
	"at": true, "concat": true, "copyWithin": true, "entries": true, "every": true, "fill": true, "filter": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "flat": true, "flatMap": true,
	"forEach": true, "includes": true, "indexOf": true, "join": true, "keys": true, "lastIndexOf": true,
	"map": true, "pop": true, "push": true, "reduce": true, "reduceRight": true, "reverse": true, "shift": true,
	"slice": true, "some": true, "sort": true, "splice": true, "toReversed": true, "toSorted": true,
	"toSpliced": true, "unshift": true, "values": true, "with": true,
}

// PropIn is `key in o` for a key Object.prototype does not hold: an own key,
// or one the prototype chain holds.
func (o *Object) PropIn(key string) bool {
	if o.Has(key) {
		return true
	}
	_, ok := o.protoGet(key)
	return ok
}
