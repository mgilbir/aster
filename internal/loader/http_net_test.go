package loader

import (
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// countingServer is a loopback server and the number of requests it has seen.
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, &n
}

// The loopback server is reachable only when AllowPrivateNetworks says so, and
// the check holds for the address a connection is made to: a name that leads to
// a private address, as DNS rebinding makes one, does not get through.
func TestHTTPLoaderDialTimeAddressPolicy(t *testing.T) {
	ts, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })
	addr := ts.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)

	// The loader's own transport: dial by name and by address.
	l := &HTTPLoader{}
	tr := l.transport(nil).(*http.Transport)
	for _, target := range []string{addr, "localhost:" + port} {
		c, err := tr.DialContext(context.Background(), "tcp", target)
		if err == nil {
			_ = c.Close()
			t.Errorf("dial %s succeeded", target)
		} else if !strings.Contains(err.Error(), "private") {
			t.Errorf("dial %s: %v", target, err)
		}
	}
	l.AllowPrivateNetworks = true
	tr = l.transport(nil).(*http.Transport)
	c, err := tr.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial with AllowPrivateNetworks: %v", err)
	}
	_ = c.Close()

	// A caller's *http.Transport with a
	// dial function of its own has the connection it makes checked.
	for name, tr := range map[string]*http.Transport{
		"dial": {DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr) // the name "resolved" to loopback
		}},
	} {
		l := &HTTPLoader{Client: &http.Client{Transport: tr}}
		before := hits.Load()
		_, err := l.Load(context.Background(), "http://rebind.example.net:"+port+"/x")
		if err == nil {
			t.Errorf("%s: loaded from a name leading to loopback", name)
		}
		if name == "dial" && (err == nil || !strings.Contains(err.Error(), "private")) {
			t.Errorf("%s: err = %v", name, err)
		}
		if hits.Load() != before {
			t.Errorf("%s: the server saw a request", name)
		}
	}

	// And opted in, the same loaders reach it.
	l = &HTTPLoader{AllowPrivateNetworks: true, Client: ts.Client()}
	if _, err := l.Load(context.Background(), ts.URL+"/x"); err != nil {
		t.Errorf("AllowPrivateNetworks: %v", err)
	}
	l = &HTTPLoader{AllowPrivateNetworks: true}
	if _, err := l.Load(context.Background(), ts.URL+"/x"); err != nil {
		t.Errorf("AllowPrivateNetworks, default client: %v", err)
	}
}

// A proxy is honoured, from the environment for the default client and from a
// caller's Transport, and the connection to it is allowed though it is on
// loopback here; a request the proxy settings send directly keeps the address
// check, and a request through the proxy still has its URL checked.
func TestHTTPLoaderHonoursProxies(t *testing.T) {
	if reflect.ValueOf(proxyFromEnvironment).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("the default client does not read the proxy environment")
	}
	var hosts []string
	var mu sync.Mutex
	proxy, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		_, _ = w.Write([]byte("[1]"))
	})
	proxyURL, _ := url.Parse(proxy.URL)
	_, port, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	// NO_PROXY=direct.example.net, as the environment would say it
	choose := func(req *http.Request) (*url.URL, error) {
		if req.URL.Hostname() == "direct.example.net" {
			return nil, nil
		}
		return proxyURL, nil
	}
	saved := proxyFromEnvironment
	proxyFromEnvironment = choose
	t.Cleanup(func() { proxyFromEnvironment = saved })

	toLoopback := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, proxy.Listener.Addr().String()) // every name "resolves" to loopback
	}
	for name, client := range map[string]*http.Client{
		"environment":     nil,
		"default client":  http.DefaultClient,
		"caller's proxy":  {Transport: &http.Transport{Proxy: choose}},
		"caller's dialer": {Transport: &http.Transport{Proxy: choose, DialContext: toLoopback}},
	} {
		t.Run(name, func(t *testing.T) {
			l := &HTTPLoader{Client: client}
			mu.Lock()
			hosts = nil
			mu.Unlock()
			got, err := l.Load(context.Background(), "http://data.example.net/x.json")
			if err != nil || string(got) != "[1]" {
				t.Fatalf("through the proxy: %q, %v", got, err)
			}
			mu.Lock()
			if len(hosts) != 1 || hosts[0] != "data.example.net" {
				t.Errorf("the proxy saw hosts %q", hosts)
			}
			mu.Unlock()

			// sent directly, a name leading to loopback is refused
			before := hits.Load()
			if name == "caller's dialer" {
				_, err = l.Load(context.Background(), "http://direct.example.net:"+port+"/x.json")
			} else {
				tr := l.transport(l.client().Transport).(*http.Transport)
				var c net.Conn
				if c, err = tr.DialContext(context.Background(), "tcp", "localhost:"+port); err == nil {
					_ = c.Close()
				}
			}
			if err == nil || !strings.Contains(err.Error(), "private") {
				t.Errorf("direct to loopback: %v", err)
			}

			// through the proxy, an address literal is still refused
			if _, err := l.Load(context.Background(), "http://127.0.0.1:"+port+"/x.json"); err == nil {
				t.Error("an address literal went through the proxy")
			}
			if hits.Load() != before {
				t.Errorf("the proxy saw %d refused requests", hits.Load()-before)
			}
		})
	}
}

