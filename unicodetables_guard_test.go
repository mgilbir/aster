package aster

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// golang.org/x/text keeps Unicode tables of its own, several of them chosen by
// the Go version that builds it (cases and unicode/norm have one set for each
// Unicode version Go ships; Go 1.27, the module's minimum, ships Unicode 17,
// the pinned node's, and a later Go may ship 18 while node stays on 17).
// Text the engine maps with them is mapped by the toolchain's Unicode, not by
// the pinned node's, so the engine would answer differently by the Go that
// built it (audit 2026-10-18: U+019B.toUpperCase() under Go 1.26). JavaScript's
// case mapping is jsval's tables, recorded from node (internal/jsval/casetables.go,
// testdata/gen_casetables.mjs). This test holds the engine, every package of
// it and not only those that read a specification, to not importing the
// module at all: a use that is not JavaScript's text semantics is to be
// listed here, with why, once there is one.
var xTextAllowed = map[string]string{
	"internal/text/system.go:golang.org/x/text/encoding/charmap": "decodes Mac OS Roman font names in a system font's name table: a fixed legacy encoding whose table no Unicode version changes, and no JavaScript reads",
}

func TestNoXTextInTheEngine(t *testing.T) {
	tools := toolPackages(t)
	fset := token.NewFileSet()
	scanned := 0
	found := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path != "." && !strings.HasPrefix(path, "internal") {
				return fs.SkipDir
			}
			for _, p := range tools {
				if filepath.ToSlash(path) == p || strings.HasPrefix(filepath.ToSlash(path), p+"/") {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		scanned++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p != "golang.org/x/text" && !strings.HasPrefix(p, "golang.org/x/text/") {
				continue
			}
			key := filepath.ToSlash(path) + ":" + p
			found[key] = true
			if _, ok := xTextAllowed[key]; !ok {
				t.Errorf("%s: imports %s, whose Unicode tables follow the Go toolchain's version, not node's: use jsval.JSLower and JSUpper for case mapping (or list it, with why, in xTextAllowed)", fset.Position(imp.Pos()), p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 250 {
		t.Fatalf("only %d files scanned: the scan is broken", scanned)
	}
	for k := range xTextAllowed {
		if !found[k] {
			t.Errorf("xTextAllowed: %s is no longer used; remove it", k)
		}
	}
}
