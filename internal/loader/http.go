package loader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"weak"

	"github.com/mgilbir/aster/internal/budget"
)

// HTTPLoader allows loading resources over HTTP and HTTPS.
//
// AllowedDomains restricts which hostnames may be accessed. If empty, all
// domains are permitted. Names are compared case-insensitively, ignoring one
// trailing dot, and only ASCII names are accepted: write an internationalized
// name in its punycode form. BaseURL is put before a relative URI as
// vega-loader's baseURL is; without an absolute one only absolute HTTP(S) URLs
// can be fetched.
//
// Sanitize returns the string vega-loader's sanitize makes of a URI (see
// SanitizeURI), the link or image URL Vega writes and then loads. Load
// requests the URL that fetch makes of that string, the URL Standard's parse,
// and the policy below is checked on that URL, so that what is checked is what
// is requested.
//
// The same scheme/userinfo/AllowedDomains policy is enforced on every HTTP
// redirect hop, so an allowed host cannot redirect the request to a
// disallowed one. At most 10 redirects are followed.
//
// Addresses that are not on the public internet are denied unless
// AllowPrivateNetworks is set: loopback, link-local (including cloud metadata
// endpoints such as 169.254.169.254), private, shared, multicast and
// unspecified ranges, in every spelling (decimal, octal or hex IPv4, IPv4
// mapped, NAT64 and 6to4 IPv6). A specification names the URLs, so without
// this a hostile one could make the server fetch from its own network. The
// check is made on the addresses a name resolves to, before anything is sent
// to them, and on the address a connection is actually made to, so DNS
// rebinding and redirects cannot get around it. It is enforced on the
// connections of the default client and of any Client whose Transport is an
// *http.Transport, on every path it dials:
//
//   - the default dialer, which also makes the plain connections of a
//     Transport that sets no plain dial function, checks each address before
//     its socket connects;
//   - a caller's DialContext (or the deprecated Dial) is given the vetted
//     public addresses of the name, as literals, one at a time, never the
//     name: the request's Host and TLS server name are still the name;
//   - a caller's DialTLSContext (or DialTLS), which takes the server name from
//     the address, is given the name once the loader's own resolution found
//     a public address for it; the connection it makes is checked again, so a
//     name that rebinds between the two lookups is refused, after its
//     connection and ClientHello.
//
// A refused connection's error wraps ErrPrivateAddress, and any other failed
// dial to a name is the same opaque error: neither says what address a name
// resolved to, nor whether a port answered. Any other http.RoundTripper
// decides for itself where it connects, as the caller wrote it to: for those
// only address literals and localhost names are refused, and AllowedDomains
// is the tool to confine them.
//
// The checked transport made from a caller's *http.Transport is shared by
// every HTTPLoader built around that Transport, so they share its connection
// pool, and is dropped once the Transport is garbage; it closes a connection
// left idle for 90 s when the Transport sets no IdleConnTimeout.
//
// A proxy is honoured: the HTTP_PROXY, HTTPS_PROXY and NO_PROXY environment
// variables for the default client (as http.DefaultTransport does), and the
// Proxy of a caller's Transport. The connection to the proxy is allowed
// whatever its address, since whoever runs the process chose it; but the proxy
// resolves the target's name itself, so for a request it carries only the
// checks on the URL apply (address literals, localhost names,
// AllowedDomains), not the check on the address finally reached. A request the
// proxy settings send directly (NO_PROXY) keeps the full check.
//
// Client nil (and http.DefaultClient) selects a client of this package's own,
// which bounds connecting, TLS and the wait for response headers.
//
// MaxResponseBytes caps how much of a response body is read, after any
// content decoding. Zero means the default cap (64 MiB); a negative value
// disables the cap entirely. Responses larger than the cap fail with an error
// instead of being truncated, so a hostile or misbehaving server cannot
// stream unbounded data into memory. Timeout bounds one request, redirects
// and the body included; the render's own context bounds it as well.
type HTTPLoader struct {
	Client           *http.Client
	AllowedDomains   []string      // if non-empty, only these hostnames are permitted
	BaseURL          string        // if set, put before relative URIs (vega-loader's baseURL); absolute to fetch them
	MaxResponseBytes int64         // response body cap; 0 = 64 MiB default, negative = unlimited
	Timeout          time.Duration // per-request limit; 0 = 60 s default, negative = none

	// AllowPrivateNetworks permits loopback, link-local, private and other
	// non-public addresses. Set it only for trusted specifications or for a
	// server that is meant to be reached (a local data service, a test).
	AllowPrivateNetworks bool

	// BlockPrivateNetworks has no effect: private networks are blocked unless
	// AllowPrivateNetworks is set.
	//
	// Deprecated: use AllowPrivateNetworks to opt out.
	BlockPrivateNetworks bool
}

