package aster

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestNoUnreviewedFusedMultiplyAdd runs scripts/fmacheck.sh: on arm64 the Go
// compiler fuses x*y+z into one rounding, which JavaScript never does, so
// every fused site under internal/ must be rounded explicitly or allowlisted.
func TestNoUnreviewedFusedMultiplyAdd(t *testing.T) {
	if testing.Short() {
		t.Skip("rebuilds the engine for arm64 (-a); skipped with -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out, err := exec.Command("sh", "scripts/fmacheck.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("fmacheck.sh: %v\n%s", err, out)
	}
}

// fmacheck.sh must not pass by checking nothing (C69): with -trimpath in
// GOFLAGS (which rewrites the positions it matches) and an allowlist holding
// one entry no site matches, it must still find every site and fail on the
// stale entry alone.
func TestFusedMultiplyAddCheckFailsOnStaleEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("rebuilds the engine for arm64 (-a); skipped with -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	allow, err := os.ReadFile("scripts/fmacheck.allow")
	if err != nil {
		t.Fatal(err)
	}
	const planted = "internal/geo/fit.go\tgeo.planted\tx := planted + stale"
	path := filepath.Join(t.TempDir(), "fmacheck.allow")
	if err := os.WriteFile(path, append(allow, planted+"\t# planted by the test\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "scripts/fmacheck.sh", "-allow", path)
	cmd.Env = append(os.Environ(), "GOFLAGS=-trimpath")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("fmacheck.sh with a stale entry: %v, want exit status 1\n%s", err, out)
	}
	if !strings.Contains(string(out), "stale allowlist entries") || !strings.Contains(string(out), planted) {
		t.Errorf("the planted entry is not reported as stale:\n%s", out)
	}
	if n := strings.Count(string(out), "\n  internal/"); n != 1 {
		t.Errorf("%d entries reported, want the planted one only:\n%s", n, out)
	}
}

// fmacheck.sh's comment says which of internal/raster's files it checks (the
// filter primitives, which port resvg's arithmetic product by product), and
// its awk regex decides it: the two must name the same files (C148, audit
// 2026-10-18: the comment said filter*.go, the regex named three of them).
func TestFusedMultiplyAddCheckRasterFilesAsDocumented(t *testing.T) {
	b, err := os.ReadFile("scripts/fmacheck.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	// The comment's list, across its "# " line breaks.
	var comment strings.Builder
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, "#") {
			comment.WriteString(strings.TrimPrefix(strings.TrimPrefix(l, "#"), " ") + " ")
		}
	}
	m := regexp.MustCompile(`filter primitives \(([^)]*)\)`).FindStringSubmatch(comment.String())
	if m == nil {
		t.Fatal("fmacheck.sh's comment no longer lists the raster files it checks; reword the test with the script")
	}
	var globs []string
	for _, g := range strings.Split(m[1], ",") {
		globs = append(globs, strings.TrimSpace(g))
	}
	r := regexp.MustCompile(`rel !~ /(\^internal\\/raster\\/[^ ]*\$)/`).FindStringSubmatch(script)
	if r == nil {
		t.Fatal("fmacheck.sh has no raster exception regex; reword the test with the script")
	}
	re, err := regexp.Compile(strings.ReplaceAll(r[1], `\/`, `/`))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("internal/raster/*.go")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		name := filepath.Base(f)
		documented := false
		for _, g := range globs {
			if ok, _ := filepath.Match(g, name); ok {
				documented, used[g] = true, true
			}
		}
		if checked := re.MatchString("internal/raster/" + name); checked != documented {
			t.Errorf("internal/raster/%s: fmacheck.sh checks it: %v; its comment says %v", name, checked, documented)
		}
	}
	for _, g := range globs {
		if !used[g] {
			t.Errorf("fmacheck.sh's comment names %s, which matches no file of internal/raster", g)
		}
	}
}
