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
// prototype's members. A member that is a method of a
// built-in prototype is a NativeFunction of that name: truthy, an object,
// printed as native code. Upstream reads a specification's values this way
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
		return protoMember(key, arrayProtoNames, "Array")
	case KindStr:
		// The length and the code units, read without decoding the whole
		// string; any other key is the prototype's.
		if key == "length" {
			return Int(UTF16Len(v.s()))
		}
		if i, ok := canonicalIndex(key); ok {
			return utf16UnitAt(v.s(), i)
		}
		return protoMember(key, stringProtoNames, "String")
	case KindNum:
		return protoMember(key, numberProtoNames, "Number")
	case KindBool:
		return protoMember(key, booleanProtoNames, "Boolean")
	}
	// A date or a pattern (no JSON value is one) reads as an object.
	f, _ := Inherited(key)
	return f
}

// protoMember is the member key of a built-in prototype whose methods are
// names (with their lengths), or of Object.prototype, which every one
// inherits from.
func protoMember(key string, names map[string]int, ctor string) Value {
	if arity, ok := names[key]; ok && arity >= 0 {
		if key == "constructor" {
			return NativeMethod(ctor, arity) // function Array() { [native code] }
		}
		return NativeMethod(key, arity)
	}
	f, _ := Inherited(key)
	return f
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

// The own property names of the built-in prototypes of the JSON kinds and of
// Function.prototype in node 24 (Object.getOwnPropertyNames), each with the
// length of the method it holds (-1 for a member that is no method), which
// TestPrototypeNamesMatchNode checks against testdata/protonames.json.
var (
	objectProtoNames = map[string]int{
		"__defineGetter__": 2, "__defineSetter__": 2, "__lookupGetter__": 1, "__lookupSetter__": 1, "__proto__": -1,
		"constructor": 1, "hasOwnProperty": 1, "isPrototypeOf": 1, "propertyIsEnumerable": 1, "toLocaleString": 0,
		"toString": 0, "valueOf": 0,
	}
	arrayProtoNames = map[string]int{
		"at": 1, "concat": 1, "constructor": 1, "copyWithin": 2, "entries": 0, "every": 1, "fill": 1, "filter": 1,
		"find": 1, "findIndex": 1, "findLast": 1, "findLastIndex": 1, "flat": 0, "flatMap": 1, "forEach": 1,
		"includes": 1, "indexOf": 1, "join": 1, "keys": 0, "lastIndexOf": 1, "length": -1, "map": 1, "pop": 0,
		"push": 1, "reduce": 1, "reduceRight": 1, "reverse": 0, "shift": 0, "slice": 2, "some": 1, "sort": 1,
		"splice": 2, "toLocaleString": 0, "toReversed": 0, "toSorted": 1, "toSpliced": 2, "toString": 0,
		"unshift": 1, "values": 0, "with": 2,
	}
	stringProtoNames = map[string]int{
		"anchor": 1, "at": 1, "big": 0, "blink": 0, "bold": 0, "charAt": 1, "charCodeAt": 1, "codePointAt": 1,
		"concat": 1, "constructor": 1, "endsWith": 1, "fixed": 0, "fontcolor": 1, "fontsize": 1, "includes": 1,
		"indexOf": 1, "isWellFormed": 0, "italics": 0, "lastIndexOf": 1, "length": -1, "link": 1,
		"localeCompare": 1, "match": 1, "matchAll": 1, "normalize": 0, "padEnd": 1, "padStart": 1, "repeat": 1,
		"replace": 2, "replaceAll": 2, "search": 1, "slice": 2, "small": 0, "split": 2, "startsWith": 1,
		"strike": 0, "sub": 0, "substr": 2, "substring": 2, "sup": 0, "toLocaleLowerCase": 0,
		"toLocaleUpperCase": 0, "toLowerCase": 0, "toString": 0, "toUpperCase": 0, "toWellFormed": 0, "trim": 0,
		"trimEnd": 0, "trimLeft": 0, "trimRight": 0, "trimStart": 0, "valueOf": 0,
	}
	numberProtoNames = map[string]int{
		"constructor": 1, "toExponential": 1, "toFixed": 1, "toLocaleString": 0, "toPrecision": 1, "toString": 1,
		"valueOf": 0,
	}
	booleanProtoNames = map[string]int{
		"constructor": 1, "toString": 0, "valueOf": 0,
	}
	functionProtoNames = map[string]int{
		"apply": 2, "arguments": -1, "bind": 1, "call": 1, "caller": -1, "constructor": 1, "length": -1, "name": -1,
		"toString": 0,
	}
)

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