// proxyFromEnvironment selects the proxy of the default client. A variable so
// that a test can stand in for the environment, which net/http reads once per
// process.
var proxyFromEnvironment = http.ProxyFromEnvironment

const (
	// defaultMaxResponseBytes bounds response bodies when MaxResponseBytes is 0.
	defaultMaxResponseBytes = 64 << 20
	// defaultHTTPTimeout bounds one request when Timeout is 0.
	defaultHTTPTimeout = 60 * time.Second
	maxRedirects       = 10
)

// loadCap is how many bytes one load may return: the configured cap (0 is the
// default, negative none) lowered to what is left of the render's load budget,
// so that a loader never reads more than the render may keep. Negative means
// no cap. capName says which of the two it is, for the error of a body over
// it: field, the loader's own setting, or the render's budget.
func loadCap(ctx context.Context, configured int64, field string) (max int64, capName string) {
	if configured == 0 {
		configured = defaultMaxResponseBytes
	}
	if left := budget.From(ctx).LoadLeft(); left != math.MaxInt64 && (configured < 0 || left < configured) {
		return left, "what is left of the render's load budget (WithMemoryLimit raises it)"
	}
	return configured, field
}

// streamChunk is the most one read takes before it is charged to a shared
// allowance (budget.Stream): what a load reading past the call's allowance
// can hold beyond it.
const streamChunk = 32 << 10

// readBody reads r whole. When the load's budget shares the call's allowance
// with loads running at once (the images PNG and PDF output fetch in
// parallel), every chunk read is charged to it as it is read, so that the
// loads together stop where the call's allowance ends rather than each read
// up to what was left when it started (C19).
func readBody(ctx context.Context, r io.Reader) ([]byte, error) {
	if b := budget.From(ctx); b.Streams() {
		r = &streamReader{r: r, b: b}
	}
	return io.ReadAll(r)
}

type streamReader struct {
	r io.Reader
	b *budget.Budget
}

func (s *streamReader) Read(p []byte) (int, error) {
	if len(p) > streamChunk {
		p = p[:streamChunk]
	}
	n, err := s.r.Read(p)
	if n > 0 {
		if e := s.b.Stream(int64(n)); e != nil {
			return 0, e
		}
	}
	return n, err
}

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
// If client is nil, the client described on HTTPLoader is used.
func NewHTTPLoader(client *http.Client) *HTTPLoader {
	return &HTTPLoader{Client: client}
}

