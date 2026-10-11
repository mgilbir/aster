package aster_test

import (
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// docs/architecture.md lists every package in a layer; each may import only
// packages of lower layers (tools may import anything).
func TestPackageLayers(t *testing.T) {
	b, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i, j := strings.Index(doc, "<!-- layers:begin"), strings.Index(doc, "<!-- layers:end")
	if i < 0 || j < i {
		t.Fatal("docs/architecture.md has no layers:begin/end block")
	}
	const tool = 1 << 30
	layer := map[string]int{}
	for _, line := range strings.Split(doc[i:j], "\n") {
		name, pkgs, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(name, "<!--") {
			continue
		}
		n := tool
		if name != "tools" {
			if n, err = strconv.Atoi(name); err != nil {
				t.Fatalf("layer %q: %v", name, err)
			}
		}
		for _, p := range strings.Fields(pkgs) {
			layer[p] = n
		}
	}

	const module = "github.com/mgilbir/aster"
	seen := 0
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if path != "." && path != "internal" && !strings.HasPrefix(path, "internal/") {
			return fs.SkipDir
		}
		if d.Name() == "testdata" {
			return fs.SkipDir
		}
		pkg, err := build.ImportDir(path, 0)
		if err != nil {
			return nil // no Go files here
		}
		name := strings.TrimPrefix(path, "internal/")
		if path == "." {
			name = "aster"
		}
		l, ok := layer[name]
		if !ok {
			t.Errorf("package %s is not in docs/architecture.md's layers", name)
			return nil
		}
		seen++
		for _, imp := range pkg.Imports {
			dep, ok := strings.CutPrefix(imp, module+"/internal/")
			if !ok {
				continue
			}
			if dl, ok := layer[dep]; !ok || (l != tool && dl >= l) {
				t.Errorf("%s (layer %d) imports %s (layer %d): imports must go down a layer", name, l, dep, dl)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(layer) {
		t.Errorf("docs/architecture.md lists %d packages, %d exist", len(layer), seen)
	}
}
