package loader

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// testdata/sanitize.json.gz is what upstream does with a URI string, recorded
// by testdata/gen_sanitize.mjs with the pinned node (see the script): the
// string vega-loader's sanitize returns for each URI and set of options, and
// the URL (and the request line a server receives) that node's fetch makes of
// a string.
type sanitizeVectors struct {
	Node     string `json:"node"`
	Sanitize []struct {
		URI     string `json:"uri"`
		Options struct {
			BaseURL         string `json:"baseURL"`
			DefaultProtocol string `json:"defaultProtocol"`
		} `json:"options"`
		Href *string `json:"href"`
	} `json:"sanitize"`
	URLs []struct {
		In   string `json:"in"`
		Kind string `json:"kind"` // ok, userinfo, notHTTP, invalid
		Href string `json:"href"`
		Wire string `json:"wire"`
	} `json:"urls"`
}

var (
	vectorsOnce sync.Once
	vectors     sanitizeVectors
	vectorsErr  error
)

func loadVectors(t *testing.T) *sanitizeVectors {
	t.Helper()
	vectorsOnce.Do(func() {
		f, err := os.Open("testdata/sanitize.json.gz")
		if err != nil {
			vectorsErr = err
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			vectorsErr = err
			return
		}
		vectorsErr = json.NewDecoder(zr).Decode(&vectors)
	})
	if vectorsErr != nil {
		t.Fatal(vectorsErr)
	}
	return &vectors
}

// SanitizeURI is vega-loader's sanitize: the same string for the same URI and
// options, and a refusal exactly where upstream refuses.
func TestSanitizeURIMatchesVegaLoader(t *testing.T) {
	v := loadVectors(t)
	if len(v.Sanitize) < 1000 {
		t.Fatalf("only %d recorded cases", len(v.Sanitize))
	}
	for _, c := range v.Sanitize {
		got, err := SanitizeURI(c.URI, URIOptions{BaseURL: c.Options.BaseURL, DefaultProtocol: c.Options.DefaultProtocol})
		switch {
		case c.Href == nil && err == nil:
			t.Errorf("SanitizeURI(%q, %+v) = %q, but upstream refuses it", c.URI, c.Options, got)
		case c.Href != nil && err != nil:
			t.Errorf("SanitizeURI(%q, %+v) refused (%v), but upstream returns %q", c.URI, c.Options, err, *c.Href)
		case c.Href != nil && got != *c.Href:
			t.Errorf("SanitizeURI(%q, %+v) = %q, upstream %q", c.URI, c.Options, got, *c.Href)
		}
	}
}

// fetchable is a string that begins like an absolute http(s) URL as the URL
// Standard reads one: a reason for a Loader to look at where it leads.
var fetchable = regexp.MustCompile(`(?i)^[\x00-\x20]*h[\t\n\r]*t[\t\n\r]*t[\t\n\r]*p[\t\n\r]*(s[\t\n\r]*)?:`)

// HTTPLoader.Sanitize returns exactly the string vega-loader's sanitize does,
// for every URI and every BaseURL: the link Vega writes, and the string it
// loads. It refuses only what upstream refuses, and an http(s) URL that fails
// the access policy or cannot be requested: there is no policy but this one
// in the table (public hosts), so every such refusal is a URL that cannot be
// parsed as one.
func TestHTTPLoaderSanitizeIsVegaLoadersString(t *testing.T) {
	v := loadVectors(t)
	n := 0
	for _, c := range v.Sanitize {
		if c.Options.DefaultProtocol != "" {
			continue // HTTPLoader has no such option
		}
		l := &HTTPLoader{BaseURL: c.Options.BaseURL}
		got, err := l.Sanitize(context.Background(), c.URI)
		n++
		switch {
		case c.Href == nil:
			if err == nil {
				t.Errorf("Sanitize(%q) base %q = %q, upstream refuses it", c.URI, c.Options.BaseURL, got)
			}
		case err != nil:
			// Only an http(s) URL may be refused, for the policy (a private
			// host, userinfo) or because fetch could not parse it either.
			if !fetchable.MatchString(*c.Href) {
				t.Errorf("Sanitize(%q) base %q refused (%v); upstream returns %q, which is no http(s) URL", c.URI, c.Options.BaseURL, err, *c.Href)
			}
		case got != *c.Href:
			t.Errorf("Sanitize(%q) base %q = %q, upstream %q", c.URI, c.Options.BaseURL, got, *c.Href)
		}
	}
	if n < 500 {
		t.Fatalf("only %d cases ran", n)
	}
}

