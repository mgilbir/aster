package vegalite

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Upstream compares a specification's keyword (a mark type, a scale type,
// an orient, config.fieldTitle) strictly, with ===, a switch, Set.has or
// indexOf, so the array ["log"] names no scale type there; it coerces the
// keyword to its text only where it reads a property with it
// (SCALE_CATEGORY_INDEX[type], config[mark]). Value.AsString() is that
// coercion, so a comparison of its text with a name answers as upstream's
// property lookup, never as its ===: `"type": ["log"]` drew a log scale
// (C40, C91 of the 2026-10-18 audit). This test holds the package to
// choosing: a Value's text (AsString(), directly, through a local variable
// or a function that returns it) must not be an operand of == or !=, a
// switch's tag, or the argument of a function that compares its string
// parameter so (contains, indexOfStr, a predicate on a mark name, found to a
// fixpoint over the package's functions). Compare with strIs, the keyword
// views (Value.Strict, StringIs) or a predicate on the Value, or list the
// site in testdata/keyword-text.allow with why upstream reads the text there
// (a template literal, String(x) === …, a property key).
func TestKeywordsAreNotComparedAsText(t *testing.T) {
	allow := readKeywordAllow(t, "testdata/keyword-text.allow")
	found := map[string]int{}
	where := map[string][]string{}
	for _, f := range scanKeywordText(t, ".") {
		found[f.key]++
		where[f.key] = append(where[f.key], f.pos+": "+f.what)
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n, ok := allow[k]
		switch {
		case !ok:
			t.Errorf("%s compares a value's text where upstream compares the value (use strIs, Value.Strict or a predicate on the Value, or list it in testdata/keyword-text.allow with why):\n\t%s", k, strings.Join(where[k], "\n\t"))
		case n != found[k]:
			t.Errorf("testdata/keyword-text.allow: %s lists %d, found %d:\n\t%s", k, n, found[k], strings.Join(where[k], "\n\t"))
		}
	}
	for k := range allow {
		if found[k] == 0 {
			t.Errorf("testdata/keyword-text.allow: %s is stale; remove it", k)
		}
	}
}

// TestKeywordGuardFindsTheClass checks the scan on planted code: each form
// of the class is found, and the strict forms are not.
func TestKeywordGuardFindsTheClass(t *testing.T) {
	dir := t.TempDir()
	src := `package vegalite

func direct(v Value) bool { return v.Get("type").AsString() == "log" }
func local(v Value) bool {
	t := v.Get("type").AsString()
	return t != "band"
}
func sw(v Value) int {
	switch v.AsString() {
	case "a":
		return 1
	}
	return 0
}
func viaResult(v Value) bool { return typeText(v) == "x" }
func typeText(v Value) string { return v.Get("type").AsString() }
func isBar(m string) bool { return m == "bar" }
func viaParam(v Value) bool { return isBar(v.AsString()) }
func viaParam2(v Value) bool { return isBarToo(v.AsString()) }
func isBarToo(m string) bool { return isBar(m) }
func viaContains(v Value) bool { return contains([]string{"a"}, v.AsString()) }
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
func fine(v Value) bool { return strIs(v, "log") || v.AsString()+"x" != "" == false }
func key(m map[string]int, v Value) int { return m[v.AsString()] }
`
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range scanKeywordText(t, dir) {
		got[f.key] = true
	}
	for _, want := range []string{"direct", "local", "sw", "viaResult", "viaParam", "viaParam2", "viaContains"} {
		if !got["planted.go:"+want] {
			t.Errorf("the scan misses %s (found %v)", want, got)
		}
	}
	for _, not := range []string{"fine", "key", "isBar", "contains", "typeText", "isBarToo"} {
		if got["planted.go:"+not] {
			t.Errorf("the scan flags %s, which compares no value's text", not)
		}
	}
}

// propertyKeyParams are the parameters (by position) of the package's
// functions that take a property key: the name upstream reads or writes a
// plain object or one of its tables with (obj[key], `key in obj`), which
// JavaScript coerces to its text, so passing a value's text is upstream's
// reading. A comparison inside them is of keys, not of keywords.
var propertyKeyParams = map[string][]int{
	"jsSeen":                {2},    // `v in u` of util.unique's plain object
	"jsIn":                  {1},    // the in operator
	"jsProp":                {1},    // obj[key]
	"jsAssign":              {2},    // obj[key] = v
	"jsHasKey":              {2},    // `key in obj`
	"assign":                {0},    // omap: obj[key] = v
	"getWithExplicit":       {0},    // a split component's property
	"measField":             {2},    // meas[field]
	"measureSet":            {2, 3}, // meas[field][op]
	"supportMark":           {1},    // getSupportedMark(channel)[mark]
	"inheritedValue":        {0},    // a table's inherited member
	"inheritedPropertyText": {0},    // likewise
	"isInheritedKey":        {0},    // `key in TABLE` for a name every object inherits
	"channelOrUndefined":    {1},    // TABLE[align]
	"jsKey":                 {0},    // a computed property name
	"isScaleChannel":        {1},    // SCALE_CHANNEL_INDEX[channel]
	"isGeoPositionChannel":  {0},    // `channel in GEOPOSITION_CHANNEL_INDEX`
}

type keywordFinding struct{ key, pos, what string }

