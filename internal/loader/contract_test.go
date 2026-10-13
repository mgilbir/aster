package loader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// Every field of a caller's *http.Transport that makes a connection gets the
// private-network check: DialContext, DialTLSContext and the deprecated Dial
// and DialTLS, each alone or together. A transport that sets only one of them
// still dials the others' connections with net/http's own dialer, which must
// be the guarded one, not the zero Dialer (C41: a Transport with only
// DialTLSContext reached loopback over plain http).
func TestHTTPLoaderChecksEveryDialPath(t *testing.T) {
	ts, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })
	addr := ts.Listener.Addr().String()
	toLoopback := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	plainToLoopback := func(network, _ string) (net.Conn, error) { return net.Dial(network, addr) }
	for name, base := range map[string]*http.Transport{
		"none":                {},
		"DialContext":         {DialContext: toLoopback},
		"DialTLSContext only": {DialTLSContext: toLoopback},
		"Dial only":           {Dial: plainToLoopback},                           //nolint:staticcheck // the deprecated field is what is tested
		"DialTLS only":        {DialTLS: plainToLoopback},                        //nolint:staticcheck // the deprecated field is what is tested
		"Dial and DialTLS":    {Dial: plainToLoopback, DialTLS: plainToLoopback}, //nolint:staticcheck // as above
		"both contexts":       {DialContext: toLoopback, DialTLSContext: toLoopback},
		"proxy func, no dial": {Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }}, // a Proxy that sends everything direct
	} {
		t.Run(name, func(t *testing.T) {
			l := &HTTPLoader{Client: &http.Client{Transport: base}}
			rt, ok := l.transport(base).(*http.Transport)
			if !ok {
				t.Fatalf("the transport is not checked: %T returned as it is", base)
			}
			if rt.Dial != nil || rt.DialTLS != nil { //nolint:staticcheck // the deprecated fields must not be left to dial unchecked
				t.Error("a deprecated dial function is left in place, unchecked")
			}
			if rt.DialContext == nil {
				t.Fatal("no DialContext: net/http dials with its zero Dialer, unchecked")
			}
			dials := map[string]func(context.Context, string, string) (net.Conn, error){"DialContext": rt.DialContext}
			if rt.DialTLSContext != nil {
				dials["DialTLSContext"] = rt.DialTLSContext
			}
			for field, dial := range dials {
				c, err := dial(context.Background(), "tcp", addr)
				if err == nil {
					_ = c.Close()
					t.Errorf("%s reached loopback", field)
					continue
				}
				if !errors.Is(err, ErrPrivateAddress) {
					t.Errorf("%s: err = %v, want ErrPrivateAddress", field, err)
				}
			}
			before := hits.Load()
			_, port, _ := net.SplitHostPort(addr)
			if _, err := l.Load(context.Background(), "http://rebind.example.net:"+port+"/x"); err == nil {
				t.Error("loaded from a name leading to loopback")
			}
			if hits.Load() != before {
				t.Error("the loopback server saw a request")
			}
		})
	}
}

// A refused connection says that the host is private, not which internal
// address its name resolved to (C88): the spec's author reads the error.
func TestHTTPLoaderBlockedDialDoesNotLeakTheAddress(t *testing.T) {
	ts, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {})
	addr := ts.Listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	loaders := map[string]*HTTPLoader{
		"default transport": {},
		"caller's dial": {Client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}}}},
	}
	for name, l := range loaders {
		// The default transport resolves names itself: dial the loopback
		// server by a name that resolves to it.
		uri := "http://rebind.example.net:" + port + "/x"
		if name == "default transport" {
			tr := l.transport(nil).(*http.Transport)
			_, err := tr.DialContext(context.Background(), "tcp", "localhost:"+port)
			if err == nil || strings.Contains(err.Error(), host) || !errors.Is(err, ErrPrivateAddress) {
				t.Errorf("%s: dial error %v: want ErrPrivateAddress, without %s", name, err, host)
			}
			continue
		}
		_, err := l.Load(context.Background(), uri)
		if err == nil || strings.Contains(err.Error(), host) || !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("%s: %v: want ErrPrivateAddress, without %s", name, err, host)
		}
		if err != nil && !strings.Contains(err.Error(), "rebind.example.net") {
			t.Errorf("%s: %v does not say which URL", name, err)
		}
	}
}