// FileLoader.Sanitize returns the path as given, which is what vega-loader
// writes for a URI without a BaseURL (the cleaning happens in Load); it
// refuses what it does not serve (a scheme, an absolute path, traversal)
// and what upstream refuses.
func TestFileLoaderSanitizeIsVegaLoadersString(t *testing.T) {
	v := loadVectors(t)
	l := &FileLoader{BaseDir: t.TempDir()}
	accepted := 0
	for _, c := range v.Sanitize {
		if c.Options.BaseURL != "" || c.Options.DefaultProtocol != "" {
			continue
		}
		got, err := l.Sanitize(context.Background(), c.URI)
		if err != nil {
			continue // a file loader serves relative paths only
		}
		accepted++
		if c.Href == nil {
			t.Errorf("Sanitize(%q) = %q, upstream refuses it", c.URI, got)
		} else if got != *c.Href {
			t.Errorf("Sanitize(%q) = %q, upstream %q", c.URI, got, *c.Href)
		}
	}
	if accepted < 20 {
		t.Errorf("only %d paths accepted", accepted)
	}
	// The path written is the path read, cleaned.
	if got, err := l.Sanitize(context.Background(), "./data/../a.csv"); err != nil || got != "./data/../a.csv" {
		t.Errorf("Sanitize(./data/../a.csv) = %q, %v", got, err)
	}
}

// The request URL: for every string, what node's fetch makes of it. A string
// fetch parses is the same URL here, except where the loader refuses to
// follow: a name outside ASCII (fetch maps it to punycode; write that), the
// credentials fetch itself refuses, and a '%' that starts no escape in the
// path (net/http refuses that, so it is sent as %25).
func TestRequestURLMatchesFetch(t *testing.T) {
	v := loadVectors(t)
	if len(v.URLs) < 200 {
		t.Fatalf("only %d recorded cases", len(v.URLs))
	}
	stray := regexp.MustCompile(`%([^0-9A-Fa-f]|[0-9A-Fa-f][^0-9A-Fa-f]|[0-9A-Fa-f]?$)`)
	for _, c := range v.URLs {
		r, err := parseRequestURL(c.In)
		switch c.Kind {
		case "notHTTP":
			if !errors.Is(err, errNotRequest) {
				t.Errorf("%q: %v, want errNotRequest", c.In, err)
			}
		case "userinfo":
			if !errors.Is(err, errUserinfo) {
				t.Errorf("%q: %v, want errUserinfo", c.In, err)
			}
		case "invalid":
			if err == nil {
				t.Errorf("%q: parsed as %q, but fetch refuses it", c.In, r)
			}
		case "ok":
			if errors.Is(err, errHostNotASCII) {
				if !strings.ContainsFunc(c.In, func(r rune) bool { return r >= 0x80 }) && !strings.Contains(strings.ToLower(c.In), "%c3") {
					t.Errorf("%q: refused as not ASCII", c.In)
				}
				continue
			}
			if err != nil {
				t.Errorf("%q: %v, but fetch requests %q", c.In, err, c.Href)
				continue
			}
			want := c.Href
			if i := strings.Index(want[len("https://"):], "/"); i >= 0 {
				head, tail := want[:len("https://")+i], want[len("https://")+i:]
				path, q, hasQ := strings.Cut(tail, "?")
				path = stray.ReplaceAllStringFunc(path, func(m string) string { return "%25" + m[1:] })
				want = head + path
				if hasQ {
					want += "?" + q
				}
			}
			if got := r.String(); got != want {
				t.Errorf("%q: %q, fetch requests %q", c.In, got, want)
			}
			if c.Wire != "" && !stray.MatchString(c.Wire) {
				if wire := strings.TrimPrefix(r.String(), r.scheme+"://"+r.hostport()); wire != c.Wire {
					t.Errorf("%q: request line %q, a server received %q", c.In, wire, c.Wire)
				}
			}
		default:
			t.Fatalf("unknown kind %q", c.Kind)
		}
	}
}

