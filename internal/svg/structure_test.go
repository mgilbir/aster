package svg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/svgcheck"
	"github.com/mgilbir/aster/internal/testutil"
)

// hostile is a string that, written into the output without escaping, closes
// whatever attribute or element it lands in and opens new ones.
const hostile = "1)\"/><script>alert(1)</script><g onload=\"x' &amp;\t\n\r"

// sly makes only an attribute the renderer does write on a path, so the
// grammar alone does not catch it; the comparison with what the writer wrote
// does.
const sly = "1)\" vector-effect=\"non-scaling-stroke"

// renderStructure renders sg and checks the output against the renderer's
// grammar (svgcheck.Check) and against the structure the writer wrote: if
// the two differ, bytes that did not go through the writer's element and
// attribute methods (an unescaped value) made markup. It returns the output,
// or "" when the scene is one upstream cannot draw either.
func renderStructure(t *testing.T, sg *scene.Scenegraph, label string) string {
	t.Helper()
	var skel []string
	opt := Options{Width: 200, Height: 100, MaxBytes: 4 << 20, skeleton: &skel}
	out, err := Render(context.Background(), sg, opt)
	if err != nil {
		if !errors.Is(err, budget.ErrLimit) && !errors.Is(err, errMarkHole) && !slices.ContainsFunc(sceneErrors, func(e error) bool { return errors.Is(err, e) }) &&
			!strings.HasPrefix(err.Error(), "scene:") {
			t.Errorf("%s: unexpected error %v", label, err)
		}
		return ""
	}
	if len(out) > opt.MaxBytes {
		t.Errorf("%s: a %d-byte document returned under MaxBytes %d", label, len(out), opt.MaxBytes)
	}
	if err := svgcheck.Check(out); err != nil {
		t.Errorf("%s: %v\n%s", label, err, out)
		return out
	}
	got, _ := svgcheck.Skeleton(out)
	if d := svgcheck.Diff(got, skel); d != "" {
		t.Errorf("%s: the output's structure is not what the writer wrote: %s\n%s", label, d, out)
	}
	return out
}

// itemProps are the item properties scene.Item.Set models, read from the
// case labels of setProp so that a property added there is covered here
// without anyone remembering to list it.
func itemProps(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../scene/props.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "setProp" {
			return true
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					k, _ := strconv.Unquote(bl.Value)
					keys = append(keys, k)
				}
			}
			return true
		})
		return false
	})
	if len(keys) < 60 {
		t.Fatalf("found only %d item properties in setProp", len(keys))
	}
	return keys
}

var markTypes = []string{"arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"}

// hostileValues are the forms a property can be given that carry the hostile
// string: as it is, inside an array (String joins it), inside an object, and
// as a gradient's id and stop colour.
func hostileValues(prop string) []any {
	vs := []any{hostile, sly, []any{hostile, 2}, map[string]any{"path": hostile}}
	if prop == "fill" || prop == "stroke" {
		for _, kind := range []string{"linear", "radial"} {
			vs = append(vs,
				map[string]any{"gradient": kind, "id": hostile, "stops": []any{map[string]any{"offset": 0, "color": hostile}}},
				map[string]any{"gradient": kind, "stops": []any{map[string]any{"offset": 0, "color": hostile}}})
		}
	}
	return vs
}

// baseItem gives every property a plain value, so that the hostile one is
// written in context: a position next to an angle, a text with a font, a
// link around a shape.
func baseItem() map[string]any {
	return map[string]any{
		"x": 10, "y": 12, "x2": 30, "y2": 40, "width": 20, "height": 15, "size": 40,
		"angle": 30, "text": "label", "font": "Liberation Sans", "fontSize": 11, "fontWeight": "bold",
		"path": "M0,0L5,5", "url": "a.png", "href": "http://a.example/", "fill": "red", "stroke": "blue",
		"strokeWidth": 2, "strokeDash": []any{2, 1}, "shape": "circle", "outerRadius": 5, "endAngle": 1,
		"description": "item", "ariaRole": "img", "opacity": 0.5, "cornerRadius": 2, "limit": 400,
		"lineBreak": "\n", "radius": 0, "dx": 1, "dy": 1,
	}
}

