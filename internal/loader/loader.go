// Package loader implements the resource loaders both rendering engines use
// to fetch external data: deny-all, HTTP(S) with a host policy, a directory
// confined with os.Root, static payloads, and fallback chains.
package loader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
)

// Loader controls how external resources (data files, remote URLs) are fetched.
// By default, all loading is denied for security. Use HTTPLoader (or
// NewHTTPLoader) to permit HTTP(S) requests, or implement a custom Loader for
// fine-grained control.
//
// Loaded payloads cross into the JavaScript runtime as UTF-8 text; Vega's
// supported formats (JSON, CSV, TSV, TopoJSON) are all textual. Binary
// payloads are not supported and would be corrupted in transit.
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

// FileLoader serves files from a base directory on disk.
// It accepts relative paths and rejects absolute URLs and path traversal.
// On supported platforms, it uses os.Root for OS-level path containment.
//
// MaxBytes caps the size of one file. Zero means the default cap (64 MiB, the
// same as HTTPLoader); a negative value disables the cap. A larger file fails
// with an error before it is read. Only regular files are served (a FIFO or
// device inside the directory is rejected without being opened).
type FileLoader struct {
	BaseDir  string
	MaxBytes int64 // per-file size cap; 0 = 64 MiB default, negative = unlimited
	once     sync.Once
	mu       sync.Mutex // guards root against a concurrent Close
	root     *os.Root
	err      error
	closed   bool
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
			// Not the path: this error reaches whoever supplied the URI.
			var pe *fs.PathError
			if errors.As(l.err, &pe) {
				l.err = pe.Err
			}
			l.err = fmt.Errorf("aster: FileLoader cannot open its base directory: %w", l.err)
		}
	})
}

// Sanitize accepts a relative path inside the base directory. The URI is a
// file path, not a URL: a name with a colon in its first segment (22:48) is a
// file name, as it is to the reference implementation, and only an actual
// scheme (file:, http:, mailto:) is refused. Nothing is decoded, so %2e%2e is
// a file name too.
func (l *FileLoader) Sanitize(_ context.Context, uri string) (string, error) {
	if hasScheme(uri) {
		return "", fmt.Errorf("aster: FileLoader only accepts relative paths, got a scheme in %q", uri)
	}
	for i := 0; i < len(uri); i++ {
		if uri[i] < 0x20 || uri[i] == 0x7f {
			return "", fmt.Errorf("aster: invalid URI %q: control character", uri)
		}
	}

	cleaned := filepath.Clean(uri)
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || os.IsPathSeparator(cleaned[0]) {
		return "", fmt.Errorf("aster: FileLoader rejects absolute path %q", uri)
	}
	if runtime.GOOS == "windows" && strings.Contains(cleaned, ":") {
		return "", fmt.Errorf("aster: FileLoader rejects %q: a colon names a drive or a stream", uri)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("aster: FileLoader rejects path traversal in %q", uri)
	}

	return cleaned, nil
}

// hasScheme reports whether s starts with a URI scheme: a letter, then
// letters, digits, '+', '-' or '.', then a colon.
func hasScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		case i > 0 && c == ':':
			return true
		default:
			return false
		}
	}
	return false
}

func (l *FileLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.initRoot()
	if l.err != nil {
		return nil, l.err
	}
	l.mu.Lock()
	root, closed := l.root, l.closed
	l.mu.Unlock()
	if closed || root == nil {
		// A converter closes its loader on Close; a loader shared with a
		// second converter must fail cleanly rather than dereference nil.
		return nil, errors.New("aster: FileLoader is closed")
	}

	max := l.MaxBytes
	if max == 0 {
		max = defaultMaxResponseBytes
	}

	// Stat before opening: opening a FIFO would block until a writer shows up.
	if fi, err := root.Stat(uri); err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", uri, err)
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("aster: FileLoader %q is not a regular file", uri)
	}
	f, err := root.Open(uri)
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", uri, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", uri, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("aster: FileLoader %q is not a regular file", uri)
	}
	if max > 0 && fi.Size() > max {
		return nil, fmt.Errorf("aster: file %q is %d bytes, exceeds %d bytes (raise FileLoader.MaxBytes to allow larger files): %w", uri, fi.Size(), max, budget.ErrLimit)
	}
	var r io.Reader = f
	if max > 0 {
		// The file may grow after the stat; read one byte past the cap.
		r = io.LimitReader(f, max+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("aster: FileLoader failed to read %q: %w", uri, err)
	}
	if max > 0 && int64(len(data)) > max {
		return nil, fmt.Errorf("aster: file %q exceeds %d bytes (raise FileLoader.MaxBytes to allow larger files): %w", uri, max, budget.ErrLimit)
	}
	return data, nil
}

// Close releases the OS-level directory handle. Safe to call multiple times.
func (l *FileLoader) Close() error {
	l.initRoot() // a Close before any Load must not let a later Load reopen
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
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

func (l *FallbackLoader) Sanitize(ctx context.Context, uri string) (string, error) {
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
