package loader

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// URIOptions are the options of vega-loader's sanitize that shape the string
// it returns. Its other options (mode, target, rel, crossOrigin) decide what
// else the result says, not the href.
type URIOptions struct {
	// BaseURL is put before a relative URI.
	BaseURL string
	// DefaultProtocol replaces the missing protocol of a //host URI: "http"
	// when empty, and "file" drops the // instead.
	DefaultProtocol string
}

// ErrURINotAllowed is the error of a URI that vega-loader's allow-list
// refuses (javascript:, vbscript:, and the like).
var ErrURINotAllowed = errors.New("aster: the URI is not allowed (its scheme is not on vega-loader's list)")

// SanitizeURI is vega-loader's sanitize: the string a Vega view writes into a
// link or an image and then loads, for the same URI and options.
//
//   - The URI must pass the allow-list, tested on it with white space removed
//     (so java\tscript: is refused): a listed scheme, a first character that
//     is not a letter, or a word not followed by a colon.
//   - A URI without a protocol (not data:, not [A-Za-z]+:// or //) is put
//     after BaseURL, with a "/" between the two when neither has one. The
//     result is a string join: nothing is resolved, so a "../" stays in it.
//   - file:// is dropped (a file load), and "//" takes DefaultProtocol.
//
// Everything is kept as written otherwise: no case folding, no percent
// encoding, no parsing. That is the string upstream writes; what a loader
// fetches from it is Load's business (HTTPLoader.Load parses it as fetch does).
func SanitizeURI(uri string, o URIOptions) (string, error) {
	if !uriAllowed(uri) {
		return "", ErrURINotAllowed
	}
	hasProtocol := hasProtocolPrefix(uri)
	if base := o.BaseURL; base != "" && !hasProtocol {
		if !strings.HasPrefix(uri, "/") && !strings.HasSuffix(base, "/") {
			uri = "/" + uri
		}
		uri = base + uri
	}
	if rest, ok := strings.CutPrefix(uri, "file://"); ok {
		return rest, nil
	}
	if strings.HasPrefix(uri, "//") {
		switch o.DefaultProtocol {
		case "file":
			return uri[2:], nil
		case "":
			return "http:" + uri, nil
		}
		return o.DefaultProtocol + ":" + uri, nil
	}
	return uri, nil
}

// hasProtocolPrefix is /^(data:|([A-Za-z]+:)?\/\/)/.
func hasProtocolPrefix(s string) bool {
	if strings.HasPrefix(s, "data:") {
		return true
	}
	i := 0
	for i < len(s) && isASCIILetter(s[i]) {
		i++
	}
	if i > 0 && i < len(s) && s[i] == ':' {
		i++
	} else {
		i = 0
	}
	return strings.HasPrefix(s[i:], "//")
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// isURISpace is /[\u0000-   ᠎ -  　]/.
func isURISpace(r rune) bool {
	switch {
	case r <= 0x20, r == 0xA0, r == 0x1680, r == 0x180E, r >= 0x2000 && r <= 0x2029, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

// uriSchemes are the schemes of vega-loader's allowed_re, lower case, with
// their colon: (?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp|file|data):
var uriSchemes = []string{"http:", "https:", "ftp:", "ftps:", "mailto:", "tel:", "callto:", "cid:", "xmpp:", "file:", "data:"}

// uriAllowed is vega-loader's allowed_re, case-insensitive, applied to the URI
// with white space removed:
//
//	^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp|file|data):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))
//
// It is written on bytes. The expression has no u flag, so its case folding
// is ASCII's: a Kelvin sign is not a k (strings.ToLower would make it one),
// and is "not a letter" as far as it goes.
func uriAllowed(uri string) bool {
	var b []byte
	for i := 0; i < len(uri); {
		r, n := utf8.DecodeRuneInString(uri[i:])
		if !isURISpace(r) {
			b = append(b, uri[i:i+n]...)
		}
		i += n
	}
	if len(b) == 0 {
		return false
	}
	for _, s := range uriSchemes {
		if hasPrefixFold(b, s) {
			return true
		}
	}
	if !isASCIILetter(b[0]) {
		return true // [^a-z]
	}
	i := 0
	for i < len(b) && (isASCIILetter(b[i]) || b[i] == '+' || b[i] == '.' || b[i] == '-') {
		i++
	}
	// The word must be followed by the end or by a character that is neither
	// of its kind nor a colon; a multi-byte character is such a character.
	return i == len(b) || b[i] != ':'
}

// hasPrefixFold reports whether b starts with the lower case ASCII s, ignoring
// the case of ASCII letters alone.
func hasPrefixFold(b []byte, s string) bool {
	if len(b) < len(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != s[i] {
			return false
		}
	}
	return true
}
