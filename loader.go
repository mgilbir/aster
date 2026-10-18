package aster

import (
	"net/http"

	"github.com/mgilbir/aster/internal/loader"
)

// Loader decides which resources a specification may use, and fetches them:
// the data a data set's url names, the images image marks draw (and the
// <image> elements of SVG given to SVGToPNG and SVGToPDF), and the links
// (href) written into SVG. By default every one is refused (DenyLoader).
//
// Sanitize validates uri, and may rewrite it, before it is loaded or written
// into the output; an error refuses it. Load returns the bytes at the URI
// Sanitize returned. In one call, Sanitize is called once for each data
// set's load and once for each distinct image or link URL, with the URL the
// input names, and Load with exactly what Sanitize returned: never Sanitize
// of its own answer, so a Sanitize that rewrites need not accept what it
// returns (a FallbackLoader's children must; see FallbackLoader). For a link
// only Sanitize is called, and vega-loader's scheme allow-list is applied to
// its result as well, so a permissive Loader cannot put a javascript: URL
// into the output.
//
// Data is read as UTF-8 text (JSON, CSV, TSV, DSV, TopoJSON); images are
// binary (PNG, JPEG, GIF) and must be returned unchanged. An error from either
// method becomes the Err of the *LoadError the render reports (see
// WithErrorHandler), under the URL the input names, once per resource and
// call, except that one wrapping ErrLimit is a limit and always fails the
// call. A Loader is called from many goroutines at once and must be safe for
// that; it should honour ctx, which carries the call's deadline, Close's
// cancellation and the values and end of a WithContext context, and bound
// what it reads: the data and images of one call share one allowance of
// loaded bytes. It may call Close, or start another call, on the Converter.
// If it implements io.Closer, Converter.Close closes it.
//
// A panic in any of its methods is recovered, whatever goroutine the engine
// called it on, and is that method's error: the resource is not loaded (a
// *LoadError, which WithErrorHandler may let go), or Close returns it. A bug
// in a Loader fails what it was asked for, never the process. The Loaders of
// this package are nil-safe: a nil one's methods return an error.
type Loader = loader.Loader

// The loader implementations live in internal/loader; their fields are
// documented here.
type (
	// DenyLoader refuses every resource. It is the default, and what
	// WithLoader(nil) selects.
	DenyLoader = loader.DenyLoader

	// HTTPLoader loads over HTTP and HTTPS, to public addresses unless
	// AllowPrivateNetworks is set. Its fields:
	//
	//   - Client *http.Client: nil (or http.DefaultClient) selects a client of
	//     this package's own, which bounds connecting, TLS and the wait for
	//     headers and honours HTTP_PROXY, HTTPS_PROXY and NO_PROXY.
	//   - AllowedDomains []string: if non-empty, only these host names
	//     (case-insensitive, one trailing dot ignored, ASCII only: write an
	//     internationalized name in punycode).
	//   - BaseURL string: relative URIs are resolved against it; without it
	//     only absolute http(s) URLs are accepted.
	//   - MaxResponseBytes int64: the body cap after decoding; 0 is 64 MiB,
	//     negative none. A larger body fails with ErrLimit.
	//   - Timeout time.Duration: one request, redirects and body included; 0
	//     is 60 s, negative none.
	//   - AllowPrivateNetworks bool: allow loopback, link-local (cloud
	//     metadata), private, shared, multicast and unspecified addresses.
	//   - BlockPrivateNetworks bool: deprecated and ignored.
	//
	// It refuses other schemes, URLs with userinfo, and hosts outside
	// AllowedDomains, on every redirect hop (at most 10). The private-network
	// check is made on the addresses a name resolves to before anything is
	// sent to them, and on the address actually connected to, so names that
	// resolve inward, DNS rebinding and redirects do not get around it; it
	// applies to the default client and to a Client whose Transport is an
	// *http.Transport, on every path that Transport dials (DialContext and
	// the deprecated Dial, which are given the vetted address rather than the
	// name, DialTLSContext and DialTLS, and net/http's own dialer when it sets
	// none). A failed connection's error says neither which address a name
	// resolved to nor whether a port answered. HTTPLoaders built around one
	// *http.Transport share one checked copy of it, and its connections. A
	// request a proxy carries, or that another RoundTripper makes, gets only
	// the checks on the URL. See docs/loading-data.md.
	HTTPLoader = loader.HTTPLoader

	// FileLoader serves files under BaseDir string, contained with os.Root:
	// relative paths only, no "..", no scheme, no control characters, no
	// symlink out of BaseDir, regular files only. MaxBytes int64 caps one file:
	// 0 is 64 MiB, negative none; a larger file fails with ErrLimit. The
	// directory is opened on first use (NewFileLoader opens it at once), and
	// Close closes it: do not share one FileLoader between Converters.
	FileLoader = loader.FileLoader

	// StaticLoader returns Value any, as JSON, for every URI, and accepts
	// every URI. For tests.
	StaticLoader = loader.StaticLoader

	// FallbackLoader tries Loaders []Loader in order: the first whose
	// Sanitize accepts a URI and whose Load succeeds answers it. Its Sanitize
	// returns what the first child to accept the URI made of it (an
	// HTTPLoader's BaseURL resolved, say), and its Load routes that answer
	// through the children again, Sanitize then Load: so every child's
	// Sanitize must return its own answer unchanged when given it, as the
	// loaders of this package do. A limit (ErrLimit) from a child ends the
	// search. Close closes every child that implements io.Closer. A nil
	// child, such as the *FileLoader of a NewFileLoader whose error was
	// dropped, makes New return an error, at any depth of nesting; used
	// directly, it refuses every URI.
	FallbackLoader = loader.FallbackLoader
)

// NewHTTPLoader creates a loader that allows HTTP(S) requests.
// If client is nil, the default client described on HTTPLoader is used.
func NewHTTPLoader(client *http.Client) *HTTPLoader { return loader.NewHTTPLoader(client) }

// NewFileLoader creates a FileLoader with eager initialization.
// It returns an error if dir does not exist or cannot be opened.
func NewFileLoader(dir string) (*FileLoader, error) { return loader.NewFileLoader(dir) }

// NewFallbackLoader creates a FallbackLoader from the given children. A nil
// child is refused by New (see FallbackLoader).
func NewFallbackLoader(loaders ...Loader) *FallbackLoader {
	return loader.NewFallbackLoader(loaders...)
}