// TestValuesCreateNoStructure gives every item property of every mark type,
// and the mark's own role, name and description, a string that would make
// markup if it were written unescaped. Whatever upstream draws for it, the
// output must parse back to exactly the elements and attributes the writer
// wrote, and those must be the renderer's.
func TestValuesCreateNoStructure(t *testing.T) {
	props := itemProps(t)
	for _, mt := range markTypes {
		for _, prop := range props {
			for i, v := range hostileValues(prop) {
				for _, also := range []string{"", "angle"} {
					a, b := baseItem(), baseItem()
					a[prop] = v
					b[prop] = v
					if also != "" && prop != also {
						a[also], b[also] = hostile, hostile
					}
					mark := map[string]any{"marktype": mt, "role": "mark", "items": []any{a, b}}
					root := map[string]any{"marktype": "group", "items": []any{map[string]any{"width": 100, "height": 100, "items": []any{mark}}}}
					data, err := json.Marshal(root)
					if err != nil {
						t.Fatal(err)
					}
					sg, err := scene.FromJSON(data)
					if err != nil {
						continue
					}
					renderStructure(t, sg, mt+"."+prop+"#"+strconv.Itoa(i)+"+"+also)
				}
			}
		}
		for _, key := range []string{"role", "name", "description", "guideCaption"} {
			mark := map[string]any{"marktype": mt, key: hostile, "items": []any{baseItem()}}
			if key == "guideCaption" {
				mark["role"] = "axis"
			}
			data, _ := json.Marshal(map[string]any{"marktype": "group", "items": []any{map[string]any{"items": []any{mark}}}})
			sg, err := scene.FromJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			renderStructure(t, sg, mt+" mark "+key)
		}
	}
}

// The structural check must catch markup made by a value: here, the bytes
// of an attribute value that closes its tag.
func TestStructureCheckCatchesInjectedMarkup(t *testing.T) {
	out := `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1)"/><script>alert(1)</script><g x=",0)"/></svg>`
	if svgcheck.Check(out) == nil {
		t.Errorf("a <script> element passed the check")
	}
	// An element and attribute the renderer does write, made by a value.
	out = `<svg xmlns="http://www.w3.org/2000/svg"><g><path transform="translate(1)" d="M0,0" fill="red"/></g></svg>`
	written := []string{"<svg", "@xmlns", "<g", "<path", "@transform", "/", "/", "/"}
	got, err := svgcheck.Skeleton(out)
	if err != nil {
		t.Fatal(err)
	}
	if svgcheck.Diff(got, written) == "" {
		t.Errorf("attributes made by a value passed the skeleton comparison")
	}
}

// TestNoRawMarkup keeps the writer's discipline (see lit): outside
// markup.go no code touches the output buffer, and nothing converts a value
// to lit, so every byte of spec or data text goes through an escaping method.
// The conversions are found by type, not by spelling: lit(s), (lit)(s), a
// conversion to an alias of lit, and a generic function instantiated with lit
// (whose T(s) converts to lit) all count (C153, audit 2026-10-18).
func TestNoRawMarkup(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) < 5 {
		t.Fatalf("parsed only %d files", len(files))
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Instances: map[*ast.Ident]types.Instance{}}
	conf := types.Config{Importer: testutil.SourceImporter(fset)}
	pkg, err := conf.Check("github.com/mgilbir/aster/internal/svg", fset, files, info)
	if err != nil {
		t.Fatal(err)
	}
	obj := pkg.Scope().Lookup("lit")
	if obj == nil {
		t.Fatal("no type lit")
	}
	litType := obj.Type()
	noRawMarkup(t, fset, files, info, litType)
}

// noRawMarkup reports every write to the output buffer outside markup.go and
// every conversion to litType in files.
func noRawMarkup(t interface {
	Helper()
	Errorf(format string, args ...any)
}, fset *token.FileSet, files []*ast.File, info *types.Info, litType types.Type) {
	t.Helper()
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if n.Sel.Name == "buf" && name != "markup.go" {
					t.Errorf("%s: the output buffer is written outside markup.go", fset.Position(n.Pos()))
				}
			case *ast.CallExpr:
				if tv, ok := info.Types[n.Fun]; ok && tv.IsType() && types.Identical(tv.Type, litType) {
					t.Errorf("%s: a value converted to lit reaches the output unescaped", fset.Position(n.Pos()))
				}
			}
			return true
		})
	}
	for id, inst := range info.Instances {
		for i := range inst.TypeArgs.Len() {
			if types.Identical(inst.TypeArgs.At(i), litType) {
				t.Errorf("%s: %s is instantiated with lit, so a conversion inside it reaches the output unescaped", fset.Position(id.Pos()), id.Name)
			}
		}
	}
}

// The check finds a conversion to lit however it is spelled: these files
// convert a value to lit each way, and each must be reported.
func TestNoRawMarkupFindsEveryConversion(t *testing.T) {
	const src = `package svg
type lit string
type alias = lit
func conv[T ~string](s string) T { return T(s) }
func a(s string) lit { return lit(s) }
func b(s string) lit { return (lit)(s) }
func c(s string) lit { return alias(s) }
func d(s string) lit { return conv[lit](s) }
func e(s string) lit { return conv[alias](s) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Instances: map[*ast.Ident]types.Instance{}}
	pkg, err := (&types.Config{}).Check("svg", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	noRawMarkup(rec, fset, []*ast.File{f}, info, pkg.Scope().Lookup("lit").Type())
	if len(rec.errs) != 5 {
		t.Errorf("reported %d conversions, want 5:\n%s", len(rec.errs), strings.Join(rec.errs, "\n"))
	}
}

// recorder collects a check's errors instead of failing the test.
type recorder struct{ errs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recorder) Helper() {}
