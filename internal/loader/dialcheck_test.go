package loader

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The tests resolve names here, not in DNS: "*.inward.test" leads to
// loopback; every other *.example.net (and example.com) name to a public
// address, which a test's dial function is free to ignore (as a caller's
// may) to go where it likes; "localhost" to loopback.
func init() {
	lookupNetIP = func(_ context.Context, network, host string) ([]netip.Addr, error) {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		var addrs []netip.Addr
		switch {
		case host == "localhost" || strings.HasSuffix(host, ".inward.test"):
			addrs = []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}
		case host == "mixed.example.net":
			addrs = []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("93.184.216.34")}
		case host == "example.com" || strings.HasSuffix(host, ".example.net"):
			addrs = []netip.Addr{netip.MustParseAddr("93.184.216.34")}
		default:
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, a := range addrs {
			if network == "ip" || network == "ip4" && a.Is4() || network == "ip6" && a.Is6() {
				out = append(out, a)
			}
		}
		return out, nil
	}
}

// A caller's dial function is not a port oracle (C18): for a name that leads
// to a private address, an open port and a closed one give the same error,
// which names neither the address nor the port's state, and the open port
// sees no connection, on every path a caller's *http.Transport dials. The
// open port listens on loopback; the closed one did.
func TestDialOfANameLeadingInwardSendsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	_, open, _ := net.SplitHostPort(ln.Addr().String())
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closed, _ := net.SplitHostPort(ln2.Addr().String())
	_ = ln2.Close()

	var d net.Dialer
	tlsDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		return (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: host}}).DialContext(ctx, network, addr)
	}
	plain := func(network, addr string) (net.Conn, error) { return d.Dial(network, addr) }
	tlsPlain := func(network, addr string) (net.Conn, error) { return tlsDial(context.Background(), network, addr) }
	for name, c := range map[string]struct {
		tr     *http.Transport
		scheme string
	}{
		"default":         {nil, "http"},
		"DialContext":     {&http.Transport{DialContext: d.DialContext}, "http"},
		"DialContext TLS": {&http.Transport{DialContext: d.DialContext}, "https"},
		"Dial":            {&http.Transport{Dial: plain}, "http"}, //nolint:staticcheck // the deprecated field is what is tested
		"DialTLSContext":  {&http.Transport{DialTLSContext: tlsDial}, "https"},
		"DialTLS":         {&http.Transport{DialTLS: tlsPlain}, "https"}, //nolint:staticcheck // as above
	} {
		t.Run(name, func(t *testing.T) {
			l := &HTTPLoader{}
			if c.tr != nil {
				l.Client = &http.Client{Transport: c.tr}
			}
			var errs []string
			for _, port := range []string{open, closed} {
				accepted.Store(0)
				var err error
				if c.tr == nil {
					// The loader's own dialer resolves names itself, in
					// DNS: dial a name every host resolves to loopback
					// (a URL naming it is refused before any dial).
					var conn net.Conn
					if conn, err = l.transport(nil).(*http.Transport).DialContext(context.Background(), "tcp", "localhost:"+port); err == nil {
						_ = conn.Close()
					}
				} else {
					_, err = l.Load(context.Background(), c.scheme+"://db.inward.test:"+port+"/")
				}
				if !errors.Is(err, ErrPrivateAddress) {
					t.Errorf("port %s: %v, want ErrPrivateAddress", port, err)
				}
				if err != nil {
					errs = append(errs, strings.ReplaceAll(err.Error(), port, "PORT"))
					for _, leak := range []string{"127.0.0.1", "::1", "refused", "connect:"} {
						if strings.Contains(err.Error(), leak) {
							t.Errorf("port %s: %v says %q", port, err, leak)
						}
					}
				}
				// The listener counts what reaches it: wait for nothing
				// to, a handful of scheduler turns, then look.
				for range 100 {
					runtime.Gosched()
				}
				if n := accepted.Load(); n != 0 {
					t.Errorf("port %s: the server accepted %d connections", port, n)
				}
			}
			if len(errs) == 2 && errs[0] != errs[1] {
				t.Errorf("open and closed ports tell apart:\n  open:   %s\n  closed: %s", errs[0], errs[1])
			}
		})
	}
}

