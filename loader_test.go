package aster_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// ---------- HTTPLoader: AllowedDomains ----------

func TestHTTPLoaderAllowedDomainAccepted(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"example.com"},
	}
	got, err := l.Sanitize(context.Background(), "https://example.com/data.json")
	if err != nil {
		t.Fatalf("expected allowed, got error: %v", err)
	}
	if got != "https://example.com/data.json" {
		t.Errorf("unexpected sanitized URI: %s", got)
	}
}

func TestHTTPLoaderBlockedDomainRejected(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"example.com"},
	}
	_, err := l.Sanitize(context.Background(), "https://evil.com/data.json")
	if err == nil {
		t.Fatal("expected error for blocked domain")
	}
	if !strings.Contains(err.Error(), "not in allowed list") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHTTPLoaderDomainCaseInsensitive(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"example.com"},
	}
	_, err := l.Sanitize(context.Background(), "https://Example.COM/data.json")
	if err != nil {
		t.Fatalf("case-insensitive match should accept: %v", err)
	}
}

func TestHTTPLoaderEmptyAllowedDomainsAllowsAll(t *testing.T) {
	l := &aster.HTTPLoader{}
	_, err := l.Sanitize(context.Background(), "https://anything.example.org/data.json")
	if err != nil {
		t.Fatalf("empty AllowedDomains should allow all: %v", err)
	}
}

func TestHTTPLoaderDomainWithPortMatchesWithoutPort(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"example.com"},
	}
	_, err := l.Sanitize(context.Background(), "https://example.com:8080/data.json")
	if err != nil {
		t.Fatalf("port should not prevent domain match: %v", err)
	}
}

func TestHTTPLoaderRejectNonHTTPSchemes(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"example.com"},
	}
	for _, scheme := range []string{"ftp", "javascript", "data", "file"} {
		uri := scheme + "://example.com/payload"
		_, err := l.Sanitize(context.Background(), uri)
		if err == nil {
			t.Errorf("expected rejection of scheme %q", scheme)
		}
	}
}

func TestHTTPLoaderRejectUserinfo(t *testing.T) {
	l := &aster.HTTPLoader{
		AllowedDomains: []string{"allowed.com"},
	}
	_, err := l.Sanitize(context.Background(), "https://user:pass@allowed.com/data")
	if err == nil {
		t.Fatal("expected rejection of userinfo URI")
	}
	if !strings.Contains(err.Error(), "userinfo") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- HTTPLoader: BaseURL ----------

func TestHTTPLoaderBaseURLResolvesRelative(t *testing.T) {
	l := &aster.HTTPLoader{
		BaseURL: "https://cdn.example.com/datasets/",
	}
	got, err := l.Sanitize(context.Background(), "cars.json")
	if err != nil {
		t.Fatalf("expected resolved URI, got error: %v", err)
	}
	if got != "https://cdn.example.com/datasets/cars.json" {
		t.Errorf("unexpected URI: %s", got)
	}
}

func TestHTTPLoaderAbsoluteIgnoresBaseURL(t *testing.T) {
	l := &aster.HTTPLoader{
		BaseURL: "https://cdn.example.com/datasets/",
	}
	got, err := l.Sanitize(context.Background(), "https://other.com/data.json")
	if err != nil {
		t.Fatalf("absolute URI should pass through: %v", err)
	}
	if got != "https://other.com/data.json" {
		t.Errorf("unexpected URI: %s", got)
	}
}

func TestHTTPLoaderRelativeRejectedWithoutBaseURL(t *testing.T) {
	l := &aster.HTTPLoader{}
	_, err := l.Sanitize(context.Background(), "cars.json")
	if err == nil {
		t.Fatal("expected error for relative URI without BaseURL")
	}
}

func TestHTTPLoaderBaseURLWithAllowedDomains(t *testing.T) {
	l := &aster.HTTPLoader{
		BaseURL:        "https://cdn.example.com/datasets/",
		AllowedDomains: []string{"cdn.example.com"},
	}
	_, err := l.Sanitize(context.Background(), "cars.json")
	if err != nil {
		t.Fatalf("resolved domain should be in allowlist: %v", err)
	}
}

func TestHTTPLoaderBaseURLResolvedDomainMustBeAllowed(t *testing.T) {
	l := &aster.HTTPLoader{
		BaseURL:        "https://cdn.example.com/datasets/",
		AllowedDomains: []string{"other.com"},
	}
	_, err := l.Sanitize(context.Background(), "cars.json")
	if err == nil {
		t.Fatal("expected rejection: resolved domain not in allowlist")
	}
}

func TestHTTPLoaderBaseURLPathTraversal(t *testing.T) {
	l := &aster.HTTPLoader{
		BaseURL:        "https://cdn.example.com/datasets/",
		AllowedDomains: []string{"cdn.example.com"},
	}
	got, err := l.Sanitize(context.Background(), "../../etc/passwd")
	if err != nil {
		t.Fatalf("path traversal in URL resolves cleanly: %v", err)
	}
	// URL resolution cleans the path; domain is still checked.
	if !strings.HasPrefix(got, "https://cdn.example.com/") {
		t.Errorf("resolved URL should stay on allowed domain: %s", got)
	}
}

func TestHTTPLoaderIntegration(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `[{"a":"A","b":28}]`)
	}))
	defer ts.Close()

	l := &aster.HTTPLoader{
		Client:         ts.Client(),
		AllowedDomains: []string{"127.0.0.1"},
	}

	ctx := context.Background()
	sanitized, err := l.Sanitize(ctx, ts.URL+"/data.json")
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	data, err := l.Load(ctx, sanitized)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(string(data), `"a":"A"`) {
		t.Errorf("unexpected data: %s", data)
	}
}

