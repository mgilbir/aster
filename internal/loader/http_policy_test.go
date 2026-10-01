package loader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// roundTripFunc is a transport that never touches the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(r *http.Request, status int, body string, hdr ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(hdr); i += 2 {
		h.Set(hdr[i], hdr[i+1])
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: h,
		Body: io.NopCloser(strings.NewReader(body)), Request: r,
		ContentLength: int64(len(body)),
	}
}

// Every spelling of an address that is not on the public internet is refused by
// default, by Sanitize (which vets links too) and by Load before any connection.
func TestHTTPLoaderDeniesPrivateAddresses(t *testing.T) {
	var dialed atomic.Int32
	l := &HTTPLoader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		dialed.Add(1)
		return respond(r, 200, "[]"), nil
	})}}
	for _, host := range []string{
		"127.0.0.1", "127.1", "127.0.0.1.", "2130706433", "0x7f.1", "0x7f000001", "017700000001", "0177.0.0.1",
		"0.0.0.0", "0", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "100.64.0.1", "198.18.0.1",
		"169.254.169.254", "2852039166", "224.0.0.1", "255.255.255.255",
		"[::1]", "[::]", "[::ffff:127.0.0.1]", "[::ffff:7f00:1]", "[::ffff:a9fe:a9fe]", "[::127.0.0.1]",
		"[64:ff9b::7f00:1]", "[64:ff9b::a9fe:a9fe]", "[64:ff9b:1::1]", "[2002:7f00:1::]", "[2002:a9fe:a9fe::1]",
		"[fc00::1]", "[fd12:3456::1]", "[fe80::1]", "[fe80::1%25eth0]", "[fec0::1]", "[ff02::1]", "[2001::1]",
		"localhost", "localhost.", "LOCALHOST", "foo.localhost",
	} {
		uri := "http://" + host + ":8080/data.json"
		if _, err := l.Sanitize(context.Background(), uri); err == nil {
			t.Errorf("Sanitize(%s) accepted", uri)
		}
		if _, err := l.Load(context.Background(), uri); err == nil {
			t.Errorf("Load(%s) succeeded", uri)
		}
	}
	if n := dialed.Load(); n != 0 {
		t.Errorf("%d requests were made", n)
	}

	// Public addresses, in the same spellings, pass.
	for _, host := range []string{
		"93.184.216.34", "8.8.8.8", "[2606:4700::1111]", "[::ffff:808:808]", "[64:ff9b::808:808]", "[2002:808:808::]",
		"example.com", "example.com.", "172.32.0.1", "11.0.0.1", "169.255.0.1", "100.128.0.1",
	} {
		if _, err := l.Sanitize(context.Background(), "http://"+host+"/data.json"); err != nil {
			t.Errorf("Sanitize(%s): %v", host, err)
		}
	}
}

// A host that a name check would take for the allowed one, or for no IP at all,
// but that leads elsewhere.
func TestHTTPLoaderAllowlistTricks(t *testing.T) {
	l := &HTTPLoader{AllowedDomains: []string{"allowed.com", "xn--bcher-kva.de", "kali.org", "Dotted.org."}}
	for _, uri := range []string{
		"http://allowed.com@evil.com/x",
		"http://allowed.com:80@evil.com/x",
		"http://user:pw@allowed.com/x",
		"http://evil.com#@allowed.com/",
		"http://evil.com/?allowed.com",
		"http://evil.com/allowed.com",
		"http://allowed.com.evil.com/",
		"http://evilallowed.com/",
		"http://allowed.com\\@evil.com/",
		"http://allowed.com%2eevil.com/",
		"http://allowed.com%00.evil.com/",
		"http://allowed.com\x00.evil.com/",
		"http://allowed.com/\r\nHost: evil.com",
		"http:///allowed.com/x",
		"http:allowed.com/x",
		"http:\\\\allowed.com/x",
		"//allowed.com/x",
		"http://bücher.de/",     // not the punycode form
		"http://ａllowed.com/",   // fullwidth a
		"http://allowed。com/",   // ideographic full stop
		"http://Kali.org/",      // Kelvin sign, which case folding takes for k
		"http://allowed.com../", // two trailing dots
	} {
		if got, err := l.Sanitize(context.Background(), uri); err == nil {
			t.Errorf("Sanitize(%q) accepted as %q", uri, got)
		}
		if _, err := l.Load(context.Background(), uri); err == nil {
			t.Errorf("Load(%q) succeeded", uri)
		}
	}
	for _, uri := range []string{
		"http://allowed.com/x",
		"HTTP://ALLOWED.COM/x",
		"http://allowed.com./x", // the fully qualified spelling
		"https://allowed.com:8443/x",
		"http://xn--bcher-kva.de/",
		"http://dotted.org/",
	} {
		if _, err := l.Sanitize(context.Background(), uri); err != nil {
			t.Errorf("Sanitize(%q): %v", uri, err)
		}
	}
}

// Redirects are judged hop by hop, whatever the scheme, host or address, and
// stop after ten.
func TestHTTPLoaderRedirects(t *testing.T) {
	for _, tc := range []struct {
		location string
		want     string
	}{
		{"http://169.254.169.254/latest/meta-data/", "private"},
		{"http://[::ffff:127.0.0.1]/", "private"},
		{"http://2130706433/", "private"},
		{"http://localhost/", "private"},
		{"file:///etc/passwd", "unsupported scheme"},
		{"ftp://example.com/x", "unsupported scheme"},
		{"data:text/plain,hello", "unsupported scheme"},
		{"gopher://example.com/", "unsupported scheme"},
		{"http://user:pw@example.com/", "userinfo"},
		{"http://other.example.net/", "not in allowed list"},
	} {
		var hops []string
		l := &HTTPLoader{
			AllowedDomains: []string{"start.example.com", "169.254.169.254", "localhost", "127.0.0.1", "2130706433", "::ffff:127.0.0.1", "example.com", "[::ffff:127.0.0.1]"},
			Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				hops = append(hops, r.URL.String())
				if len(hops) == 1 {
					return respond(r, http.StatusFound, "", "Location", tc.location), nil
				}
				return respond(r, 200, "secret"), nil
			})},
		}
		_, err := l.Load(context.Background(), "http://start.example.com/x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("redirect to %s: err = %v, want %q", tc.location, err, tc.want)
		}
		if len(hops) != 1 {
			t.Errorf("redirect to %s was followed: %v", tc.location, hops)
		}
	}

	var n int
	l := &HTTPLoader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n++
		return respond(r, http.StatusFound, "", "Location", fmt.Sprintf("http://example.com/%d", n)), nil
	})}}
	if _, err := l.Load(context.Background(), "http://example.com/start"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("redirect chain: err = %v", err)
	}
	if n != maxRedirects {
		t.Errorf("%d requests in a redirect loop, want %d", n, maxRedirects)
	}
}

// A body is capped after decoding, and a declared size over the cap is refused
// before reading.
func TestHTTPLoaderCapLimitErrors(t *testing.T) {
	l := &HTTPLoader{MaxResponseBytes: 10, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return respond(r, 200, strings.Repeat("x", 11)), nil
	})}}
	if _, err := l.Load(context.Background(), "http://example.com/x"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("err = %v, want one wrapping ErrLimit", err)
	}
	l.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := respond(r, 200, "[]")
		resp.ContentLength = 1 << 40
		resp.Body = io.NopCloser(blockingReader{r.Context()})
		return resp, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := l.Load(ctx, "http://example.com/x"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("declared size: err = %v, want one wrapping ErrLimit", err)
	}
}

// blockingReader never returns data.
type blockingReader struct{ ctx context.Context }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
