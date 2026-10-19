package vegalite

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// unmeteredStringsAllowed are the functions that may build a string outside
// sbuf and joinCC (or scan a list in a loop), each with why its output
// cannot outgrow its input (or its list is short): the
// byte meter (mem.go) bounds what a specification makes the compiler build,
// so a builder, join, repeat or loop of appends elsewhere is a quadratic
// output nobody charges (C3, C6, C11: audit 2026-10-18). The key is
// "file.go:function" (a method as Type.method).
var unmeteredStringsAllowed = map[string]string{
	// Linear in the one string they rewrite, with no other input.
	"compositemark.go:jsEscape":         "at most six bytes per UTF-16 unit of its input",
	"dyn.go:varName":                    "at most twice its input",
	"fieldpath.go:escapePathAccess":     "at most twice its input",
	"fieldpath.go:replacePathInField":   "the pieces of one field path, each at most twice as long",
	"fieldpath.go:replacePathInFieldV":  "the pieces of one field path, each at most twice as long",
	"fieldpath.go:removePathFromField":  "the pieces of one field path, at most its length",
	"fieldpath.go:removePathFromFieldV": "the pieces of one field path, at most its length",
	"fieldpath.go:flatAccessWithDatum":  "the pieces of one field path, at most its length",
	"fieldpath.go:flatAccessWithDatumV": "the pieces of one field path, at most its length",
	"fieldpath.go:splitAccessPathN":     "the pieces of one string, at most its length",
	"datanodes3.go:groupbyConcat":       "one copy of the groupby and of the fields it is given",
	// A fixed number of parts, whatever the input.
	"warnings.go:msgErrorBarCenterAndExtentAreNotNeeded": "a fixed sentence",
	"warnings.go:msgFacetChannelDropped":                 "the facet channels, at most three",
	"datanodes3.go:xformNode.assembleImpute":             "a sequence's start, stop and step",
	"datanodes3.go:facetNode.hash":                       "one part per facet channel, at most three",
	"selection.go:unitName":                              "one part per facet channel, at most three",
	"defs.go:dateTimePartsText":                          "the seven parts of a datetime",
	"defs.go:joinStrings":                                "its callers join one part per encoding channel of a model, a fixed set",
	"normalize.go:joinNonEmpty":                          "its callers pass a fixed number of parts",
	// Linear scans of short lists, in a loop.
	"channel.go:filterOut":                              "its callers drop a fixed list of channels",
	"facet.go:facetModel.assembleLabelTitle":            "the two orients of a header",
	"normalize.go:topLevelSelectionsNormalizer.mapUnit": "the path of view names, at most maxDepth long",
}

