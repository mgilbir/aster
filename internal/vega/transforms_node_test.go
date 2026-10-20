package vega

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/text"
	"github.com/mgilbir/aster/internal/transforms/wordcloud"
)

// transformCase is one case of testdata/transforms.node.json: a
// specification and what upstream did with it.
type transformCase struct {
	Spec     json.RawMessage
	Error    string
	Warnings []string
	Data     map[string]string
	Signal   string
	Keep     []string
	Drop     []string
	// Canvas is "box" for a case recorded with the box canvas, which the
	// engine's wordcloud.BoxRenderer reproduces on every platform.
	Canvas string
}

// TestTransformsMatchNode replays testdata/transforms.node.json, which
// testdata/gen_transforms.mjs records with upstream Vega: every transform with
// each optional parameter absent and null (vega-parser applies no definition
// default, so the transform's own code decides what an absent parameter
// means), and hand-written cases of transform semantics. Each must give
// upstream's first error, its warnings and its data.
//
// A wordcloud places words by the pixels node-canvas rasterizes them to,
// which depends on the platform: Linux's node-canvas (Pango 1.48, Cairo
// 1.16) draws the glyph outlines fontconfig hints, the engine (and macOS's
// node-canvas) the unhinted ones; recorded with hinting off, every case
// matches. So each wordcloud case is recorded a second time with the box
// canvas, whose rasterization the engine's BoxRenderer reproduces on every
// platform: those hold the layout to upstream's exactly, positions
// included, and the cases with node-canvas whose positions differ on the
// recording's platform are listed in testdata/transforms.node.divergences.txt
// with a hash of the engine's answer.
func TestTransformsMatchNode(t *testing.T) {
	b, err := os.ReadFile("testdata/transforms.node.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		// Canvas is the platform and the Pango and Cairo node-canvas
		// rasterized glyphs with when the file was recorded.
		Canvas string
		Cases  map[string]transformCase
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Cases) < 1000 {
		t.Fatalf("%d cases recorded", len(rec.Cases))
	}
	names := make([]string, 0, len(rec.Cases))
	for n := range rec.Cases {
		names = append(names, n)
	}
	slices.Sort(names)
	shaper := dejavuMeasurer(t)
	expected := transformDivergences(t, rec.Canvas)
	for name := range expected {
		if _, ok := rec.Cases[name]; !ok {
			t.Errorf("transforms.node.divergences.txt: %s names no recorded case", name)
		}
	}
	for _, name := range names {
		c := rec.Cases[name]
		t.Run(name, func(t *testing.T) {
			spec, err := jsval.ParseJSON(c.Spec)
			if err != nil {
				t.Fatal(err)
			}
			got := runTransformCase(spec, shaper, c.Canvas == "box")
			if got.err != c.Error {
				t.Errorf("first error %q, want %q", got.err, c.Error)
			}
			if !slices.Equal(got.warnings, c.Warnings) {
				t.Errorf("warnings %q, want %q", got.warnings, c.Warnings)
			}
			if got.view == nil {
				if len(c.Data) > 0 {
					t.Errorf("no view; want data %v", c.Data)
				}
				return
			}
			for d, want := range c.Data {
				v, _ := got.view.root.Data(d)
				g := string(jsval.AppendJSON(nil, nodeJSONView(v, c.Keep, c.Drop)))
				if pin, ok := expected[name]; ok {
					// a divergence of the recording platform's
					// node-canvas, pinned both ways: node's answer and
					// the engine's
					sum := sha256.Sum256([]byte(g))
					switch {
					case g == want:
						t.Errorf("data %s matches node: remove the case from transforms.node.divergences.txt", d)
					case hex.EncodeToString(sum[:8]) != pin:
						t.Errorf("data %s: the engine's answer changed (sha256 %x, pinned %s):\n got %.2000s\nwant %.2000s", d, sum[:8], pin, g, want)
					}
					continue
				}
				if g != want {
					t.Errorf("data %s:\n got %.2000s\nwant %.2000s", d, g, want)
				}
			}
			if c.Signal != "" {
				g := ""
				if v, ok := got.view.root.Signal("out"); ok {
					g = string(jsval.AppendJSON(nil, nodeJSONView(v, nil, nil)))
				}
				if g != c.Signal {
					t.Errorf("signal out:\n got %.1000s\nwant %.1000s", g, c.Signal)
				}
			}
		})
	}
}

// transformDivergences reads testdata/transforms.node.divergences.txt: the
// cases whose data, on the platform recorded (canvas), depends on how that
// platform's node-canvas rasterizes glyphs, each with the first 8 bytes of
// the sha256 of the engine's data. Lines are `case<TAB>platform<TAB>hash<TAB>
// # reason`; those of other platforms do not apply to this recording.
func transformDivergences(t *testing.T, canvas string) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/transforms.node.divergences.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 || !strings.HasPrefix(f[3], "# ") {
			t.Fatalf("transforms.node.divergences.txt:%d: want case, platform, hash and # reason", i+1)
		}
		if f[1] == canvas {
			out[f[0]] = f[2]
		}
	}
	return out
}

