package aster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Loader controls how external resources (data files, remote URLs) are fetched.
// By default, all loading is denied for security. Use AllowHTTPLoader to permit
// HTTP(S) requests, or implement a custom Loader for fine-grained control.
type Loader interface {
	// Load fetches the content at the given URI.
	Load(ctx context.Context, uri string) ([]byte, error)

	// Sanitize validates and optionally transforms a URI before loading.
	// Return an error to deny access to a URI.
	Sanitize(ctx context.Context, uri string) (string, error)
}

// DenyLoader denies all resource loading. This is the default.
type DenyLoader struct{}

func (DenyLoader) Load(_ context.Context, uri string) ([]byte, error) {
	return nil, fmt.Errorf("aster: resource loading denied for %q (no loader configured)", uri)
}

func (DenyLoader) Sanitize(_ context.Context, uri string) (string, error) {
	return "", fmt.Errorf("aster: resource loading denied for %q (no loader configured)", uri)
}

// HTTPLoader allows loading resources over HTTP and HTTPS.
//
// AllowedDomains restricts which hostnames may be accessed. If empty, all
// domains are permitted. BaseURL enables resolution of relative URIs; if
// empty, only absolute HTTP(S) URLs are accepted.
//
// The same scheme/userinfo/AllowedDomains policy is enforced on every HTTP
// redirect hop, so an allowed host cannot redirect the request to a
// disallowed one.
//
// BlockPrivateNetworks, when set, additionally rejects any request whose host
// resolves to a loopback, link-local, private, or unspecified address
// (including cloud metadata endpoints such as 169.254.169.254). It is off by
// default so that local development and test servers keep working; enable it
// when rendering specs from untrusted sources. Note that name resolution here
// happens at policy-check time, so it does not by itself defend against DNS
// rebinding — pair it with AllowedDomains for untrusted input.
type HTTPLoader struct {
	Client               *http.Client
	AllowedDomains       []string // if non-empty, only these hostnames are permitted
	BaseURL              string   // if set, relative URIs are resolved against this URL
	BlockPrivateNetworks bool     // if set, reject loopback/link-local/private hosts
}

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
// If client is nil, http.DefaultClient is used.
func NewHTTPLoader(client *http.Client) *HTTPLoader {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPLoader{Client: client}
}

func (l *HTTPLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to create request for %q: %w", uri, err)
	}

	// Re-validate the initial URL independently of Sanitize so Load is safe
	// even when called directly.
	if err := l.checkURL(ctx, req.URL); err != nil {
		return nil, err
	}

	base := l.Client
	if base == nil {
		base = http.DefaultClient
	}
	// Shallow-copy the client so the policy-enforcing CheckRedirect below does
	// not mutate a caller-supplied client. Every redirect target is run
	// through the same scheme/domain/network policy as the initial URL.
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("aster: stopped after 10 redirects")
		}
		return l.checkURL(req.Context(), req.URL)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to load %q: %w", uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aster: HTTP %d loading %q", resp.StatusCode, uri)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to read response from %q: %w", uri, err)
	}

	return data, nil
}

func (l *HTTPLoader) Sanitize(ctx context.Context, uri string) (string, error) {
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

	if err := l.checkURL(ctx, parsed); err != nil {
		return "", err
	}

	return parsed.String(), nil
}

// checkURL enforces the HTTPLoader access policy on a fully-resolved URL:
// no userinfo, http/https only, host in AllowedDomains (when set), and — when
// BlockPrivateNetworks is set — a host that does not resolve to a
// loopback/link-local/private/unspecified address. It is applied to the
// initial request URL and to every redirect target.
func (l *HTTPLoader) checkURL(ctx context.Context, u *url.URL) error {
	if u.User != nil {
		return fmt.Errorf("aster: URI %q contains userinfo (not allowed)", u.Redacted())
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("aster: unsupported scheme %q in URI %q (only http/https allowed)", scheme, u.String())
	}

	hostname := u.Hostname()
	if len(l.AllowedDomains) > 0 {
		allowed := false
		for _, d := range l.AllowedDomains {
			if strings.EqualFold(hostname, d) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("aster: domain %q not in allowed list for URI %q", hostname, u.String())
		}
	}

	if l.BlockPrivateNetworks {
		private, err := hostResolvesPrivate(ctx, hostname)
		if err != nil {
			return fmt.Errorf("aster: resolving host %q: %w", hostname, err)
		}
		if private {
			return fmt.Errorf("aster: host %q resolves to a private/loopback address (blocked)", hostname)
		}
	}

	return nil
}