// A dial error for a name, a public one included, says nothing of the
// network: neither where the name resolved to nor how the connection failed
// (C18). A name that does not resolve fails the same way.
func TestDialErrorsAreOpaqueForNames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closed, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	var d net.Dialer
	// The public name's address is not reachable here: send it to the closed
	// loopback port, which refuses, and say it is public.
	refusing := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network, "127.0.0.1:"+closed)
	}
	l := &HTTPLoader{Client: &http.Client{Transport: &http.Transport{DialContext: refusing}}}
	for _, uri := range []string{"http://public.example.net:" + closed + "/", "http://nowhere.invalid/"} {
		_, err := l.Load(context.Background(), uri)
		if !errors.Is(err, errDial) {
			t.Errorf("%s: %v, want errDial", uri, err)
			continue
		}
		for _, leak := range []string{"127.0.0.1", "93.184", "refused", "no such host"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: %v says %q", uri, err, leak)
			}
		}
	}
	// The end of the call is still the call's.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Load(ctx, "http://public.example.net/"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

// A caller's DialContext is given the vetted address, not the name, so it
// cannot resolve the name again to somewhere else; and of a name with public
// and private addresses, only the public ones are dialed. The request still
// names the host.
func TestCallerDialIsGivenTheVettedAddress(t *testing.T) {
	var dialed []string
	tr := &http.Transport{DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return nil, errors.New("no network in tests")
	}}
	l := &HTTPLoader{Client: &http.Client{Transport: tr}}
	_, _ = l.Load(context.Background(), "http://mixed.example.net:8080/")
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:8080" {
		t.Errorf("dialed %q, want the public address alone", dialed)
	}
}

// HTTPLoaders built around one *http.Transport share one checked transport,
// and so its connection pool (C28): 50 loaders made one per request opened 50
// connections that nothing closed, against one when the transport is used as
// it is.
func TestHTTPLoadersShareTheCheckedTransport(t *testing.T) {
	var opened atomic.Int32
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) }))
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	ts.Start()
	t.Cleanup(ts.Close)
	addr := ts.Listener.Addr().String()
	shared := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return publicConn{c}, nil // stands for a connection to the public host
	}}
	for range 50 {
		l := &HTTPLoader{Client: &http.Client{Transport: shared}}
		if _, err := l.Load(context.Background(), "http://public.example.net/"); err != nil {
			t.Fatal(err)
		}
	}
	if n := opened.Load(); n != 1 {
		t.Errorf("50 loaders over one transport opened %d connections, want 1", n)
	}
	if rt := derived.get(shared); rt.IdleConnTimeout <= 0 {
		t.Errorf("the derived transport keeps idle connections forever (IdleConnTimeout %v)", rt.IdleConnTimeout)
	}
	if a, b := (&HTTPLoader{}).transport(nil), (&HTTPLoader{}).transport(nil); a != b {
		t.Error("two loaders with the default client have transports of their own")
	}
}

// The derived transport of a base the caller dropped is dropped too: the
// cache holds no more than the caller keeps alive.
func TestDerivedTransportOfADroppedBaseIsDropped(t *testing.T) {
	before := derived.size()
	func() {
		base := &http.Transport{DialContext: (&net.Dialer{}).DialContext}
		_ = (&HTTPLoader{Client: &http.Client{Transport: base}}).transport(base)
	}()
	if derived.size() != before+1 {
		t.Fatalf("size %d, want %d", derived.size(), before+1)
	}
	// Cleanups run after a collection, on a goroutine of the runtime's:
	// poll a bounded number of collections.
	for i := 0; i < 200 && derived.size() > before; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if n := derived.size(); n > before {
		t.Errorf("%d derived transports outlive their bases", n-before)
	}
}

type publicConn struct{ net.Conn }

func (publicConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(93, 184, 216, 34), Port: 80}
}
