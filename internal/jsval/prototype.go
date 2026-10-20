package jsval

import "strings"

// Every JavaScript object inherits the members of Object.prototype, so a read
// of a name the object does not hold answers a function for these rather than
// undefined: datum.toString is a function whatever the row holds. The functions
// are modelled as inert objects that print as native code; the engine never
// calls them.

// protoFuncs maps an inherited member to the function that stands for it.
var protoFuncs = map[string]Value{}

func init() {
	for key, arity := range objectProtoNames {
		if arity < 0 {
			continue // __proto__, which is not modelled
		}
		name := key
		if key == "constructor" {
			name = "Object"
		}
		protoFuncs[key] = NativeMethod(name, arity)
	}
}

// nativeFunction marks the objects that stand for a function of a built-in
// prototype: its name and its length (-1 when not known).
type nativeFunction struct {
	name  string
	arity int
}

// NativeFunction is a function of a built-in prototype named name (an array's
// values, say), as inert as Inherited's: it prints as native code and JSON
// leaves it out. Its length is not known; NativeMethod gives one.
func NativeFunction(name string) Value { return NativeMethod(name, -1) }

// NativeMethod is NativeFunction with the function's length, which a read of
// its `length` member answers.
func NativeMethod(name string, arity int) Value {
	src := "function " + name + "() { [native code] }"
	return Obj(&Object{host: nativeFunction{name: name, arity: arity}, str: func(*Object) string { return src }})
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

// Prop is a JavaScript property read o[key]: the object's own value, else,
// for a function, its length and name and Function.prototype's methods,
// else the member of Object.prototype of that name, else undefined.
func (o *Object) Prop(key string) Value {
	if v, ok := o.Get(key); ok {
		return v
	}
	if o.host != nil {
		if v, ok := functionMember(o, key); ok {
			return v
		}
	}
	v, _ := Inherited(key)
	return v
}

// functionMember is a function's own length and name, or a method of
// Function.prototype; ok is false for any other key or object.
func functionMember(o *Object, key string) (Value, bool) {
	var name string
	var arity int
	switch h := o.host.(type) {
	case nativeFunction:
		name, arity = h.name, h.arity
	case hostFunction:
		name, arity = functionSignature(o.str(o))
	default:
		return Undefined, false
	}
	switch key {
	case "length":
		if arity < 0 {
			return Undefined, false
		}
		return Int(arity), true
	case "name":
		return Str(name), true
	}
	if a, ok := functionProtoNames[key]; ok && a >= 0 {
		if key == "constructor" {
			return NativeMethod("Function", a), true
		}
		return NativeMethod(key, a), true
	}
	return Undefined, false
}

// functionSignature reads the name and the number of parameters of a
// function from its source text, `function name(a, b) {...}`, as
// Function.prototype.length counts them (no default or rest parameters in
// the sources the engine makes).
func functionSignature(src string) (name string, arity int) {
	open := strings.IndexByte(src, '(')
	close := strings.IndexByte(src, ')')
	if !strings.HasPrefix(src, "function") || open < 0 || close < open {
		return "", -1
	}
	name = JSTrim(src[len("function"):open])
	params := JSTrim(src[open+1 : close])
	if params == "" {
		return name, 0
	}
	return name, strings.Count(params, ",") + 1
}