func (l *HTTPLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	if l == nil {
		return nil, errNilLoader("*HTTPLoader")
	}
	timeout := l.Timeout
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// The URL requested is the one fetch would request for this string (the
	// URL Standard's parse), and the policy is checked on that URL, so that
	// what is checked is what is fetched. Load is safe when called directly,
	// without Sanitize in front of it.
	target, canonical, err := l.target(uri)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, nil)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to create request for %q: %w", uri, err)
	}
	req.URL, req.Host = target, target.Host

	// The client is a shallow copy, so the policy-enforcing CheckRedirect
	// below does not mutate a caller-supplied client. Every redirect target is
	// run through the same scheme/domain/network policy as the initial URL,
	// then through the caller's own CheckRedirect (if any), so a stricter
	// caller-supplied redirect policy still applies.
	client := l.client()
	callerCheck := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("aster: stopped after %d redirects", maxRedirects)
		}
		if err := l.checkURL(req.URL); err != nil {
			return err
		}
		if callerCheck != nil {
			return callerCheck(req, via)
		}
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		// The *url.Error repeats the URL, with the credentials of a redirect
		// target the server sent; uri says which load failed.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("aster: failed to load %q: %w", uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aster: HTTP %d loading %q", resp.StatusCode, uri)
	}

	max, capName := loadCap(ctx, l.MaxResponseBytes, "HTTPLoader.MaxResponseBytes")
	if max < 0 {
		data, err := readBody(ctx, resp.Body)
		if err != nil {
			return nil, fmt.Errorf("aster: failed to read response from %q: %w", uri, err)
		}
		return data, nil
	}

	// The declared size can be a lie, but when it is over the cap there is
	// nothing to read.
	if resp.ContentLength > max {
		return nil, fmt.Errorf("aster: response from %q is %d bytes, exceeds the %d bytes of %s: %w", uri, resp.ContentLength, max, capName, budget.ErrLimit)
	}
	// Read one byte past the cap to distinguish "exactly at the cap" from
	// "exceeds it".
	data, err := readBody(ctx, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("aster: failed to read response from %q: %w", uri, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("aster: response from %q exceeds the %d bytes of %s: %w", uri, max, capName, budget.ErrLimit)
	}
	return data, nil
}

// Sanitize returns the string vega-loader's sanitize makes of uri with the
// loader's BaseURL (SanitizeURI): the link or image URL Vega writes, and the
// string it then hands to Load. A URI the allow-list refuses is an error.
//
// A result that fetch would request, an absolute http or https URL, is also
// held to the access policy (no userinfo, AllowedDomains, no private address),
// on the URL Load would request, and refused when it fails it or cannot be
// parsed as one. Any other result, a relative reference (BaseURL empty or
// itself relative), mailto:, tel: and the like, is returned as it is: it is a
// valid link, and Load refuses it, since it cannot be fetched.
//
// Sanitize does not resolve names: it also vets the links of a chart, so a
// specification could otherwise make the server look up any number of hosts.
// Names are checked where a connection is made (see Load).
func (l *HTTPLoader) Sanitize(_ context.Context, uri string) (string, error) {
	if l == nil {
		return "", errNilLoader("*HTTPLoader")
	}
	href, err := SanitizeURI(uri, URIOptions{BaseURL: l.BaseURL})
	if err != nil {
		return "", err
	}
	if _, _, err := l.target(href); err != nil && !errors.Is(err, errNotRequest) {
		return "", err
	}
	return href, nil
}

// target is the URL fetch would request for s and the string it is requested
// by (its serialization), or an error when s is not an absolute http or https
// URL (errNotRequest), cannot be parsed as one, or fails the access policy.
// The policy is checked on the URL net/http will use, parsed from the
// serialization; its scheme and host must be what the URL Standard's parse
// found, so that no parser disagreement can make one name checked and
// another connected to.
func (l *HTTPLoader) target(s string) (*url.URL, string, error) {
	ru, err := parseRequestURL(s)
	if err != nil {
		if errors.Is(err, errNotRequest) {
			return nil, "", fmt.Errorf("%w: it cannot be fetched (is BaseURL set, and absolute?)", err)
		}
		return nil, "", err
	}
	canonical := ru.String()
	u, err := url.Parse(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("aster: not a URL net/http can request: %w", errBadURL)
	}
	if u.Scheme != ru.scheme || u.Host != ru.hostport() || u.User != nil || u.Opaque != "" {
		return nil, "", fmt.Errorf("aster: not a URL net/http can request: %w", errBadURL)
	}
	if err := l.checkURL(u); err != nil {
		return nil, "", err
	}
	return u, canonical, nil
}

