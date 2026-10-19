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

// hostFunction marks an object that stands for a function the embedding
// program made at run time (a shape generator): impl is the Go value that
// does what the function does.
type hostFunction struct{ impl any }

// HostFunction is a new function value whose behaviour is impl, a Go value
// only the program that made it can read back (FunctionImpl): no JSON a
// specification or its data spell can stand for it. It prints as src, the
// source text of the function upstream returns, and like any function JSON
// leaves it out. It is garbage once nothing refers to it.
func HostFunction(src string, impl any) Value {
	return Obj(&Object{host: hostFunction{impl: impl}, str: func(*Object) string { return src }})
}

// FunctionImpl is the impl of a HostFunction value, and false for any other
// value.
func FunctionImpl(v Value) (any, bool) {
	if v.Kind() != KindObj {
		return nil, false
	}
	f, ok := v.ObjValue().host.(hostFunction)
	return f.impl, ok
}

// IsFunction reports whether v is one of the functions Inherited answers, or
// a HostFunction. JSON has no functions: JSON.stringify leaves such a member
// out of an object and writes null for it in an array.
func IsFunction(v Value) bool {
	if v.Kind() != KindObj {
		return false
	}
	switch v.ObjValue().host.(type) {
	case nativeFunction, hostFunction:
		return true
	}
	return false
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

// Prop is a JavaScript property read o[key]: the object's own value, else the
// member of Object.prototype of that name, else undefined.
func (o *Object) Prop(key string) Value {
	if v, ok := o.Get(key); ok {
		return v
	}
	v, _ := Inherited(key)
	return v
}
