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
	"strings"
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
type Value struct {
	k Kind
	n float64 // KindNum, KindTimestamp, KindBool (0/1)
	s string  // KindStr
	r any     // *arrayBox, *Object, *Pattern
}

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

// ArrSlab hands out n equally sized arrays from two allocations in all, for
// decoders that produce one small array per point.
type ArrSlab struct {
	boxes []arrayBox
	vals  []Value
	dim   int
}

// NewArrSlab prepares n arrays of dim elements each.
func NewArrSlab(n, dim int) *ArrSlab {
	return &ArrSlab{boxes: make([]arrayBox, n), vals: make([]Value, n*dim), dim: dim}
}

// Next returns the next array and its element slice, to be filled in.
func (s *ArrSlab) Next() (Value, []Value) {
	b := &s.boxes[0]
	b.items = s.vals[:s.dim:s.dim]
	s.boxes, s.vals = s.boxes[1:], s.vals[s.dim:]
	return Value{k: KindArr, r: b}, b.items
}

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
	return Value{k: KindArr, r: b}, b.items
}

var (
	Undefined = Value{}
	Null      = Value{k: KindNull}
	True      = Value{k: KindBool, n: 1}
	False     = Value{k: KindBool}
)

// Num makes a number.
func Num(f float64) Value { return Value{k: KindNum, n: f} }

// Int makes a number from an int.
func Int(i int) Value { return Value{k: KindNum, n: float64(i)} }

// Str makes a string.
func Str(s string) Value { return Value{k: KindStr, s: s} }

// Bool makes a boolean.
func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

// Timestamp makes a date from epoch milliseconds (NaN is an Invalid Date).
func Timestamp(epochMillis float64) Value { return Value{k: KindTimestamp, n: epochMillis} }

// Arr makes an array that takes ownership of items.
func Arr(items []Value) Value { return Value{k: KindArr, r: &arrayBox{items: items}} }

// ArrOf makes an array from the given values.
func ArrOf(items ...Value) Value { return Arr(items) }

// Obj wraps an object. A nil object becomes an empty one.
func Obj(o *Object) Value {
	if o == nil {
		o = NewObject(0)
	}
	return Value{k: KindObj, r: o}
}

// PatternValue wraps a compiled pattern.
func PatternValue(p *Pattern) Value { return Value{k: KindPattern, r: p} }

// Kind reports the variant.
func (v Value) Kind() Kind { return v.k }

func (v Value) IsUndefined() bool { return v.k == KindUndefined }
func (v Value) IsNull() bool      { return v.k == KindNull }
func (v Value) IsBool() bool      { return v.k == KindBool }
func (v Value) IsNum() bool       { return v.k == KindNum }
func (v Value) IsStr() bool       { return v.k == KindStr }
func (v Value) IsTimestamp() bool { return v.k == KindTimestamp }
func (v Value) IsArr() bool       { return v.k == KindArr }
func (v Value) IsObj() bool       { return v.k == KindObj }
func (v Value) IsPattern() bool   { return v.k == KindPattern }

// IsNullish is JavaScript's `v == null`: true for Null and Undefined.
func (v Value) IsNullish() bool { return v.k == KindNull || v.k == KindUndefined }

// IsMissing is true for Null, Undefined and numeric NaN, Vega's notion of a
// missing value.
func (v Value) IsMissing() bool {
	switch v.k {
	case KindNull, KindUndefined:
		return true
	case KindNum:
		return math.IsNaN(v.n)
	}
	return false
}

// NumValue returns the float of a KindNum or KindTimestamp value, and 0 for
// anything else. Use AsNumber when the kind is not known.
func (v Value) NumValue() float64 { return v.n }

// BoolValue returns the boolean of a KindBool value (false otherwise).
func (v Value) BoolValue() bool { return v.k == KindBool && v.n != 0 }

// StrValue returns the string of a KindStr value ("" otherwise).
func (v Value) StrValue() string { return v.s }

// Items returns the elements of an array (nil otherwise). The slice is shared;
// do not modify it.
func (v Value) Items() []Value {
	if v.k != KindArr {
		return nil
	}
	return v.r.(*arrayBox).items
}

// Len is the length of an array or the number of keys of an object.
func (v Value) Len() int {
	switch v.k {
	case KindArr:
		return len(v.r.(*arrayBox).items)
	case KindObj:
		return v.r.(*Object).Len()
	}
	return 0
}

// ObjValue returns the object of a KindObj value (nil otherwise).
func (v Value) ObjValue() *Object {
	if v.k != KindObj {
		return nil
	}
	return v.r.(*Object)
}

// PatternOf returns the pattern of a KindPattern value (nil otherwise).
func (v Value) PatternOf() *Pattern {
	if v.k != KindPattern {
		return nil
	}
	return v.r.(*Pattern)
}

