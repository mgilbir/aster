package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The manifest names exactly the files the command fetches, and the vendored
// copies are the bytes it lists (C90).
func TestManifestMatchesVendoredFiles(t *testing.T) {
	dir := filepath.Join("..", "..", outDir)
	manifest, err := readManifest(filepath.Join(dir, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkListed(manifest); err != nil {
		t.Fatal(err)
	}
	for _, f := range dataFiles {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := verify(manifest, f, data); err != nil {
			t.Error(err)
		}
	}
	// A changed byte is refused.
	data, _ := os.ReadFile(filepath.Join(dir, dataFiles[0]))
	data = append(data, ' ')
	if verify(manifest, dataFiles[0], data) == nil {
		t.Error("a changed file passed verification")
	}
}

// Run where there is no manifest (outside the repository root), the command
// fails before it writes anything, a directory included (C147, audit
// 2026-10-18: it left an empty data directory behind).
func TestNoManifestWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	err := run(dir, func(string) ([]byte, error) {
		t.Fatal("downloaded without a manifest")
		return nil, nil
	})
	if err == nil {
		t.Fatal("no error without a manifest")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s was created (%v)", dir, err)
	}
}

// dataFiles is the data the example specs name by a relative URL, all of it
// and nothing else.
func TestDataFilesAreTheExamples(t *testing.T) {
	specs, err := filepath.Glob(filepath.Join("..", "..", examplesDir, "*.vl.json"))
	if err != nil || len(specs) == 0 {
		t.Fatalf("%s: %d specs (%v)", examplesDir, len(specs), err)
	}
	url := regexp.MustCompile(`"url"\s*:\s*"(data/[^"]+)"`)
	named := map[string]bool{}
	for _, f := range specs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range url.FindAllStringSubmatch(string(b), -1) {
			named[m[1]] = true
		}
	}
	listed := map[string]bool{}
	for _, f := range dataFiles {
		listed[f] = true
		if !named[f] {
			t.Errorf("dataFiles lists %s, which no example under %s names", f, examplesDir)
		}
	}
	for f := range named {
		if !listed[f] {
			t.Errorf("an example under %s names %s, which dataFiles does not list", examplesDir, f)
		}
	}
}
