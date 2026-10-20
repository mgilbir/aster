package loader

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is the WHATWG URL parser, as far as an http or https URL goes: the
// request URL fetch makes of the string Vega hands it. The string reaches
// HTTPLoader.Load as vega-loader's sanitize wrote it (SanitizeURI), and
// upstream gives it to fetch, which parses it with the URL Standard:
// backslashes are slashes, tabs and newlines are gone, dot segments are
// resolved, percent-encoded dots count, a host is lower-cased and a number
// like 0x7f.1 is an IPv4 address. Go's net/url reads the same string
// differently (it keeps a backslash, refuses a space in a path, leaves
// %2e%2e alone), so a name check made on one and a connection made on the
// other could disagree. Load therefore requests the URL parsed here, and the
// policy is checked on the URL that is requested.

// errNotRequest is the error of a string that is not an absolute http or
// https URL: a relative reference, mailto:, data:, a path. Such a string is
// a link at most, never something to fetch.
var errNotRequest = errors.New("aster: not an absolute http or https URL")

// errHostNotASCII is the error of a host name with a character outside ASCII.
// The URL Standard maps it (IDNA) to the punycode form fetch connects to; the
// loader does not, so it refuses such a host rather than check one name and
// connect to another: write the punycode form.
var errHostNotASCII = errors.New("aster: the host is not ASCII (use the punycode form)")

// errUserinfo is the error of a URL with credentials; fetch refuses them too.
var errUserinfo = errors.New("aster: the URI contains userinfo (not allowed)")

var errBadURL = errors.New("aster: not a valid http or https URL")

// requestURL is a parsed http or https URL, in the form fetch requests it
// (the fragment is not part of a request).
type requestURL struct {
	scheme   string // "http" or "https"
	host     string // lower case; an IPv6 address in brackets, an IPv4 one dotted
	port     string // empty when the scheme's default
	path     string // begins with "/"
	query    string
	hasQuery bool
}

// hostport is the host, and the port unless it is the default one.
func (r *requestURL) hostport() string {
	if r.port == "" {
		return r.host
	}
	return r.host + ":" + r.port
}

// String is the URL as the URL Standard serializes it (without a fragment),
// but for a '%' that starts no escape in the path, which is written %25: net/url
// refuses such a path, and fetch would send it as it is.
func (r *requestURL) String() string {
	s := r.scheme + "://" + r.hostport() + r.path
	if r.hasQuery {
		s += "?" + r.query
	}
	return s
}

// parseRequestURL parses s as new URL(s) does, for an http or https URL.
// errNotRequest says s is not one (it may be a valid link); any other error
// says it is one that cannot be requested.
func parseRequestURL(s string) (*requestURL, error) {
	// Leading and trailing C0 controls and spaces go, and tabs and newlines
	// anywhere.
	start, end := 0, len(s)
	for start < end && s[start] <= 0x20 {
		start++
	}
	for end > start && s[end-1] <= 0x20 {
		end--
	}
	s = s[start:end]
	if strings.ContainsAny(s, "\t\n\r") {
		s = strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, s)
	}

	i := 0
	for i < len(s) && (isASCIILetter(s[i]) || i > 0 && (s[i] >= '0' && s[i] <= '9' || s[i] == '+' || s[i] == '-' || s[i] == '.')) {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != ':' {
		return nil, errNotRequest // no scheme: a relative reference
	}
	r := &requestURL{scheme: strings.ToLower(s[:i])}
	if r.scheme != "http" && r.scheme != "https" {
		return nil, errNotRequest
	}
	rest := s[i+1:]
	// Special schemes take any run of slashes and backslashes after the
	// colon, however few ("http:example.com" is a host).
	for len(rest) > 0 && (rest[0] == '/' || rest[0] == '\\') {
		rest = rest[1:]
	}
	if h := strings.IndexByte(rest, '#'); h >= 0 {
		rest = rest[:h]
	}
	e := strings.IndexAny(rest, "/\\?")
	if e < 0 {
		e = len(rest)
	}
	authority, remainder := rest[:e], rest[e:]

	// The host is what follows the last @; what precedes it is credentials,
	// which are refused unless empty (http://@host/ has none).
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		if c := authority[:at]; c != "" && c != ":" {
			return nil, errUserinfo
		}
		authority = authority[at+1:]
	}
	host, port, ok := splitHostPort(authority)
	if !ok {
		return nil, errBadURL
	}
	var err error
	if r.host, err = parseHost(host); err != nil {
		return nil, err
	}
	if r.port, ok = canonicalPort(port, r.scheme); !ok {
		return nil, errBadURL
	}

	pathPart := remainder
	if q := strings.IndexByte(remainder, '?'); q >= 0 {
		pathPart = remainder[:q]
		r.query = encodeSet(remainder[q+1:], inSpecialQuerySet)
		r.hasQuery = true
	}
	r.path = parsePath(pathPart)
	return r, nil
}