// A server that sends nothing, or a body a byte at a time, cannot hold a load
// past its Timeout (nor the render's context).
func TestHTTPLoaderSlowServers(t *testing.T) {
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	silent, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stall:
		case <-r.Context().Done():
		}
	})
	drip, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		for {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	})
	for name, ts := range map[string]*httptest.Server{"headers": silent, "body": drip} {
		l := &HTTPLoader{AllowPrivateNetworks: true, Timeout: 300 * time.Millisecond}
		start := time.Now()
		_, err := l.Load(context.Background(), ts.URL+"/x")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: err = %v, want a deadline", name, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", name, d)
		}

		// The render's context ends the load too.
		l.Timeout = -1
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err = l.Load(ctx, ts.URL+"/x")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: context: err = %v", name, err)
		}
	}
}

// A compressed body is capped on its decompressed size, a declared Content-Length
// over the cap is refused without waiting for the body, and a declared length
// the server does not keep fails.
func TestHTTPLoaderBodyLimits(t *testing.T) {
	bomb, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		chunk := make([]byte, 1<<20)
		for range 256 { // 256 MiB of zeros, a few hundred KiB on the wire
			if _, err := zw.Write(chunk); err != nil {
				return
			}
		}
		_ = zw.Close()
	})
	lie, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1073741824")
		_, _ = w.Write([]byte("[1,2,"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // ... and no more
	})
	short, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("[1,2,"))
	})

	l := &HTTPLoader{AllowPrivateNetworks: true, MaxResponseBytes: 1 << 20}
	if _, err := l.Load(context.Background(), bomb.URL+"/x"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("gzip bomb: err = %v, want a limit", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := l.Load(ctx, lie.URL+"/x"); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("declared length: err = %v, want a limit", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("declared length: took %v", d)
	}
	if data, err := l.Load(context.Background(), short.URL+"/x"); err == nil {
		t.Errorf("a body shorter than its Content-Length loaded: %q", data)
	}
}

// Sanitize vets links as well as data URLs, so it must not trigger name
// resolution: a specification could make the server look up any number of
// hosts.
func TestHTTPLoaderSanitizeDoesNotResolve(t *testing.T) {
	var lookups atomic.Int32
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		lookups.Add(1)
		return nil, errors.New("no network in tests")
	}}
	t.Cleanup(func() { net.DefaultResolver = old })
	l := &HTTPLoader{}
	for _, uri := range []string{"http://a.example.net/", "https://b.example.net/x", "http://c.example.net:8080/y"} {
		if _, err := l.Sanitize(context.Background(), uri); err != nil {
			t.Errorf("Sanitize(%s): %v", uri, err)
		}
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("%d name lookups", n)
	}
}
