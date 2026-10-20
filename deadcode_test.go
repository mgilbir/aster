package aster_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
	"testing"
)

// The root package's unexported functions, variables and constants are each
// used by the package's own code: one the tests alone use belongs in a test
// file (C140: two aliases of PNG helpers lived in pngopt.go for the tests).
func TestRootHelpersAreUsedByTheLibrary(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range trackedFiles(t, "*.go") {
		if path.Dir(p) != "." || strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	declared := map[string]token.Pos{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && !d.Name.IsExported() && d.Name.Name != "init" {
					declared[d.Name.Name] = d.Name.Pos()
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, s := range d.Specs {
					for _, n := range s.(*ast.ValueSpec).Names {
						if !n.IsExported() && n.Name != "_" {
							declared[n.Name] = n.Pos()
						}
					}
				}
			}
		}
	}
	used := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && declared[id.Name] != id.Pos() {
				used[id.Name] = true
			}
			return true
		})
	}
	for name, pos := range declared {
		if !used[name] {
			t.Errorf("%s: %s is used only by tests: move it to a test file", fset.Position(pos), name)
		}
	}
}
