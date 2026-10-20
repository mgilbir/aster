package loader

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"
)

// allowed_re is case-insensitive without the u flag, so only ASCII letters
// fold, and a character outside ASCII is never one of [a-z]: the KELVIN SIGN
// (U+212A) and U+0130 are not k and i there, where Go's Unicode lowercasing
// made them so. Every character, first in the URI or after a letter, is
// checked against that rule.
func TestURISchemeLettersAreASCII(t *testing.T) {
	asciiLetter := func(r rune) bool { return r|0x20 >= 'a' && r|0x20 <= 'z' }
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if utf16.IsSurrogate(r) || isURISpace(r) {
			continue
		}
		c := string(r)
		// first: a letter starts a word that the colon makes a scheme
		// (none of these is allow-listed); anything else is [^a-z]
		if got, want := uriAllowed(c+"x:y"), !asciiLetter(r); got != want {
			t.Errorf("uriAllowed(%q) = %v; want %v", c+"x:y", got, want)
		}
		// after a letter: a letter or + . - continues the scheme word,
		// anything else ends it before the colon
		if got, want := uriAllowed("a"+c+":y"), !(asciiLetter(r) || r == '+' || r == '.' || r == '-' || r == ':'); got != want {
			t.Errorf("uriAllowed(%q) = %v; want %v", "a"+c+":y", got, want)
		}
	}
	for _, s := range []string{"HTTP://x", "Data:x", "MAILTO:x", "javascript:x", "JavaScript:x", "Ka:b", "İx:y", "jaſcript:x"} {
		want := !strings.HasPrefix(strings.ToLower(s), "javascript")
		if s == "Ka:b" || s == "İx:y" || s == "jaſcript:x" {
			want = true // upstream: not a scheme at all
		}
		if got := uriAllowed(s); got != want {
			t.Errorf("uriAllowed(%q) = %v; want %v", s, got, want)
		}
	}
}