// hostResolvesPrivate reports whether host (an IP literal or a name) maps to
// any address in a loopback, link-local, private, or unspecified range.
func hostResolvesPrivate(ctx context.Context, host string) (bool, error) {
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip), nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false, err
	}
	for _, a := range addrs {
		if isPrivateIP(a.IP) {
			return true, nil
		}
	}
	return false, nil
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified()
}

// FileLoader serves files from a base directory on disk.
// It accepts relative paths and rejects absolute URLs and path traversal.
// On supported platforms, it uses os.Root for OS-level path containment.
type FileLoader struct {
	BaseDir string
	once    sync.Once
	root    *os.Root
	err     error
}

// NewFileLoader creates a FileLoader with eager initialization.
// It returns an error if dir does not exist or cannot be opened.
func NewFileLoader(dir string) (*FileLoader, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader cannot open root %q: %w", dir, err)
	}
	return &FileLoader{BaseDir: dir, root: root}, nil
}

func (l *FileLoader) initRoot() {
	l.once.Do(func() {
		if l.root != nil {
			return // already initialized (NewFileLoader path)
		}
		l.root, l.err = os.OpenRoot(l.BaseDir)
		if l.err != nil {
			l.err = fmt.Errorf("aster: FileLoader cannot open root %q: %w", l.BaseDir, l.err)
		}
	})
}

func (l *FileLoader) Sanitize(_ context.Context, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("aster: invalid URI %q: %w", uri, err)
	}

	if parsed.Scheme != "" {
		return "", fmt.Errorf("aster: FileLoader only accepts relative paths, got scheme %q in %q", parsed.Scheme, uri)
	}

	cleaned := filepath.Clean(uri)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("aster: FileLoader rejects absolute path %q", uri)
	}
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("aster: FileLoader rejects path traversal in %q", uri)
	}

	return cleaned, nil
}

func (l *FileLoader) Load(_ context.Context, uri string) ([]byte, error) {
	l.initRoot()
	if l.err != nil {
		return nil, l.err
	}

	data, err := l.root.ReadFile(uri)
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", uri, err)
	}
	return data, nil
}

// Close releases the OS-level directory handle. Safe to call multiple times.
func (l *FileLoader) Close() error {
	if l.root != nil {
		err := l.root.Close()
		l.root = nil
		return err
	}
	return nil
}

// StaticLoader returns a JSON-serialized payload for every Load call,
// regardless of the URI. Useful for injecting test data.
type StaticLoader struct {
	Value any // JSON-serialized and returned for every Load call
}

func (l *StaticLoader) Sanitize(_ context.Context, uri string) (string, error) {
	return uri, nil
}

func (l *StaticLoader) Load(_ context.Context, _ string) ([]byte, error) {
	data, err := json.Marshal(l.Value)
	if err != nil {
		return nil, fmt.Errorf("aster: StaticLoader failed to marshal value: %w", err)
	}
	return data, nil
}

// FallbackLoader routes requests to multiple child loaders in order.
// The first child whose Sanitize accepts the URI handles the request.
//
// Order matters: a permissive child shadows every child after it. For
// example a StaticLoader (whose Sanitize accepts any URI) placed first will
// answer every request, so put the most specific loaders first and broad
// catch-alls last.
type FallbackLoader struct {
	Loaders []Loader
}

// NewFallbackLoader creates a FallbackLoader from the given children.
func NewFallbackLoader(loaders ...Loader) *FallbackLoader {
	return &FallbackLoader{Loaders: loaders}
}

func (l *FallbackLoader) Sanitize(_ context.Context, uri string) (string, error) {
	ctx := context.Background()
	var lastErr error
	for _, child := range l.Loaders {
		if _, err := child.Sanitize(ctx, uri); err == nil {
			// At least one child accepts — return the original URI so Load()
			// can independently route each child with its own Sanitize+Load.
			return uri, nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("aster: FallbackLoader has no child loaders")
}

func (l *FallbackLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	var lastErr error
	for _, child := range l.Loaders {
		sanitized, err := child.Sanitize(ctx, uri)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := child.Load(ctx, sanitized)
		if err != nil {
			lastErr = err
			continue
		}
		return data, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("aster: FallbackLoader has no child loaders")
}

// Close closes any children that implement io.Closer.
func (l *FallbackLoader) Close() error {
	var firstErr error
	for _, child := range l.Loaders {
		if closer, ok := child.(io.Closer); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
