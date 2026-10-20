package jsval

import (
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

func TestInheritedProperties(t *testing.T) {
	o := ObjectOf("a", Num(1), "valueOf", Str("own"))
	if got := o.Prop("a"); !got.IsNum() || got.NumValue() != 1 {
		t.Errorf("own value: %v", got)
	}
	if got := o.Prop("valueOf"); !got.IsStr() || got.StrValue() != "own" {
		t.Errorf("an own property wins: %v", got)
	}
	if got := o.Prop("missing"); !got.IsUndefined() {
		t.Errorf("missing: %v", got)
	}
	for key, want := range map[string]string{
		"toString":       "function toString() { [native code] }",
		"constructor":    "function Object() { [native code] }",
		"hasOwnProperty": "function hasOwnProperty() { [native code] }",
	} {
		got := o.Prop(key)
		if !got.IsObj() || got.AsString() != want || !got.IsTruthy() || !got.AsBoolean() {
			t.Errorf("%s: %q", key, got.AsString())
		}
		if !SameRef(got, o.Prop(key)) {
			t.Errorf("%s: two reads gave two functions", key)
		}
		if ToNumber(got) == ToNumber(got) {
			t.Errorf("%s: a function is not a number", key)
		}
	}
	if _, ok := Inherited("length"); ok {
		t.Error("length is not inherited from Object.prototype")
	}
}

// TestInheritedFunctionsAreImmutable: the members Inherited answers are one
// value per process, so a write on one must not reach a later read
// (upstream's writes on built-ins are kept per compilation by its caller).
func TestInheritedFunctionsAreImmutable(t *testing.T) {
	f, _ := Inherited("constructor")
	f.ObjValue().Set("mean", Str("x"))
	if g, _ := Inherited("constructor"); g.ObjValue().Has("mean") {
		t.Fatal("a write on an inherited function is visible to the next reader")
	}
	n := NativeFunction("values")
	n.ObjValue().Set("a", Int(1))
	if n.ObjValue().Has("a") {
		t.Fatal("a native function was written to")
	}
}

// stringOf is v.AsString(), with the TypeError it panics with as an error.
func stringOf(v Value) (s string, err error) {
	defer func() {
		if r := recover(); r != nil {
			te, ok := r.(TypeError)
			if !ok {
				panic(r)
			}
			err = te
		}
	}()
	return v.AsString(), nil
}

func mustParse(t *testing.T, src string) Value {
	t.Helper()
	v, err := ParseJSONString(src)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestNullPrototype: an object given a null prototype inherits nothing,
// Object.prototype's members included, and in its conversion scope String
// of it is JavaScript's TypeError; once the scope ends it is
// "[object Object]" again.
func TestNullPrototype(t *testing.T) {
	scope := &ConversionScope{}
	o := mustParse(t, `{"a":1}`).ObjValue()
	o.SetPrototype(Null, scope)
	if !o.Prototype().IsNull() {
		t.Fatalf("Prototype() = %v, want null", o.Prototype())
	}
	if o.InheritsObjectPrototype() {
		t.Error("InheritsObjectPrototype() = true for a null prototype")
	}
	if v := o.Prop("toString"); !v.IsUndefined() {
		t.Errorf(`Prop("toString") = %v, want undefined`, v)
	}
	if v := o.Prop("a"); v.AsString() != "1" {
		t.Errorf(`Prop("a") = %v, want 1`, v)
	}
	if _, err := stringOf(Obj(o)); err == nil || err.Error() != "Cannot convert object to primitive value" {
		t.Errorf("String of a null-prototype object: err = %v, want the TypeError", err)
	}
	// Nested in an array, the conversion fails as well.
	if _, err := stringOf(ArrOf(Obj(o))); err == nil {
		t.Error("String of [nullProto]: no TypeError")
	}
	scope.End()
	if s, err := stringOf(Obj(o)); err != nil || s != "[object Object]" {
		t.Errorf("after End: %q, %v; want [object Object]", s, err)
	}
	// A plain object (no scope) holding a toString of its own: String of it
	// is the TypeError of upstream's OrdinaryToPrimitive (a *Thrown), as
	// before there was any scope.
	func() {
		defer func() {
			if r := recover(); r != errNoPrimitive {
				t.Errorf("unscoped object with a toString of its own: recovered %v, want the TypeError", r)
			}
		}()
		_ = mustParse(t, `{"toString":1}`).AsString()
	}()
}

// TestPrototypeChainConversion: String of an object follows its chain to the
// first toString: a null at the end of a deeper prototype, a toString held as
// data, an array (Array.prototype.toString joins the object read as an
// array), Object.prototype.
func TestPrototypeChainConversion(t *testing.T) {
	scope := &ConversionScope{}
	nullEnd := NewObject(0)
	nullEnd.SetPrototype(Null, scope)
	viaNull := NewObject(0)
	viaNull.SetPrototype(Obj(nullEnd), scope)
	if viaNull.InheritsObjectPrototype() {
		t.Error("a chain ending in null: InheritsObjectPrototype() = true")
	}
	if _, err := stringOf(Obj(viaNull)); err == nil {
		t.Error("chain ending in null: no TypeError")
	}

	dataToString := NewObject(0)
	dataToString.SetPrototype(mustParse(t, `{"toString":"x"}`), scope)
	if _, err := stringOf(Obj(dataToString)); err == nil {
		t.Error("a toString held as data on the chain: no TypeError")
	}

	arr := NewObject(0)
	arr.SetPrototype(mustParse(t, `["q",1,null,[2,3]]`), scope)
	if s, err := stringOf(Obj(arr)); err != nil || s != "q,1,,2,3" {
		t.Errorf("array prototype: %q, %v; want q,1,,2,3", s, err)
	}
	// Its own length and elements are read first.
	arr.Set("length", Int(2))
	arr.Set("0", Str("own"))
	if s, err := stringOf(Obj(arr)); err != nil || s != "own,1" {
		t.Errorf("array prototype with own length and element: %q, %v; want own,1", s, err)
	}
	// A join held as data is no function: Object.prototype's toString.
	arr.Set("join", Int(1))
	if s, err := stringOf(Obj(arr)); err != nil || s != "[object Object]" {
		t.Errorf("array prototype, join shadowed: %q, %v", s, err)
	}

	objProto := NewObject(0)
	objProto.SetPrototype(mustParse(t, `{"a":1}`), scope)
	if s, err := stringOf(Obj(objProto)); err != nil || s != "[object Object]" {
		t.Errorf("object prototype: %q, %v", s, err)
	}
	if !objProto.InheritsObjectPrototype() {
		t.Error("an object prototype: InheritsObjectPrototype() = false")
	}

	own := mustParse(t, `{"toString":1}`).ObjValue()
	own.SetConversionScope(scope)
	if _, err := stringOf(Obj(own)); err == nil {
		t.Error("in scope, a toString of its own held as data: no TypeError")
	}
	valueOnly := mustParse(t, `{"valueOf":1}`).ObjValue()
	valueOnly.SetConversionScope(scope)
	if s, err := stringOf(Obj(valueOnly)); err != nil || s != "[object Object]" {
		t.Errorf("in scope, only a valueOf of its own: %q, %v", s, err)
	}
}

// TestArrayPrototypeLengthBound: a length the object holds itself, far past
// the elements its chain answers, is bounded by the engine's string limit
// rather than built.
func TestArrayPrototypeLengthBound(t *testing.T) {
	scope := &ConversionScope{}
	o := NewObject(0)
	o.SetPrototype(ArrOf(Int(1)), scope)
	o.Set("length", Num(1e15))
	defer func() {
		r := recover()
		s, ok := r.(*budget.Stop)
		if !ok || !errors.Is(s.Err, budget.ErrLimit) {
			t.Fatalf("recovered %v, want a budget.Stop wrapping ErrLimit", r)
		}
	}()
	_ = Obj(o).AsString()
	t.Fatal("String of an array-like of length 1e15 returned")
}

// TestChainGet: ChainGet reads own keys and the chain, never
// Object.prototype's members.
func TestChainGet(t *testing.T) {
	o := mustParse(t, `{"a":1}`).ObjValue()
	o.SetPrototype(mustParse(t, `{"b":2}`), nil)
	for key, want := range map[string]string{"a": "1", "b": "2"} {
		if v, ok := o.ChainGet(key); !ok || v.AsString() != want {
			t.Errorf("ChainGet(%q) = %v, %v", key, v, ok)
		}
	}
	if _, ok := o.ChainGet("toString"); ok {
		t.Error(`ChainGet("toString") found Object.prototype's member`)
	}
	if v := o.Prop("toString"); !IsFunction(v) {
		t.Errorf(`Prop("toString") = %v, want Object.prototype's function`, v)
	}
}

// TestConversionDepthBound: objects whose array prototypes hold objects of
// the same kind, past MaxValueDepth of them, stop with ErrLimit rather than
// recurse without bound.
func TestConversionDepthBound(t *testing.T) {
	scope := &ConversionScope{}
	inner := Str("x")
	for range MaxValueDepth + 8 {
		o := NewObject(0)
		o.SetPrototype(ArrOf(inner), scope)
		inner = Obj(o)
	}
	defer func() {
		r := recover()
		s, ok := r.(*budget.Stop)
		if !ok || !errors.Is(s.Err, budget.ErrLimit) {
			t.Fatalf("recovered %v, want a budget.Stop wrapping ErrLimit", r)
		}
		if scope.depth != 0 {
			t.Errorf("depth %d after the stop, want 0", scope.depth)
		}
	}()
	_ = inner.AsString()
	t.Fatal("a conversion past MaxValueDepth returned")
}
