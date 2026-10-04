package jsval

import "unicode/utf8"

// primitive is ToPrimitive with hint "number" as the relational operators use
// it: dates read as their epoch, arrays and objects as their string form.
func primitive(v Value) Value {
	switch v.Kind() {
	case KindTimestamp:
		return Num(v.NumValue())
	case KindArr, KindObj, KindPattern:
		return Str(v.AsString())
	}
	return v
}

// Relate is JavaScript's abstract relational comparison of a and b: it
// returns -1, 0 or +1, and ok=false when either side coerces to NaN (every
// operator then answers false). Two strings compare by UTF-16 code units;
// anything else compares as numbers.
func Relate(a, b Value) (c int, ok bool) {
	// Fast path: the overwhelmingly common same-kind cases.
	if a.Kind() == KindNum && b.Kind() == KindNum {
		return cmpFloat(a.NumValue(), b.NumValue())
	}
	a, b = primitive(a), primitive(b)
	if a.Kind() == KindStr && b.Kind() == KindStr {
		return CompareUTF16(a.StrValue(), b.StrValue()), true
	}
	return cmpFloat(ToNumber(a), ToNumber(b))
}

func cmpFloat(x, y float64) (int, bool) {
	switch {
	case x < y:
		return -1, true
	case x > y:
		return 1, true
	case x == y:
		return 0, true
	}
	return 0, false
}

// CompareUTF16 orders strings by UTF-16 code unit, as JavaScript does. It
// differs from byte order only when one string has a supplementary-plane
// character where the other has a BMP character above U+D7FF.
func CompareUTF16(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	if i == n {
		switch {
		case len(a) < len(b):
			return -1
		case len(a) > len(b):
			return 1
		}
		return 0
	}
	// Back up to the start of the rune that differs.
	for i > 0 && !utf8.RuneStart(a[i]) {
		i--
	}
	ra, _ := utf8.DecodeRuneInString(a[i:])
	rb, _ := utf8.DecodeRuneInString(b[i:])
	ua, ub := utf16Unit(ra), utf16Unit(rb)
	switch {
	case ua < ub:
		return -1
	case ua > ub:
		return 1
	}
	return 0
}

// utf16Unit is the first UTF-16 code unit of r.
func utf16Unit(r rune) rune {
	if r >= 0x10000 {
		return 0xD800 + ((r - 0x10000) >> 10)
	}
	return r
}
