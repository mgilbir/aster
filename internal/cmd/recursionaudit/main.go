// Command recursionaudit lists every recursive function in the engine and
// checks the list against scripts/recursion.allow.
//
// In Go a stack overflow is a fatal error that no recover can catch: a
// specification that drives a recursion deep enough takes the host process
// down. So every recursion over input must be bounded by a limit (a depth
// counter, a maximum checked on the way in), and each one is listed in the
// allowlist with how it is bounded. TestDeepInput exercises the shapes; this
// finds the recursions those shapes have to reach.
//
// It type-checks the module's non-test packages (go list supplies the
// dependencies' export data), builds the static call graph — direct calls,
// method calls on concrete types, and closures that call themselves through
// the variable they are assigned to — and reports its strongly connected
// components (Tarjan). Calls through an interface or a function value other
// than such a variable are not resolved; a recursion only reachable that way
// is not found.
//
//	go run ./internal/cmd/recursionaudit          # check against scripts/recursion.allow
//	go run ./internal/cmd/recursionaudit -list    # print every cycle (allowlist format)
//	go run ./internal/cmd/recursionaudit -list -cut f,g   # ... with functions f and g removed
//
// -cut answers "does every cycle in this component go through a function that
// checks the limit?": cut the checking functions and list what is left; an
// empty list means each recursion passes one.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const module = "github.com/mgilbir/aster"

// skipped are packages that do not process untrusted input (commands, and the
// test support that drives node and reads the vectors it records).
var skipped = []string{module + "/internal/cmd/", module + "/internal/oracle", module + "/internal/upstream", module + "/cmd/"}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Standard   bool
}

func main() {
	list := flag.Bool("list", false, "print every cycle in allowlist format")
	allow := flag.String("allow", "scripts/recursion.allow", "the allowlist")
	cut := flag.String("cut", "", "comma-separated functions to remove from the call graph before looking for cycles (with -list)")
	flag.Parse()

	pkgs, err := goList()
	if err != nil {
		fail(err)
	}
	g := newGraph()
	fset := token.NewFileSet()
	exports := map[string]string{}
	for _, p := range pkgs {
		exports[p.ImportPath] = p.Export
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	for _, p := range pkgs {
		if !audited(p.ImportPath) {
			continue
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				fail(err)
			}
			files = append(files, f)
		}
		info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check(p.ImportPath, fset, files, info); err != nil {
			fail(fmt.Errorf("%s: %w", p.ImportPath, err))
		}
		for _, f := range files {
			g.addFile(fset, f, info)
		}
	}
	for _, name := range strings.Split(*cut, ",") {
		if name == "" {
			continue
		}
		delete(g.edges, name)
		for _, to := range g.edges {
			delete(to, name)
		}
	}
	cycles := g.cycles()
	var lines []string
	for _, c := range cycles {
		lines = append(lines, strings.Join(c, " "))
	}
	sort.Strings(lines)
	if *list {
		for _, l := range lines {
			fmt.Println(l)
		}
		return
	}
	allowed, err := readAllow(*allow)
	if err != nil {
		fail(err)
	}
	found := map[string]bool{}
	bad := false
	for _, l := range lines {
		found[l] = true
		if _, ok := allowed[l]; !ok {
			if !bad {
				fmt.Fprintln(os.Stderr, "recursionaudit: recursions not in the allowlist (bound each by a limit, then list it with how):")
			}
			bad = true
			fmt.Fprintln(os.Stderr, "  "+l)
		}
	}
	var stale []string
	for l := range allowed {
		if !found[l] {
			stale = append(stale, l)
		}
	}
	sort.Strings(stale)
	for _, l := range stale {
		bad = true
		fmt.Fprintln(os.Stderr, "recursionaudit: stale allowlist entry (no longer recursive; remove it): "+l)
	}
	if bad {
		os.Exit(1)
	}
	fmt.Printf("recursionaudit: ok (%d recursive cycles, each bounded)\n", len(lines))
}

func audited(path string) bool {
	if path != module && !strings.HasPrefix(path, module+"/") {
		return false
	}
	for _, s := range skipped {
		if strings.HasPrefix(path+"/", s) || path == strings.TrimSuffix(s, "/") {
			return false
		}
	}
	return true
}

func goList() ([]listedPackage, error) {
	cmd := exec.Command("go", "list", "-deps", "-export", "-json", "./...")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(&out)
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// readAllow reads "cycle<TAB># how it is bounded" lines.
func readAllow(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cycle, why, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(strings.TrimSpace(why), "#") {
			return nil, fmt.Errorf("%s: an entry needs a tab and a # justification: %q", path, line)
		}
		m[cycle] = why
	}
	return m, sc.Err()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "recursionaudit:", err)
	os.Exit(2)
}

