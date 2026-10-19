package aster

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The walks of a value (its String, JSON, stringValue, a deep comparison, a
// relational comparison of arrays) are bounded by the call that makes them
// only when they are made with the call's jsval.Walker (ADR 0018): the
// detached forms (Value.AsString, jsval.Equal, Relate, StringValue,
// AppendJSON, AppendJSONIndent, and a zero jsval.Walker) are bounded by fixed
// caps instead, which are no bound on a call that makes many of them. So in
// the engine's run-time packages a detached walk is allowed only where the
// value is not one an expression or a transform can have built: a
// specification's own literal read at parse time (tree-shaped, its size the
// specification's), or a value whose kind the code has just checked to be
// no array (no walk at all). Each such function is listed in
// scripts/walk.allow with the number of detached walks it makes and why
// they are safe; a new one, a changed count, or a stale entry fails, so
// every detached walk in the engine has been looked at.
//
// Not scanned: internal/jsval, which defines the walks; internal/vegalite,
// which compiles a specification's literals (its walks of them are bounded
// by the specification's size and metered by the compiler's work); and the
// tools (commands, the oracle, the upstream replays, test support), which
// see no untrusted value.
func TestEngineWalksUseTheCallsWalker(t *testing.T) {
	allow, err := readWalkAllow("scripts/walk.allow")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	where := map[string][]string{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path != "." && !strings.HasPrefix(path, "internal") || !walkScanned(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		jsval := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/mgilbir/aster/internal/jsval" {
				jsval = "jsval"
				if imp.Name != nil {
					jsval = imp.Name.Name
				}
			}
		}
		pkg := filepath.Dir(path)
		if pkg == "." {
			pkg = "aster"
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := pkg + "." + funcName(fn)
			ast.Inspect(fn, func(n ast.Node) bool {
				if detachedWalk(n, jsval) {
					found[name]++
					where[name] = append(where[name], fset.Position(n.Pos()).String())
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 20 {
		t.Fatalf("only %d functions with detached walks found: the scan is broken", len(found))
	}
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a, ok := allow[n]
		switch {
		case !ok:
			t.Errorf("%s makes %d detached walks of a value (%s): walk it with the call's jsval.Walker, or list it in scripts/walk.allow with why the value cannot be one a run built", n, found[n], strings.Join(where[n], ", "))
		case a.count != found[n]:
			t.Errorf("%s makes %d detached walks, scripts/walk.allow says %d (%s): review the new ones and update the count", n, found[n], a.count, strings.Join(where[n], ", "))
		}
	}
	for n := range allow {
		if _, ok := found[n]; !ok {
			t.Errorf("scripts/walk.allow: %s makes no detached walk any more; remove it", n)
		}
	}
}

// walkScanned reports whether the directory path holds a run-time package of
// the engine.
func walkScanned(path string) bool {
	for _, skip := range []string{"internal/jsval", "internal/vegalite", "internal/cmd", "internal/oracle", "internal/upstream", "internal/svgdiff", "internal/svgcheck", "internal/fuzzutil", "internal/testutil"} {
		if path == skip || strings.HasPrefix(path, skip+"/") {
			return false
		}
	}
	return true
}

// detachedWalk reports whether n is a walk without the call's Walker:
// v.AsString (called, or as a method value or expression), one of jsval's
// package-level walks (called or passed as a function), or a zero
// jsval.Walker made to walk with.
func detachedWalk(n ast.Node, jsval string) bool {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		if n.Sel.Name == "AsString" {
			return true
		}
		if id, ok := n.X.(*ast.Ident); ok && jsval != "" && id.Name == jsval {
			switch n.Sel.Name {
			case "Equal", "Relate", "StringValue", "AppendJSON", "AppendJSONIndent":
				return true
			}
		}
	case *ast.CallExpr:
		sel, ok := n.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "WalkerFor" || len(n.Args) != 1 {
			return false
		}
		if id, ok := sel.X.(*ast.Ident); ok && jsval != "" && id.Name == jsval {
			if a, ok := n.Args[0].(*ast.Ident); ok && a.Name == "nil" {
				return true
			}
		}
	case *ast.CompositeLit:
		if sel, ok := n.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Walker" {
			if id, ok := sel.X.(*ast.Ident); ok && jsval != "" && id.Name == jsval {
				return true
			}
		}
	}
	return false
}

// funcName is a function's name, a method's with its receiver's type.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	var b strings.Builder
	var typ func(e ast.Expr)
	typ = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.StarExpr:
			b.WriteByte('*')
			typ(e.X)
		case *ast.Ident:
			b.WriteString(e.Name)
		case *ast.IndexExpr:
			typ(e.X)
		case *ast.IndexListExpr:
			typ(e.X)
		}
	}
	b.WriteByte('(')
	typ(fn.Recv.List[0].Type)
	b.WriteString(").")
	b.WriteString(fn.Name.Name)
	return b.String()
}

type walkAllow struct {
	count  int
	reason string
}

// readWalkAllow reads "function<TAB>count<TAB># reason" lines.
func readWalkAllow(path string) (map[string]walkAllow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	m := map[string]walkAllow{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		parts := strings.SplitN(s, "\t", 3)
		if len(parts) != 3 || !strings.HasPrefix(strings.TrimSpace(parts[2]), "# ") {
			return nil, fmt.Errorf("%s:%d: want function<TAB>count<TAB># reason", path, line)
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%s:%d: bad count %q", path, line, parts[1])
		}
		if _, dup := m[parts[0]]; dup {
			return nil, fmt.Errorf("%s:%d: %s listed twice", path, line, parts[0])
		}
		m[parts[0]] = walkAllow{count: n, reason: parts[2]}
	}
	return m, sc.Err()
}