// checkURL enforces the HTTPLoader access policy on a fully-resolved URL, as
// far as it can be told without a connection: no userinfo, http/https only, an
// ASCII host in AllowedDomains (when set), and unless AllowPrivateNetworks is
// set a host that is not a private address literal or a localhost name. It is
// applied to the initial request URL and to every redirect target.
func (l *HTTPLoader) checkURL(u *url.URL) error {
	if u.User != nil {
		return fmt.Errorf("aster: URI %q contains userinfo (not allowed)", u.Redacted())
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("aster: unsupported scheme %q in URI %q (only http/https allowed)", scheme, u.Redacted())
	}

	host := hostName(u)
	if host == "" {
		return fmt.Errorf("aster: URI %q has no host", u.Redacted())
	}
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return fmt.Errorf("aster: host %q is not ASCII (use the punycode form)", host)
		}
	}
	if len(l.AllowedDomains) > 0 {
		allowed := false
		for _, d := range l.AllowedDomains {
			if strings.EqualFold(host, strings.TrimSuffix(d, ".")) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("aster: domain %q not in allowed list for URI %q", host, u.Redacted())
		}
	}

	if !l.AllowPrivateNetworks && privateHost(host) {
		return fmt.Errorf("aster: host %q is a private/loopback address (blocked)", host)
	}

	return nil
}

// hostName is the host of u without brackets, port or one trailing dot.
func hostName(u *url.URL) string {
	return strings.TrimSuffix(u.Hostname(), ".")
}

// client returns the client to make one request with: a copy, so that it is
// safe to set CheckRedirect on, with the transport that enforces the policy.
func (l *HTTPLoader) client() *http.Client {
	var c http.Client
	if l.Client != nil {
		c = *l.Client
	}
	c.Transport = l.transport(c.Transport)
	return &c
}

// transport returns the transport to use in place of base: the loader's own
// for nil and http.DefaultTransport, a clone of an *http.Transport with its
// dialing checked, or base itself when it is not one this package can check.
func (l *HTTPLoader) transport(base http.RoundTripper) http.RoundTripper {
	t, _ := base.(*http.Transport)
	switch {
	case base == nil || base == http.DefaultTransport:
		return derived.defaultTransport(l.AllowPrivateNetworks)
	case t == nil:
		return base // another RoundTripper: the caller's own egress
	case l.AllowPrivateNetworks:
		return base // nothing to check
	}

	return derived.get(t)
}

// derived holds the checked transports, one per base transport, shared by
// every HTTPLoader (C28): a loader made per request around one shared
// *http.Transport pools its connections in one derived transport, as the
// caller meant the shared one to, rather than in a clone of its own that
// nothing closes. The checked transport depends on the base alone (a loader
// that allows private networks uses the base as it is). An entry is keyed by
// a weak pointer to its base, and dropped, its idle connections closed, once
// the base is garbage: the cache holds no more transports than the caller
// keeps alive, and nothing of the base (the derived transport is a copy).
var derived = derivedTransports{byBase: map[weak.Pointer[http.Transport]]*http.Transport{}}

type derivedTransports struct {
	mu sync.Mutex
	// def are the loader's own, for nil and http.DefaultTransport: checked,
	// and for AllowPrivateNetworks not.
	def, open *http.Transport
	byBase    map[weak.Pointer[http.Transport]]*http.Transport
}

// defaultTransport is the loader's own transport, checked unless allow.
func (d *derivedTransports) defaultTransport(allow bool) *http.Transport {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := &d.def
	if allow {
		p = &d.open
	}
	if *p == nil {
		*p = checkedTransport(nil, allow)
	}
	return *p
}

// get is the checked transport for base t.
func (d *derivedTransports) get(t *http.Transport) *http.Transport {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := weak.Make(t)
	if rt := d.byBase[key]; rt != nil {
		return rt
	}
	rt := checkedTransport(t, false)
	d.byBase[key] = rt
	runtime.AddCleanup(t, d.drop, key)
	return rt
}

// drop forgets the transport derived from a base that is garbage.
func (d *derivedTransports) drop(key weak.Pointer[http.Transport]) {
	d.mu.Lock()
	rt := d.byBase[key]
	delete(d.byBase, key)
	d.mu.Unlock()
	if rt != nil {
		rt.CloseIdleConnections()
	}
}

// size is how many base transports have a derived one, for the tests.
func (d *derivedTransports) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.byBase)
}

// idleConnTimeout bounds how long a derived transport keeps an idle
// connection when its base sets no bound (a zero IdleConnTimeout, as an
// &http.Transport{...} literal has): http.DefaultTransport's.
const idleConnTimeout = 90 * time.Second