// A body over the cap names the cap that was reached: the loader's own
// (MaxResponseBytes, MaxBytes) or what is left of the render's load budget
// (C88: it named MaxResponseBytes either way).
func TestLoaderLimitNamesTheCap(t *testing.T) {
	big := func(r *http.Request) (*http.Response, error) {
		resp := respond(r, 200, strings.Repeat("x", 5000))
		resp.ContentLength = -1
		return resp, nil
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.csv"), bytes.Repeat([]byte("a"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	budgetCtx := budget.With(context.Background(), &budget.Budget{MaxLoadBytes: 1000})
	for _, tc := range []struct {
		name      string
		load      func(ctx context.Context) error
		ctx       context.Context
		want, not string
	}{
		{"HTTP own cap", func(ctx context.Context) error {
			_, err := (&HTTPLoader{MaxResponseBytes: 100, Client: &http.Client{Transport: roundTripFunc(big)}}).Load(ctx, "http://example.com/x")
			return err
		}, context.Background(), "MaxResponseBytes", "memory limit"},
		{"HTTP budget", func(ctx context.Context) error {
			_, err := (&HTTPLoader{Client: &http.Client{Transport: roundTripFunc(big)}}).Load(ctx, "http://example.com/x")
			return err
		}, budgetCtx, "load budget", "MaxResponseBytes"},
		{"HTTP declared, budget", func(ctx context.Context) error {
			_, err := (&HTTPLoader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp := respond(r, 200, "")
				resp.ContentLength = 5000
				resp.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 5000)))
				return resp, nil
			})}}).Load(ctx, "http://example.com/x")
			return err
		}, budgetCtx, "load budget", "MaxResponseBytes"},
		{"file own cap", func(ctx context.Context) error {
			_, err := (&FileLoader{BaseDir: dir, MaxBytes: 100}).Load(ctx, "big.csv")
			return err
		}, context.Background(), "FileLoader.MaxBytes", "memory limit"},
		{"file budget", func(ctx context.Context) error {
			_, err := (&FileLoader{BaseDir: dir}).Load(ctx, "big.csv")
			return err
		}, budgetCtx, "load budget", "FileLoader.MaxBytes"},
	} {
		err := tc.load(tc.ctx)
		if !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: err = %v, want ErrLimit", tc.name, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), tc.not) {
			t.Errorf("%s: %v: want it to name %q, not %q", tc.name, err, tc.want, tc.not)
		}
	}
}

// FallbackLoader.Sanitize returns what the child that accepts the URI made of
// it, so a link or an image URL is written as that child resolved it (C42: an
// HTTPLoader's BaseURL was dropped), and Load of that answer reaches the same
// child.
func TestFallbackLoaderKeepsTheChildsRewrite(t *testing.T) {
	var got string
	h := &HTTPLoader{BaseURL: "https://d.example/", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return respond(r, 200, "[]"), nil
	})}}
	f := NewFallbackLoader(DenyLoader{}, h)
	s, err := f.Sanitize(context.Background(), "page.html")
	if err != nil {
		t.Fatal(err)
	}
	if s != "https://d.example/page.html" {
		t.Errorf("Sanitize = %q, want the child's https://d.example/page.html", s)
	}
	if _, err := f.Load(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got != "https://d.example/page.html" {
		t.Errorf("Load fetched %q", got)
	}
}

// The engine calls Sanitize once and hands Load what it returned; a
// FallbackLoader routes that answer through its children's Sanitize again,
// which is only sound because every loader of this package is idempotent:
// Sanitize of what Sanitize returned is itself.
func TestSanitizeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	loaders := map[string]Loader{
		"http":          &HTTPLoader{},
		"http base":     &HTTPLoader{BaseURL: "https://d.example/a/b/"},
		"http domains":  &HTTPLoader{BaseURL: "https://d.example/", AllowedDomains: []string{"d.example", "e.example"}},
		"file":          &FileLoader{BaseDir: dir},
		"static":        &StaticLoader{},
		"fallback":      NewFallbackLoader(&FileLoader{BaseDir: dir}, &HTTPLoader{BaseURL: "https://d.example/"}),
		"fallback http": NewFallbackLoader(&HTTPLoader{AllowedDomains: []string{"e.example"}}, &HTTPLoader{BaseURL: "https://d.example/x/"}),
	}
	uris := []string{
		"data/cars.json", "./data/../data/cars.json", "a//b", "page.html?q=1#f", "../up.json", "/abs.json",
		"https://d.example/x.json", "HTTP://D.EXAMPLE/Y", "https://e.example/./a/../b", "//d.example/proto-relative",
		"https://d.example/%2e%2e/x", "https://d.example/a b", "https://d.example/é", "22:48.json", "?q", "#frag", "",
	}
	for name, l := range loaders {
		for _, u := range uris {
			s, err := l.Sanitize(context.Background(), u)
			if err != nil {
				continue
			}
			s2, err := l.Sanitize(context.Background(), s)
			if err != nil || s2 != s {
				t.Errorf("%s: Sanitize(%q) = %q, but Sanitize(%q) = %q, %v", name, u, s, s, s2, err)
			}
		}
	}
}