// splitHostPort splits an authority into host (with its brackets) and port
// text.
func splitHostPort(a string) (host, port string, ok bool) {
	if strings.HasPrefix(a, "[") {
		j := strings.IndexByte(a, ']')
		if j < 0 {
			return "", "", false
		}
		host, a = a[:j+1], a[j+1:]
		if a == "" {
			return host, "", true
		}
		if a[0] != ':' {
			return "", "", false
		}
		return host, a[1:], true
	}
	if c := strings.IndexByte(a, ':'); c >= 0 {
		host, port = a[:c], a[c+1:]
	} else {
		host = a
	}
	return host, port, host != ""
}

// canonicalPort is port as a number without leading zeros, empty when it is
// the scheme's default; !ok when it is not a port.
func canonicalPort(port, scheme string) (string, bool) {
	if port == "" {
		return "", true
	}
	n := 0
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return "", false
		}
		if n <= 65535 {
			n = n*10 + int(port[i]-'0')
		}
	}
	if n > 65535 {
		return "", false
	}
	if n == 80 && scheme == "http" || n == 443 && scheme == "https" {
		return "", true
	}
	return strconv.Itoa(n), true
}

// parseHost is the URL Standard's host parser for a special scheme.
func parseHost(h string) (string, error) {
	if strings.HasPrefix(h, "[") {
		if !strings.HasSuffix(h, "]") {
			return "", errBadURL
		}
		inner := h[1 : len(h)-1]
		if strings.ContainsRune(inner, '%') { // no zone ids in a URL
			return "", errBadURL
		}
		a, err := netip.ParseAddr(inner)
		if err != nil || !a.Is6() {
			return "", errBadURL
		}
		return "[" + serializeIPv6(a) + "]", nil
	}
	for i := 0; i < len(h); i++ {
		if h[i] >= 0x80 {
			return "", errHostNotASCII
		}
	}
	// Percent-decoding, then ASCII lower case. (The URL Standard's IDNA step
	// changes nothing else in an ASCII name that matters here.)
	b := make([]byte, 0, len(h))
	for i := 0; i < len(h); i++ {
		c := h[i]
		if c == '%' && i+2 < len(h) && isHex(h[i+1]) && isHex(h[i+2]) {
			c = unhex(h[i+1])<<4 | unhex(h[i+2])
			i += 2
		}
		if c >= 0x80 {
			return "", errHostNotASCII
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	if len(b) == 0 {
		return "", errBadURL
	}
	for _, c := range b {
		if c <= 0x1f || c == 0x7f || strings.IndexByte(" #%/:<>?@[\\]^|", c) >= 0 {
			return "", errBadURL // a forbidden domain code point
		}
	}
	d := string(b)
	if endsInANumber(d) {
		v4, ok := parseIPv4(d)
		if !ok {
			return "", errBadURL
		}
		return v4, nil
	}
	return d, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// endsInANumber is the URL Standard's check for a host that is an IPv4
// address in some spelling.
func endsInANumber(d string) bool {
	parts := strings.Split(d, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number is one part of an IPv4 address: decimal, octal (leading 0)
// or hex (0x). Values past 2^40 are not told apart: they fail either way.
func parseIPv4Number(p string) (uint64, bool) {
	if p == "" {
		return 0, false
	}
	base := uint64(10)
	switch {
	case len(p) >= 2 && (p[:2] == "0x" || p[:2] == "0X"):
		p, base = p[2:], 16
	case len(p) >= 2 && p[0] == '0':
		p, base = p[1:], 8
	}
	var n uint64
	for i := 0; i < len(p); i++ {
		c := p[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			d = uint64(unhex(c))
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		if n < 1<<40 {
			n = n*base + d
		}
	}
	return n, true
}

// parseIPv4 is the URL Standard's IPv4 parser, returning the dotted form.
func parseIPv4(d string) (string, bool) {
	parts := strings.Split(d, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := parseIPv4Number(p)
		if !ok {
			return "", false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return "", false
		}
	}
	last := nums[len(nums)-1]
	if last >= 1<<(8*(5-len(nums))) {
		return "", false
	}
	v := last
	for i, n := range nums[:len(nums)-1] {
		v += n << (8 * (3 - i))
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String(), true
}

// serializeIPv6 is the URL Standard's IPv6 serializer: lower case hex pieces,
// the first longest run of two or more zero pieces compressed, and no dotted
// tail (netip's String writes ::ffff:1.2.3.4 for a mapped address).
func serializeIPv6(a netip.Addr) string {
	b := a.As16()
	var p [8]uint16
	for i := range p {
		p[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	best, bestLen := -1, 1
	for i := 0; i < 8; {
		if p[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && p[j] == 0 {
			j++
		}
		if j-i > bestLen {
			best, bestLen = i, j-i
		}
		i = j
	}
	var sb strings.Builder
	skip := false
	for i := 0; i < 8; i++ {
		if skip {
			if p[i] == 0 {
				continue
			}
			skip = false
		}
		if i == best {
			if i == 0 {
				sb.WriteByte(':')
			}
			sb.WriteByte(':')
			skip = true
			continue
		}
		sb.WriteString(strconv.FormatUint(uint64(p[i]), 16))
		if i != 7 {
			sb.WriteByte(':')
		}
	}
	return sb.String()
}

// parsePath is the path state for a special scheme: separators are / and \,
// "." and "%2e" segments vanish, ".." (and its percent-encoded spellings)
// removes the segment before, and the rest is percent-encoded.
func parsePath(p string) string {
	if p == "" {
		return "/"
	}
	// p begins with a separator: the authority ended there.
	var segs []string
	rest := p[1:]
	for {
		end := strings.IndexAny(rest, "/\\")
		seg, last := rest, end < 0
		if !last {
			seg = rest[:end]
		}
		switch {
		case isDoubleDot(seg):
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
			if last {
				segs = append(segs, "")
			}
		case isSingleDot(seg):
			if last {
				segs = append(segs, "")
			}
		default:
			segs = append(segs, encodePath(seg))
		}
		if last {
			break
		}
		rest = rest[end+1:]
	}
	return "/" + strings.Join(segs, "/")
}

func isSingleDot(s string) bool { return s == "." || strings.EqualFold(s, "%2e") }

func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

// encodePath percent-encodes a path segment with the URL Standard's path set,
// and a '%' that starts no escape with %25 (see requestURL.String).
func encodePath(s string) string {
	return encodeSetFn(s, inPathSet, true)
}

func encodeSet(s string, in func(byte) bool) string { return encodeSetFn(s, in, false) }

// inC0Set is the C0 control percent-encode set: C0 controls and everything
// past '~'.
func inC0Set(c byte) bool { return c <= 0x1f || c > 0x7e }

// inQuerySet adds space, ", # < and > .
func inQuerySet(c byte) bool {
	return inC0Set(c) || c == ' ' || c == '"' || c == '#' || c == '<' || c == '>'
}

// inSpecialQuerySet is the query set of a special scheme: it adds '.
func inSpecialQuerySet(c byte) bool { return inQuerySet(c) || c == '\'' }

// inPathSet is the query set and ? ^ ` { } .
func inPathSet(c byte) bool {
	return inQuerySet(c) || c == '?' || c == '^' || c == '`' || c == '{' || c == '}'
}

const upperHex = "0123456789ABCDEF"

// encodeSetFn writes s with each byte in the set as %XX (UTF-8 for the
// characters past ASCII; an invalid byte is U+FFFD, as the string conversion
// upstream makes). With strayPercent a '%' not followed by two hex digits is
// written %25.
func encodeSetFn(s string, in func(byte) bool, strayPercent bool) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if in(s[i]) || strayPercent && s[i] == '%' {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	pct := func(c byte) {
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&15])
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x80 {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				pct(0xEF)
				pct(0xBF)
				pct(0xBD)
			} else {
				for _, x := range []byte(s[i : i+n]) {
					pct(x)
				}
			}
			i += n
			continue
		}
		switch {
		case strayPercent && c == '%' && !(i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2])):
			b.WriteString("%25")
		case in(c):
			pct(c)
		default:
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}
