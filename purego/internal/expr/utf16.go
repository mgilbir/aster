package expr

import (
	"unicode/utf8"
)

// JavaScript strings are sequences of UTF-16 code units, while Go strings are
// UTF-8. Lengths and indices in expressions (length, substring, slice, indexof,
// pad, truncate, ...) are code-unit based, so string functions go through these
// helpers. ASCII strings, by far the common case, take a fast path where a byte
// is a code unit.

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// utf16Len is String.prototype.length.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			n++
			i++
			continue
		}
		if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
			n++ // a WTF-8 lone surrogate
			i += 3
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// toUTF16 converts to code units. Invalid UTF-8 bytes become U+FFFD, and a
// WTF-8 encoded lone surrogate (which fromUTF16 emits) round-trips.
func toUTF16(s string) []uint16 {
	u := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			u = append(u, uint16(c))
			i++
			continue
		}
		// WTF-8 surrogate: ED A0..BF xx
		if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
			u = append(u, uint16(0xD000|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F)))
			i += 3
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		if r >= 0x10000 {
			r -= 0x10000
			u = append(u, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u = append(u, uint16(r))
		}
	}
	return u
}

// fromUTF16 converts code units to a Go string. A lone surrogate is written as
// three WTF-8 bytes so that concatenating a split pair and slicing again works;
// it is not valid UTF-8, so text that leaves the engine should be sanitised by
// the consumer.
func fromUTF16(u []uint16) string {
	b := make([]byte, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c < 0x80:
			b = append(b, byte(c))
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000:
			r := 0x10000 + (c-0xD800)<<10 + (rune(u[i+1]) - 0xDC00)
			b = utf8.AppendRune(b, r)
			i++
		case c >= 0xD800 && c < 0xE000:
			b = append(b, 0xED, byte(0x80|(c>>6)&0x3F), byte(0x80|c&0x3F))
		default:
			b = utf8.AppendRune(b, c)
		}
	}
	return string(b)
}

// substrUnits is s.slice(from, to) in code units with 0 <= from <= to <= len.
func substrUnits(s string, from, to int) string {
	if isASCII(s) {
		return s[from:to]
	}
	return fromUTF16(toUTF16(s)[from:to])
}

// indexOfUnits is String.prototype.indexOf(sub, from) in code units.
func indexOfUnits(s, sub string, from int) int {
	if isASCII(s) && isASCII(sub) {
		if from > len(s) {
			from = len(s)
		}
		if from < 0 {
			from = 0
		}
		return indexByteString(s, sub, from)
	}
	a, b := toUTF16(s), toUTF16(sub)
	if from < 0 {
		from = 0
	}
	if from > len(a) {
		from = len(a)
	}
	for i := from; i+len(b) <= len(a); i++ {
		if equalUnits(a[i:i+len(b)], b) {
			return i
		}
	}
	return -1
}

func indexByteString(s, sub string, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func equalUnits(a, b []uint16) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lastIndexOfUnits is String.prototype.lastIndexOf(sub, from) in code units;
// from is already clamped to [0, len].
func lastIndexOfUnits(s, sub string, from int) int {
	a, b := toUTF16(s), toUTF16(sub)
	if from > len(a)-len(b) {
		from = len(a) - len(b)
	}
	for i := from; i >= 0; i-- {
		if equalUnits(a[i:i+len(b)], b) {
			return i
		}
	}
	return -1
}

// compareUTF16 orders two strings by code unit, as JavaScript's < does. This
// differs from Go's byte order only for supplementary characters against
// U+E000-U+FFFF, whose surrogates sort below them.
func compareUTF16(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] && a[i] < utf8.RuneSelf {
			i++
			j++
			continue
		}
		ra, wa := decodeWTF8(a[i:])
		rb, wb := decodeWTF8(b[j:])
		if ra != rb {
			ua, ub := firstUnit(ra), firstUnit(rb)
			switch {
			case ua < ub:
				return -1
			case ua > ub:
				return 1
			}
			// Same high surrogate: compare the low ones.
			if ra < rb {
				return -1
			}
			return 1
		}
		i += wa
		j += wb
	}
	switch {
	case len(a)-i < len(b)-j:
		return -1
	case len(a)-i > len(b)-j:
		return 1
	}
	return 0
}

// decodeWTF8 decodes one character, treating the three-byte form of a lone
// surrogate (which fromUTF16 writes) as that surrogate.
func decodeWTF8(s string) (rune, int) {
	if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && s[2]&0xC0 == 0x80 {
		return rune(0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F)), 3
	}
	return utf8.DecodeRuneInString(s)
}

func firstUnit(r rune) rune {
	if r >= 0x10000 {
		return 0xD800 + ((r - 0x10000) >> 10)
	}
	return r
}
