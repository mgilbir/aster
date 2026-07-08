package aster_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		_, _ = fmt.Fprintln(w, `[{"a":"A","b":28}]`)
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

// A redirect to a host outside AllowedDomains must be refused mid-request,
// not silently followed (audit C2). CheckRedirect runs before the new host is
// dialed, so pointing at an unreachable external host is enough: if the policy
// were not enforced the client would attempt the connection and fail
// differently.
func TestHTTPLoaderRedirectToDisallowedHostBlocked(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://blocked.example.invalid/secret.json", http.StatusFound)
	}))
	defer redirector.Close()

	l := &aster.HTTPLoader{
		Client:         redirector.Client(),
		AllowedDomains: []string{urlHost(t, redirector.URL)},
	}

	_, err := l.Load(context.Background(), redirector.URL+"/start.json")
	if err == nil {
		t.Fatal("expected redirect to disallowed host to be blocked")
	}
	if !strings.Contains(err.Error(), "not in allowed list") {
		t.Fatalf("expected allowlist rejection, got: %v", err)
	}
}

// A redirect between two allowed hosts is still followed.
func TestHTTPLoaderRedirectToAllowedHostFollowed(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `"ok"`)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/data.json", http.StatusFound)
	}))
	defer redirector.Close()

	l := &aster.HTTPLoader{
		AllowedDomains: []string{urlHost(t, redirector.URL), urlHost(t, final.URL)},
	}

	data, err := l.Load(context.Background(), redirector.URL+"/start.json")
	if err != nil {
		t.Fatalf("Load across allowed redirect: %v", err)
	}
	if !strings.Contains(string(data), "ok") {
		t.Errorf("unexpected data: %s", data)
	}
}

// BlockPrivateNetworks rejects loopback targets at policy-check time.
func TestHTTPLoaderBlockPrivateNetworks(t *testing.T) {
	l := &aster.HTTPLoader{BlockPrivateNetworks: true}
	ctx := context.Background()
	for _, uri := range []string{
		"http://127.0.0.1/data.json",
		"http://localhost/data.json",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/x",
	} {
		if _, err := l.Sanitize(ctx, uri); err == nil {
			t.Errorf("expected %q to be blocked as private", uri)
		}
	}
	// A public IP literal is allowed through the policy check.
	if _, err := l.Sanitize(ctx, "http://93.184.216.34/data.json"); err != nil {
		t.Errorf("public IP should pass policy check: %v", err)
	}
}

func urlHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Hostname()
}

// ---------- FileLoader: os.Root ----------

func TestFileLoaderBasicRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "data.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &aster.FileLoader{BaseDir: dir}
	defer func() { _ = l.Close() }()

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
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside base dir pointing outside.
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	l := &aster.FileLoader{BaseDir: dir}
	defer func() { _ = l.Close() }()

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
	if err := os.WriteFile(filepath.Join(dir, "local.json"), []byte(`"from-file"`), 0o644); err != nil {
		t.Fatal(err)
	}

	file := &aster.FileLoader{BaseDir: dir}
	http := aster.NewHTTPLoader(nil)
	l := aster.NewFallbackLoader(file, http)
	defer func() { _ = l.Close() }()

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
	_ = c.Close()
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

// ---------- HTTPLoader: response size cap ----------

func TestHTTPLoaderResponseCapEnforced(t *testing.T) {
	big := strings.Repeat("x", 4096)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer ts.Close()

	l := &aster.HTTPLoader{Client: ts.Client(), MaxResponseBytes: 1024}
	_, err := l.Load(context.Background(), ts.URL+"/big.json")
	if err == nil {
		t.Fatal("expected error for response exceeding MaxResponseBytes")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}

	// A response exactly at the cap is allowed.
	l.MaxResponseBytes = int64(len(big))
	data, err := l.Load(context.Background(), ts.URL+"/big.json")
	if err != nil {
		t.Fatalf("response at cap should load: %v", err)
	}
	if len(data) != len(big) {
		t.Fatalf("truncated response: got %d bytes, want %d", len(data), len(big))
	}

	// Negative disables the cap.
	l.MaxResponseBytes = -1
	if _, err := l.Load(context.Background(), ts.URL+"/big.json"); err != nil {
		t.Fatalf("negative cap should disable the limit: %v", err)
	}
}

// ---------- HTTPLoader: caller redirect policy composes ----------

// A CheckRedirect configured on the caller's client must still run (after
// aster's own policy check), so a stricter caller policy is not silently
// replaced.
func TestHTTPLoaderCallerCheckRedirectStillApplies(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `"ok"`)
	}))
	defer final.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/data.json", http.StatusFound)
	}))
	defer redirector.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("caller-policy: no redirects")
		},
	}
	l := &aster.HTTPLoader{Client: client}

	_, err := l.Load(context.Background(), redirector.URL+"/start.json")
	if err == nil {
		t.Fatal("expected the caller's CheckRedirect to block the redirect")
	}
	if !strings.Contains(err.Error(), "caller-policy") {
		t.Fatalf("expected caller policy error, got: %v", err)
	}

	// The original client must not have been mutated.
	if client.CheckRedirect == nil {
		t.Fatal("caller client was mutated")
	}
}

// ---------- FallbackLoader: context propagation ----------

// ctxCheckLoader records whether the ctx passed to Sanitize carried a deadline.
type ctxCheckLoader struct {
	sawDeadline bool
}

func (l *ctxCheckLoader) Sanitize(ctx context.Context, uri string) (string, error) {
	_, l.sawDeadline = ctx.Deadline()
	return uri, nil
}

func (l *ctxCheckLoader) Load(_ context.Context, _ string) ([]byte, error) {
	return []byte(`[]`), nil
}

func TestFallbackLoaderSanitizePropagatesContext(t *testing.T) {
	child := &ctxCheckLoader{}
	l := aster.NewFallbackLoader(child)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := l.Sanitize(ctx, "data.json"); err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if !child.sawDeadline {
		t.Error("child Sanitize did not receive the caller's context deadline")
	}
}