// graph is the call graph over named functions and function literals.
type graph struct {
	edges map[string]map[string]bool
}

func newGraph() *graph { return &graph{edges: map[string]map[string]bool{}} }

func (g *graph) edge(from, to string) {
	if g.edges[from] == nil {
		g.edges[from] = map[string]bool{}
	}
	g.edges[from][to] = true
	if g.edges[to] == nil {
		g.edges[to] = map[string]bool{}
	}
}

func funcName(fn *types.Func) string {
	name := fn.FullName()
	name = strings.ReplaceAll(name, module+"/", "")
	return strings.ReplaceAll(name, module, "aster")
}

// addFile adds the calls made in one file. A call is attributed to the
// innermost function literal or declaration around it; a literal assigned to
// a variable is the target of calls through that variable.
func (g *graph) addFile(fset *token.FileSet, f *ast.File, info *types.Info) {
	pkgName := f.Name.Name
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		obj, _ := info.Defs[fd.Name].(*types.Func)
		if obj == nil {
			continue
		}
		top := funcName(obj)
		if fd.Recv == nil && fd.Name.Name == "init" {
			// A package may have several init functions; tell them apart.
			top += "@" + filepath.Base(fset.Position(fd.Pos()).Filename)
		}
		g.edge(top, top)
		delete(g.edges[top], top) // a node, no self edge yet
		litVars := map[types.Object]string{}
		// Name each literal; record those assigned to a variable.
		var stack []string
		var visit func(n ast.Node) bool
		// A literal is named by its enclosing function and its position among
		// that function's literals in source order: stable when unrelated
		// lines move, and distinct for two literals on one line.
		ordinal := map[*ast.FuncLit]int{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				ordinal[lit] = len(ordinal) + 1
			}
			return true
		})
		litName := func(lit *ast.FuncLit) string {
			return fmt.Sprintf("%s$lit%d", top, ordinal[lit])
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range s.Rhs {
					if lit, ok := rhs.(*ast.FuncLit); ok && i < len(s.Lhs) {
						if id, ok := s.Lhs[i].(*ast.Ident); ok {
							if o := info.Defs[id]; o != nil {
								litVars[o] = litName(lit)
							} else if o := info.Uses[id]; o != nil {
								litVars[o] = litName(lit)
							}
						}
					}
				}
			case *ast.ValueSpec:
				for i, v := range s.Values {
					if lit, ok := v.(*ast.FuncLit); ok && i < len(s.Names) {
						if o := info.Defs[s.Names[i]]; o != nil {
							litVars[o] = litName(lit)
						}
					}
				}
			}
			return true
		})
		_ = pkgName
		visit = func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				name := litName(x)
				g.edge(stack[len(stack)-1], name) // defining a literal may run it
				stack = append(stack, name)
				ast.Inspect(x.Body, visit)
				stack = stack[:len(stack)-1]
				return false
			case *ast.CallExpr:
				from := stack[len(stack)-1]
				switch fun := ast.Unparen(x.Fun).(type) {
				case *ast.Ident:
					switch o := info.Uses[fun].(type) {
					case *types.Func:
						g.edge(from, funcName(o))
					case *types.Var:
						if lit, ok := litVars[o]; ok {
							g.edge(from, lit)
						}
					}
				case *ast.SelectorExpr:
					if sel := info.Selections[fun]; sel != nil {
						if fn, ok := sel.Obj().(*types.Func); ok {
							if _, isIface := sel.Recv().Underlying().(*types.Interface); !isIface {
								g.edge(from, funcName(fn))
							}
						}
					} else if fn, ok := info.Uses[fun.Sel].(*types.Func); ok {
						g.edge(from, funcName(fn)) // pkg.Func
					}
				}
			}
			return true
		}
		stack = []string{top}
		ast.Inspect(fd.Body, visit)
	}
}

// cycles returns each strongly connected component that recurses (more than
// one function, or one that calls itself), its members sorted.
func (g *graph) cycles() [][]string {
	index := map[string]int{}
	low := map[string]int{}
	on := map[string]bool{}
	var stack []string
	var out [][]string
	next := 0
	nodes := make([]string, 0, len(g.edges))
	for n := range g.edges {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var strong func(v string)
	strong = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		succ := make([]string, 0, len(g.edges[v]))
		for w := range g.edges[v] {
			succ = append(succ, w)
		}
		sort.Strings(succ)
		for _, w := range succ {
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 || g.edges[v][v] {
				sort.Strings(comp)
				out = append(out, comp)
			}
		}
	}
	for _, n := range nodes {
		if _, seen := index[n]; !seen {
			strong(n)
		}
	}
	return out
}