// A URL the URL Standard parses is also one net/http requests, with the host
// and port parsed here: no spelling is read two ways. The exceptions are
// listed: hosts fetch accepts that net/url refuses (characters it does not
// allow in a host), which Load refuses.
func TestRequestURLIsWhatNetHTTPRequests(t *testing.T) {
	v := loadVectors(t)
	refused := map[string]bool{}
	for _, c := range v.URLs {
		if c.Kind != "ok" {
			continue
		}
		r, err := parseRequestURL(c.In)
		if err != nil {
			continue
		}
		u, err := url.Parse(r.String())
		if err != nil {
			refused[r.host] = true
			continue
		}
		if u.Scheme != r.scheme || u.Host != r.hostport() || u.User != nil || u.Opaque != "" {
			t.Errorf("%q: net/url reads %q as %q host %q", c.In, r.String(), u.Scheme, u.Host)
		}
		// What net/http sends is what fetch sends, but for a '|', which it
		// writes %7C.
		wire := r.String()[len(r.scheme+"://"+r.hostport()):]
		if got := u.RequestURI(); got != strings.ReplaceAll(wire, "|", "%7C") {
			t.Errorf("%q: net/http sends %q, fetch sends %q", c.In, got, wire)
		}
		// The path net/http sends is the path parsed here, as bytes decode.
		if want, _ := url.PathUnescape(r.path); u.Path != want {
			t.Errorf("%q: net/url path %q, want %q", c.In, u.Path, want)
		}
	}
	for h := range refused {
		// Hosts of the vectors that net/url does not take: punctuation it
		// does not allow in a host name.
		if !strings.ContainsAny(h, "\"'`{}") {
			t.Errorf("host %q is refused by net/url for no known reason", h)
		}
	}
	if len(refused) == 0 {
		t.Error("no case exercises a host net/url refuses")
	}
}

// recorder is a RoundTripper that records the URLs it is asked for.
type recorder struct {
	mu   sync.Mutex
	urls []*url.URL
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL)
	r.mu.Unlock()
	return respond(req, 200, "[]"), nil
}