// TestNoUnmeteredBuilders is the structural guard of the compiler's byte
// meter: outside sbuf (mem.go), no function of the package holds a
// strings.Builder or bytes.Buffer, joins or repeats a list with
// strings.Join or strings.Repeat, replaces with a replacement that is not a
// constant, or appends to a string in a loop, unless it is listed in
// unmeteredStringsAllowed with why its output is linear in one input. Each
// of those builds a string as long as the specification makes it, which
// must be charged before it is built (joinCC, sbuf, jsReplaceAll). Nor does
// a loop dedupe or look up by a linear scan of a list (contains,
// indexOfStr, slices.Contains or Index), quadratic over a list the
// specification sizes (C150), unless the list is short by construction.
func TestNoUnmeteredBuilders(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var parsed []*ast.File
	names := map[*ast.File]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		names[f] = name
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("github.com/mgilbir/aster/internal/vegalite", fset, parsed, info); err != nil {
		t.Fatal(err)
	}
	var found []string
	used := map[string]bool{}
	type unit struct {
		key  string
		body ast.Node
	}
	for _, f := range parsed {
		file := names[f]
		if file == "mem.go" {
			continue // sbuf and the meter itself
		}
		// The functions, and the package-level variables whose
		// initializers hold function literals (the selection compilers).
		var units []unit
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					units = append(units, unit{file + ":" + funcName(d), d.Body})
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for i, v := range vs.Values {
							units = append(units, unit{file + ":" + vs.Names[min(i, len(vs.Names)-1)].Name, v})
						}
					}
				}
			}
		}
		for _, u := range units {
			key := u.key
			report := func(n ast.Node, what string) {
				if _, ok := unmeteredStringsAllowed[key]; ok {
					used[key] = true
					return
				}
				found = append(found, fset.Position(n.Pos()).String()+": "+key+": "+what)
			}
			walkLoops(u.body, false, func(n ast.Node, inLoop bool) {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if obj, ok := info.Uses[n.Sel].(*types.TypeName); ok && obj.Pkg() != nil {
						if p := obj.Pkg().Path(); (p == "strings" && obj.Name() == "Builder") || (p == "bytes" && obj.Name() == "Buffer") {
							report(n, "a raw "+p+"."+obj.Name())
						}
					}
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok && inLoop && (id.Name == "contains" || id.Name == "indexOfStr") {
						if _, lit := n.Args[0].(*ast.CompositeLit); !lit {
							report(n, id.Name+" of a list in a loop (a linear scan per item)")
						}
					}
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return
					}
					if inLoop && isSlicesPkg(info, sel) && (strings.HasPrefix(sel.Sel.Name, "Contains") || strings.HasPrefix(sel.Sel.Name, "Index")) {
						if _, lit := n.Args[0].(*ast.CompositeLit); !lit {
							report(n, "slices."+sel.Sel.Name+" in a loop (a linear scan per item)")
						}
					}
					fn, ok := info.Uses[sel.Sel].(*types.Func)
					if !ok || fn.Pkg() == nil || (fn.Pkg().Path() != "strings" && fn.Pkg().Path() != "bytes") {
						return
					}
					switch fn.Name() {
					case "Join":
						report(n, fn.Pkg().Path()+".Join")
					case "Repeat":
						if tv := info.Types[n.Args[1]]; tv.Value == nil {
							report(n, fn.Pkg().Path()+".Repeat by a count that is not a constant")
						}
					case "ReplaceAll", "Replace":
						if tv := info.Types[n.Args[2]]; tv.Value == nil {
							report(n, fn.Pkg().Path()+"."+fn.Name()+" with a replacement that is not a constant")
						}
					case "NewReplacer":
						report(n, "strings.NewReplacer")
					}
				case *ast.AssignStmt:
					if !inLoop || len(n.Lhs) != 1 || len(n.Rhs) != 1 {
						return
					}
					appends := n.Tok == token.ADD_ASSIGN
					if be, ok := n.Rhs[0].(*ast.BinaryExpr); ok && n.Tok == token.ASSIGN && be.Op == token.ADD {
						// s = s + x
						l, lok := n.Lhs[0].(*ast.Ident)
						r, rok := be.X.(*ast.Ident)
						appends = lok && rok && l.Name == r.Name
					}
					if typ := info.TypeOf(n.Lhs[0]); appends && typ != nil && isStringType(typ) {
						report(n, "a string appended to in a loop")
					}
				}
			})
		}
	}
	for _, f := range found {
		t.Error(f)
	}
	for key := range unmeteredStringsAllowed {
		if !used[key] {
			t.Errorf("%s is allowed an unmetered string but builds none: drop it from unmeteredStringsAllowed", key)
		}
	}
	if len(found) > 0 {
		slices.Sort(found)
		t.Logf("%d unmetered string builds or linear scans in a loop", len(found))
	}
}

// funcName is a function's name, or Type.name for a method.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if ix, ok := t.(*ast.IndexExpr); ok {
		t = ix.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// walkLoops visits every node under n, telling visit whether it is inside
// the body of a for or range loop. The body of a function literal passed
// to a call counts as one: it is a callback (forEachFieldDef's, a
// visitor's) that may run per item.
func walkLoops(n ast.Node, inLoop bool, visit func(ast.Node, bool)) {
	ast.Inspect(n, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		visit(x, inLoop)
		switch x := x.(type) {
		case *ast.ForStmt:
			if x.Init != nil {
				walkLoops(x.Init, inLoop, visit)
			}
			if x.Cond != nil {
				walkLoops(x.Cond, inLoop, visit)
			}
			if x.Post != nil {
				walkLoops(x.Post, true, visit)
			}
			walkLoops(x.Body, true, visit)
			return false
		case *ast.RangeStmt:
			walkLoops(x.X, inLoop, visit)
			walkLoops(x.Body, true, visit)
			return false
		case *ast.CallExpr:
			walkLoops(x.Fun, inLoop, visit)
			for _, a := range x.Args {
				if fl, ok := a.(*ast.FuncLit); ok {
					visit(fl, inLoop)
					walkLoops(fl.Body, true, visit)
					continue
				}
				walkLoops(a, inLoop, visit)
			}
			return false
		}
		return true
	})
}

