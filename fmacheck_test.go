package aster

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	subprocessInputs(t, "scripts/fmacheck.sh", "scripts/fmacheck.allow")
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
