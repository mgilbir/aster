package main

import (
	"os"
	"path/filepath"
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
