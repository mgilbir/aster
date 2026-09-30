package vega

import (
	"compress/gzip"
	"context"
	"io"
	"regexp"
	"strconv"
	"time"

	"encoding/json"
	"fmt"
	"github.com/mgilbir/aster/internal/expr"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// testLoader serves "data/<file>" urls from the shared dataset directories.
type testLoader struct{ dirs []string }

func (l testLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }

func (l testLoader) Load(_ context.Context, uri string) ([]byte, error) {
	rel := strings.TrimPrefix(uri, "data/")
	for _, d := range l.dirs {
		if b, err := os.ReadFile(filepath.Join(d, rel)); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not found: %s", uri)
}

var repoRoot = func() string {
	wd, _ := os.Getwd()
	// internal/vega -> repo root
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}()

func newTestLoader() testLoader {
	return testLoader{dirs: []string{
		filepath.Join(repoRoot, "testdata", "vega-datasets", "data"),
		filepath.Join(repoRoot, "testdata", "data", "data"),
	}}
}

// numJSON encodes non-finite numbers the way gen_scenegraph.mjs does.
// readGz reads a gzip-compressed recording.
func readGz(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func numJSON(f float64) jsval.Value {
	switch {
	case math.IsNaN(f):
		return jsval.Str("NaN")
	case math.IsInf(f, 1):
		return jsval.Str("Infinity")
	case math.IsInf(f, -1):
		return jsval.Str("-Infinity")
	}
	return jsval.Num(f)
}

func boundsJSON(b scene.Bounds) jsval.Value {
	return jsval.ArrOf(numJSON(b.X1), numJSON(b.Y1), numJSON(b.X2), numJSON(b.Y2))
}

func convJSON(v jsval.Value, depth int) jsval.Value {
	switch v.Kind() {
	case jsval.KindNum:
		return numJSON(v.NumValue())
	case jsval.KindTimestamp:
		return obj("$date", jsval.Num(v.NumValue()))
	case jsval.KindArr:
		out := make([]jsval.Value, v.Len())
		for i, it := range v.Items() {
			c := convJSON(it, depth+1)
			if c.IsUndefined() {
				c = jsval.Null
			}
			out[i] = c
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		if depth > 6 {
			return jsval.Null
		}
		o := jsval.NewObject(v.Len())
		for i := 0; i < v.ObjValue().Len(); i++ {
			c := convJSON(v.ObjValue().ValueAt(i), depth+1)
			if !c.IsUndefined() {
				o.Set(v.ObjValue().KeyAt(i), c)
			}
		}
		return jsval.Obj(o)
	}
	return v
}

func itemJSON(it *scene.Item, group bool) jsval.Value {
	o := jsval.NewObject(16)
	eachItemProp(it, func(name string, v jsval.Value) {
		if strings.HasPrefix(name, "_") {
			return
		}
		if c := convJSON(v, 0); !c.IsUndefined() {
			o.Set(name, c)
		}
	})
	o.Set("bounds", boundsJSON(it.Bounds))
	if group {
		items := make([]jsval.Value, len(it.Items))
		for i, m := range it.Items {
			items[i] = markJSON(m)
		}
		o.Set("items", jsval.Arr(items))
	}
	return jsval.Obj(o)
}

func markJSON(m *scene.Mark) jsval.Value {
	if m == nil {
		return jsval.Null
	}
	g := m.Type == scene.MarkGroup
	items := make([]jsval.Value, len(m.Items))
	for i, it := range m.Items {
		items[i] = itemJSON(it, g)
	}
	o := jsval.ObjectOf(
		"marktype", jsval.Str(m.Type.String()),
		"interactive", jsval.Bool(!m.NonInteractive),
		"clip", jsval.Bool(m.Clip),
		"zindex", jsval.Num(m.Zindex),
		"bounds", boundsJSON(m.Bounds),
		"items", jsval.Arr(items),
	)
	if m.Name != "" {
		o.Set("name", jsval.Str(m.Name))
	}
	if m.Role != "" {
		o.Set("role", jsval.Str(m.Role))
	}
	if m.Aria != scene.Unset {
		o.Set("aria", jsval.Bool(m.Aria == scene.Yes))
	}
	if m.Description != "" {
		o.Set("description", jsval.Str(m.Description))
	}
	return jsval.Obj(o)
}

// diffScene compares two JSON trees with a numeric tolerance and returns up to
// max differences as path: got vs want.
func diffScene(path string, got, want jsval.Value, tol float64, out *[]string, max int) {
	if len(*out) >= max {
		return
	}
	add := func(format string, args ...any) {
		if len(*out) < max {
			*out = append(*out, path+": "+fmt.Sprintf(format, args...))
		}
	}
	if got.Kind() != want.Kind() {
		// null and undefined match
		if got.IsNullish() && want.IsNullish() {
			return
		}
		// Items keep whatever JavaScript type the encoder produced; typed items
		// keep the string or number the renderer would read.
		if (got.IsStr() && want.IsNum()) || (got.IsNum() && want.IsStr()) {
			if got.AsString() == want.AsString() {
				return
			}
		}
		if (got.IsNum() && want.IsBool()) || (got.IsBool() && want.IsNum()) {
			if got.AsDouble() == want.AsDouble() {
				return
			}
		}
		// numeric strings for non-finite values
		add("got %s want %s", trunc(got.String()), trunc(want.String()))
		return
	}
	switch got.Kind() {
	case jsval.KindNum:
		g, w := got.NumValue(), want.NumValue()
		if g == w || math.Abs(g-w) <= tol*math.Max(1, math.Abs(w)) {
			return
		}
		add("got %v want %v", g, w)
	case jsval.KindArr:
		if got.Len() != want.Len() {
			add("array length got %d want %d", got.Len(), want.Len())
			return
		}
		for i := range got.Items() {
			diffScene(fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i), tol, out, max)
		}
	case jsval.KindObj:
		g, w := got.ObjValue(), want.ObjValue()
		keys := map[string]bool{}
		for _, k := range g.Keys() {
			keys[k] = true
		}
		for _, k := range w.Keys() {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			gv, gok := g.Get(k)
			wv, wok := w.Get(k)
			switch {
			case !gok:
				if ignoredKeys[k] || wv.IsNullish() {
					continue
				}
				add("missing key %q (want %s)", k, trunc(wv.String()))
			case !wok:
				add("extra key %q = %s", k, trunc(gv.String()))
			default:
				diffScene(path+"."+k, gv, wv, tol, out, max)
			}
		}
	case jsval.KindStr:
		if got.StrValue() != want.StrValue() && !numericStringsClose(got.StrValue(), want.StrValue(), 1e-6) {
			add("got %s want %s", trunc(got.String()), trunc(want.String()))
		}
	default:
		if !jsval.Equal(got, want) {
			add("got %s want %s", trunc(got.String()), trunc(want.String()))
		}
	}
}

var numberRE = regexp.MustCompile(`-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?`)

// numericStringsClose compares strings that differ only in their numbers, such
// as path data, with a relative tolerance.
func numericStringsClose(a, b string, tol float64) bool {
	if numberRE.ReplaceAllString(a, "#") != numberRE.ReplaceAllString(b, "#") {
		return false
	}
	x, y := numberRE.FindAllString(a, -1), numberRE.FindAllString(b, -1)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		f, _ := strconv.ParseFloat(x[i], 64)
		g, _ := strconv.ParseFloat(y[i], 64)
		if math.Abs(f-g) > tol*math.Max(1, math.Abs(g)) {
			return false
		}
	}
	return true
}

func trunc(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func renderSpecFile(t testing.TB, path string) (*Result, jsval.Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(context.Background(), spec, Options{
		Loader: newTestLoader(),
		// the recordings freeze the clock and seed the generator
		Now:    func() time.Time { return time.UnixMilli(1700000000000) },
		Random: expr.NewLCG(12345),
	})
	if err != nil {
		return nil, jsval.Undefined, err
	}
	return res, markJSON(res.Scenegraph.Root), nil
}

func TestSmoke(t *testing.T) {
	spec := `{"width":200,"height":100,"padding":5,
	 "data":[{"name":"t","values":[{"k":"a","v":1},{"k":"b","v":3}]}],
	 "scales":[{"name":"x","type":"band","domain":{"data":"t","field":"k"},"range":"width","padding":0.1},
	           {"name":"y","type":"linear","domain":{"data":"t","field":"v"},"range":"height","nice":true}],
	 "marks":[{"type":"rect","from":{"data":"t"},"encode":{"enter":{"x":{"scale":"x","field":"k"},"width":{"scale":"x","band":1},"y":{"scale":"y","field":"v"},"y2":{"scale":"y","value":0}}}}]}`
	v, err := jsval.ParseJSONString(spec)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(context.Background(), v, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := jsval.AppendJSONIndent(nil, markJSON(res.Scenegraph.Root), " ")
	t.Log(res.Width, res.Height, res.Origin, string(out))
}

var _ = json.Marshal

func TestAxisOnly(t *testing.T) {
	spec := `{"width":200,"height":100,
	 "scales":[{"name":"y","type":"linear","domain":[0,3],"range":"height"}],
	 "axes":[{"orient":"left","scale":"y"}]}`
	v, _ := jsval.ParseJSONString(spec)
	res, err := Render(context.Background(), v, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	structure(markJSON(res.Scenegraph.Root), 0, "", &b)
	t.Log("\n" + b.String())
}

func TestAxisTable(t *testing.T) {
	spec := `{"width":200,"height":100,
	 "data":[{"name":"table","values":[{"category":"A","value":1}]}],
	 "scales":[{"name":"y","type":"band","domain":{"data":"table","field":"category"},"range":"height"},
	           {"name":"x","type":"linear","domain":{"data":"table","field":"value"},"range":"width","zero":true,"nice":true}],
	 "axes":[{"orient":"left","scale":"y","tickSize":0},{"orient":"bottom","scale":"x"}]}`
	v, _ := jsval.ParseJSONString(spec)
	res, err := Render(context.Background(), v, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	structure(markJSON(res.Scenegraph.Root), 0, "", &b)
	t.Log("\n" + b.String())
}

// ignoredKeys are properties upstream caches on items that carry no information
// of their own.
var ignoredKeys = map[string]bool{"_bounds": true, "pathCache": true, "zdirty": true, "exit": true}

func TestBinSignal(t *testing.T) {
	spec := `{"signals":[{"name":"binCount","update":"(bins.stop - bins.start) / bins.step"}],
	"data":[{"name":"raw","values":[{"v":1},{"v":24}],"transform":[{"type":"bin","field":"v","extent":[1,24],"maxbins":20,"nice":false,"signal":"bins"}]}]}`
	v, _ := jsval.ParseJSONString(spec)
	_, err := Render(context.Background(), v, Options{})
	t.Log(err)
}

func TestDebugWorld(t *testing.T) {
	res, _, err := renderSpecFile(t, filepath.Join(repoRoot, "testdata", "corpus", "vega", "world-map.vg.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Scenegraph.RootItem().Items[1]
	for _, i := range []int{0, 6, 53} {
		it := m.Items[i]
		t.Logf("item %d bounds %+v x=%v", i, it.Bounds, it.X)
	}
}

// debugView builds and runs a view so tests can inspect signals and scales.
func debugView(t testing.TB, spec jsval.Value) *runView {
	t.Helper()
	config := mergeConfig(defaultConfig(), jsval.Undefined, spec.Get("config"))
	scope := newScope(config, &parseOptions{})
	parseView(spec, scope)
	flow := scope.toRuntime()
	v := newView(context.Background(), Options{Loader: newTestLoader()}, scope.locale)
	v.build(flow)
	if err := v.run(); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDebugVL(t *testing.T) {
	name := os.Getenv("VEGA_DEBUG_VL")
	if name == "" {
		t.Skip()
	}
	b, err := readGz(filepath.Join("testdata", "vl", name+".json.gz"))
	if err != nil {
		b, err = os.ReadFile(filepath.Join(os.Getenv("VEGA_VL_DIR"), name+".json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := jsval.ParseJSON(b)
	v := debugView(t, rec.Get("vega"))
	for _, sig := range strings.Split(os.Getenv("VEGA_DEBUG_SIGNALS"), ",") {
		if n := v.root.signal(sig); n != nil {
			t.Logf("signal %s = %s", sig, trunc(n.sig().String()))
		}
	}
	for _, sc := range strings.Split(os.Getenv("VEGA_DEBUG_SCALES"), ",") {
		if n := v.root.scaleNode(sc); n != nil {
			t.Logf("scale %s = %v", sc, n.value)
		}
	}
}

func TestDebugDomain(t *testing.T) {
	p := os.Getenv("VEGA_DEBUG_SPEC")
	if p == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(p)
	spec, _ := jsval.ParseJSON(b)
	v := debugView(t, spec)
	for _, sc := range strings.Split(os.Getenv("VEGA_DEBUG_SCALES"), ",") {
		if n := v.root.scaleNode(sc); n != nil {
			if s, ok := n.value.(interface{ Domain() []jsval.Value }); ok {
				t.Logf("scale %s domain = %s", sc, trunc(jsval.Arr(s.Domain()).String()))
			}
		}
	}
}

// specFiles lists the Vega specifications with recordings: the shared corpus
// and this package's own targeted specifications.
func specFiles() []string {
	var out []string
	for _, g := range []string{
		filepath.Join(repoRoot, "testdata", "corpus", "vega", "*.vg.json"),
		filepath.Join("testdata", "specs", "*.vg.json"),
	} {
		m, _ := filepath.Glob(g)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}