// The URL that is checked is the URL that is fetched. Strings that net/url,
// the URL Standard and a name check would each read differently are given to
// Load under each policy; whatever reaches the transport must pass that
// policy, as the transport sees it, and a string that leads somewhere else
// than it looks must never reach it.
func TestHTTPLoaderChecksTheURLItFetches(t *testing.T) {
	schemes := []string{"http://", "https://", "HTTP://", "http:", "http:/", "http:///", "http:\\\\", " \thttp://", "ht\ttp://", "//", "\\\\"}
	hosts := []string{
		// private spellings
		"127.0.0.1", "127.1", "0x7f.1", "0x7f000001", "2130706433", "017700000001", "0177.0.0.1", "127.0.0.1.", "127。0。0。1",
		"[::1]", "[::ffff:127.0.0.1]", "[::ffff:7f00:1]", "[0:0:0:0:0:0:0:1]", "[fe80::1%25eth0]", "[fe80::1%eth0]",
		"localhost", "LOCALHOST", "localhost.", "foo.localhost", "10.0.0.1", "169.254.169.254", "0xa9.0xfe.0xa9.0xfe", "2852039166", "0", "0.0.0.0",
		"1.1.1.1%0a.127.0.0.1", "127.0.0.1%09", "127%2e0%2e0%2e1", "%31%32%37.0.0.1", "127.0.0.1%00", "%6c%6f%63%61%6c%68%6f%73%74",
		// the allowed name, and what imitates it
		"allowed.com", "ALLOWED.COM", "allowed.com.", "allowed.com..", "allowed%2ecom", "allowed.com%2eevil.com", "allowed.com%00.evil.com",
		"allowed.com\\@evil.com", "allowed.com%5c@evil.com", "allowed.com%40evil.com", "allowed.com@evil.com", "evil.com@allowed.com",
		"allowed.com:80@evil.com", "allowed.com:@evil.com", "evil.com#@allowed.com", "evil.com?@allowed.com", "evil.com\\@allowed.com", "evil.com/@allowed.com",
		"evil.com%23@allowed.com", "evil.com%2f@allowed.com", "allowed.com.evil.com", "evilallowed.com", "allowed.com%0d%0aHost:evil.com",
		"ａllowed.com", "allowed。com", "allowed．com", "allowed。com", "bücher.de", "xn--bcher-kva.de", "allowed.com%e3%80%82evil.com",
		"[allowed.com]", "allowed.com]", "[allowed.com", "[::1]x", "allowed.com:80", "allowed.com:", "allowed.com:0080", "allowed.com:8080", "allowed.com:65536", "allowed.com:-1", "allowed.com:+1", "allowed.com:1e2", "allowed.com:０",
		"public.example", "93.184.216.34", "8.8.8.8", "[2606:4700::1111]", "0x5db8d822", "1572395042",
	}
	suffixes := []string{"", "/", "/x.json", "/a/../x", "/%2e%2e/x", "/a b", "/\r\nHost: evil.com", "/\\evil.com", "?q=1", "?q=a b#frag", "#frag", "/..%2f..%2fx", "/%00", "\x00", "\\x", "/é"}

	type policy struct {
		name string
		l    *HTTPLoader
		ok   func(host string) bool // whether a request to host may be made
	}
	rec := &recorder{}
	client := &http.Client{Transport: rec}
	policies := []policy{
		{"default", &HTTPLoader{Client: client}, func(h string) bool { return !privateHost(strings.Trim(strings.TrimSuffix(h, "."), "[]")) }},
		{"allowed.com", &HTTPLoader{Client: client, AllowedDomains: []string{"allowed.com"}, AllowPrivateNetworks: true}, func(h string) bool { return strings.TrimSuffix(h, ".") == "allowed.com" }},
		{"public.example", &HTTPLoader{Client: client, AllowedDomains: []string{"public.example"}}, func(h string) bool { return strings.TrimSuffix(h, ".") == "public.example" }},
	}
	requested := 0
	for _, p := range policies {
		for _, sc := range schemes {
			for _, h := range hosts {
				for _, sf := range suffixes {
					in := sc + h + sf
					// Load called directly, with the string as given: whatever
					// it requests passes the policy.
					before := len(rec.urls)
					_, loadErr := p.l.Load(context.Background(), in)
					if len(rec.urls) > before {
						requested++
						checkRequested(t, p.name, in, rec.urls[len(rec.urls)-1], p.ok)
						if loadErr != nil {
							t.Errorf("policy %s: Load(%q) made a request and failed: %v", p.name, in, loadErr)
						}
					} else if loadErr == nil {
						t.Errorf("policy %s: Load(%q) succeeded without a request", p.name, in)
					}

					// The engine's way: Sanitize, then Load of what it returned.
					s, sanErr := p.l.Sanitize(context.Background(), in)
					if sanErr != nil {
						continue
					}
					before = len(rec.urls)
					_, loadErr = p.l.Load(context.Background(), s)
					if len(rec.urls) > before {
						requested++
						checkRequested(t, p.name, in, rec.urls[len(rec.urls)-1], p.ok)
					} else if fetchable.MatchString(s) {
						// An http(s) string Load refuses is refused by Sanitize too.
						t.Errorf("policy %s: Sanitize(%q) accepted %q, which Load refuses: %v", p.name, in, s, loadErr)
					}
					if want, err := SanitizeURI(in, URIOptions{}); err != nil || s != want {
						t.Errorf("policy %s: Sanitize(%q) = %q, vega-loader's string is %q (%v)", p.name, in, s, want, err)
					}
				}
			}
		}
	}
	if requested < 1000 {
		t.Errorf("only %d requests were made: the table does not test what it should", requested)
	}
}

// checkRequested fails the test when the request that reached the transport
// is one the policy refuses.
func checkRequested(t *testing.T, policy, in string, u *url.URL, ok func(host string) bool) {
	t.Helper()
	if u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || !ok(u.Hostname()) {
		t.Errorf("policy %s: %q requested %q", policy, in, u)
	}
}
