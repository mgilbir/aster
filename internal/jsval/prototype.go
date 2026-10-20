package jsval

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
)

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

// Prop is a JavaScript property read o[key]: the object's own value, else
// what its prototype answers (see SetPrototype), else, for a function, its
// length and name and Function.prototype's methods, else the member of
// Object.prototype of that name, else undefined.
func (o *Object) Prop(key string) Value {
	if v, ok := o.Get(key); ok {
		return v
	}
	if v, ok := o.protoGet(key); ok {
		return v
	}
	if o.host != nil {
		if v, ok := functionMember(o, key); ok {
			return v
		}
	}
	if !o.InheritsObjectPrototype() {
		return Undefined
	}
	v, _ := Inherited(key)
	return v
}

// protoHost is the host payload of an object whose prototype is another
// object, an array or null rather than Object.prototype.
type protoHost struct {
	proto Value
	// scope makes String of the object JavaScript's conversion while it is
	// open (see ConversionScope).
	scope *ConversionScope
}

// A ConversionScope is the span in which the objects given a prototype
// convert to text as JavaScript converts them: through the toString their
// prototype chain answers, which an object whose chain ends in null (or
// holds a toString that is not a function) does not have, so String of it
// is a TypeError, thrown as a panic of TypeError. The Vega-Lite compiler
// opens one per compilation and ends it when the compilation returns:
// what it hands on (the compiled specification, read by the Vega runtime)
// converts as before, "[object Object]", and never panics.
type ConversionScope struct {
	ended bool
	// depth is how many conversions are under way: one converts its
	// prototype's elements, which may be objects with prototypes of their
	// own (bounded by MaxValueDepth, as a walk is).
	depth int
}

// End closes the scope: its objects' String is "[object Object]" again.
func (s *ConversionScope) End() {
	if s != nil {
		s.ended = true
	}
}

// TypeError is JavaScript's TypeError, panicked by String of an object
// that has no conversion to text (see ConversionScope).
type TypeError struct{ Msg string }

func (e TypeError) Error() string { return e.Msg }

// SetPrototype makes p (an object, an array, or null for none) the object's
// prototype: a property read the object does not answer itself (Value.Get,
// Prop, PropIn) is p's (an array's elements, its length and
// Array.prototype's members), as JavaScript reads through its prototype
// chain, and with a null prototype the object inherits nothing, not even
// Object.prototype's members (Prop, InheritsObjectPrototype); its own keys
// (Keys, Has, Len, JSON) are unchanged. Only the Vega-Lite compiler sets
// one: upstream's code assigns a specification's key "__proto__" in places
// (vega-lite 5.8's clone, a copy made by assignment), and the assignment
// replaces the prototype. scope is the compilation's (see
// ConversionScope). An object with a host payload of its own (a native
// function) takes no prototype. Anything but an object, an array or null
// gives the object Object.prototype back.
func (o *Object) SetPrototype(p Value, scope *ConversionScope) {
	switch {
	case p.Kind() != KindObj && p.Kind() != KindArr && p.Kind() != KindNull:
		if isProtoHost(o.host) {
			o.host = nil
		}
	case o.host == nil || isProtoHost(o.host):
		o.host = protoHost{p, scope}
	}
}

// SetConversionScope puts an object whose prototype is Object.prototype in
// scope (see ConversionScope), so that its String is JavaScript's while the
// scope is open: a TypeError when the object holds a "toString" of its own,
// which no JSON value makes a function. An object with a prototype of its
// own keeps the scope SetPrototype gave it; a native function takes none.
func (o *Object) SetConversionScope(scope *ConversionScope) {
	if o.host == nil {
		o.host = protoHost{Undefined, scope}
	}
}

// ConversionScope is the scope SetPrototype or SetConversionScope gave the
// object, nil when none.
func (o *Object) ConversionScope() *ConversionScope {
	if ph, ok := o.host.(protoHost); ok {
		return ph.scope
	}
	return nil
}