// isStringType says whether t is a string type.
func isStringType(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// isSlicesPkg says whether sel is a function of package slices.
func isSlicesPkg(info *types.Info, sel *ast.SelectorExpr) bool {
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "slices"
}

// byteBoundSpecs are specifications whose compiled output is quadratic in
// their size, as upstream's is, or multiplied by a fan-out, each far past
// boundBytes: the compilation must refuse them at the call's budget, before
// it allocates their output.
func byteBoundSpecs() map[string]func() Value {
	data := func() Value { return mkv("values", arr(mkv("a", 1, "b", 2))) }
	list := func(n int, f func(i int) Value) Value {
		items := make([]Value, n)
		for i := range items {
			items[i] = f(i)
		}
		return jsval.Arr(items)
	}
	return map[string]func() Value{
		// C3: a format.parse key of 4,000 pieces, whose parse expression
		// guards every prefix (about 40 MB).
		"parse-dotted-key": func() Value {
			parse := jsval.NewObject(1)
			parse.Set(strings.TrimSuffix(strings.Repeat("a.", 4000), "."), jsval.Str("number"))
			return mkv("data", mkv("values", arr(mkv("b", 1)), "format", mkv("parse", jsval.Obj(parse))),
				"mark", "point", "encoding", mkv("x", mkv("field", "b", "type", "quantitative")))
		},
		// C6: a header labelExpr of 3,000 datum.label with a format of $`,
		// each match replaced by all the text before it (about 54 MB).
		"labelexpr-backtick": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
				"row", mkv("field", "b", "type", "quantitative", "header", mkv("format", "$`",
					"labelExpr", strings.TrimSuffix(strings.Repeat("datum.label+", 3000), "+")))))
		},
		// C6: the same with $', the text after each match.
		"labelexpr-quote": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
				"row", mkv("field", "b", "type", "quantitative", "header", mkv("format", "$'",
					"labelExpr", strings.TrimSuffix(strings.Repeat("datum.label+", 3000), "+")))))
		},
		// C6: no pattern, but a format of 20 KB written in place of each of
		// 2,000 datum.label (40 MB).
		"labelexpr-long-format": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
				"row", mkv("field", "b", "type", "quantitative", "header", mkv("format", strings.Repeat("x", 20000),
					"labelExpr", strings.TrimSuffix(strings.Repeat("datum.label+", 2000), "+")))))
		},
		// A config of 200 KB that every one of 256 repeated views compiles
		// into its label, tooltip and description expressions (257 MB).
		"config-fanout": func() Value {
			return mkv("data", data(), "config", mkv("numberFormat", strings.Repeat("x", 200000)),
				"repeat", list(256, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) }),
				"spec", mkv("mark", "point", "encoding", mkv("x", mkv("field", mkv("repeat", "repeat"), "type", "quantitative"),
					"tooltip", mkv("field", "a", "type", "quantitative"))))
		},
		// C10: 400 top-level selections, each copied into each of 64 views.
		"selections-views": func() Value {
			return mkv("data", data(), "params", list(400, func(i int) Value { return mkv("name", fmt.Sprintf("s%d", i), "select", "point") }),
				"repeat", list(64, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) }),
				"spec", mkv("mark", "point", "encoding", mkv("x", mkv("field", mkv("repeat", "repeat"), "type", "quantitative"))))
		},
		// One view named with 200 KB, and 200 selections of its own: each
		// selection's signals name the unit (40 MB and more).
		"selections-long-name": func() Value {
			return mkv("data", data(), "name", strings.Repeat("n", 200000), "mark", "point",
				"encoding", mkv("x", mkv("field", "a", "type", "quantitative")),
				"params", list(200, func(i int) Value { return mkv("name", fmt.Sprintf("s%d", i), "select", "point") }))
		},
		// One view: 2,000 tooltip fields, each formatted with a 20 KB
		// config number format, joined in the tooltip and the description.
		"tooltip-config": func() Value {
			return mkv("data", data(), "config", mkv("numberFormat", strings.Repeat("x", 20000)), "mark", "point",
				"encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
					"tooltip", list(2000, func(i int) Value { return mkv("field", fmt.Sprintf("f%d", i), "type", "quantitative") })))
		},
	}
}

// boundBytes is the ledger byte limit byteBoundSpecs compile under.
const boundBytes = 16 << 20

