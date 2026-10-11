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
	return RelatePrimitives(primitive(a), primitive(b))
}

// RelatePrimitives is Relate after ToPrimitive, for a caller that converts
// objects itself (the expression evaluator writes a Date in an array in the
// view's time zone): two strings compare by code unit, anything else as
// numbers.
func RelatePrimitives(a, b Value) (c int, ok bool) {
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
// differs from byte order only where one string has a character above U+FFFF
// and the other a code unit above U+D7FF there, or a lone surrogate, which the
// expression evaluator writes as three WTF-8 bytes (ED A0..BF xx) and which
// compares as the code unit it is.
func CompareUTF16(a, b string) int {
	for {
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
		// Back up to the start of the character that differs.
		for i > 0 && !utf8.RuneStart(a[i]) {
			i--
		}
		ua, la := firstUnits(a[i:])
		ub, lb := firstUnits(b[i:])
		switch {
		case ua != ub:
			return cmpRune(ua, ub)
		case la != lb:
			// The same high surrogate: the low ones decide.
			return cmpRune(la, lb)
		}
		// Equal code units from different bytes: only invalid UTF-8, which
		// reads as U+FFFD whatever its bytes. Compare what follows.
		_, wa := decodeWTF8(a[i:])
		_, wb := decodeWTF8(b[i:])
		a, b = a[i+wa:], b[i+wb:]
	}
}

func cmpRune(x, y rune) int {
	if x < y {
		return -1
	}
	return 1
}

// firstUnits are the first code unit of s and, when that is the high
// surrogate of a pair, the low one (else -1).
func firstUnits(s string) (rune, rune) {
	if s == "" {
		return -1, -1
	}
	r, _ := decodeWTF8(s)
	if r >= 0x10000 {
		r -= 0x10000
		return 0xD800 + r>>10, 0xDC00 + r&0x3FF
	}
	return r, -1
}

// decodeWTF8 decodes one character, reading the three-byte form of a lone
// surrogate as that surrogate.
func decodeWTF8(s string) (rune, int) {
	if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && s[2]&0xC0 == 0x80 {
		return 0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
	}
	return utf8.DecodeRuneInString(s)
}
