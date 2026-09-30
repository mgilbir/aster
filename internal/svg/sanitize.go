package svg

import (
	"strings"
	"unicode/utf8"
)

// URLOptions are the loader options that shape sanitized URLs, mirroring the
// vega-loader options the scenegraph passes through.
type URLOptions struct {
	// BaseURL is prepended to relative URLs.
	BaseURL string
	// DefaultProtocol replaces the missing protocol of `//host` URLs ("http"
	// when empty). The value "file" is not supported here (it would switch to a
	// file load).
	DefaultProtocol string
	// Target and Rel become attributes of the <a> element when non-empty.
	Target, Rel string
}

// HrefAttr is one attribute of the <a> element for a hyperlinked item.
type HrefAttr struct{ Name, Value string }

// SanitizeURL applies vega-loader's URI sanitizer. ok is false for URIs it
// rejects (for example `javascript:` URLs); upstream then renders no link.
func SanitizeURL(uri string, opt URLOptions) (href string, ok bool) {
	if !uriAllowed(uri) {
		return "", false
	}
	hasProtocol := hasProtocolPrefix(uri)
	if base := opt.BaseURL; base != "" && !hasProtocol {
		// Ensure there is a slash between the base URL and the path.
		if !strings.HasPrefix(uri, "/") && !strings.HasSuffix(base, "/") {
			uri = "/" + uri
		}
		uri = base + uri
	}
	if strings.HasPrefix(uri, "file://") {
		uri = uri[len("file://"):]
	} else if strings.HasPrefix(uri, "//") {
		p := opt.DefaultProtocol
		if p == "" {
			p = "http"
		}
		uri = p + ":" + uri
	}
	return uri, true
}

// DefaultHref is the link attributes vega emits for an href: the configured
// target and rel, then xlink:href with the sanitized URL. The order matters for
// byte compatibility.
func DefaultHref(opt URLOptions) func(uri string) ([]HrefAttr, bool) {
	return func(uri string) ([]HrefAttr, bool) {
		href, ok := SanitizeURL(uri, opt)
		if !ok {
			return nil, false
		}
		var attrs []HrefAttr
		if opt.Target != "" {
			attrs = append(attrs, HrefAttr{"target", opt.Target})
		}
		if opt.Rel != "" {
			attrs = append(attrs, HrefAttr{"rel", opt.Rel})
		}
		return append(attrs, HrefAttr{"xlink:href", href}), true
	}
}

// hasProtocolPrefix is /^(data:|([A-Za-z]+:)?\/\/)/.
func hasProtocolPrefix(s string) bool {
	if strings.HasPrefix(s, "data:") {
		return true
	}
	i := 0
	for i < len(s) && (s[i]|0x20 >= 'a' && s[i]|0x20 <= 'z') {
		i++
	}
	if i > 0 && i < len(s) && s[i] == ':' {
		i++
	} else {
		i = 0
	}
	return strings.HasPrefix(s[i:], "//")
}

func isSanitizeSpace(r rune) bool {
	switch {
	case r <= 0x20, r == 0xA0, r == 0x1680, r == 0x180E, r >= 0x2000 && r <= 0x2029, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

// uriAllowed is vega-loader's allowed_re applied to the URI with white space
// removed:
//
//	^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp|file|data):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))  (case-insensitive)
//
// i.e. a whitelisted scheme, a first character that is not a letter, or a
// relative path whose leading word is not followed by a colon.
func uriAllowed(uri string) bool {
	var b strings.Builder
	for _, r := range uri {
		if !isSanitizeSpace(r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, scheme := range []string{"http:", "https:", "ftp:", "ftps:", "mailto:", "tel:", "callto:", "cid:", "xmpp:", "file:", "data:"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	c := lower[0]
	if c >= 0x80 || c < 'a' || c > 'z' {
		return true // [^a-z]
	}
	i := 0
	for i < len(lower) && (lower[i] >= 'a' && lower[i] <= 'z' || lower[i] == '+' || lower[i] == '.' || lower[i] == '-') {
		i++
	}
	if i == len(lower) {
		return true
	}
	// A multi-byte character after the run is "not in the class".
	r, _ := utf8.DecodeRuneInString(lower[i:])
	return r != ':'
}