// checkedTransport is base (nil: the loader's own default) with every dial
// checked against private networks; allow (for the default alone) checks
// nothing.
func checkedTransport(t *http.Transport, allow bool) *http.Transport {
	var rt *http.Transport
	if t == nil {
		rt = &http.Transport{
			ForceAttemptHTTP2:      true,
			MaxIdleConns:           16,
			MaxIdleConnsPerHost:    4,
			IdleConnTimeout:        30 * time.Second,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  30 * time.Second,
			ExpectContinueTimeout:  time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			// Read at each request, so that a test standing in for the
			// environment reaches the shared transport.
			Proxy: func(r *http.Request) (*url.URL, error) { return proxyFromEnvironment(r) },
		}
	} else {
		rt = t.Clone()
		if rt.IdleConnTimeout <= 0 {
			rt.IdleConnTimeout = idleConnTimeout
		}
	}
	px := &proxies{}
	if rt.Proxy != nil {
		rt.Proxy = px.record(rt.Proxy)
	}
	// Every field that makes a connection is checked (C41). net/http dials a
	// plain connection with DialContext, else the deprecated Dial, else its
	// own zero Dialer, and a TLS one with DialTLSContext, else DialTLS, else
	// the plain dial and a handshake. So the caller's dial functions are
	// wrapped (the deprecated ones moved to their context forms, which take
	// precedence) and a missing plain dial is the guarded dialer, never the
	// zero Dialer.
	dial, dialTLS := dialFuncs(t)
	rt.Dial, rt.DialTLS = nil, nil //nolint:staticcheck // replaced by their checked context forms
	if dial != nil {
		// A plain connection: the transport makes the TLS handshake over
		// it, with the request's host as the server name, so the caller's
		// function can be given the vetted address itself.
		rt.DialContext = checkedDial(dial, px, true)
	} else {
		direct := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		guarded := *direct
		if !allow {
			guarded.Control = denyPrivate
		}
		rt.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if px.has(addr) {
				return direct.DialContext(ctx, network, addr)
			}
			// The guarded dialer resolves the name and checks each address
			// before its socket connects (denyPrivate): no SYN reaches a
			// private one.
			c, err := guarded.DialContext(ctx, network, addr)
			if err != nil && !allow {
				return nil, dialError(ctx, addr, err)
			}
			return c, err // allowed private networks: the error as it is
		}
		if t == nil || t.DialContext == nil && t.DialTLSContext == nil && t.Dial == nil && t.DialTLS == nil { //nolint:staticcheck // as above
			// Setting DialContext turns off net/http's automatic HTTP/2;
			// the transport had none, so it keeps it.
			rt.ForceAttemptHTTP2 = true
		}
	}
	// A TLS dial function makes the handshake itself, with the server name
	// it reads from the address it is given: it is given the name, checked
	// before (on the loader's own resolution) and after (on the address it
	// reached).
	rt.DialTLSContext = checkedDial(dialTLS, px, false)
	return rt
}

// dialFuncs are the functions t dials plain and TLS connections with, in
// their context forms: DialContext, else the deprecated Dial; DialTLSContext,
// else DialTLS. Nil for a nil t, and for one that leaves dialing to net/http.
func dialFuncs(t *http.Transport) (dial, dialTLS func(context.Context, string, string) (net.Conn, error)) {
	if t == nil {
		return nil, nil
	}
	dial, dialTLS = t.DialContext, t.DialTLSContext
	if f := t.Dial; dial == nil && f != nil { //nolint:staticcheck // a deprecated dial is still a dial
		dial = func(_ context.Context, network, addr string) (net.Conn, error) { return f(network, addr) }
	}
	if f := t.DialTLS; dialTLS == nil && f != nil { //nolint:staticcheck // as above
		dialTLS = func(_ context.Context, network, addr string) (net.Conn, error) { return f(network, addr) }
	}
	return dial, dialTLS
}

// proxies are the addresses of the proxies a transport's Proxy function has
// chosen, as the transport dials them: the dial check lets those through.
type proxies struct{ addrs sync.Map }

