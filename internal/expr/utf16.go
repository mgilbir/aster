package expr

import (
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/jsval"
)

// JavaScript strings are sequences of UTF-16 code units, while Go strings are
// UTF-8. Lengths and indices in expressions (length, substring, slice, indexof,
// pad, truncate, ...) are code-unit based, so string functions go through these
// helpers. ASCII strings, by far the common case, take a fast path where a byte
// is a code unit.
//
// A surrogate with no partner (half of a character above U+FFFF, which
// slicing can make) is written as three WTF-8 bytes. Strings are kept
// canonical: a high surrogate followed by a low one is always the four-byte
// UTF-8 of their character, which every builder ensures where two strings
// meet (appendJoin). So two strings are equal as code units exactly when they
// are equal as bytes, and a needle that neither begins with a low surrogate
// nor ends with a high one occurs in a string exactly where its bytes do.

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isSurrogateAt reports whether the WTF-8 bytes at s[i:] encode a lone
// surrogate whose second byte lies in [lo, hi]: A0-AF for a high surrogate,
// B0-BF for a low one.
func isSurrogateAt(s string, i int, lo, hi byte) bool {
	return i+2 < len(s) && s[i] == 0xED && s[i+1] >= lo && s[i+1] <= hi && s[i+2]&0xC0 == 0x80
}

func startsWithLow(s string) bool { return isSurrogateAt(s, 0, 0xB0, 0xBF) }

func endsWithHigh(s string) bool { return len(s) >= 3 && isSurrogateAt(s, len(s)-3, 0xA0, 0xAF) }

// joins reports whether concatenating x and y joins two surrogate halves.
func joins(x, y string) bool { return endsWithHigh(x) && startsWithLow(y) }

// surrogateAt decodes the lone surrogate at s[i:i+3].
func surrogateAt(s string, i int) rune {
	return 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
}

// appendJoin appends p to b as JavaScript concatenates strings: a high
// surrogate ending b and a low one starting p become the one character they
// encode, so the result stays canonical.
func appendJoin(b []byte, p string) []byte {
	// The first byte of a lone low surrogate is ED: anything else appends.
	if len(p) < 3 || p[0] != 0xED || len(b) < 3 || b[len(b)-3] != 0xED {
		return append(b, p...)
	}
	return appendJoinSlow(b, p)
}

func appendJoinSlow(b []byte, p string) []byte {
	if n := len(b); startsWithLow(p) && endsWithHigh(unsafe.String(unsafe.SliceData(b), n)) {
		hi := 0xD000 | rune(b[n-2]&0x3F)<<6 | rune(b[n-1]&0x3F)
		lo := surrogateAt(p, 0)
		b = utf8.AppendRune(b[:n-3], 0x10000+(hi-0xD800)<<10+(lo-0xDC00))
		p = p[3:]
	}
	return append(b, p...)
}

// appendUnit appends one code unit, a lone surrogate as WTF-8.
func appendUnit(b []byte, u uint16) []byte {
	c := rune(u)
	switch {
	case c < 0x80:
		return append(b, byte(c))
	case c >= 0xD800 && c < 0xE000:
		return append(b, 0xED, byte(0x80|(c>>6)&0x3F), byte(0x80|c&0x3F))
	}
	return utf8.AppendRune(b, c)
}

// charAt decodes the character at s[i:]: its width in bytes and in code units
// and, for a character above U+FFFF, the rune. Invalid UTF-8 reads as one
// U+FFFD, one byte wide.
func charAt(s string, i int) (r rune, bytes, units int) {
	c := s[i]
	if c < utf8.RuneSelf {
		return rune(c), 1, 1
	}
	if c == 0xED && isSurrogateAt(s, i, 0xA0, 0xBF) {
		return surrogateAt(s, i), 3, 1
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	if r >= 0x10000 {
		return r, w, 2
	}
	return r, w, 1
}

// utf16Len is String.prototype.length.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			n++
			i++
			continue
		}
		_, w, u := charAt(s, i)
		n += u
		i += w
	}
	return n
}

// toUTF16 converts to code units. Invalid UTF-8 bytes become U+FFFD, and a
// WTF-8 encoded lone surrogate (which fromUTF16 emits) round-trips.
func toUTF16(s string) []uint16 {
	u := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, w, n := charAt(s, i)
		i += w
		if n == 2 {
			r -= 0x10000
			u = append(u, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u = append(u, uint16(r))
		}
	}
	return u
}

