package aster

import (
	"os"
	"os/exec"
	"regexp"
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
	// docs/testing.md states the number of cycles; it is pinned here, where
	// it is known (C61, audit 2026-10-18: it had drifted twice).
	m := regexp.MustCompile(`ok \((\d+) recursive cycles`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("recursionaudit printed no count:\n%s", out)
	}
	doc, err := os.ReadFile("docs/testing.md")
	if err != nil {
		t.Fatal(err)
	}
	stated := regexp.MustCompile(`in the call graph \(([\d,]+) cycles\)`).FindSubmatch(doc)
	if stated == nil {
		t.Fatal("docs/testing.md no longer states the number of recursion cycles; reword the test with the doc")
	}
	if string(stated[1]) != string(m[1]) {
		t.Errorf("docs/testing.md states %s recursion cycles, recursionaudit finds %s", stated[1], m[1])
	}
}
