package loader

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fileTree is a directory with a few files, next to a secret outside it.
func fileTree(t *testing.T) (dir, secret string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "data")
	secret = filepath.Join(root, "secret.txt")
	for name, body := range map[string]string{
		filepath.Join(dir, "ok.csv"):       "a,b\n1,2\n",
		filepath.Join(dir, "sub", "x.csv"): "x\n",
		filepath.Join(dir, "..hidden"):     "dots",
		filepath.Join(dir, "a..b"):         "dots in the middle",
		secret:                             "TOP SECRET",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, secret
}

// No spelling of a path leads out of the base directory, through Sanitize or
// through Load called directly.
func TestFileLoaderConfinement(t *testing.T) {
	dir, secret := fileTree(t)
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink("../", filepath.Join(dir, "up")); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat("/dev/zero"); err == nil {
		_ = os.Symlink("/dev/zero", filepath.Join(dir, "zero")) // a device that never ends
	}
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	paths := []string{
		"../secret.txt", "../../secret.txt", "sub/../../secret.txt", "./../secret.txt", "sub/../..",
		secret, "/etc/passwd", "//etc/passwd", "file://" + secret, "file:///etc/passwd", "file:secret.txt",
		"link", "up/secret.txt", "sub/../link", "zero",
		"%2e%2e/secret.txt", "..%2fsecret.txt", "%2e%2e%2fsecret.txt", "..%5csecret.txt",
		"..\\secret.txt", "sub\\..\\..\\secret.txt",
		"ok.csv\x00", "ok.csv\x00../secret.txt", "\x00", "ok.csv\n", "ok.csv\r\n../secret.txt",
		"C:\\Windows\\win.ini", "C:/Windows/win.ini", "C:secret.txt", `\\server\share\x`,
		"http://example.com/x.csv", "data:text/plain,hi", "javascript:alert(1)",
		"/dev/zero", "/dev/random", "/proc/self/environ",
	}
	for _, p := range paths {
		s, err := l.Sanitize(context.Background(), p)
		if err == nil {
			// Accepted by Sanitize: loading it must still not reach the secret.
			data, err := l.Load(context.Background(), s)
			if err == nil && strings.Contains(string(data), "SECRET") {
				t.Errorf("%q: Sanitize(%q) then Load read the secret", p, s)
			}
		}
		// Load alone, with no Sanitize in front of it.
		if data, err := l.Load(context.Background(), p); err == nil {
			t.Errorf("Load(%q) = %q", p, data)
		}
	}
	// Names that only look like traversal are files.
	for name, want := range map[string]string{"..hidden": "dots", "a..b": "dots in the middle", "./sub/../ok.csv": "a,b\n1,2\n"} {
		s, err := l.Sanitize(context.Background(), name)
		if err != nil {
			t.Errorf("Sanitize(%q): %v", name, err)
			continue
		}
		if data, err := l.Load(context.Background(), s); err != nil || string(data) != want {
			t.Errorf("Load(%q) = %q, %v", name, data, err)
		}
	}
}

// A path is not a URL: a name with a colon in its first segment (a time such
// as 22:48, say) is a relative file name, as it is to a browser and to the
// loader of the reference implementation, not a malformed URL. Only a real
// scheme (a letter, then letters, digits, + - . and a colon) is refused.
func TestFileLoaderColonInName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a colon is not valid in a Windows file name")
	}
	dir := t.TempDir()
	for _, name := range []string{"22:48", "2:30pm.csv", "1a:b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, name := range []string{"22:48", "2:30pm.csv", "./22:48", "1a:b"} {
		s, err := l.Sanitize(context.Background(), name)
		if err != nil {
			t.Errorf("Sanitize(%q): %v", name, err)
			continue
		}
		if data, err := l.Load(context.Background(), s); err != nil || string(data) != "ok" {
			t.Errorf("Load(%q) = %q, %v", name, data, err)
		}
	}
	for _, name := range []string{"a:b", "ab+c.d-e:f", "mailto:x@example.com", "tel:123"} {
		if _, err := l.Sanitize(context.Background(), name); err == nil {
			t.Errorf("Sanitize(%q) accepted a scheme", name)
		}
	}
}

// Errors name what the specification asked for, not where the server keeps it.
func TestFileLoaderErrorsDoNotLeakPaths(t *testing.T) {
	dir, secret := fileTree(t)
	if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	l, err := NewFileLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, p := range []string{"missing.csv", "sub", "link", "../secret.txt", "sub/missing", "ok.csv/x"} {
		_, err := l.Load(context.Background(), p)
		if err == nil {
			t.Errorf("Load(%q) succeeded", p)
			continue
		}
		for _, leak := range []string{dir, filepath.Dir(dir), secret} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("Load(%q) error leaks %q: %v", p, leak, err)
			}
		}
	}
	// A directory that cannot be opened is a configuration error.
	bad := &FileLoader{BaseDir: filepath.Join(dir, "does-not-exist")}
	if _, err := bad.Load(context.Background(), "x"); err == nil || strings.Contains(err.Error(), dir) {
		t.Errorf("unopenable base: err = %v", err)
	}
}
