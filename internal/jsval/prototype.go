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
