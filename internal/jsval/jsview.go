package jsval

import (
	"math"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// Views of a value as upstream's code reads it, where Go's nearest
// convenience reads it otherwise: Items() takes only an array where
// Array.prototype's methods take any array-like, and a Go map is not a
// plain object (its keys are unordered, and `k in obj` holds for
// Object.prototype's members too). The keyword views (Value.Strict,
// Value.PropertyKey, Value.StringIs) are in walk.go, Join in Walker.

// ArrayLike is a value read as Array.prototype's methods read `this`
// (ToObject, then LengthOfArrayLike, then Get(String(i))): an array's
// elements; a string's UTF-16 code units, each a string of one unit; an
// object's `length` (ToLength of ToNumber, so 0 for a missing or NaN one,
// at most 2^53 - 1) and its members "0", "1", ...; nothing (length 0) for a
// number, boolean, date or pattern. A loop over one must meter its steps:
// {length: 1e15} is an array-like.
type ArrayLike struct {
	v     Value
	n     int64
	units []uint16 // a non-ASCII string's code units, decoded once
}

// ToArrayLike is v's ArrayLike; ok is false for null and undefined, which
// ToObject refuses (the TypeError upstream throws).
func ToArrayLike(v Value) (a ArrayLike, ok bool) {
	a.v = v
	switch v.Kind() {
	case KindNull, KindUndefined:
		return a, false
	case KindArr:
		a.n = int64(len(v.arr().items))
	case KindStr:
		s := v.s()
		if isASCII(s) {
			a.n = int64(len(s))
		} else {
			a.units = utf16Units16(s)
			a.n = int64(len(a.units))
		}
	case KindObj:
		f := ToNumber(v.obj().Lookup("length"))
		switch {
		case math.IsNaN(f) || f <= 0:
			a.n = 0
		case f >= 1<<53-1:
			a.n = 1<<53 - 1
		default:
			a.n = int64(f)
		}
	}
	return a, true
}

// Len is the length.
func (a ArrayLike) Len() int64 { return a.n }

// At is element i (undefined outside [0, Len)).
func (a ArrayLike) At(i int64) Value {
	if i < 0 || i >= a.n {
		return Undefined
	}
	switch a.v.Kind() {
	case KindArr:
		return a.v.arr().items[i]
	case KindStr:
		if a.units == nil {
			return Str(a.v.s()[i : i+1])
		}
		u := rune(a.units[i])
		if utf16.IsSurrogate(u) {
			return Str(string(AppendSurrogate(nil, u)))
		}
		return Str(string(u))
	case KindObj:
		return a.v.obj().Lookup(JSNumberString(float64(i)))
	}
	return Undefined
}

// utf16Units16 is s's UTF-16 code units, a lone surrogate's WTF-8 bytes
// read as that surrogate.
func utf16Units16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for len(s) > 0 {
		r, n := decodeWTF8(s)
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			out = append(out, uint16(hi), uint16(lo))
		} else {
			if r == utf8.RuneError && n == 1 {
				r = 0xFFFD
			}
			out = append(out, uint16(r))
		}
		s = s[n:]
	}
	return out
}

// PlainMap is upstream's `{}` used as a map from strings: its keys listed in
// JavaScript's order (the array indexes first, ascending, then the rest as
// they were added), and `k in obj` true for Object.prototype's members too.
// A Go map has neither property. "__proto__" names the prototype, not a key:
// setting it adds none (and is not modelled further).
type PlainMap[V any] struct {
	keys []string
	nidx int
	m    map[string]V
}

// Get is an own key's value.
func (p *PlainMap[V]) Get(k string) (v V, ok bool) {
	v, ok = p.m[k]
	return v, ok
}

// Has is hasOwnProperty.
func (p *PlainMap[V]) Has(k string) bool { _, ok := p.m[k]; return ok }

// In is `k in obj`: an own key, or a member every plain object inherits.
func (p *PlainMap[V]) In(k string) bool {
	if p.Has(k) || k == "__proto__" {
		return true
	}
	_, ok := Inherited(k)
	return ok
}

// Set is obj[k] = v.
func (p *PlainMap[V]) Set(k string, v V) {
	if k == "__proto__" {
		return
	}
	if p.m == nil {
		p.m = map[string]V{}
	}
	if _, ok := p.m[k]; !ok {
		if n, isIdx := ArrayIndex(k); isIdx {
			i := sort.Search(p.nidx, func(i int) bool { m, _ := ArrayIndex(p.keys[i]); return m > n })
			p.keys = append(p.keys, "")
			copy(p.keys[i+1:], p.keys[i:])
			p.keys[i] = k
			p.nidx++
		} else {
			p.keys = append(p.keys, k)
		}
	}
	p.m[k] = v
}

// Delete is `delete obj[k]`.
func (p *PlainMap[V]) Delete(k string) {
	if _, ok := p.m[k]; !ok {
		return
	}
	delete(p.m, k)
	for i, x := range p.keys {
		if x == k {
			if i < p.nidx {
				p.nidx--
			}
			p.keys = append(p.keys[:i], p.keys[i+1:]...)
			return
		}
	}
}

// Keys is Object.keys(obj). The slice is shared; do not modify it.
func (p *PlainMap[V]) Keys() []string { return p.keys }

// Len is the number of own keys.
func (p *PlainMap[V]) Len() int { return len(p.keys) }
