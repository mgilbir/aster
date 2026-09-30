package aster

import (
	"os/exec"
	"runtime"
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