// scanKeywordText lists the comparisons of a value's text in the non-test
// Go files of dir.
func scanKeywordText(t *testing.T, dir string) []keywordFinding {
	t.Helper()
	fset := token.NewFileSet()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	type fnInfo struct {
		decl   *ast.FuncDecl
		file   string
		params []string // the names of its string parameters, by position ("" otherwise)
	}
	var fns []*fnInfo
	byName := map[string][]*fnInfo{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			// A package variable's function literals (a table of rules,
			// the selection compilers) are scanned as functions named
			// after the variable.
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for _, v := range vs.Values {
						ast.Inspect(v, func(n ast.Node) bool {
							if fl, ok := n.(*ast.FuncLit); ok {
								fd := &ast.FuncDecl{Name: vs.Names[0], Type: fl.Type, Body: fl.Body}
								fns = append(fns, &fnInfo{decl: fd, file: filepath.Base(path)})
								return false
							}
							return true
						})
					}
				}
				continue
			}
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fi := &fnInfo{decl: fd, file: filepath.Base(path)}
			for _, fld := range fd.Type.Params.List {
				isStr := false
				if id, ok := fld.Type.(*ast.Ident); ok && id.Name == "string" {
					isStr = true
				}
				if len(fld.Names) == 0 {
					fi.params = append(fi.params, "")
				}
				for _, n := range fld.Names {
					if isStr {
						fi.params = append(fi.params, n.Name)
					} else {
						fi.params = append(fi.params, "")
					}
				}
			}
			fns = append(fns, fi)
			byName[fd.Name.Name] = append(byName[fd.Name.Name], fi)
		}
	}
	// callee resolves a call to the package's function or method of that
	// name, when the name is unique (a method is matched by name alone).
	callee := func(c *ast.CallExpr) *fnInfo {
		var name string
		switch fun := c.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		default:
			return nil
		}
		if l := byName[name]; len(l) == 1 {
			return l[0]
		}
		return nil
	}
	textResult := map[*fnInfo]bool{} // returns a value's text
	strictParam := map[*fnInfo]map[int]bool{}
	isKeyParam := func(fi *fnInfo, i int) bool {
		for _, k := range propertyKeyParams[fi.decl.Name.Name] {
			if k == i {
				return true
			}
		}
		return false
	}
	// isText reports whether e is a value's text: an AsString() call, a
	// local assigned one, or a call of a function that returns one.
	var isText func(e ast.Expr, locals map[string]bool) bool
	isText = func(e ast.Expr, locals map[string]bool) bool {
		switch x := e.(type) {
		case *ast.ParenExpr:
			return isText(x.X, locals)
		case *ast.Ident:
			return locals[x.Name]
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "AsString" && len(x.Args) == 0 {
				return true
			}
			if fi := callee(x); fi != nil && textResult[fi] {
				return true
			}
		}
		return false
	}
	// localTexts lists the locals of fn assigned a value's text.
	localTexts := func(fn *ast.FuncDecl) map[string]bool {
		locals := map[string]bool{}
		for changed := true; changed; {
			changed = false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != len(as.Rhs) {
					return true
				}
				for i, l := range as.Lhs {
					if id, ok := l.(*ast.Ident); ok && !locals[id.Name] && isText(as.Rhs[i], locals) {
						locals[id.Name] = true
						changed = true
					}
				}
				return true
			})
		}
		return locals
	}
	isCompared := func(fi *fnInfo, name string) bool {
		hit := false
		ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if x.Op == token.EQL || x.Op == token.NEQ {
					for _, o := range []ast.Expr{x.X, x.Y} {
						if id, ok := o.(*ast.Ident); ok && id.Name == name {
							hit = true
						}
					}
				}
			case *ast.SwitchStmt:
				if id, ok := x.Tag.(*ast.Ident); ok && id.Name == name {
					hit = true
				}
			case *ast.CallExpr:
				if c := callee(x); c != nil {
					for i, a := range x.Args {
						if id, ok := a.(*ast.Ident); ok && id.Name == name && strictParam[c][i] {
							hit = true
						}
					}
				}
			}
			return true
		})
		return hit
	}
	// The summaries, to a fixpoint.
	for changed := true; changed; {
		changed = false
		for _, fi := range fns {
			locals := localTexts(fi.decl)
			if !textResult[fi] {
				ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
					if _, ok := n.(*ast.FuncLit); ok {
						return false
					}
					if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 1 && isText(r.Results[0], locals) {
						textResult[fi] = true
						changed = true
					}
					return true
				})
			}
			for i, p := range fi.params {
				if p == "" || strictParam[fi][i] || isKeyParam(fi, i) {
					continue
				}
				if isCompared(fi, p) {
					if strictParam[fi] == nil {
						strictParam[fi] = map[int]bool{}
					}
					strictParam[fi][i] = true
					changed = true
				}
			}
		}
	}
	var out []keywordFinding
	for _, fi := range fns {
		locals := localTexts(fi.decl)
		key := fi.file + ":" + fi.decl.Name.Name
		add := func(n ast.Node, what string) {
			out = append(out, keywordFinding{key, fset.Position(n.Pos()).String(), what})
		}
		ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if (x.Op == token.EQL || x.Op == token.NEQ) && (isText(x.X, locals) || isText(x.Y, locals)) {
					add(x, "text "+x.Op.String())
				}
			case *ast.SwitchStmt:
				if x.Tag != nil && isText(x.Tag, locals) {
					add(x, "switch on text")
				}
			case *ast.CallExpr:
				if c := callee(x); c != nil {
					for i, a := range x.Args {
						if strictParam[c][i] && isText(a, locals) {
							add(x, fmt.Sprintf("text to %s's compared parameter %d", c.decl.Name.Name, i))
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// readKeywordAllow reads the allowlist: "file.go:func count  # why" lines.
func readKeywordAllow(t *testing.T, path string) map[string]int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		body, why, ok := strings.Cut(s, "#")
		fields := strings.Fields(body)
		if !ok || strings.TrimSpace(why) == "" || len(fields) != 2 {
			t.Fatalf("%s:%d: want \"file.go:func count  # why\"", path, line)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("%s:%d: %v", path, line, err)
		}
		out[fields[0]] = n
	}
	return out
}
