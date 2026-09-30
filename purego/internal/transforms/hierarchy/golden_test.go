package hierarchy

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// The golden vectors in testdata/hierarchy.json are recorded from upstream
// vega by testdata/gen_hierarchy.mjs (each case is a dataset plus a chain of
// transforms and the resulting tuple fields).

func field(name string) Accessor {
	return func(v jsval.Value) jsval.Value { return v.Get(name) }
}

func compareFor(spec jsval.Value) Compare {
	get := NodeFieldPath(spec.Get("field").AsString())
	desc := spec.Get("order").AsString() == "descending"
	return func(a, b *Node) int {
		x, y := get(a), get(b)
		c := 0
		if x.IsNum() && y.IsNum() {
			switch {
			case x.NumValue() < y.NumValue():
				c = -1
			case x.NumValue() > y.NumValue():
				c = 1
			}
		} else {
			c = strings.Compare(x.AsString(), y.AsString())
		}
		if desc {
			c = -c
		}
		return c
	}
}

func optFloat(v jsval.Value) *float64 {
	if v.IsUndefined() {
		return nil
	}
	return Float(v.NumValue())
}

func floats(v jsval.Value) []float64 {
	if !v.IsArr() {
		return nil
	}
	out := make([]float64, v.Len())
	for i := range out {
		e := v.Index(i)
		if e.IsObj() { // {signal: ...}: the harness supplies the width
			out[i] = math.NaN()
		} else {
			out[i] = e.NumValue()
		}
	}
	return out
}

func strs(v jsval.Value) []string {
	var out []string
	for _, e := range v.Items() {
		out = append(out, e.AsString())
	}
	return out
}

func common(t jsval.Value) Common {
	c := Common{As: strs(t.Get("as"))}
	if f := t.Get("field"); f.IsStr() {
		c.Field = field(f.StrValue())
	}
	if s := t.Get("sort"); s.IsObj() {
		c.Sort = compareFor(s)
	}
	return c
}

// runLayout applies the layout transform t to tree; width replaces a
// {signal} size entry.
func runLayout(tree *Tree, t jsval.Value, width float64) error {
	ctx := context.Background()
	size := floats(t.Get("size"))
	for i := range size {
		if math.IsNaN(size[i]) {
			size[i] = width
		}
	}
	switch typ := t.Get("type").StrValue(); typ {
	case "tree":
		return LayoutTree(ctx, tree, TreeParams{
			Common: common(t), Method: t.Get("method").StrValue(),
			Size: size, NodeSize: floats(t.Get("nodeSize")),
			UniformSeparation: t.Get("separation").IsBool() && !t.Get("separation").BoolValue(),
		})
	case "pack":
		p := PackParams{Common: common(t), Size: size, Padding: optFloat(t.Get("padding"))}
		if r := t.Get("radius"); r.IsObj() {
			p.Radius = NodeFieldPath(r.Get("field").StrValue())
		}
		return LayoutPack(ctx, tree, p)
	case "partition":
		return LayoutPartition(ctx, tree, PartitionParams{
			Common: common(t), Size: size, Padding: optFloat(t.Get("padding")),
			Round: t.Get("round").BoolValue(),
		})
	case "treemap":
		return LayoutTreemap(ctx, tree, TreemapParams{
			Common: common(t), Method: t.Get("method").StrValue(), Ratio: optFloat(t.Get("ratio")),
			Size: size, Round: t.Get("round").BoolValue(),
			Padding: optFloat(t.Get("padding")), PaddingInner: optFloat(t.Get("paddingInner")),
			PaddingOuter: optFloat(t.Get("paddingOuter")), PaddingTop: optFloat(t.Get("paddingTop")),
			PaddingRight: optFloat(t.Get("paddingRight")), PaddingBottom: optFloat(t.Get("paddingBottom")),
			PaddingLeft: optFloat(t.Get("paddingLeft")),
		})
	}
	panic("unknown transform in golden file")
}

func near(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func checkRows(t *testing.T, got []jsval.Value, want jsval.Value) {
	t.Helper()
	if len(got) != want.Len() {
		t.Fatalf("row count %d, want %d", len(got), want.Len())
	}
	for i, row := range want.Items() {
		o := row.ObjValue()
		for k := range o.All() {
			g, w := got[i].Get(k), o.Lookup(k)
			switch {
			case w.IsNum():
				if !g.IsNum() || !near(g.NumValue(), w.NumValue()) {
					t.Errorf("row %d field %s = %v, want %v", i, k, g, w)
				}
			case w.IsNull():
				if !g.IsNullish() {
					t.Errorf("row %d field %s = %v, want null", i, k, g)
				}
			default:
				if g.AsString() != w.AsString() {
					t.Errorf("row %d field %s = %v, want %v", i, k, g, w)
				}
			}
		}
	}
}

func TestGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/hierarchy.json")
	if err != nil {
		t.Fatal(err)
	}
	file, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Get("cases").Items() {
		t.Run(c.Get("name").StrValue(), func(t *testing.T) {
			tuples := file.Get("datasets").Get(c.Get("dataset").StrValue()).Items()
			// fresh copies: layouts write into the tuples
			data := make([]jsval.Value, len(tuples))
			for i, tu := range tuples {
				data[i] = jsval.Obj(tu.ObjValue().Clone())
			}
			var tree *Tree
			out := data
			for _, tr := range c.Get("transforms").Items() {
				switch tr.Get("type").StrValue() {
				case "stratify":
					tree, err = Stratify(data, field(tr.Get("key").StrValue()), field(tr.Get("parentKey").StrValue()))
				case "nest":
					var keys []Accessor
					for _, k := range tr.Get("keys").Items() {
						keys = append(keys, field(k.StrValue()))
					}
					var gen []jsval.Value
					tree, gen, err = Nest(data, keys, tr.Get("generate").BoolValue())
					out = append(append([]jsval.Value(nil), data...), gen...)
				default:
					err = runLayout(tree, tr, 600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			checkRows(t, out, c.Get("output"))
			if c.Get("rerun").IsObj() {
				tr := c.Get("transforms").Index(1)
				if err := runLayout(tree, tr, c.Get("rerun").Get("second").NumValue()); err != nil {
					t.Fatal(err)
				}
				checkRows(t, out, c.Get("output2"))
			}
			if c.Get("linkOutput").IsArr() {
				links, err := TreeLinks(tree, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := c.Get("linkOutput")
				if len(links) != want.Len() {
					t.Fatalf("%d links, want %d", len(links), want.Len())
				}
				for i, l := range links {
					w := want.Index(i)
					if l.Get("source").Get("id").AsString() != w.Index(0).AsString() ||
						l.Get("target").Get("id").AsString() != w.Index(1).AsString() {
						t.Fatalf("link %d mismatch", i)
					}
				}
			}
		})
	}
}
