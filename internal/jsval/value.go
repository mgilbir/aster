// Package jsval holds the value model shared by every layer of the pure-Go
// Vega engine: parsed specification literals, datum fields, signal values,
// transform output and expression results.
//
// Values follow JavaScript semantics where Vega relies on them. Numbers are always
// float64. A Value is a small tagged struct rather than an interface so that
// numbers, booleans and strings never allocate when stored in a datum.
package jsval

import (
	"math"
	"unsafe"
)

// Kind discriminates the variants of a Value.
type Kind uint8

const (
	// KindUndefined is JavaScript's undefined, which is not null: reading a
	// property that is not there. It is the zero Value.
	KindUndefined Kind = iota
	KindNull
	KindBool
	KindNum
	KindStr
	// KindTimestamp is a UTC instant in epoch milliseconds, kept distinct from
	// KindNum so time scales can dispatch and isDate can answer true.
	KindTimestamp
	KindArr
	KindObj
	// KindPattern is a compiled regular expression (regexp()).
	KindPattern
)

func (k Kind) String() string {
	switch k {
	case KindUndefined:
		return "undefined"
	case KindNull:
		return "null"
	case KindBool:
		return "boolean"
	case KindNum:
		return "number"
	case KindStr:
		return "string"
	case KindTimestamp:
		return "date"
	case KindArr:
		return "array"
	case KindObj:
		return "object"
	case KindPattern:
		return "regexp"
	}
	return "invalid"
}

// Value is one JavaScript-like value. The zero Value is Undefined.
//
// Arrays, objects and patterns are held by reference: copying a Value copies
// the reference, as JavaScript does. By convention values reachable from a
// specification or a datum are treated as immutable once published; code that
// needs to change one clones it first.
//
// A Value is two words. p is the string's bytes, the *arrayBox, *Object or
// *Pattern, or for null, booleans, numbers and dates the address of the kind's
// tag; x is then the string's length with the kind in its top byte, the kind
// alone, or the number's bits. Undefined is both zero. Values are not
// comparable: == would compare the bytes of a string by address; Key gives
// the comparison of the values.
type Value struct {
	_ [0]func() // not comparable
	p unsafe.Pointer
	x uint64
}

// tags are what p points at for the kinds that hold no reference: the kind
// is the offset.
var tags [KindPattern + 1]byte

const (
	kindShift = 56
	lenMask   = 1<<kindShift - 1
)

func scalar(k Kind, bits uint64) Value { return Value{p: unsafe.Pointer(&tags[k]), x: bits} }

func ref(k Kind, p unsafe.Pointer) Value { return Value{p: p, x: uint64(k) << kindShift} }

// n is the float of a number, date or boolean.
func (v Value) n() float64 { return math.Float64frombits(v.x) }

// s is the string of a string.
func (v Value) s() string { return unsafe.String((*byte)(v.p), int(v.x&lenMask)) }

func (v Value) arr() *arrayBox { return (*arrayBox)(v.p) }
func (v Value) obj() *Object   { return (*Object)(v.p) }
func (v Value) pat() *Pattern  { return (*Pattern)(v.p) }

type arrayBox struct{ items []Value }

// Small arrays are allocated together with their elements: one allocation
// instead of two, which matters for the coordinate pairs of a map.
type (
	arrayBox1 struct {
		arrayBox
		buf [1]Value
	}
	arrayBox2 struct {
		arrayBox
		buf [2]Value
	}
	arrayBox3 struct {
		arrayBox
		buf [3]Value
	}
	arrayBox4 struct {
		arrayBox
		buf [4]Value
	}
	arrayBox5 struct {
		arrayBox
		buf [5]Value
	}
	arrayBox6 struct {
		arrayBox
		buf [6]Value
	}
	arrayBox7 struct {
		arrayBox
		buf [7]Value
	}
	arrayBox8 struct {
		arrayBox
		buf [8]Value
	}
)

