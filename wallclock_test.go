package aster_test

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"strings"
	"testing"
)

// No test compares the wall-clock time of a computation with a fixed bound:
// it fails on a loaded runner and under -race, five to ten times slower. A
// deadline or hang guard scales its slack (testutil.Scaled, or a product with
// RaceSlowdown), and a complexity guard counts work at n and 2n instead of
// timing it (a ratio of times flaked on CI runners). The check walks every tracked test file and fails on a
// comparison of time.Since(...) (or of a variable holding it) with a
// constant duration that is not scaled.
func TestNoUnscaledWallClockBounds(t *testing.T) {
	files := trackedFiles(t, "*_test.go")
	fset := token.NewFileSet()
	parsed := map[string][]*ast.File{} // by directory: a package's constants
	for _, p := range trackedFiles(t, "*.go") {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		parsed[path.Dir(p)] = append(parsed[path.Dir(p)], f)
	}
	consts := map[string]map[string]bool{}
	for dir, fs := range parsed {
		consts[dir] = map[string]bool{}
		for _, f := range fs {
			for _, d := range f.Decls {
				if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
					for _, s := range g.Specs {
						for _, n := range s.(*ast.ValueSpec).Names {
							consts[dir][n.Name] = true
						}
					}
				}
			}
		}
	}
	isTest := map[string]bool{}
	for _, p := range files {
		isTest[p] = true
	}
	checked := 0
	for dir, fs := range parsed {
		for _, f := range fs {
			name := fset.Position(f.Pos()).Filename
			if !isTest[name] {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				var body *ast.BlockStmt
				// A benchmark may report a rate (b.ReportMetric of runs over
				// seconds); it asserts nothing, so only its comparisons count.
				bench := false
				if ok {
					body = fn.Body
					bench = strings.HasPrefix(fn.Name.Name, "Benchmark")
				} else if lit, ok := n.(*ast.FuncLit); ok {
					body = lit.Body
				}
				if body == nil {
					return true
				}
				elapsed := elapsedVars(body)
				ast.Inspect(body, func(n ast.Node) bool {
					be, ok := n.(*ast.BinaryExpr)
					if !ok {
						return true
					}
					// A ratio of elapsed times is a timing of the machine,
					// not of the code: complexity guards count work instead.
					if !bench && be.Op == token.QUO && (containsElapsed(be.X, elapsed) || containsElapsed(be.Y, elapsed)) {
						t.Errorf("%s: divides elapsed time (%s); a time ratio depends on the runner, so count work instead",
							fset.Position(be.Pos()), exprText(be))
						return true
					}
					if be.Op != token.GTR && be.Op != token.GEQ && be.Op != token.LSS && be.Op != token.LEQ {
						return true
					}
					for _, side := range [][2]ast.Expr{{be.X, be.Y}, {be.Y, be.X}} {
						if isElapsed(side[0], elapsed) {
							checked++
							if constDuration(side[1], consts[dir]) {
								t.Errorf("%s: compares elapsed time with the fixed bound %s; scale it (testutil.Scaled) or count work",
									fset.Position(be.Pos()), exprText(side[1]))
							}
						}
					}
					return true
				})
				return false
			})
		}
	}
	if checked < 10 {
		t.Errorf("found %d elapsed-time comparisons, expected at least 10: the walk finds nothing", checked)
	}
}

// elapsedVars are the variables of body assigned time.Since(...).
func elapsedVars(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for i, r := range as.Rhs {
				if i < len(as.Lhs) && isSinceCall(r) {
					if id, ok := as.Lhs[i].(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return out
}

func isSinceCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "time" && sel.Sel.Name == "Since"
}

// containsElapsed reports whether e reads an elapsed time anywhere inside it:
// float64(time.Since(t)), d.Seconds() of an elapsed d, and the like.
func containsElapsed(e ast.Expr, vars map[string]bool) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if x, ok := n.(ast.Expr); ok && isElapsed(x, vars) {
			found = true
		}
		return !found
	})
	return found
}

func isElapsed(e ast.Expr, vars map[string]bool) bool {
	if id, ok := e.(*ast.Ident); ok {
		return vars[id.Name]
	}
	return isSinceCall(e)
}

// constDuration reports whether e is built of constants only (literals,
// time's units, the package's constants), with no scaling: no call and no
// race-slowdown factor.
func constDuration(e ast.Expr, consts map[string]bool) bool {
	ok := true
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			ok = false // testutil.Scaled(...), or anything computed
		case *ast.SelectorExpr:
			if id, isID := n.X.(*ast.Ident); !isID || id.Name != "time" {
				ok = false
			}
			return false
		case *ast.Ident:
			if !consts[n.Name] || strings.Contains(strings.ToLower(n.Name), "slowdown") {
				ok = false
			}
		}
		return ok
	})
	return ok
}

func exprText(e ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, token.NewFileSet(), e)
	return b.String()
}
