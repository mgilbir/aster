package jsval

import (
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// JavaScript's string operations, for every package that reads a value as
// upstream does. Go's own differ where it matters to a specification:
// strings.ToLower maps İ (U+0130) to "i" where toLowerCase gives "i̇", and
// misses ß -> "SS" and the final sigma; strings.TrimSpace and unicode.IsSpace
// count U+0085 as space and not U+FEFF, the reverse of JavaScript's. Engine
// code that reads a specification's text uses these, never the Go ones
// (TestNoGoCaseOrSpaceInTheEngine holds it to that).
//
// Strings are JavaScript's code units written as UTF-8, a surrogate with no
// partner (which slicing a string, or a JSON escape, can make) as three
// WTF-8 bytes (ED A0..BF xx); these functions keep such a surrogate as it is,
// as JavaScript's do.

var (
	upperCaser = cases.Upper(language.Und)
	lowerCaser = cases.Lower(language.Und)
	caseMu     sync.Mutex
)

// JSLower is String.prototype.toLowerCase: the full, locale-independent
// Unicode case mapping (İ -> "i̇", Σ -> ς at the end of a word). An ASCII
// string takes a fast path; a cases.Caser is stateful, hence the lock.
func JSLower(s string) string {
	if isASCII(s) {
		return asciiLower(s)
	}
	return caseMap(s, lowerCaser)
}

// JSUpper is String.prototype.toUpperCase (ß -> "SS", ŉ -> "ʼN").
func JSUpper(s string) string {
	if isASCII(s) {
		return asciiUpper(s)
	}
	return caseMap(s, upperCaser)
}

// caseMap maps s with c, leaving the lone surrogates it holds as they are:
// the caser would read their WTF-8 bytes as invalid UTF-8. A lone surrogate
// is neither cased nor case-ignorable, so it ends a word for the final
// sigma's context as it does in JavaScript, and mapping the text between them
// piece by piece gives the same result.
func caseMap(s string, c cases.Caser) string {
	caseMu.Lock()
	defer caseMu.Unlock()
	if !strings.Contains(s, "\xed") {
		c.Reset()
		return c.String(s)
	}
	var b strings.Builder
	for len(s) > 0 {
		i := lonePos(s)
		if i < 0 {
			i = len(s)
		}
		c.Reset()
		b.WriteString(c.String(s[:i]))
		if i == len(s) {
			break
		}
		b.WriteString(s[i : i+3])
		s = s[i+3:]
	}
	return b.String()
}

// lonePos is the position of the first lone surrogate's WTF-8 bytes in s, or
// -1.
func lonePos(s string) int {
	for i := 0; i < len(s); i++ {
		if _, ok := surrogateAt(s, i); ok {
			return i
		}
	}
	return -1
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; 'A' <= c && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

func asciiUpper(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'a' <= c && c <= 'z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; 'a' <= c && c <= 'z' {
					b[j] = c - ('a' - 'A')
				}
			}
			return string(b)
		}
	}
	return s
}

// IsJSSpace reports whether r is white space to String.prototype.trim,
// Number() and parseFloat: ECMA-262's WhiteSpace (TAB, VT, FF, SP, NBSP,
// U+FEFF and the Space_Separator category) and LineTerminator (LF, CR, U+2028,
// U+2029). Go's unicode.IsSpace differs: it has U+0085 and lacks U+FEFF.
func IsJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// JSTrim is String.prototype.trim.
func JSTrim(s string) string { return strings.TrimFunc(s, IsJSSpace) }

// JSTrimStart is String.prototype.trimStart.
func JSTrimStart(s string) string { return strings.TrimLeftFunc(s, IsJSSpace) }

// JSTrimEnd is String.prototype.trimEnd.
func JSTrimEnd(s string) string { return strings.TrimRightFunc(s, IsJSSpace) }

// UTF16Len is String.prototype.length: s's UTF-16 code units, a character
// above U+FFFF counting two and a lone surrogate's WTF-8 bytes one. A byte of
// invalid UTF-8 counts one, as the U+FFFD it reads as.
func UTF16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c < utf8.RuneSelf:
			n++
			i++
		case c == 0xED:
			if _, ok := surrogateAt(s, i); ok {
				n++
				i += 3
				continue
			}
			fallthrough
		default:
			r, w := utf8.DecodeRuneInString(s[i:])
			n++
			if r >= 0x10000 {
				n++
			}
			i += w
		}
	}
	return n
}

// ToWellFormed is String.prototype.toWellFormed: each lone surrogate becomes
// U+FFFD. It is what a string's code units become when JavaScript writes them
// as UTF-8 (V8's String::WriteUtf8, a Buffer, node-canvas's text): one U+FFFD
// a surrogate, so text that leaves the engine (an SVG document, a measured
// run) goes through it.
func ToWellFormed(s string) string {
	i := lonePos(s)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i >= 0 {
		b.WriteString(s[:i])
		b.WriteString("�")
		s = s[i+3:]
		i = lonePos(s)
	}
	b.WriteString(s)
	return b.String()
}

// AppendSurrogate appends the WTF-8 bytes of the lone surrogate u
// (U+D800..U+DFFF): how a string holds a code unit that is half of a pair.
func AppendSurrogate(b []byte, u rune) []byte {
	return append(b, 0xED, byte(0x80|(u>>6)&0x3F), byte(0x80|u&0x3F))
}

