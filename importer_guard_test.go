package aster_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A test that type-checks source uses testutil.SourceImporter: the source
// importer itself runs cgo where the test binary is cross-built (linux/386 on
// amd64) and fails there, which only the 386 gate shows.
func TestSourceImporterIsTestutils(t *testing.T) {
	raw := regexp.MustCompile(`importer\.(?:ForCompiler|For)\(.*"source"`)
	seen := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" || d.Name()[0] == '.' && path != "." {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || path == filepath.Join("internal", "testutil", "importer.go") || path == "importer_guard_test.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		seen++
		if raw.Match(b) {
			t.Errorf(`%s: importer.ForCompiler(…, "source", …): use testutil.SourceImporter`, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 100 {
		t.Fatalf("read %d Go files", seen)
	}
}