// Get reads a property of an object, answering Undefined when v is not an
// object or has no such key.
func (v Value) Get(key string) Value {
	if v.k != KindObj {
		return Undefined
	}
	return v.r.(*Object).Lookup(key)
}

// Index reads an array element, answering Undefined out of range.
func (v Value) Index(i int) Value {
	if v.k != KindArr {
		return Undefined
	}
	items := v.r.(*arrayBox).items
	if i < 0 || i >= len(items) {
		return Undefined
	}
	return items[i]
}

// NumberOrNull returns the number held by a Num or Timestamp, and ok=false for
// anything else. A date is a number to arithmetic but not to isNumber.
func (v Value) NumberOrNull() (float64, bool) {
	if v.k == KindNum || v.k == KindTimestamp {
		return v.n, true
	}
	return 0, false
}

// AsDouble is a *reading*, not JavaScript's Number(x): NaN means the value
// held no number. null, "" and [] read NaN (Number would say 0). Use
// ToNumber for the coercion.
func (v Value) AsDouble() float64 {
	switch v.k {
	case KindNum, KindTimestamp:
		return v.n
	case KindBool:
		return v.n
	case KindStr:
		return parseLooseDouble(strings.TrimSpace(v.s))
	case KindArr:
		items := v.Items()
		if len(items) == 1 {
			return items[0].AsDouble()
		}
	}
	return math.NaN()
}

// IsTruthy is JavaScript truthiness. Every object (including a Date, even the
// epoch or an Invalid Date, and a pattern) is truthy.
func (v Value) IsTruthy() bool {
	switch v.k {
	case KindBool:
		return v.n != 0
	case KindNum:
		return v.n != 0 && !math.IsNaN(v.n)
	case KindStr:
		return v.s != ""
	case KindTimestamp, KindArr, KindObj, KindPattern:
		return true
	}
	return false
}

// AsBoolean is like IsTruthy except that a Timestamp reads as its number.
func (v Value) AsBoolean() bool {
	if v.k == KindTimestamp {
		return v.n != 0 && !math.IsNaN(v.n)
	}
	return v.IsTruthy()
}

// AsString is JavaScript's String(x) for the variants this model holds,
// except that a Timestamp is written as its epoch number (callers that need
// Date.prototype.toString format it themselves). Array elements that are
// nullish are written as "" (Array.prototype.join).
func (v Value) AsString() string {
	switch v.k {
	case KindStr:
		return v.s
	case KindNum, KindTimestamp:
		return JSNumberString(v.n)
	case KindBool:
		if v.n != 0 {
			return "true"
		}
		return "false"
	case KindNull:
		return "null"
	case KindUndefined:
		return "undefined"
	case KindArr:
		var b strings.Builder
		for i, it := range v.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			if !it.IsNullish() {
				b.WriteString(it.AsString())
			}
		}
		return b.String()
	case KindObj:
		if o := v.ObjValue(); o != nil && o.str != nil {
			return o.str(o)
		}
		return "[object Object]"
	case KindPattern:
		return v.PatternOf().String()
	}
	return ""
}

// String implements fmt.Stringer for debugging; it writes compact JSON-like
// text and is not a JavaScript conversion.
func (v Value) String() string {
	switch v.k {
	case KindStr:
		return quoteJSON(v.s)
	case KindUndefined:
		return "undefined"
	case KindTimestamp:
		return "Date(" + JSNumberString(v.n) + ")"
	case KindPattern:
		return v.PatternOf().String()
	}
	return string(AppendJSON(nil, v))
}

// Equal is deep structural equality (not JavaScript ===). NaN equals NaN.
func Equal(a, b Value) bool {
	if a.k != b.k {
		return false
	}
	switch a.k {
	case KindUndefined, KindNull:
		return true
	case KindBool:
		return a.n == b.n
	case KindNum, KindTimestamp:
		return a.n == b.n || (math.IsNaN(a.n) && math.IsNaN(b.n))
	case KindStr:
		return a.s == b.s
	case KindArr:
		x, y := a.Items(), b.Items()
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case KindObj:
		x, y := a.ObjValue(), b.ObjValue()
		if x == y {
			return true
		}
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			bv, ok := y.Get(x.KeyAt(i))
			if !ok || !Equal(x.ValueAt(i), bv) {
				return false
			}
		}
		return true
	case KindPattern:
		p, q := a.PatternOf(), b.PatternOf()
		return p.Source == q.Source && p.Flags == q.Flags
	}
	return false
}

// SameRef reports whether two arrays/objects/patterns are the same reference
// (JavaScript === for objects). For primitives it is strict equality.
func SameRef(a, b Value) bool {
	if a.k != b.k {
		return false
	}
	switch a.k {
	case KindArr, KindObj, KindPattern:
		return a.r == b.r
	case KindNum:
		return a.n == b.n
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
		switch cur.k {
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
