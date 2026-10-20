package aster

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	subprocessInputs(t, "scripts/recursion.allow")
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

// subprocessInputs makes what a test's subprocess reads inputs of the test:
// go test caches a passing result by the files the test process itself opens,
// and a subprocess's reads are not seen, so an edit to the allowlist (or to a
// source file of a package the test binary does not link, the audit tool
// itself among them) would otherwise be answered "ok (cached)" (C75, audit
// 2026-10-18). Reading the files and listing every directory of Go sources
// records them.
func subprocessInputs(t *testing.T, files ...string) {
	t.Helper()
	for _, f := range files {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if path != "." && (d.Name() == "testdata" || d.Name()[0] == '.' || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		_, err = os.ReadDir(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
