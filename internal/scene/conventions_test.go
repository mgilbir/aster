package scene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func obj(kv ...any) jsval.Value {
	o := jsval.NewObject(len(kv) / 2)
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1].(jsval.Value))
	}
	return jsval.Obj(o)
}

// TestLiveValuesIgnoreTheRecordingConventions is C95 (audit 2026-10-18): the
// encodings of the serialized scenegraphs the tests load ({$num} for a
// non-finite number, {$gid}/{$gref} for gradient identity, {path, ops} for a
// recorded path generator) are FromJSON's. A value a live specification
// computes is read as JavaScript reads it, whatever keys it has: an object
// where a number belongs is NaN (and prints as [object Object]), an object
// clip is a truthy clip of the group's box, an object shape is no generator.
func TestLiveValuesIgnoreTheRecordingConventions(t *testing.T) {
	inf := obj("$num", jsval.Str("Infinity"))
	recorded := obj("path", jsval.Str("M0,0L10,0L10,10Z"), "ops", jsval.ArrOf(jsval.ArrOf(jsval.Str("R"), jsval.Num(0), jsval.Num(0), jsval.Num(5), jsval.Num(5))))

	live := &Item{}
	for _, k := range []string{"x", "width", "size", "fontSize", "angle", "strokeWidth"} {
		if _, err := live.Set(k, inf); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []Num{live.X, live.Width, live.Size, live.FontSize(), live.Angle, live.StrokeWidth} {
		if v := n.Val(); v == v {
			t.Errorf("a live {$num: 'Infinity'} reads as %v; want NaN, as +{} is", v)
		}
	}
	for _, k := range []string{"x", "width", "angle", "strokeWidth"} {
		if v, ok := live.RawValue(k); !ok || !v.IsObj() {
			t.Errorf("%s: the object given is not kept (%v): upstream writes it as String(value)", k, v)
		}
	}
	live.Set("fill", obj("gradient", jsval.Str("linear"), "$gid", jsval.Num(0), "x1", inf))
	if g := live.Fill.Gradient(); g == nil || !math.IsNaN(g.X1.Val()) {
		t.Errorf("a live gradient's {$num} coordinate: %+v; want NaN", g)
	}
	live.Set("stroke", obj("$gref", jsval.Num(0)))
	if live.Stroke.Gradient() != nil || live.Stroke.Str() != "[object Object]" {
		t.Errorf("a live {$gref}: %+v; want the colour String(value)", live.Stroke)
	}
	live.Set("clip", recorded)
	if live.Clip != Yes || live.ClipPath != nil {
		t.Errorf("a live object clip: Clip %v, path generator %v; want a truthy clip without a generator", live.Clip, live.ClipPath != nil)
	}
	live.Set("shape", recorded)
	if live.Shape.Func != nil {
		t.Error("a live object shape became a path generator")
	}

	// The loader keeps reading its own encodings.
	sg, err := FromJSON([]byte(`{"marktype":"group","items":[{"clip":{"path":"M0,0L1,1"},"items":[
		{"marktype":"symbol","zindex":{"$num":"Infinity"},"items":[{"x":{"$num":"Infinity"},"size":{"$num":"-Infinity"},"fill":{"$gid":3,"gradient":"radial","x1":{"$num":"NaN"},"stops":[]}},{"fill":{"$gref":3}}]},
		{"marktype":"shape","items":[{"shape":{"path":"M0,0L2,2"}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := sg.Root.Items[0]
	sym, shp := g.Items[0], g.Items[1]
	if g.ClipPath == nil || g.ClipPath(nil) != "M0,0L1,1" {
		t.Error("FromJSON: a recorded clip path is not replayed")
	}
	if !math.IsInf(sym.Zindex, 1) || !math.IsInf(sym.Items[0].X.Val(), 1) || !math.IsInf(sym.Items[0].Size.Val(), -1) {
		t.Errorf("FromJSON: {$num} not decoded: zindex %v, x %v, size %v", sym.Zindex, sym.Items[0].X.Val(), sym.Items[0].Size.Val())
	}
	if _, ok := sym.Items[0].RawValue("x"); ok {
		t.Error("FromJSON: a recorded number kept as a raw value")
	}
	if a, b := sym.Items[0].Fill.Gradient(), sym.Items[1].Fill.Gradient(); a == nil || a != b || !math.IsNaN(a.X1.Val()) {
		t.Errorf("FromJSON: gradient identity or coordinates lost: %p %p", a, b)
	}
	if f := shp.Items[0].Shape.Func; f == nil || f(nil, shp.Items[0]) != "M0,0L2,2" {
		t.Error("FromJSON: a recorded shape is not replayed")
	}
}

// TestRecordingConventionsStayInTheLoader is the structural guard of C95:
// the keys of the recording encodings, and the loader's helpers that read
// them, appear in json.go only, so no code that sets a live item can reach
// them.
func TestRecordingConventionsStayInTheLoader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	loaderOnly := map[string]bool{"jsonLoader": true, "pathFuncFromValue": true, "decodeRecorded": true}
	for _, f := range files {
		if f == "json.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					s, _ := strconv.Unquote(n.Value)
					for _, k := range []string{"$num", "$gid", "$gref", "ops"} {
						if s == k {
							t.Errorf("%s: the recording key %q outside the loader", f, k)
						}
					}
				}
			case *ast.Ident:
				if loaderOnly[n.Name] {
					t.Errorf("%s: %s used outside the loader", f, n.Name)
				}
			}
			return true
		})
	}
}
