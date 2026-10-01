package aster

import (
	"os/exec"
	"testing"
)

// TestRecursionsAreBounded runs internal/cmd/recursionaudit: every recursive
// function in the engine must be listed in scripts/recursion.allow with how
// its depth is bounded, since a stack overflow in Go cannot be recovered.
func TestRecursionsAreBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module; skipped with -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.Command("go", "run", "./internal/cmd/recursionaudit").CombinedOutput()
	if err != nil {
		t.Fatalf("recursionaudit: %v\n%s", err, out)
	}
}
