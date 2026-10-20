package jsval

import (
	"math"
	"unicode/utf16"
	"unicode/utf8"
)

// Member is the JavaScript property read v[key] for a value that is not null
// or undefined (the read of those is a TypeError, the caller's): an object's
// own member, else Object.prototype's; an array's or a string's length and
// elements, else its prototype's members; a number's or a boolean's
// prototype's members; a date's or a pattern's the same. A member that is a
// method of a built-in prototype is one shared inert function of that name
// (see Proto): truthy, an object, printed as native code. Upstream reads a specification's values this way
// wherever it does not check their type first, so `[1, 2].fill` is a
// function and `"abc".length` is 3.
func Member(v Value, key string) Value {
	switch v.Kind() {
	case KindObj:
		return v.obj().Prop(key)
	case KindArr:
		items := v.arr().list()
		if key == "length" {
			return Int(len(items))
		}
		if i, ok := canonicalIndex(key); ok {
			if i < int64(len(items)) {
				return items[i]
			}
			return Undefined
		}
		if pr := v.arr().props; pr != nil {
			// A named own property (a tuple's field) shadows the prototype's.
			if x, ok := pr.Get(key); ok {
				return x
			}
		}
		return ArrayProto.Get(key)
	case KindStr:
		// The length and the code units, read without decoding the whole
		// string; any other key is the prototype's.
		if key == "length" {
			return Int(UTF16Len(v.s()))
		}
		if i, ok := canonicalIndex(key); ok {
			return utf16UnitAt(v.s(), i)
		}
		return StringProto.Get(key)
	case KindNum:
		return NumberProto.Get(key)
	case KindBool:
		return BooleanProto.Get(key)
	case KindTimestamp:
		return DateProto.Get(key)
	case KindPattern:
		return RegExpProto.Get(key)
	}
	return Undefined
}

// utf16UnitAt is s[i]: the UTF-16 code unit at index i as a string of one
// unit (half of a character above U+FFFF as its lone surrogate), undefined
// past the end.
func utf16UnitAt(s string, i int64) Value {
	var n int64
	for k := 0; k < len(s); {
		c := s[k]
		if c < utf8.RuneSelf {
			if n == i {
				return Str(s[k : k+1])
			}
			n++
			k++
			continue
		}
		if _, ok := surrogateAt(s, k); ok {
			if n == i {
				return Str(s[k : k+3])
			}
			n++
			k += 3
			continue
		}
		r, w := utf8.DecodeRuneInString(s[k:])
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			switch i {
			case n:
				return Str(string(AppendSurrogate(nil, hi)))
			case n + 1:
				return Str(string(AppendSurrogate(nil, lo)))
			}
			n += 2
		} else {
			if n == i {
				if r == utf8.RuneError && w == 1 {
					return Str("\uFFFD") // as ToArrayLike reads a stray byte
				}
				return Str(s[k : k+w])
			}
			n++
		}
		k += w
	}
	return Undefined
}

// canonicalIndex parses a canonical array index ("0", "17"; not "01", "-1"
// or "1e3"), up to 2^53.
func canonicalIndex(key string) (int64, bool) {
	if key == "" || len(key) > 16 || len(key) > 1 && key[0] == '0' {
		return 0, false
	}
	var n int64
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(key[i]-'0')
	}
	return n, n < math.MaxInt64
}

// StrictEqual is JavaScript's ===: primitives by value (NaN equals nothing),
// objects and arrays by reference. Dates compare by time value: values carry
// no object identity, so two Date objects for one instant are equal here.
func StrictEqual(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case KindUndefined, KindNull:
		return true
	case KindNum:
		return a.NumValue() == b.NumValue()
	case KindTimestamp:
		x, y := a.NumValue(), b.NumValue()
		return x == y || x != x && y != y
	case KindBool:
		return a.BoolValue() == b.BoolValue()
	case KindStr:
		return a.StrValue() == b.StrValue()
	}
	return SameRef(a, b)
}