// record wraps a Proxy function to remember the address of every proxy it
// returns.
func (p *proxies) record(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if u != nil {
			p.addrs.Store(proxyAddr(u), true)
		}
		return u, err
	}
}

func (p *proxies) has(addr string) bool {
	_, ok := p.addrs.Load(addr)
	return ok
}

// proxyAddr is the host:port net/http dials for a proxy URL, the port
// defaulting by scheme.
func proxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// lookupNetIP resolves a host name for the check of a caller's dial
// function: a variable so that a test can stand in for DNS.
var lookupNetIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// checkedDial wraps a caller's dial function so that it reaches no private
// address, and sends nothing to one (C18: it was checked only on the
// connection it had made, so an open port of the internal network saw a
// connection, and a closed one's error named the address and the port's
// state). Unless addr is one of the transport's proxies, the name is
// resolved here and refused when it has no public address, before the
// function is called; then:
//
//   - byAddress: the function is given each public address in turn, as a
//     literal, so it connects where the check looked (no second resolution
//     to rebind);
//   - otherwise (a TLS dial function, which needs the name for the server
//     name it sends), it is given the name, and may resolve it again: a
//     rebinding between the two lookups can reach a private address, which
//     the check of the connection made then refuses, after its SYN and
//     ClientHello.
//
// Either way the connection made is checked too (a function may go where it
// likes), and an error for a name says neither where it resolved to nor how
// the connection failed (dialError). A nil dial stays nil.
func checkedDial(dial func(context.Context, string, string) (net.Conn, error), px *proxies, byAddress bool) func(context.Context, string, string) (net.Conn, error) {
	if dial == nil {
		return nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if px.has(addr) {
			return dial(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, dialError(ctx, addr, err)
		}
		targets, err := publicAddrs(ctx, network, host)
		if err != nil {
			return nil, err
		}
		if !byAddress {
			targets = []string{addr}
		} else {
			for i, a := range targets {
				targets[i] = net.JoinHostPort(a, port)
			}
		}
		var lastErr error
		for _, target := range targets {
			c, err := dial(ctx, network, target)
			if err != nil {
				lastErr = err
				continue
			}
			if tcp, ok := c.RemoteAddr().(*net.TCPAddr); ok && privateAddr(tcp.AddrPort().Addr()) {
				_ = c.Close()
				return nil, errPrivate
			}
			return c, nil
		}
		return nil, dialError(ctx, addr, lastErr)
	}
}

// publicAddrs are the public addresses host resolves to, as literals, or
// errPrivate when it has none but private ones; host itself when it is a
// literal, public.
func publicAddrs(ctx context.Context, network, host string) ([]string, error) {
	if a, ok := hostAddr(host); ok {
		if privateAddr(a) {
			return nil, errPrivate
		}
		return []string{host}, nil
	}
	ipNet := "ip"
	switch network {
	case "tcp4":
		ipNet = "ip4"
	case "tcp6":
		ipNet = "ip6"
	}
	addrs, err := lookupNetIP(ctx, ipNet, host)
	if err != nil {
		return nil, dialError(ctx, host, err)
	}
	var public []string
	for _, a := range addrs {
		if !privateAddr(a) {
			public = append(public, a.Unmap().String())
		}
	}
	if len(public) == 0 {
		if len(addrs) > 0 {
			return nil, errPrivate
		}
		return nil, dialError(ctx, host, errors.New("no address"))
	}
	return public, nil
}

// errDial is the error of a connection to a name that could not be made:
// the name did not resolve, or no address it resolved to answered. It says
// no more, for the error reaches whoever wrote the URL, and the resolution
// and the state of the ports of the network the loader runs in are not
// theirs to know (C18).
var errDial = errors.New("aster: could not connect to the host")

// dialError is err for a dial to addr (host:port, or a host), opaque unless
// the host is an address literal, which the URL said already: a refused
// address is errPrivate, the end of the call's context its error, and any
// other failure errDial.
func dialError(ctx context.Context, addr string, err error) error {
	if errors.Is(err, ErrPrivateAddress) {
		return errPrivate
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("%w: %w", errDial, cerr)
	}
	host := addr
	if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
		host = h
	}
	if _, ok := hostAddr(strings.Trim(host, "[]")); ok {
		return err
	}
	return errDial
}