// fromUTF16 converts code units to a canonical Go string: a pair becomes its
// character, a lone surrogate three WTF-8 bytes. Text that leaves the engine
// should be sanitised by the consumer, as WTF-8 is not valid UTF-8.
func fromUTF16(u []uint16) string {
	b := make([]byte, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000 {
			b = utf8.AppendRune(b, 0x10000+(c-0xD800)<<10+(rune(u[i+1])-0xDC00))
			i++
			continue
		}
		b = appendUnit(b, u[i])
	}
	return string(b)
}

// unitOffset is the byte offset of code unit u of s (0 <= u <= length). When
// u falls between the halves of a pair, mid is true and the offset is the
// pair's first byte.
func unitOffset(s string, u int) (off int, mid bool) {
	n := 0
	for i := 0; i < len(s); {
		if n == u {
			return i, false
		}
		if s[i] < utf8.RuneSelf {
			n++
			i++
			continue
		}
		_, w, k := charAt(s, i)
		if n+k > u {
			return i, true
		}
		n += k
		i += w
	}
	return len(s), false
}

// pairHalves splits the four-byte character at s[i:] into its surrogates.
func pairHalves(s string, i int) (hi, lo uint16) {
	r, _ := utf8.DecodeRuneInString(s[i:])
	r -= 0x10000
	return uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)
}

// substr is s.slice(from, to) in code units with 0 <= from, to <= length. A
// slice that begins or ends inside a pair keeps the half it holds; otherwise
// it is a substring of s, sharing its bytes. It costs a pass up to to.
func (sc *Scope) substr(s string, from, to int) string {
	if from >= to {
		return ""
	}
	sc.workBytes(to)
	if to <= len(s) && isASCII(s[:to]) {
		return s[from:to]
	}
	a, amid := unitOffset(s, from)
	b, bmid := unitOffset(s, to)
	if !amid && !bmid {
		return s[a:b]
	}
	out := make([]byte, 0, b-a+6)
	if amid {
		_, lo := pairHalves(s, a)
		out = appendUnit(out, lo)
		a += 4
	}
	out = append(out, s[a:b]...)
	if bmid {
		hi, _ := pairHalves(s, b)
		out = appendUnit(out, hi)
	}
	return string(out)
}

// unitAt is s[i], the string of code unit i, and false past the end.
func (sc *Scope) unitAt(s string, i int) (string, bool) {
	sc.workBytes(i)
	off, mid := unitOffset(s, i)
	if off >= len(s) {
		return "", false
	}
	return unitString(s, off, mid), true
}

// unitString is the code unit at byte offset off of s: the low half of the
// pair there when mid, else the high half of a pair or the whole character.
func unitString(s string, off int, mid bool) string {
	r, w, k := charAt(s, off)
	switch {
	case k == 2:
		hi, lo := pairHalves(s, off)
		if mid {
			return string(appendUnit(nil, lo))
		}
		return string(appendUnit(nil, hi))
	case r == utf8.RuneError && w == 1:
		return "�"
	}
	return s[off : off+w]
}

// unitSearch reports whether a search for sub must run on code units: a
// needle beginning with a low surrogate or ending with a high one can match
// half of a pair, which no byte search finds.
func unitSearch(sub string) bool { return startsWithLow(sub) || endsWithHigh(sub) }

// shortNeedle is the needle length up to which strings.Index is used: its
// worst case is the needle length times the haystack's, which is linear for a
// needle this short. A longer one is searched with Knuth-Morris-Pratt, linear
// whatever the input (upstream's V8 search is linear too).
const shortNeedle = 64