// TestCompileBytesBounded checks the compiler's byte meter end to end: each
// of byteBoundSpecs, compiled under a ledger of boundBytes (and the views
// that limit admits), fails with an error wrapping ErrLimit, having
// allocated no more than a small multiple of the limit. Bytes allocated are
// counted by the runtime, the same on every machine outside the race
// detector, under which only the error is checked.
func TestCompileBytesBounded(t *testing.T) {
	for name, spec := range byteBoundSpecs() {
		t.Run(name, func(t *testing.T) {
			s := spec()
			l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: boundBytes})
			opts := Options{Context: budget.WithLedger(context.Background(), l), MaxViews: boundBytes / ViewBytes}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			out, err := Compile(s, opts)
			runtime.ReadMemStats(&after)
			alloc := after.TotalAlloc - before.TotalAlloc
			size := 0
			if err == nil {
				size = len(jsval.AppendJSON(nil, out))
			}
			t.Logf("err=%v; %d MB allocated, %d MB charged at the peak, output %d bytes", err, alloc>>20, l.PeakBytes()>>20, size)
			if !errors.Is(err, budget.ErrLimit) {
				t.Fatalf("compiled with err=%v (output %d bytes), want an error wrapping ErrLimit", err, size)
			}
			if !testutil.RaceEnabled && alloc > 3*boundBytes {
				t.Errorf("allocated %d MB before failing, more than 3 times the %d MB limit", alloc>>20, boundBytes>>20)
			}
		})
	}
}

// TestCompileMarginOverCorpus keeps the compiler's bounds far above real
// charts: every Vega-Lite specification of the repository's corpora (and of
// testdata/corpora-cache when it is there), in both versions, charges the
// ledger at most an eighth of the 64 MiB a hostile-input render is given
// (TestBoundedInputs) and weighs at most an eighth of the views that limit
// admits. When written, the heaviest charged 1.1 MiB (wild/vl_0123, a
// spec of 494 KiB) and weighed 51 views (wild/vl_0015).
func TestCompileMarginOverCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles every corpus specification twice")
	}
	const limit = 64 << 20
	maxCharged, maxWeight := int64(limit/8), float64(limit/ViewBytes/8)
	n := 0
	// The cache is a link, which WalkDir does not follow: walked on its own.
	testdata := filepath.Join("..", "..", "testdata")
	for _, root := range []string{testdata, filepath.Join(testdata, "corpora-cache") + string(filepath.Separator)} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // an absent corpus cache
			}
			if d.IsDir() && p != root && (d.Name() == "node_modules" || d.Name() == "security" || d.Name() == "corpora-cache") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if !strings.HasSuffix(p, ".vl.json") && !strings.Contains(string(b[:min(len(b), 400)]), "vega-lite") {
				return nil
			}
			v, err := jsval.ParseJSON(b)
			if err != nil || !v.IsObj() {
				return nil
			}
			for _, ver := range []string{"6.4", "5.8"} {
				l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
				if _, err := Compile(v, Options{Version: ver, Context: budget.WithLedger(context.Background(), l)}); err != nil {
					continue // a specification upstream rejects too
				}
				cc := &compileCtx{v5: ver == "5.8"}
				w := countViews(cc, v, specBytes(cc, v.Get("config"), 0))
				n++
				if l.Bytes() > maxCharged || w > maxWeight {
					t.Errorf("%s (%s): charged %d KiB and weighs %.0f views, past an eighth of the bounds of %d MiB (%d KiB, %.0f views)",
						p, ver, l.Bytes()>>10, w, limit>>20, maxCharged>>10, maxWeight)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 1000 {
		t.Fatalf("only %d specifications compiled: the corpus is not where it was", n)
	}
	t.Logf("%d compilations", n)
}

// TestCopiesAreUsed is the guard of C149 (audit 2026-10-18): a call that
// returns a copy (deeper, a clone, a duplicate, a merge) is never a
// statement of its own, whose result is dropped: `cp.deeper()` read as if
// it deepened cp, and only charged a step and checked a depth on a copy.
func TestCopiesAreUsed(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	copying := map[string]bool{"deeper": true, "clone": true, "cloneAP": true, "cloneObj": true, "deepClone": true, "duplicate": true,
		"merged": true, "mergedCC": true, "omit": true, "pick": true, "copyShared": true}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			st, ok := n.(*ast.ExprStmt)
			if !ok {
				return true
			}
			call, ok := st.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := ""
			switch f := call.Fun.(type) {
			case *ast.Ident:
				fn = f.Name
			case *ast.SelectorExpr:
				fn = f.Sel.Name
			}
			if copying[fn] {
				t.Errorf("%s: the copy %s returns is dropped", fset.Position(st.Pos()), fn)
			}
			return true
		})
	}
}