// Prototype is the value SetPrototype set (null for none), undefined for
// Object.prototype.
func (o *Object) Prototype() Value {
	if ph, ok := o.host.(protoHost); ok {
		return ph.proto
	}
	return Undefined
}

func isProtoHost(h any) bool { _, ok := h.(protoHost); return ok }

// protoGet reads key through the object's prototype chain (not its own
// keys), at most MaxValueDepth prototypes deep: a chain only a
// specification's nesting builds. Object.prototype's members are not
// answered (see Prop).
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
			items := p.arr().list()
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

// ChainGet is o[key] without Object.prototype's members: o's own value, else
// what its prototype chain holds.
func (o *Object) ChainGet(key string) (Value, bool) {
	if v, ok := o.Get(key); ok {
		return v, true
	}
	return o.protoGet(key)
}

// InheritsObjectPrototype reports whether Object.prototype is on the
// object's prototype chain: false when the chain ends in null.
func (o *Object) InheritsObjectPrototype() bool {
	p := o.Prototype()
	for depth := 0; depth <= MaxValueDepth; depth++ {
		switch p.Kind() {
		case KindObj:
			p = p.obj().Prototype()
		case KindNull:
			return false
		default: // Object.prototype, or Array.prototype on top of it
			return true
		}
	}
	return true
}

// protoString is String(o) for an object given a prototype, inside its
// conversion scope: JavaScript's ToPrimitive, which calls the first
// toString on the chain (o's own, a prototype's, Array.prototype's or
// Object.prototype's). Array.prototype's joins o read as an array (its
// length and elements through the chain); Object.prototype's is
// "[object Object]". Without one that is a function (a JSON value never
// is; a chain ending in null has none), valueOf is tried, and
// Object.prototype's returns the object itself, which is no primitive:
// a TypeError.
func protoString(o *Object, scope *ConversionScope) string {
	scope.depth++
	defer func() { scope.depth-- }()
	if scope.depth > MaxValueDepth {
		stop(errWalkDepth)
	}
	if v, ok := o.Get("toString"); ok {
		if IsFunction(v) {
			return "[object Object]"
		}
		panic(TypeError{"Cannot convert object to primitive value"})
	}
	p := o.Prototype()
	for depth := 0; depth <= MaxValueDepth; depth++ {
		switch p.Kind() {
		case KindObj:
			if v, ok := p.obj().Get("toString"); ok {
				if IsFunction(v) {
					return "[object Object]"
				}
				panic(TypeError{"Cannot convert object to primitive value"})
			}
			p = p.obj().Prototype()
		case KindArr:
			return arrayToString(o)
		case KindNull:
			panic(TypeError{"Cannot convert object to primitive value"})
		default:
			return "[object Object]"
		}
	}
	return "[object Object]"
}

// arrayToString is Array.prototype.toString called on o: its join, unless
// o's chain answers a join that is not a function (Object.prototype's
// toString then), joining o.length elements read through the chain.
func arrayToString(o *Object) string {
	self := Obj(o)
	if !IsFunction(self.Get("join")) {
		return "[object Object]"
	}
	n := ToNumber(self.Get("length"))
	switch {
	case n != n || n < 1:
		return ""
	case n-1 > MaxStringLength:
		stop(fmt.Errorf("%w: a string of %v UTF-16 code units exceeds the limit of %d", budget.ErrLimit, n-1, MaxStringLength))
	}
	// Built as it goes, not as an array of n elements: a "length" o holds
	// itself may be far more than the elements its chain answers.
	var b strings.Builder
	for i := range int(n) {
		if i > 0 {
			b.WriteByte(',')
		}
		if v := self.Get(strconv.Itoa(i)); !v.IsNullish() {
			b.WriteString(v.AsString())
		}
		if b.Len() > maxStringBytes {
			stop(fmt.Errorf("%w: a value's text of more than %d bytes", budget.ErrLimit, maxStringBytes))
		}
	}
	return b.String()
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