// MakeArr makes an array of n undefined elements and returns it with its
// element slice, to be filled in before the array is shared.
func MakeArr(n int) (Value, []Value) {
	var b *arrayBox
	switch {
	case n <= 0:
		b = &arrayBox{}
	case n == 1:
		x := &arrayBox1{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 2:
		x := &arrayBox2{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 3:
		x := &arrayBox3{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 4:
		x := &arrayBox4{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 5:
		x := &arrayBox5{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 6:
		x := &arrayBox6{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 7:
		x := &arrayBox7{}
		x.items, b = x.buf[:], &x.arrayBox
	case n == 8:
		x := &arrayBox8{}
		x.items, b = x.buf[:], &x.arrayBox
	default:
		b = &arrayBox{items: make([]Value, n)}
	}
	return ref(KindArr, unsafe.Pointer(b)), b.items
}

var (
	Undefined = Value{}
	Null      = scalar(KindNull, 0)
	True      = scalar(KindBool, math.Float64bits(1))
	False     = scalar(KindBool, 0)
)

// Num makes a number.
func Num(f float64) Value { return scalar(KindNum, math.Float64bits(f)) }

// Int makes a number from an int.
func Int(i int) Value { return Num(float64(i)) }

// Str makes a string.
func Str(s string) Value {
	return Value{p: unsafe.Pointer(unsafe.StringData(s)), x: uint64(len(s)) | uint64(KindStr)<<kindShift}
}

// Bool makes a boolean.
func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

// Timestamp makes a date from epoch milliseconds (NaN is an Invalid Date).
func Timestamp(epochMillis float64) Value {
	return scalar(KindTimestamp, math.Float64bits(epochMillis))
}

// Arr makes an array that takes ownership of items.
func Arr(items []Value) Value { return ref(KindArr, unsafe.Pointer(&arrayBox{items: items})) }

// ArrOf makes an array from the given values.
func ArrOf(items ...Value) Value { return Arr(items) }

// Obj wraps an object. A nil object becomes an empty one.
func Obj(o *Object) Value {
	if o == nil {
		o = NewObject(0)
	}
	return ref(KindObj, unsafe.Pointer(o))
}

// PatternValue wraps a compiled pattern.
func PatternValue(p *Pattern) Value { return ref(KindPattern, unsafe.Pointer(p)) }

// Kind reports the variant.
func (v Value) Kind() Kind {
	if d := uintptr(v.p) - uintptr(unsafe.Pointer(&tags)); d < uintptr(len(tags)) {
		return Kind(d)
	}
	return Kind(v.x >> kindShift)
}

// is reports whether v is of kind k, a kind that holds no reference.
func (v Value) is(k Kind) bool { return v.p == unsafe.Pointer(&tags[k]) }

func (v Value) IsUndefined() bool { return v.p == nil && v.x == 0 }
func (v Value) IsNull() bool      { return v.is(KindNull) }
func (v Value) IsBool() bool      { return v.is(KindBool) }
func (v Value) IsNum() bool       { return v.is(KindNum) }
func (v Value) IsStr() bool       { return v.Kind() == KindStr }
func (v Value) IsTimestamp() bool { return v.is(KindTimestamp) }
func (v Value) IsArr() bool       { return v.Kind() == KindArr }
func (v Value) IsObj() bool       { return v.Kind() == KindObj }
func (v Value) IsPattern() bool   { return v.Kind() == KindPattern }

// IsNullish is JavaScript's `v == null`: true for Null and Undefined.
func (v Value) IsNullish() bool { return v.IsNull() || v.IsUndefined() }

// IsMissing is true for Null, Undefined and numeric NaN, Vega's notion of a
// missing value.
func (v Value) IsMissing() bool {
	switch v.Kind() {
	case KindNull, KindUndefined:
		return true
	case KindNum:
		return math.IsNaN(v.n())
	}
	return false
}

// NumValue returns the float of a KindNum or KindTimestamp value, and 0 for
// anything else. Use AsNumber when the kind is not known.
func (v Value) NumValue() float64 {
	switch v.Kind() {
	case KindNum, KindTimestamp, KindBool:
		return v.n()
	}
	return 0
}

// BoolValue returns the boolean of a KindBool value (false otherwise).
func (v Value) BoolValue() bool { return v.Kind() == KindBool && v.n() != 0 }

// StrValue returns the string of a KindStr value ("" otherwise).
func (v Value) StrValue() string {
	if v.Kind() != KindStr {
		return ""
	}
	return v.s()
}

// Items returns the elements of an array (nil otherwise). The slice is shared;
// do not modify it.
func (v Value) Items() []Value {
	if v.Kind() != KindArr {
		return nil
	}
	return v.arr().items
}

// Len is the length of an array or the number of keys of an object.
func (v Value) Len() int {
	switch v.Kind() {
	case KindArr:
		return len(v.arr().items)
	case KindObj:
		return v.obj().Len()
	}
	return 0
}

// ObjValue returns the object of a KindObj value (nil otherwise).
func (v Value) ObjValue() *Object {
	if v.Kind() != KindObj {
		return nil
	}
	return v.obj()
}

// PatternOf returns the pattern of a KindPattern value (nil otherwise).
func (v Value) PatternOf() *Pattern {
	if v.Kind() != KindPattern {
		return nil
	}
	return v.pat()
}

// Get reads a property of an object, answering Undefined when v is not an
// object or has no such key.
func (v Value) Get(key string) Value {
	if v.Kind() != KindObj {
		return Undefined
	}
	return v.obj().Lookup(key)
}

// Index reads an array element, answering Undefined out of range.
func (v Value) Index(i int) Value {
	if v.Kind() != KindArr {
		return Undefined
	}
	items := v.arr().items
	if i < 0 || i >= len(items) {
		return Undefined
	}
	return items[i]
}

// NumberOrNull returns the number held by a Num or Timestamp, and ok=false for
// anything else. A date is a number to arithmetic but not to isNumber.
func (v Value) NumberOrNull() (float64, bool) {
	if v.Kind() == KindNum || v.Kind() == KindTimestamp {
		return v.n(), true
	}
	return 0, false
}

// AsDouble is a *reading*, not JavaScript's Number(x): NaN means the value
// held no number. null, "" and [] read NaN (Number would say 0). Use
// ToNumber for the coercion.
func (v Value) AsDouble() float64 {
	switch v.Kind() {
	case KindNum, KindTimestamp:
		return v.n()
	case KindBool:
		return v.n()
	case KindStr:
		return parseLooseDouble(JSTrim(v.s()))
	case KindArr:
		// A chain of one-element arrays is followed in a loop, not by
		// recursion, whatever its depth.
		for {
			items := v.Items()
			if len(items) != 1 {
				return math.NaN()
			}
			v = items[0]
			if v.Kind() != KindArr {
				return v.AsDouble()
			}
		}
	}
	return math.NaN()
}

// IsTruthy is JavaScript truthiness. Every object (including a Date, even the
// epoch or an Invalid Date, and a pattern) is truthy.
func (v Value) IsTruthy() bool {
	switch v.Kind() {
	case KindBool:
		return v.n() != 0
	case KindNum:
		return v.n() != 0 && !math.IsNaN(v.n())
	case KindStr:
		return v.s() != ""
	case KindTimestamp, KindArr, KindObj, KindPattern:
		return true
	}
	return false
}

// AsBoolean is like IsTruthy except that a Timestamp reads as its number.
func (v Value) AsBoolean() bool {
	if v.Kind() == KindTimestamp {
		return v.n() != 0 && !math.IsNaN(v.n())
	}
	return v.IsTruthy()
}

// AsString is JavaScript's String(x) for the variants this model holds,
// except that a Timestamp is written as its epoch number (callers that need
// Date.prototype.toString format it themselves). Array elements that are
// nullish are written as "" (Array.prototype.join).
//
// The String of an array is a walk of it, and this is the detached walk (see
// Walker): the String of a value an expression or a transform built is taken
// with the call's Walker (Walker.String), which the call's budgets bound.
func (v Value) AsString() string {
	switch v.Kind() {
	case KindArr:
		return Walker{}.String(v)
	case KindObj:
		return objectString(v.obj())
	}
	return v.primString()
}

// String implements fmt.Stringer for messages and debugging: compact
// JSON-like text, not a JavaScript conversion. It shows at most the first
// previewBytes bytes of the value's text, followed by "…" when there is more,
// so a message about a value costs no more than its preview.
func (v Value) String() string {
	switch v.Kind() {
	case KindStr:
		if len(v.s()) <= previewBytes {
			return quoteJSON(v.s())
		}
	case KindUndefined:
		return "undefined"
	case KindTimestamp:
		return "Date(" + JSNumberString(v.n()) + ")"
	case KindPattern:
		return v.PatternOf().String()
	}
	b := Walker{}.appendJSONPrefix(nil, v, previewBytes)
	if len(b) > previewBytes {
		b = append(cutUTF8(b, previewBytes), "…"...)
	}
	return string(b)
}

// previewBytes is how much of a value's text String shows.
const previewBytes = 1 << 10

// cutUTF8 is b cut to at most n bytes at a character boundary.
func cutUTF8(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	for n > 0 && b[n]&0xC0 == 0x80 {
		n--
	}
	return b[:n]
}

// Equal is deep structural equality (not JavaScript ===). NaN equals NaN;
// an array or object equals itself without a walk. It is the detached walk
// (see Walker): values an expression or a transform built are compared with
// the call's Walker (Walker.Equal).
func Equal(a, b Value) bool {
	if a.Kind() == KindNum && b.Kind() == KindNum {
		return a.n() == b.n() || (math.IsNaN(a.n()) && math.IsNaN(b.n()))
	}
	return Walker{}.Equal(a, b)
}

// SameRef reports whether two arrays/objects/patterns are the same reference
// (JavaScript === for objects). For primitives it is strict equality.
func SameRef(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case KindArr, KindObj, KindPattern:
		return a.p == b.p
	case KindNum:
		return a.n() == b.n()
	}
	return Equal(a, b)
}

// Field reads a Vega field path ("a.b", "a[0]", "a['b c']") from v, answering
// Null when any step is missing. Missing steps answer Null (not Undefined)
// because this accessor feeds encoders and transforms, which treat both as
// missing.
func (v Value) Field(path string) Value {
	cur := v
	for _, seg := range ParseFieldPath(path) {
		switch cur.Kind() {
		case KindObj:
			next, ok := cur.ObjValue().Get(seg)
			if !ok {
				return Null
			}
			cur = next
		case KindArr:
			idx, ok := parseIndex(seg)
			if !ok {
				return Null
			}
			items := cur.Items()
			if idx >= len(items) {
				return Null
			}
			cur = items[idx]
		default:
			return Null
		}
	}
	return cur
}

func parseIndex(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// Key is a comparable form of a Value, for map keys and ==: two values have
// the same Key when they are the same primitive (NaN matching nothing, -0
// matching 0, strings by content) or the same reference.
type Key struct {
	k Kind
	n float64
	s string
	r unsafe.Pointer
}

// Key returns v's Key.
func (v Value) Key() Key {
	switch k := v.Kind(); k {
	case KindBool, KindNum, KindTimestamp:
		return Key{k: k, n: v.n()}
	case KindStr:
		return Key{k: k, s: v.s()}
	case KindArr, KindObj, KindPattern:
		return Key{k: k, r: v.p}
	default:
		return Key{k: k}
	}
}

// InternKey is a comparable form of a Value as d3's InternMap and InternSet
// file it, for map keys: the value's valueOf() (a Date is its time value,
// an array, object or pattern itself), compared as a JavaScript Map compares
// keys (SameValueZero: NaN matches NaN, -0 matches 0, strings by content,
// objects by reference). So a Date and its number are one key, two arrays of
// the same elements are two, and an array is the same key however it is
// reached, which no walk of it decides: a value of any size is a key in
// O(1). Key differs only where JavaScript's === does: NaN, and dates.
type InternKey struct {
	k    Kind
	bits uint64
	s    string
	r    unsafe.Pointer
}

// InternKey returns v's InternKey.
func (v Value) InternKey() InternKey {
	switch k := v.Kind(); k {
	case KindNum, KindTimestamp:
		f := v.n()
		switch {
		case f != f:
			return InternKey{k: KindNum, bits: canonicalNaN}
		case f == 0:
			return InternKey{k: KindNum} // -0 and 0
		}
		return InternKey{k: KindNum, bits: math.Float64bits(f)}
	case KindBool:
		return InternKey{k: k, bits: v.x}
	case KindStr:
		return InternKey{k: k, s: v.s()}
	case KindArr, KindObj, KindPattern:
		return InternKey{k: k, r: v.p}
	default:
		return InternKey{k: k}
	}
}

// canonicalNaN stands for every NaN in an InternKey.
var canonicalNaN = math.Float64bits(math.NaN())

// MapKey is the InternKey of v as a JavaScript Map keys it, without
// InternMap's valueOf(): SameValueZero, so a Date is not its number. A date
// is held here by its time value, not as an object of its own, so two
// Dates of the same time are one key (in JavaScript they are two).
func (v Value) MapKey() InternKey {
	k := v.InternKey()
	if v.Kind() == KindTimestamp {
		k.k = KindTimestamp
	}
	return k
}