// ---------- FileLoader: os.Root ----------

func TestFileLoaderBasicRead(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "data.json"), []byte(`{"ok":true}`), 0o644)

	l := &aster.FileLoader{BaseDir: dir}
	defer l.Close()

	ctx := context.Background()
	sanitized, err := l.Sanitize(ctx, "sub/data.json")
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	data, err := l.Load(ctx, sanitized)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("unexpected data: %s", data)
	}
}

func TestFileLoaderRejectsAbsolutePath(t *testing.T) {
	l := &aster.FileLoader{BaseDir: t.TempDir()}
	_, err := l.Sanitize(context.Background(), "/etc/passwd")
	if err == nil {
		t.Fatal("expected rejection of absolute path")
	}
}

func TestFileLoaderRejectsSchemes(t *testing.T) {
	l := &aster.FileLoader{BaseDir: t.TempDir()}
	for _, uri := range []string{"file:///etc/passwd", "http://example.com"} {
		_, err := l.Sanitize(context.Background(), uri)
		if err == nil {
			t.Errorf("expected rejection of %q", uri)
		}
	}
}

func TestFileLoaderRejectsPathTraversal(t *testing.T) {
	l := &aster.FileLoader{BaseDir: t.TempDir()}
	for _, uri := range []string{
		"../../../etc/passwd",
		"data/../../etc/passwd",
		"foo/../../../etc/passwd",
	} {
		_, err := l.Sanitize(context.Background(), uri)
		if err == nil {
			t.Errorf("expected rejection of path traversal %q", uri)
		}
	}
}

func TestFileLoaderOSRootBlocksSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	// Create a file outside the base dir.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)

	// Create a symlink inside base dir pointing outside.
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	l := &aster.FileLoader{BaseDir: dir}
	defer l.Close()

	ctx := context.Background()
	_, err := l.Load(ctx, "escape/secret.txt")
	if err == nil {
		t.Fatal("expected os.Root to reject symlink escape")
	}
}

func TestFileLoaderNewFileLoaderInvalidDir(t *testing.T) {
	_, err := aster.NewFileLoader("/nonexistent/path/to/dir")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestFileLoaderCloseMultipleTimes(t *testing.T) {
	dir := t.TempDir()
	l, err := aster.NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second close should not fail: %v", err)
	}
}

func TestFileLoaderCloseBeforeLoad(t *testing.T) {
	dir := t.TempDir()
	l := &aster.FileLoader{BaseDir: dir}
	// Close before any Load — should be a no-op.
	if err := l.Close(); err != nil {
		t.Fatalf("close before load: %v", err)
	}
}

