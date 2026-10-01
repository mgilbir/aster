package aster

import (
	"net/http"

	"github.com/mgilbir/aster/internal/loader"
)

// Loader controls how external resources (data files, remote URLs) are fetched.
// By default, all loading is denied for security. Use HTTPLoader (or
// NewHTTPLoader) to permit HTTP(S) requests, or implement a custom Loader for
// fine-grained control.
//
// Loaded payloads are read as UTF-8 text: Vega's supported formats (JSON,
// CSV, TSV, TopoJSON) are all textual, and binary payloads are not supported.
type Loader = loader.Loader

// The loader implementations live in internal/loader, which documents them.
type (
	// DenyLoader denies all resource loading. This is the default.
	DenyLoader = loader.DenyLoader
	// HTTPLoader allows loading resources over HTTP and HTTPS, to public addresses
	// unless AllowPrivateNetworks is set.
	HTTPLoader = loader.HTTPLoader
	// FileLoader serves files from a base directory on disk. It accepts
	// relative paths and rejects absolute URLs and path traversal.
	FileLoader = loader.FileLoader
	// StaticLoader returns a JSON-serialized payload for every Load call,
	// regardless of the URI. Useful for injecting test data.
	StaticLoader = loader.StaticLoader
	// FallbackLoader routes requests to multiple child loaders in order; the
	// first child whose Sanitize accepts the URI handles the request.
	FallbackLoader = loader.FallbackLoader
)

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
// If client is nil, the default client described on HTTPLoader is used.
func NewHTTPLoader(client *http.Client) *HTTPLoader { return loader.NewHTTPLoader(client) }

// NewFileLoader creates a FileLoader with eager initialization.
// It returns an error if dir does not exist or cannot be opened.
func NewFileLoader(dir string) (*FileLoader, error) { return loader.NewFileLoader(dir) }

// NewFallbackLoader creates a FallbackLoader from the given children.
func NewFallbackLoader(loaders ...Loader) *FallbackLoader {
	return loader.NewFallbackLoader(loaders...)
}
