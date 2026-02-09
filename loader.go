package aster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
type HTTPLoader struct {
	Client         *http.Client
	AllowedDomains []string // if non-empty, only these hostnames are permitted
	BaseURL        string   // if set, relative URIs are resolved against this URL
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

	client := l.Client
	if client == nil {
		client = http.DefaultClient
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

func (l *HTTPLoader) Sanitize(_ context.Context, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("aster: invalid URI %q: %w", uri, err)
	}

	// Reject URIs with userinfo (e.g. https://user:pass@host/).
	if parsed.User != nil {
		return "", fmt.Errorf("aster: URI %q contains userinfo (not allowed)", uri)
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

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("aster: unsupported scheme %q in URI %q (only http/https allowed)", scheme, uri)
	}

	// Check domain allowlist.
	if len(l.AllowedDomains) > 0 {
		hostname := parsed.Hostname()
		allowed := false
		for _, d := range l.AllowedDomains {
			if strings.EqualFold(hostname, d) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("aster: domain %q not in allowed list for URI %q", hostname, uri)
		}
	}

	return parsed.String(), nil
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
