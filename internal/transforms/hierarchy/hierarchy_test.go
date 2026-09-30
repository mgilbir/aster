package hierarchy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func row(kv ...any) jsval.Value {
	o := jsval.NewObject(len(kv) / 2)
	for i := 0; i < len(kv); i += 2 {
		var v jsval.Value
		switch x := kv[i+1].(type) {
		case nil:
			v = jsval.Null
		case string:
			v = jsval.Str(x)
		case float64:
			v = jsval.Num(x)
		case int:
			v = jsval.Int(x)
		}
		o.Set(kv[i].(string), v)
	}
	return jsval.Obj(o)
}

func TestStratifyErrors(t *testing.T) {
	id, pid := field("id"), field("p")
	cases := []struct {
		name string
		rows []jsval.Value
		want string
	}{
		{"missing", []jsval.Value{row("id", "a", "p", nil), row("id", "b", "p", "zz")}, "missing: zz"},
		{"ambiguous", []jsval.Value{row("id", "a", "p", nil), row("id", "b", "p", "a"), row("id", "b", "p", "a"), row("id", "c", "p", "b")}, "ambiguous: b"},
		{"multiple", []jsval.Value{row("id", "a", "p", nil), row("id", "b", "p", nil)}, "multiple roots"},
		{"noroot", []jsval.Value{row("id", "a", "p", "b"), row("id", "b", "p", "a")}, "no root"},
		{"cycle", []jsval.Value{row("id", "r", "p", nil), row("id", "a", "p", "b"), row("id", "b", "p", "a")}, "cycle"},
	}
	for _, c := range cases {
		_, err := Stratify(c.rows, id, pid)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestStratifyEmpty(t *testing.T) {
	tree, err := Stratify(nil, field("id"), field("p"))
	if err != nil || tree.Root == nil || len(tree.Root.Children) != 0 {
		t.Fatalf("tree=%v err=%v", tree, err)
	}
	if err := LayoutTreemap(context.Background(), tree, TreemapParams{}); err != nil {
		t.Fatal(err)
	}
}

func TestNestOrderAndShape(t *testing.T) {
	data := []jsval.Value{
		row("k", "b", "n", 1), row("k", 10, "n", 2), row("k", "a", "n", 3), row("k", 2, "n", 4), row("k", "b", "n", 5),
	}
	tree, gen, err := Nest(data, []Accessor{field("k")}, true)
	if err != nil {
		t.Fatal(err)
	}
	// array-index keys ascending first, then insertion order
	var keys []string
	for _, c := range tree.Root.Children {
		keys = append(keys, c.Data.Get("key").AsString())
	}
	if fmt.Sprint(keys) != "[2 10 b a]" {
		t.Fatalf("key order %v", keys)
	}
	if len(gen) != 5 { // root + 4 groups
		t.Fatalf("generated %d", len(gen))
	}
	if tree.Root.Data.Get("key").Kind() != jsval.KindUndefined {
		t.Fatal("root must have no key")
	}
	if tree.Root.Children[2].Children[1].Data.Get("n").NumValue() != 5 {
		t.Fatal("leaves keep input order within a group")
	}
	if tree.NodeOf(data[0]) == nil || tree.NodeOf(data[0]).Depth != 2 {
		t.Fatal("NodeOf")
	}
}

func TestNestNoKeys(t *testing.T) {
	data := []jsval.Value{row("a", 1), row("a", 2)}
	tree, gen, err := Nest(data, nil, true)
	if err != nil || len(tree.Root.Children) != 2 || len(gen) != 1 {
		t.Fatalf("%v %v %v", tree, gen, err)
	}
}

func TestLimits(t *testing.T) {
	// a chain deeper than MaxDepth is refused, not walked
	n := MaxDepth + 5
	rows := make([]jsval.Value, n)
	for i := range rows {
		var p any = fmt.Sprint(i - 1)
		if i == 0 {
			p = nil
		}
		rows[i] = row("id", fmt.Sprint(i), "p", p)
	}
	if _, err := Stratify(rows, field("id"), field("p")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancel(t *testing.T) {
	rows := []jsval.Value{row("id", "r", "p", nil)}
	for i := 0; i < 5; i++ {
		rows = append(rows, row("id", fmt.Sprint(i), "p", "r", "v", 1))
	}
	tree, _ := Stratify(rows, field("id"), field("p"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := LayoutPack(ctx, tree, PackParams{}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestBadMethods(t *testing.T) {
	tree, _ := Stratify([]jsval.Value{row("id", "r", "p", nil)}, field("id"), field("p"))
	if err := LayoutTree(context.Background(), tree, TreeParams{Method: "nope"}); err == nil {
		t.Fatal("tree method")
	}
	if err := LayoutTreemap(context.Background(), tree, TreemapParams{Method: "nope"}); err == nil {
		t.Fatal("treemap method")
	}
	if err := LayoutTree(context.Background(), nil, TreeParams{}); err == nil {
		t.Fatal("nil tree")
	}
}

// Degenerate values (NaN, zero, huge, negative) must not panic or hang.
func TestDegenerateNoPanic(t *testing.T) {
	vals := []float64{0, math.NaN(), math.Inf(1), -3, 1e300, 5}
	for _, v := range vals {
		rows := []jsval.Value{row("id", "r", "p", nil)}
		for i := 0; i < 7; i++ {
			rows = append(rows, row("id", fmt.Sprint(i), "p", "r", "v", vals[i%len(vals)]))
			rows = append(rows, row("id", "x"+fmt.Sprint(i), "p", fmt.Sprint(i), "v", v))
		}
		build := func() *Tree {
			tr, err := Stratify(rows, field("id"), field("p"))
			if err != nil {
				t.Fatal(err)
			}
			return tr
		}
		ctx := context.Background()
		c := Common{Field: field("v")}
		_ = LayoutTree(ctx, build(), TreeParams{Common: c})
		_ = LayoutTree(ctx, build(), TreeParams{Common: c, Method: "cluster", Size: []float64{0, 0}})
		_ = LayoutPack(ctx, build(), PackParams{Common: c, Size: []float64{0, 5}})
		_ = LayoutPartition(ctx, build(), PartitionParams{Common: c})
		for _, m := range []string{"squarify", "resquarify", "binary", "dice", "slice", "slicedice"} {
			tr := build()
			_ = LayoutTreemap(ctx, tr, TreemapParams{Common: c, Method: m, Padding: Float(100)})
			_ = LayoutTreemap(ctx, tr, TreemapParams{Common: c, Method: m, Size: []float64{9, 0}})
		}
	}
}

func TestTreeLinksFilter(t *testing.T) {
	rows := []jsval.Value{row("id", "r", "p", nil), row("id", "a", "p", "r"), row("id", "b", "p", "a")}
	tree, _ := Stratify(rows, field("id"), field("p"))
	links, _ := TreeLinks(tree, func(v jsval.Value) bool { return v.Get("id").AsString() != "a" })
	if len(links) != 0 {
		t.Fatalf("links through a filtered node must be dropped, got %d", len(links))
	}
	links, _ = TreeLinks(tree, nil)
	if len(links) != 2 || links[0].Get("target").Get("id").AsString() != "a" {
		t.Fatal("links")
	}
}

func TestPath(t *testing.T) {
	rows := []jsval.Value{row("id", "r", "p", nil), row("id", "a", "p", "r"), row("id", "b", "p", "a"), row("id", "c", "p", "r")}
	tree, _ := Stratify(rows, field("id"), field("p"))
	var ids string
	for _, n := range tree.NodeByKey("b").Path(tree.NodeByKey("c")) {
		ids += n.Data.Get("id").AsString()
	}
	if ids != "barc" {
		t.Fatalf("path %q", ids)
	}
}

func BenchmarkTreemap(b *testing.B) { benchLayout(b, "treemap") }
func BenchmarkPack(b *testing.B)    { benchLayout(b, "pack") }
func BenchmarkTidy(b *testing.B)    { benchLayout(b, "tidy") }

func benchLayout(b *testing.B, kind string) {
	rows := []jsval.Value{row("id", "n0", "p", nil, "v", 0)}
	seed := uint64(7)
	for i := 1; i < 5000; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		p := int(seed>>33) % i
		rows = append(rows, row("id", fmt.Sprint("n", i), "p", fmt.Sprint("n", p), "v", float64(i%17+1)))
	}
	tree, err := Stratify(rows, field("id"), field("p"))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	c := Common{Field: field("v")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		switch kind {
		case "treemap":
			_ = LayoutTreemap(ctx, tree, TreemapParams{Common: c, Size: []float64{800, 600}, Padding: Float(1)})
		case "pack":
			_ = LayoutPack(ctx, tree, PackParams{Common: c, Size: []float64{800, 800}})
		default:
			_ = LayoutTree(ctx, tree, TreeParams{Common: c, Size: []float64{800, 600}})
		}
	}
}
