package svg

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

func TestEscapedOutputIsWellFormedXML(t *testing.T) {
	in := "a\x01b\x08c￾d￿e\xffg\xed\xa0\x80h\tij<&>\"ké\U0001F600"
	for _, attr := range []bool{false, true} {
		out := string(appendEscaped(nil, in, attr))
		if !utf8.ValidString(out) {
			t.Errorf("attr=%v: invalid UTF-8: %q", attr, out)
		}
		doc := `<a x="` + string(appendEscaped(nil, in, true)) + `">` + string(appendEscaped(nil, in, false)) + `</a>`
		if err := xml.Unmarshal([]byte(doc), new(struct{})); err != nil {
			t.Fatalf("not well-formed: %v\n%q", err, doc)
		}
		if strings.ContainsAny(out, "\x01\x08￾￿") {
			t.Errorf("forbidden characters survived: %q", out)
		}
	}
	var w writer
	w.reset(context.Background(), nil, 0)
	w.attrBytes("d", []byte("a\x02<"))
	if got := string(w.bytes()); got != ` d="a&lt;"` {
		t.Errorf("bytes variant: %q", got)
	}
	if got := string(appendEscaped(nil, "plain é text", false)); got != "plain é text" {
		t.Errorf("valid text must be untouched: %q", got)
	}
}

func TestBlendIsAllowListed(t *testing.T) {
	for _, m := range []string{"multiply", "color-dodge", "luminosity"} {
		if string(blendMode(m)) != m {
			t.Errorf("%s must be allowed", m)
		}
	}
	for _, m := range []string{"normal;background:url(http://x)", "", "Multiply", "url(x)"} {
		if m != "" && blendMode(m) != "" {
			t.Errorf("%q must be rejected", m)
		}
	}
}

// TestURISchemeLettersAreASCII is C152 (audit 2026-10-18): vega-loader's
// allowed_re is case-insensitive without the u flag, so only ASCII letters
// fold, and a character outside ASCII is never one of [a-z]: the KELVIN SIGN
// (U+212A) and U+0130 are not k and i there, where Go's Unicode lowercasing
// made them so. Every character, first in the URI or after a letter, is
// checked against that rule.
func TestURISchemeLettersAreASCII(t *testing.T) {
	asciiLetter := func(r rune) bool { return r|0x20 >= 'a' && r|0x20 <= 'z' }
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if utf16.IsSurrogate(r) || isSanitizeSpace(r) {
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
