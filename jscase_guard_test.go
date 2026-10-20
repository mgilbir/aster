package aster

import (
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

// Go's case mapping and white space are not JavaScript's: strings.ToLower
// maps İ (U+0130) to "i" where toLowerCase gives "i̇", strings.ToUpper misses
// ß -> "SS", and strings.TrimSpace, strings.Fields and unicode.IsSpace count
// U+0085 as space and not U+FEFF. In the packages that read a
// specification's values as upstream does, text is lower-cased, upper-cased
// and trimmed with jsval's JSLower, JSUpper, JSTrim and IsJSSpace (C90,
// audit 2026-10-18); this test holds them to it. The packages that read SVG,
// CSS, fonts and URLs, whose grammars have rules of their own, are not
// scanned, and the uses below are listed with why they stay.
var goCaseOrSpaceAllowed = map[string]string{
	// The wordcloud's font is node-canvas's parse of a CSS font (C, not
	// JavaScript).
	"internal/transforms/wordcloud/canvas.go:canvasFont:strings.TrimSpace": "node-canvas's font parser",
}

func TestNoGoCaseOrSpaceInTheEngine(t *testing.T) {
	// The engine packages that read SVG, CSS, fonts and URLs, and every
	// tool, which docs/architecture.md's layer list names (so a new tool
	// cannot be missed here).
	notJS := []string{"internal/raster", "internal/svgpdf", "internal/text", "internal/csscolor", "internal/imageref",
		"internal/loader", "internal/fontsubset", "internal/pngopt", "internal/fonts", "internal/textcut"}
	notJS = append(notJS, toolPackages(t)...)
	forbidden := map[string]map[string]bool{
		"strings": {"ToLower": true, "ToUpper": true, "ToTitle": true, "TrimSpace": true, "Fields": true, "EqualFold": true},
		"bytes":   {"ToLower": true, "ToUpper": true, "TrimSpace": true, "Fields": true, "EqualFold": true},
		"unicode": {"IsSpace": true, "ToLower": true, "ToUpper": true},
	}
	found := map[string]bool{}
	fset := token.NewFileSet()
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path != "." && !strings.HasPrefix(path, "internal") {
				return fs.SkipDir
			}
			for _, p := range notJS {
				if path == p || strings.HasPrefix(path, p+"/") {
					return fs.SkipDir
				}
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
		scanned++
		names := map[string]string{}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbidden[p] != nil {
				n := p
				if imp.Name != nil {
					n = imp.Name.Name
				}
				names[n] = p
			}
		}
		for _, decl := range f.Decls {
			// A package-level declaration (a var's initializer, a function
			// literal assigned at package level) is scanned as well, under
			// the name "init".
			where := "init"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				where = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if p, ok := names[id.Name]; ok && forbidden[p][sel.Sel.Name] {
					key := filepath.ToSlash(path) + ":" + where + ":" + p + "." + sel.Sel.Name
					if _, ok := goCaseOrSpaceAllowed[key]; !ok {
						t.Errorf("%s: %s.%s reads text as Go does, not JavaScript: use jsval.JSLower, JSUpper, JSTrim or IsJSSpace (or list it, with why, in goCaseOrSpaceAllowed)", fset.Position(sel.Pos()), p, sel.Sel.Name)
					}
					found[key] = true
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 200 {
		t.Fatalf("only %d files scanned: the scan is broken", scanned)
	}
	var stale []string
	for k := range goCaseOrSpaceAllowed {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		t.Errorf("goCaseOrSpaceAllowed: %s is no longer used; remove it", k)
	}
}

// TestCSSFoldsASCIIOnly: CSS reads its keywords, function and property
// names, units and font family names ASCII case-insensitively, so the
// packages that read SVG's CSS fold with csscolor.ASCIILower and
// ASCIIEqualFold, never with Go's Unicode case mapping, under which "whİte"
// is a colour and "stroKe" (a Kelvin sign) a property (routed from the
// PR02 review of the 2026-10-18 audit).
func TestCSSFoldsASCIIOnly(t *testing.T) {
	forbidden := map[string]map[string]bool{
		"strings": {"ToLower": true, "ToUpper": true, "ToTitle": true, "EqualFold": true},
		"bytes":   {"ToLower": true, "ToUpper": true, "ToTitle": true, "EqualFold": true},
		"unicode": {"ToLower": true, "ToUpper": true, "ToTitle": true, "SimpleFold": true},
	}
	scanned := 0
	fset := token.NewFileSet()
	for _, dir := range []string{"internal/csscolor", "internal/raster", "internal/svgpdf"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
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
			scanned++
			names := map[string]string{}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if forbidden[p] != nil {
					n := p
					if imp.Name != nil {
						n = imp.Name.Name
					}
					names[n] = p
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok {
					if p, ok := names[id.Name]; ok && forbidden[p][sel.Sel.Name] {
						t.Errorf("%s: %s.%s folds Unicode case; CSS folds ASCII only: use csscolor.ASCIILower or ASCIIEqualFold", fset.Position(sel.Pos()), p, sel.Sel.Name)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 40 {
		t.Fatalf("only %d files scanned: the scan is broken", scanned)
	}
}

// toolPackages are the packages docs/architecture.md's layer list puts in
// its tools line, as paths: the commands and the test harness, which read no
// specification.
func toolPackages(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i, j := strings.Index(doc, "<!-- layers:begin"), strings.Index(doc, "<!-- layers:end")
	if i < 0 || j < i {
		t.Fatal("docs/architecture.md has no layers:begin/end block")
	}
	for _, line := range strings.Split(doc[i:j], "\n") {
		if pkgs, ok := strings.CutPrefix(line, "tools:"); ok {
			var out []string
			for _, p := range strings.Fields(pkgs) {
				out = append(out, "internal/"+p)
			}
			return out
		}
	}
	t.Fatal("docs/architecture.md's layer list has no tools line")
	return nil
}