// ---------- StaticLoader ----------

func TestStaticLoaderReturnsJSON(t *testing.T) {
	data := []map[string]any{{"a": "A", "b": 28}, {"a": "B", "b": 55}}
	l := &aster.StaticLoader{Value: data}

	ctx := context.Background()
	uri, err := l.Sanitize(ctx, "anything.json")
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if uri != "anything.json" {
		t.Errorf("Sanitize should return URI unchanged, got %q", uri)
	}

	got, err := l.Load(ctx, uri)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(string(got), `"a":"A"`) {
		t.Errorf("unexpected JSON: %s", got)
	}
}

func TestStaticLoaderSanitizeAcceptsAnyURI(t *testing.T) {
	l := &aster.StaticLoader{Value: "hello"}
	for _, uri := range []string{
		"http://example.com",
		"/etc/passwd",
		"relative/path.json",
		"",
	} {
		_, err := l.Sanitize(context.Background(), uri)
		if err != nil {
			t.Errorf("Sanitize(%q) should accept: %v", uri, err)
		}
	}
}

// ---------- FallbackLoader ----------

func TestFallbackLoaderFirstMatchServes(t *testing.T) {
	data1 := &aster.StaticLoader{Value: "first"}
	data2 := &aster.StaticLoader{Value: "second"}
	l := aster.NewFallbackLoader(data1, data2)

	got, err := l.Load(context.Background(), "any")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != `"first"` {
		t.Errorf("expected first child, got: %s", got)
	}
}

func TestFallbackLoaderFallsThrough(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "local.json"), []byte(`"from-file"`), 0o644)

	file := &aster.FileLoader{BaseDir: dir}
	http := aster.NewHTTPLoader(nil)
	l := aster.NewFallbackLoader(file, http)
	defer l.Close()

	// Relative URI → FileLoader handles it.
	ctx := context.Background()
	got, err := l.Load(ctx, "local.json")
	if err != nil {
		t.Fatalf("Load local: %v", err)
	}
	if string(got) != `"from-file"` {
		t.Errorf("expected file content, got: %s", got)
	}
}

func TestFallbackLoaderAllChildrenReject(t *testing.T) {
	l := aster.NewFallbackLoader(aster.DenyLoader{}, aster.DenyLoader{})
	_, err := l.Load(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error when all children reject")
	}
}

func TestFallbackLoaderSanitizeAllReject(t *testing.T) {
	l := aster.NewFallbackLoader(aster.DenyLoader{}, aster.DenyLoader{})
	_, err := l.Sanitize(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error when all children reject sanitize")
	}
}

func TestFallbackLoaderClosePropagatesToClosers(t *testing.T) {
	dir := t.TempDir()
	fl, err := aster.NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}

	l := aster.NewFallbackLoader(fl, aster.DenyLoader{})
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// FileLoader should be closed — a second close should be a no-op.
	if err := fl.Close(); err != nil {
		t.Fatalf("FileLoader second close: %v", err)
	}
}

func TestFallbackLoaderOnlyDenyLoaders(t *testing.T) {
	l := aster.NewFallbackLoader(aster.DenyLoader{}, aster.DenyLoader{})
	ctx := context.Background()

	_, err := l.Sanitize(ctx, "data.json")
	if err == nil {
		t.Fatal("expected all-deny sanitize to fail")
	}

	_, err = l.Load(ctx, "data.json")
	if err == nil {
		t.Fatal("expected all-deny load to fail")
	}
}

// ---------- Converter auto-close ----------

// closerTracker is a Loader that tracks whether Close was called.
type closerTracker struct {
	aster.DenyLoader
	closed bool
}

func (c *closerTracker) Close() error {
	c.closed = true
	return nil
}

func TestConverterClosesLoader(t *testing.T) {
	tracker := &closerTracker{}
	c, err := aster.New(aster.WithLoader(tracker))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Close()
	if !tracker.closed {
		t.Error("expected loader Close to be called")
	}
}

func TestConverterCloseWithDenyLoader(t *testing.T) {
	// DenyLoader does not implement io.Closer — should not panic.
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