// UTF16Prefix is s.substr(0, n): the first n UTF-16 code units of s. A cut
// inside a character above U+FFFF keeps its high surrogate, alone, as WTF-8.
func UTF16Prefix(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		if units >= n {
			return s[:i]
		}
		c := s[i]
		if c < utf8.RuneSelf {
			units++
			i++
			continue
		}
		if _, ok := surrogateAt(s, i); ok {
			units++
			i += 3
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			if units+1 == n {
				hi := 0xD800 + (r-0x10000)>>10
				return string(AppendSurrogate([]byte(s[:i]), hi))
			}
			units++
		}
		units++
		i += w
	}
	return s
}

// UpperFirst is s[0].toUpperCase() + s.slice(1) (and "" for ""): the first
// UTF-16 code unit upper-cased with toUpperCase, so ß becomes "SS". A first
// character above U+FFFF is a lone surrogate to s[0], which has no upper
// case: the string is unchanged.
func UpperFirst(s string) string { return mapFirst(s, JSUpper) }

// LowerFirst is s[0].toLowerCase() + s.slice(1).
func LowerFirst(s string) string { return mapFirst(s, JSLower) }

func mapFirst(s string, f func(string) string) string {
	if s == "" {
		return s
	}
	if s[0] < utf8.RuneSelf {
		return f(s[:1]) + s[1:]
	}
	if _, ok := surrogateAt(s, 0); ok {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	if r >= 0x10000 || r == utf8.RuneError {
		return s
	}
	return f(s[:n]) + s[n:]
}

// RegExpFoldEqual reports whether a and b match as a regular expression
// with the i flag (and no u) matches a literal: code unit by code unit after
// ECMA-262's Canonicalize, which upper-cases a unit with toUpperCase unless
// that gives more than one unit, or turns a non-ASCII unit into an ASCII one
// (so "ſ" does not match "s", nor the Kelvin sign "k"). Go's strings.EqualFold
// folds both of those.
func RegExpFoldEqual(a, b string) bool {
	for a != "" && b != "" {
		ra, na := decodeWTF8(a)
		rb, nb := decodeWTF8(b)
		if ra != rb && canonicalize(ra) != canonicalize(rb) {
			return false
		}
		a, b = a[na:], b[nb:]
	}
	return a == "" && b == ""
}

// canonicalize is ECMA-262's Canonicalize for a non-Unicode, ignoreCase
// pattern, of a character of the BMP (one above it is two code units, which
// no case mapping changes).
func canonicalize(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	if r >= 0x10000 || r >= 0xD800 && r <= 0xDFFF {
		return r
	}
	u := JSUpper(string(r))
	ur, n := utf8.DecodeRuneInString(u)
	if n != len(u) || ur >= 0x10000 || ur < utf8.RuneSelf {
		return r
	}
	return ur
}

// DecodeRune is one step of the WHATWG UTF-8 decoder (what TextDecoder and
// Response.text(), which upstream's loaders read text with, do): the
// character b starts with and its length, or, where b does not start with
// a valid sequence, U+FFFD for its maximal subpart (a lead byte and the
// continuation bytes that can follow it, at least one byte): a truncated
// sequence is one U+FFFD, not one a byte as Go's []rune conversion gives.
func DecodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return utf8.RuneError, 0
	}
	c := b[0]
	if c < utf8.RuneSelf {
		return rune(c), 1
	}
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 1
	case c == 0xE0:
		need, lo = 2, 0xA0
	case c == 0xED:
		need, hi = 2, 0x9F
	case c >= 0xE1 && c <= 0xEF:
		need = 2
	case c == 0xF0:
		need, lo = 3, 0x90
	case c == 0xF4:
		need, hi = 3, 0x8F
	case c >= 0xF1 && c <= 0xF3:
		need = 3
	default:
		return utf8.RuneError, 1
	}
	for i := 1; i <= need; i++ {
		if i >= len(b) || b[i] < lo || b[i] > hi {
			return utf8.RuneError, i
		}
		lo, hi = 0x80, 0xBF
	}
	r, n := utf8.DecodeRune(b[:need+1])
	return r, n
}

// AppendDecoded appends the text the WHATWG UTF-8 decoder makes of b: b
// itself when it is valid UTF-8, else each maximal subpart of an invalid
// sequence as one U+FFFD (DecodeRune).
func AppendDecoded(dst, b []byte) []byte {
	if utf8.Valid(b) {
		return append(dst, b...)
	}
	for len(b) > 0 {
		r, n := DecodeRune(b)
		if r == utf8.RuneError { // an invalid subpart, or U+FFFD itself
			dst = append(dst, "\uFFFD"...)
		} else {
			dst = append(dst, b[:n]...)
		}
		b = b[n:]
	}
	return dst
}

// DecodedLen is len(AppendDecoded(nil, b)), without making it.
func DecodedLen(b []byte) int {
	if utf8.Valid(b) {
		return len(b)
	}
	n := 0
	for len(b) > 0 {
		r, w := DecodeRune(b)
		if r == utf8.RuneError {
			n += 3
		} else {
			n += w
		}
		b = b[w:]
	}
	return n
}
