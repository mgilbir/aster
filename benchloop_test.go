package aster_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Every benchmark measures only the work it names. A `for i := 0; i < b.N;
// i++` loop (or `for range b.N`) times everything since the benchmark began,
// so setup before it, building the input or a Converter, is measured with it
// and spread over b.N. Such a loop must have b.ResetTimer() between the setup
// and the loop; `for b.Loop()` needs nothing, it times only its body. The
// check walks every tracked test file, each function or closure that takes a
// *testing.B, and the statements before each b.N loop in its body.
func TestBenchmarksTimeOnlyTheLoop(t *testing.T) {
	files := trackedFiles(t, "*_test.go")
	fset := token.NewFileSet()
	loops := 0
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var typ *ast.FuncType
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				typ, body = fn.Type, fn.Body
			case *ast.FuncLit:
				typ, body = fn.Type, fn.Body
			default:
				return true
			}
			if body == nil {
				return true
			}
			for _, b := range benchParams(typ) {
				loops += checkBenchBlock(t, fset, b, body.List)
			}
			return true
		})
	}
	// A floor, so that a walk that finds nothing fails.
	if loops < 20 {
		t.Errorf("found %d b.N loops in %d test files, expected at least 20", loops, len(files))
	}
}

// benchParams names the *testing.B parameters of a function.
func benchParams(typ *ast.FuncType) []string {
	var out []string
	for _, p := range typ.Params.List {
		star, ok := p.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "B" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
			continue
		}
		for _, name := range p.Names {
			out = append(out, name.Name)
		}
	}
	return out
}

// checkBenchBlock reports a b.N loop in stmts (looking into nested blocks)
// that setup precedes with no b.ResetTimer() after it, and returns how many
// b.N loops it saw. Setup is any statement other than a call of b's
// bookkeeping methods (ReportAllocs, SetBytes, Helper, Skip, ...).
func checkBenchBlock(t *testing.T, fset *token.FileSet, b string, stmts []ast.Stmt) int {
	t.Helper()
	loops := 0
	setup := token.NoPos // the first setup statement since the last ResetTimer
	stopped := false     // between StopTimer and StartTimer: off the clock
	for _, s := range stmts {
		if isBNLoop(s, b) {
			loops++
			if setup.IsValid() {
				t.Errorf("%s: a b.N loop times the setup at %s; call %s.ResetTimer() before the loop, or loop with `for %s.Loop()`",
					fset.Position(s.Pos()), fset.Position(setup), b, b)
			}
			continue
		}
		switch m := benchMethod(s, b); m {
		case "ResetTimer":
			setup = token.NoPos
			continue
		case "StopTimer":
			stopped = true
			continue
		case "StartTimer":
			stopped = false
			continue
		case "ReportAllocs", "SetBytes", "Helper", "Skip", "Skipf", "SkipNow", "ReportMetric", "Cleanup", "SetParallelism":
			continue
		}
		// A b.N loop nested in a block (an if, a closure called in place)
		// sees the setup before the block as well as the block's own.
		if inner := nestedBlocks(s); inner != nil && usesBN(s, b) {
			for _, blk := range inner {
				before := setup
				loops += checkBenchBlockFrom(t, fset, b, blk, before)
			}
			continue
		}
		if !setup.IsValid() && !stopped {
			setup = s.Pos()
		}
	}
	return loops
}

// checkBenchBlockFrom is checkBenchBlock with setup already pending.
func checkBenchBlockFrom(t *testing.T, fset *token.FileSet, b string, stmts []ast.Stmt, setup token.Pos) int {
	t.Helper()
	if !setup.IsValid() {
		return checkBenchBlock(t, fset, b, stmts)
	}
	// Pretend the pending setup is the block's first statement.
	return checkBenchBlock(t, fset, b, append([]ast.Stmt{&ast.EmptyStmt{Semicolon: setup}}, stmts...))
}

// isBNLoop reports whether s is a loop whose trip count is b.N: its condition
// (or what it ranges over) refers to b.N. A loop that only reads b.N in its
// body (reporting a metric per op) is not one.
func isBNLoop(s ast.Stmt, b string) bool {
	switch s := s.(type) {
	case *ast.ForStmt:
		return s.Cond != nil && usesBN(s.Cond, b)
	case *ast.RangeStmt:
		return usesBN(s.X, b)
	}
	return false
}

// nestedBlocks are the statement lists directly inside s that run when s
// does: the branches of an if, a bare block.
func nestedBlocks(s ast.Stmt) [][]ast.Stmt {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return [][]ast.Stmt{s.List}
	case *ast.IfStmt:
		out := [][]ast.Stmt{s.Body.List}
		if s.Else != nil {
			out = append(out, nestedBlocks(s.Else)...)
		}
		return out
	}
	return nil
}

// benchMethod is the name of the method of b that s calls, if s is such a call.
func benchMethod(s ast.Stmt, b string) string {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return ""
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == b {
		return sel.Sel.Name
	}
	return ""
}

// usesBN reports whether n refers to b.N outside any function literal (a
// closure has its own *testing.B, or is not the benchmark's loop).
func usesBN(n ast.Node, b string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name == b && n.Sel.Name == "N" {
				found = true
			}
		}
		return true
	})
	return found
}
