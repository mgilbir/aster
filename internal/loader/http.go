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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// HTTPLoader allows loading resources over HTTP and HTTPS.
//
// AllowedDomains restricts which hostnames may be accessed. If empty, all
// domains are permitted. Names are compared case-insensitively, ignoring one
// trailing dot, and only ASCII names are accepted: write an internationalized
// name in its punycode form. BaseURL enables resolution of relative URIs; if
// empty, only absolute HTTP(S) URLs are accepted.
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
// check is made on the address a connection is actually made to, not on the
// name, so DNS rebinding and redirects cannot get around it. It is enforced
// on the connections of the default client and of any Client whose Transport
// is an *http.Transport (a DialContext or DialTLSContext of its own is wrapped
// with the check). A Transport with a deprecated Dial function, and any other
// http.RoundTripper, decide for themselves where they connect, as the caller
// wrote them to: for those only address literals and localhost names are
// refused, and AllowedDomains is the tool to confine them.
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
	BaseURL          string        // if set, relative URIs are resolved against this URL
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

	mu     sync.Mutex // guards cached
	cached derivedTransport
}

// proxyFromEnvironment selects the proxy of the default client. A variable so
// that a test can stand in for the environment, which net/http reads once per
// process.
var proxyFromEnvironment = http.ProxyFromEnvironment

// derivedTransport is the transport made from the last base transport seen,
// kept so that connections are reused from one load to the next.
type derivedTransport struct {
	base  *http.Transport // nil: the loader's own default
	allow bool
	rt    http.RoundTripper
	ok    bool
}

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
// no cap.
func loadCap(ctx context.Context, configured int64) int64 {
	if configured == 0 {
		configured = defaultMaxResponseBytes
	}
	if left := budget.From(ctx).LoadLeft(); left != math.MaxInt64 && (configured < 0 || left < configured) {
		return left
	}
	return configured
}

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
// If client is nil, the client described on HTTPLoader is used.
func NewHTTPLoader(client *http.Client) *HTTPLoader {
	return &HTTPLoader{Client: client}
}

func (l *HTTPLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	timeout := l.Timeout
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to create request for %q: %w", uri, err)
	}

	// The client is a shallow copy, so the policy-enforcing CheckRedirect
	// below does not mutate a caller-supplied client. Every redirect target is
	// run through the same scheme/domain/network policy as the initial URL,
	// then through the caller's own CheckRedirect (if any), so a stricter
	// caller-supplied redirect policy still applies.
	client := l.client()
	// Re-validate the initial URL independently of Sanitize so Load is safe
	// even when called directly.
	if err := l.checkURL(req.URL); err != nil {
		return nil, err
	}
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

	max := loadCap(ctx, l.MaxResponseBytes)
	if max < 0 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("aster: failed to read response from %q: %w", uri, err)
		}
		return data, nil
	}

	// The declared size can be a lie, but when it is over the cap there is
	// nothing to read.
	if resp.ContentLength > max {
		return nil, fmt.Errorf("aster: response from %q is %d bytes, exceeds %d bytes (raise HTTPLoader.MaxResponseBytes or the memory limit to allow larger payloads): %w", uri, resp.ContentLength, max, budget.ErrLimit)
	}
	// Read one byte past the cap to distinguish "exactly at the cap" from
	// "exceeds it".
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("aster: failed to read response from %q: %w", uri, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("aster: response from %q exceeds %d bytes (raise HTTPLoader.MaxResponseBytes or the memory limit to allow larger payloads): %w", uri, max, budget.ErrLimit)
	}
	return data, nil
}

func (l *HTTPLoader) Sanitize(_ context.Context, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("aster: invalid URI %q: %w", uri, err)
	}

	// Resolve relative URIs against BaseURL if configured.
	if parsed.Scheme == "" {
		if l.BaseURL == "" {
			return "", fmt.Errorf("aster: relative URI %q not allowed (no BaseURL configured)", uri)
		}
		base, err := url.Parse(l.BaseURL)
		if err != nil {
			return "", fmt.Errorf("aster: invalid BaseURL %q: %w", l.BaseURL, err)
		}
		parsed = base.ResolveReference(parsed)
	}

	// Sanitize also vets the links of a chart, so it does not resolve names: a
	// specification could otherwise make the server look up any number of
	// hosts. Names are checked where a connection is made (see Load).
	if err := l.checkURL(parsed); err != nil {
		return "", err
	}

	return parsed.String(), nil
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
		t = nil
	case t == nil || t.Dial != nil || t.DialTLS != nil:
		return base // the caller's own egress
	case l.AllowPrivateNetworks:
		return base // nothing to check
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if d := &l.cached; d.ok && d.base == t && d.allow == l.AllowPrivateNetworks {
		return d.rt
	}
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
			Proxy:                  proxyFromEnvironment,
		}
	} else {
		rt = t.Clone()
	}
	px := &proxies{}
	if rt.Proxy != nil {
		rt.Proxy = px.record(rt.Proxy)
	}
	if t != nil && (t.DialContext != nil || t.DialTLSContext != nil) {
		rt.DialContext = checkedDial(t.DialContext, px)
		rt.DialTLSContext = checkedDial(t.DialTLSContext, px)
	} else {
		direct := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dialer := direct
		if !l.AllowPrivateNetworks {
			guarded := *direct
			guarded.Control = denyPrivate
			dialer = &guarded
		}
		rt.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if px.has(addr) {
				return direct.DialContext(ctx, network, addr)
			}
			return dialer.DialContext(ctx, network, addr)
		}
		rt.ForceAttemptHTTP2 = true
	}
	l.cached = derivedTransport{base: t, allow: l.AllowPrivateNetworks, rt: rt, ok: true}
	return rt
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

// checkedDial wraps a caller's dial function: a connection it makes to a
// private address is closed, unless it is to one of the transport's proxies.
// A nil dial stays nil.
func checkedDial(dial func(context.Context, string, string) (net.Conn, error), px *proxies) func(context.Context, string, string) (net.Conn, error) {
	if dial == nil {
		return nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil || px.has(addr) {
			return c, err
		}
		if tcp, ok := c.RemoteAddr().(*net.TCPAddr); ok && privateAddr(tcp.AddrPort().Addr()) {
			_ = c.Close()
			return nil, fmt.Errorf("aster: address %s is private/loopback (blocked)", tcp.IP)
		}
		return c, nil
	}
}

// denyPrivate is a net.Dialer Control function: it sees the address a socket
// is about to connect to, after name resolution, and refuses a private one.
func denyPrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if privateAddr(ap.Addr()) {
		return fmt.Errorf("aster: address %s is private/loopback (blocked)", ap.Addr())
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
