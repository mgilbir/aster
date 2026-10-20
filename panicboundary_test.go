package aster_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
	"testing"
)

// The engine's panic boundaries, checked on the source:
//
//   - every goroutine the library starts recovers a panic first thing: no
//     recover of the caller's reaches another goroutine, so a panic there
//     ends the process (C15: imageref.Fetch's did, when a Loader panicked);
//   - in the root package, recover is called only by the two boundaries,
//     callback (the caller's code: Loader, error handler, MarshalJSON) and
//     recoverStage (a stage's own panic, through stageErr, so its message
//     names the stage: C58);
//   - the caller's Loader is called only through those boundaries: the raw
//     cfg.loader is read by New (to check and wrap it) and by closeLoader,
//     and every stage calls c.loader, the wrapped one.
func TestPanicBoundaries(t *testing.T) {
	fset := token.NewFileSet()
	// Test support, not the library: the fuzzing helpers and the node oracle.
	skip := func(p string) bool {
		return strings.HasSuffix(p, "_test.go") || strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "testdata/") ||
			strings.HasPrefix(p, "internal/cmd/") || strings.HasPrefix(p, "internal/fuzzutil/") || strings.HasPrefix(p, "internal/oracle/") ||
			strings.HasPrefix(p, "internal/testutil/")
	}
	goStmts := 0
	for _, p := range trackedFiles(t, "*.go") {
		if skip(p) {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		root := path.Dir(p) == "."
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.GoStmt:
				goStmts++
				lit, ok := n.Call.Fun.(*ast.FuncLit)
				if !ok || !defersRecover(lit.Body) {
					t.Errorf("%s: a goroutine that does not recover a panic first thing (defer func() { if r := recover() … }())", fset.Position(n.Pos()))
				}
			case *ast.FuncDecl:
				if !root || n.Body == nil {
					return true
				}
				name := n.Name.Name
				ast.Inspect(n.Body, func(m ast.Node) bool {
					switch m := m.(type) {
					case *ast.CallExpr:
						if id, ok := m.Fun.(*ast.Ident); ok && id.Name == "recover" && name != "callback" && name != "recoverStage" {
							t.Errorf("%s: recover in %s: recover through callback or recoverStage", fset.Position(m.Pos()), name)
						}
					case *ast.SelectorExpr:
						if x, ok := m.X.(*ast.SelectorExpr); ok && x.Sel.Name == "cfg" && m.Sel.Name == "loader" && name != "New" && name != "closeLoader" {
							t.Errorf("%s: %s reads the caller's Loader unwrapped: call c.loader, which goes through callback", fset.Position(m.Pos()), name)
						}
					}
					return true
				})
			}
			return true
		})
	}
	if goStmts == 0 {
		t.Error("no go statement found: the check reads nothing")
	}
}

// defersRecover reports whether body starts with a deferred function literal
// that calls recover, possibly after other defers.
func defersRecover(body *ast.BlockStmt) bool {
	for _, st := range body.List {
		d, ok := st.(*ast.DeferStmt)
		if !ok {
			return false
		}
		lit, ok := d.Call.Fun.(*ast.FuncLit)
		if !ok {
			continue
		}
		found := false
		ast.Inspect(lit.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "recover" {
					found = true
				}
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}
