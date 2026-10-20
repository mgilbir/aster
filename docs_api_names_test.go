package aster_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// A sentinel error or an option the user documentation names in code is one
// package aster exports: a name it does not export sends the reader after an
// identifier that is not there (C146, audit 2026-10-18: the v0.2.0 notes
// announced an ErrPrivateAddress that is internal). The audits and the
// history record the tree as it was, so they are not read.
func TestDocsNameExportedAPI(t *testing.T) {
	exported := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range trackedFiles(t, "*.go") {
		if strings.Contains(f, "/") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					exported[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.ValueSpec:
						for _, n := range s.Names {
							exported[n.Name] = true
						}
					case *ast.TypeSpec:
						exported[s.Name.Name] = true
					}
				}
			}
		}
	}
	// `ErrLimit`, `aster.ErrClosed`, `WithTimeout(...)`, `aster.New`: a name
	// in the package's own style, alone in its code span.
	name := regexp.MustCompile("`(?:aster\\.)?((?:Err|With)[A-Z]\\w*)(?:\\([^`]*\\))?`")
	n := 0
	for _, doc := range trackedFiles(t, "*.md") {
		if strings.HasPrefix(doc, "docs/audits/") || strings.HasPrefix(doc, "docs/history/") {
			continue
		}
		prose, _ := splitMarkdown(readDoc(t, doc))
		for _, m := range name.FindAllStringSubmatch(prose, -1) {
			n++
			if !exported[m[1]] {
				t.Errorf("%s names %s, which package aster does not export", doc, m[1])
			}
		}
	}
	if n < 100 {
		t.Errorf("checked %d names in the docs, expected at least 100", n)
	}
}
