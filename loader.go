package aster

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
type HTTPLoader struct {
	Client *http.Client
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

	resp, err := l.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aster: failed to load %q: %w", uri, err)
	}
	defer resp.Body.Close()

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

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("aster: unsupported scheme %q in URI %q (only http/https allowed)", scheme, uri)
	}

	return parsed.String(), nil
}

// FileLoader serves files from a base directory on disk.
// It accepts relative paths and rejects absolute URLs and path traversal.
type FileLoader struct {
	BaseDir string
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
	path := filepath.Join(l.BaseDir, uri)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", path, err)
	}
	return data, nil
}