func dejavuMeasurer(t testing.TB) *text.Measurer {
	t.Helper()
	m, err := text.New(
		text.WithFont("DejaVu Sans", dejavu.SansRegular), text.WithFont("DejaVu Sans", dejavu.SansBold),
		text.WithFont("DejaVu Sans", dejavu.SansOblique), text.WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		text.WithDefaultFontFamily("DejaVu Sans"), text.WithExactAdvances(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type transformRun struct {
	view     *runView
	err      string
	warnings []string
}

// inExpression is what the engine appends to an error an expression raised.
var inExpression = regexp.MustCompile(` \(in expression .*\)$`)

// runTransformCase parses and runs spec as upstream's View does with a
// logger that records: the first error (a parse error ends it), and the
// distinct warnings in order.
func runTransformCase(spec jsval.Value, shaper *text.Measurer, box bool) (r transformRun) {
	report := func(err error) error {
		if w, ok := err.(Warning); ok {
			if !slices.Contains(r.warnings, string(w)) {
				r.warnings = append(r.warnings, string(w))
			}
			return nil
		}
		if r.err == "" {
			r.err = nodeMessage(err.Error())
		}
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			var e error
			switch x := p.(type) {
			case *parseError:
				e = x
			case *opError:
				e = x.err
			case *scale.Thrown:
				e = x
			case *budget.Stop:
				e = x.Err
			default:
				panic(p)
			}
			r.view = nil
			r.err = nodeMessage(e.Error())
		}
	}()
	config := mergeConfig(defaultConfig(), jsval.Undefined, spec.Get("config"))
	scope := newScope(config, &parseOptions{})
	parseView(spec, scope)
	flow := scope.toRuntime()
	var wc wordcloud.TextRenderer = wordcloud.NewCanvasRenderer(shaper)
	if box {
		wc = wordcloud.BoxRenderer{}
	}
	v := newView(context.Background(), Options{
		Report:        report,
		Random:        expr.NewLCG(12345),
		TextMeasurer:  text.NewCanvasContext(shaper),
		WordcloudText: wc,
	}, scope.locale)
	v.build(flow)
	if err := v.run(); err != nil {
		r.err = nodeMessage(err.Error())
	}
	r.view = v
	return r
}

// nodeMessage is an error's text as node's logger prints it: the message
// alone, without an Error's name or where in an expression it arose.
func nodeMessage(s string) string {
	s = inExpression.ReplaceAllString(s, "")
	if i := strings.Index(s, ": "); i >= 0 && strings.HasSuffix(s[:i], "Error") && !strings.Contains(s[:i], " ") {
		s = s[i+2:]
	}
	return s
}

// nodeJSONView is v as the generator writes it: each tuple of an array kept
// to the fields of keep, or without those of drop, and every reference to
// an object that contains it replaced by "[Circular]".
func nodeJSONView(v jsval.Value, keep, drop []string) jsval.Value {
	if v.IsArr() && (keep != nil || drop != nil) {
		items := v.Items()
		out := make([]jsval.Value, len(items))
		for i, t := range items {
			o := t.ObjValue()
			if o == nil {
				out[i] = t
				continue
			}
			p := jsval.NewObject(o.Len())
			if keep != nil {
				for _, k := range keep { // in keep's order, as the generator writes them
					if v, ok := o.Get(k); ok && !slices.Contains(drop, k) {
						p.Set(k, v)
					}
				}
			} else {
				for j := 0; j < o.Len(); j++ {
					if k := o.KeyAt(j); !slices.Contains(drop, k) {
						p.Set(k, o.ValueAt(j))
					}
				}
			}
			out[i] = jsval.Obj(p)
		}
		v = jsval.Arr(out)
	}
	return acyclic(v, nil)
}

func acyclic(v jsval.Value, stack []any) jsval.Value {
	var id any
	switch v.Kind() {
	case jsval.KindObj:
		id = v.ObjValue()
	case jsval.KindArr:
		id = fmt.Sprintf("%p", v.Items())
		if len(v.Items()) == 0 {
			return v
		}
	case jsval.KindTimestamp:
		// JSON.stringify writes a Date with its toJSON
		if s, ok := format.ISOFormat(v.NumValue()); ok {
			return jsval.Str(s)
		}
		return jsval.Null
	default:
		return v
	}
	if slices.Contains(stack, id) {
		return jsval.Str("[Circular]")
	}
	stack = append(stack, id)
	if v.IsArr() {
		items := v.Items()
		out := make([]jsval.Value, len(items))
		for i, x := range items {
			out[i] = acyclic(x, stack)
		}
		return jsval.Arr(out)
	}
	o := v.ObjValue()
	p := jsval.NewObject(o.Len())
	for j := 0; j < o.Len(); j++ {
		p.Set(o.KeyAt(j), acyclic(o.ValueAt(j), stack))
	}
	return jsval.Obj(p)
}