// bytesOf views a string as bytes, without a copy; the slice is read only.
func bytesOf(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// indexOf is String.prototype.indexOf(sub, from) in code units, with from
// already clamped to [0, length]. It runs in time linear in the strings.
func (s *Scope) indexOf(str, sub string, from int) int {
	if sub == "" {
		return from
	}
	s.workBytes(len(str) + len(sub))
	if unitSearch(sub) {
		u, v := s.toUnits(str), s.toUnits(sub)
		return searchUnits(s, u, v, from, false)
	}
	off, mid := unitOffset(str, from)
	if mid {
		off += 4 // sub cannot start on the low half of a pair
	}
	i := s.indexBytes(str, sub, off)
	if i < 0 {
		return -1
	}
	return from + utf16LenRange(str, off, i, mid)
}

// utf16LenRange is the number of code units from unit from (at byte off, or
// the low half before it when mid) to byte i.
func utf16LenRange(str string, off, i int, mid bool) int {
	n := utf16Len(str[off:i])
	if mid {
		n++
	}
	return n
}

// lastIndexOf is String.prototype.lastIndexOf(sub, from) with from clamped
// to [0, length], in linear time.
func (s *Scope) lastIndexOf(str, sub string, from int) int {
	s.workBytes(len(str) + len(sub))
	if unitSearch(sub) {
		u, v := s.toUnits(str), s.toUnits(sub)
		return searchUnits(s, u, v, from, true)
	}
	n := utf16Len(str)
	if sub == "" {
		return min(from, n)
	}
	// Any start at or before unit from: the byte of the character holding it.
	off, _ := unitOffset(str, from)
	end := min(len(str), off+len(sub))
	var i int
	if len(sub) <= shortNeedle {
		i = strings.LastIndex(str[:end], sub)
	} else {
		i = searchLast(s, bytesOf(str[:end]), bytesOf(sub))
	}
	if i < 0 {
		return -1
	}
	return utf16Len(str[:i])
}

// indexBytes is the first byte offset at or after from where sub occurs in
// str, or -1.
func (s *Scope) indexBytes(str, sub string, from int) int {
	if from > len(str) {
		return -1
	}
	var i int
	if len(sub) <= shortNeedle {
		i = strings.Index(str[from:], sub)
	} else {
		i = searchFirst(s, bytesOf(str[from:]), bytesOf(sub))
	}
	if i < 0 {
		return -1
	}
	return from + i
}

// toUnits is toUTF16, charged to the string budget.
func (s *Scope) toUnits(str string) []uint16 {
	s.chargeBytes(2 * len(str))
	s.workBytes(len(str))
	return toUTF16(str)
}

// searchUnits is indexOf (or, with last, lastIndexOf) on code units.
func searchUnits(s *Scope, u, v []uint16, from int, last bool) int {
	if !last {
		if from > len(u) {
			return -1
		}
		i := searchFirst(s, u[from:], v)
		if i < 0 {
			return -1
		}
		return from + i
	}
	end := min(len(u), from+len(v))
	return searchLast(s, u[:end], v)
}

// kmpTable is the failure function of Knuth-Morris-Pratt for needle p:
// t[i] is the length of the longest proper border of p[:i+1].
func kmpTable[E byte | uint16](s *Scope, p []E) []int32 {
	s.chargeBytes(4 * len(p))
	t := make([]int32, len(p))
	k := int32(0)
	steps := int64(len(p))
	for i := 1; i < len(p); i++ {
		for k > 0 && p[i] != p[k] {
			k = t[k-1]
			steps++
		}
		if p[i] == p[k] {
			k++
		}
		t[i] = k
		if i&0xFFFF == 0 {
			s.work(1 << 10)
		}
	}
	s.searchSteps += steps
	return t
}

// searchFirst is the first index of p in h, or -1, in O(len(h)+len(p)).
func searchFirst[E byte | uint16](s *Scope, h, p []E) int {
	if len(p) == 0 {
		return 0
	}
	if len(p) > len(h) {
		return -1
	}
	t := kmpTable(s, p)
	k := int32(0)
	m := int32(len(p))
	var steps int64 // one per character read and per fallback (see Scope.searchSteps)
	for i := 0; i < len(h); i++ {
		steps++
		for k > 0 && h[i] != p[k] {
			k = t[k-1]
			steps++
		}
		if h[i] == p[k] {
			k++
		}
		if k == m {
			s.searchSteps += steps
			return i + 1 - int(m)
		}
		if i&0xFFFF == 0 {
			s.work(1 << 10)
		}
	}
	s.searchSteps += steps
	return -1
}

// searchLast is the last index of p in h, or -1: Knuth-Morris-Pratt on the
// reversed needle, scanning h from its end.
func searchLast[E byte | uint16](s *Scope, h, p []E) int {
	m := len(p)
	if m == 0 {
		return len(h)
	}
	if m > len(h) {
		return -1
	}
	s.chargeBytes(2 * m)
	r := make([]E, m)
	for i := range p {
		r[i] = p[m-1-i]
	}
	t := kmpTable(s, r)
	k := int32(0)
	var steps int64 // as in searchFirst
	for j := 0; j < len(h); j++ {
		steps++
		c := h[len(h)-1-j]
		for k > 0 && c != r[k] {
			k = t[k-1]
			steps++
		}
		if c == r[k] {
			k++
		}
		if int(k) == m {
			s.searchSteps += steps
			return len(h) - 1 - j
		}
		if j&0xFFFF == 0 {
			s.work(1 << 10)
		}
	}
	s.searchSteps += steps
	return -1
}

// compareUTF16 orders two strings by code unit, as JavaScript's < does.
func compareUTF16(a, b string) int { return jsval.CompareUTF16(a, b) }