// denyPrivate is a net.Dialer Control function: it sees the address a socket
// is about to connect to, after name resolution, and refuses a private one.
func denyPrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if privateAddr(ap.Addr()) {
		return ErrPrivateAddress
	}
	return nil
}

// privateHost reports whether host (without brackets or a trailing dot) is a
// non-public address literal in any spelling, or a name that is always local.
func privateHost(host string) bool {
	if a, ok := hostAddr(host); ok {
		return privateAddr(a)
	}
	host = strings.ToLower(host)
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// hostAddr parses an address literal: IPv6, dotted IPv4, or the legacy forms
// inet_aton and a system resolver accept: one to four numbers, in decimal,
// octal (leading 0) or hex (0x), the last one filling the remaining bytes
// (2130706433, 0x7f.1, 017700000001, 127.1).
func hostAddr(host string) (netip.Addr, bool) {
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i] // a zone
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a, true
	}
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	var v uint64
	for i, p := range parts {
		base := 10
		switch {
		case len(p) > 2 && (p[:2] == "0x" || p[:2] == "0X"):
			p, base = p[2:], 16
		case len(p) > 1 && p[0] == '0':
			p, base = p[1:], 8
		}
		n, err := strconv.ParseUint(p, base, 32)
		if err != nil {
			return netip.Addr{}, false
		}
		if i == len(parts)-1 {
			if n >= 1<<(8*(5-len(parts))) {
				return netip.Addr{}, false
			}
			v = v<<(8*(5-len(parts))) | n
		} else {
			if n > 255 {
				return netip.Addr{}, false
			}
			v = v<<8 | n
		}
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}

// privateNets are the address ranges that are not the public internet and so
// are denied by default.
var privateNets = func() []netip.Prefix {
	var ps []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",      // "this" network, unspecified
		"10.0.0.0/8",     // private
		"100.64.0.0/10",  // shared (carrier-grade NAT)
		"127.0.0.0/8",    // loopback
		"169.254.0.0/16", // link-local, cloud metadata
		"172.16.0.0/12",  // private
		"192.0.0.0/24",   // IETF protocol assignments
		"192.168.0.0/16", // private
		"198.18.0.0/15",  // benchmarking
		"224.0.0.0/3",    // multicast, reserved, broadcast
		"::/96",          // unspecified, loopback, IPv4-compatible
		"64:ff9b:1::/48", // local-use NAT64
		"100::/64",       // discard-only
		"2001::/23",      // IETF protocol assignments, Teredo
		"2001:db8::/32",  // documentation
		"fc00::/7",       // unique local
		"fe80::/10",      // link-local
		"fec0::/10",      // site-local
		"ff00::/8",       // multicast
	} {
		ps = append(ps, netip.MustParsePrefix(s))
	}
	return ps
}()

// privateAddr reports whether a is outside the public internet, looking
// through the IPv6 forms that carry an IPv4 address (mapped, NAT64, 6to4).
func privateAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if a.Is6() {
		b := a.As16()
		switch {
		case nat64.Contains(a):
			if inPrivateNet(netip.AddrFrom4([4]byte(b[12:]))) {
				return true
			}
		case b[0] == 0x20 && b[1] == 0x02: // 6to4
			if inPrivateNet(netip.AddrFrom4([4]byte(b[2:6]))) {
				return true
			}
		}
	}
	return inPrivateNet(a)
}

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

func inPrivateNet(a netip.Addr) bool {
	for _, p := range privateNets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ErrPrivateAddress is wrapped by the error of a connection refused because
// it would reach an address that is not on the public internet. Its text does
// not say which address: the name's resolution is the network's to know, and
// the error reaches whoever wrote the URL.
var ErrPrivateAddress = errors.New("the host is a private or loopback address (blocked; AllowPrivateNetworks allows it)")

// errPrivate is a refused dial's error, with no *net.OpError around it, which
// would name the address.
var errPrivate = fmt.Errorf("aster: %w", ErrPrivateAddress)
