package vega

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// Sorting on keys read once per item must order the items as a sort over their
// tuple views does, for paths into the datum, the item and its bounds, with
// ties, undefined and mixed keys, either order.
func TestSortItemsByKeysMatchesViews(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	mk := func(n int) []*scene.Item {
		items := make([]*scene.Item, n)
		for i := range items {
			it := &scene.Item{Seq: uint64(i + 1), Bounds: scene.NewBounds()}
			o := jsval.NewObject(3)
			switch r.Intn(5) {
			case 0:
				o.Set("a", jsval.Str(fmt.Sprint(r.Intn(6))))
			case 1:
				o.Set("a", jsval.Null)
			case 2: // no a
			default:
				o.Set("a", jsval.Num(float64(r.Intn(6))))
			}
			o.Set("b", jsval.Num(float64(r.Intn(3))))
			o.Set("n", jsval.Obj(jsval.ObjectOf("c", jsval.Num(float64(r.Intn(4))))))
			it.Datum = jsval.Obj(o)
			if r.Intn(4) > 0 {
				it.X = scene.N(float64(r.Intn(8)))
			}
			it.Y = scene.N(r.Float64())
			it.Bounds.X1, it.Bounds.X2 = float64(r.Intn(5)), 9
			items[i] = it
		}
		return items
	}
	for _, c := range []struct {
		fields []any
		orders []any
	}{
		{[]any{"x"}, nil},
		{[]any{"datum.a"}, []any{"descending"}},
		{[]any{"datum.b", "datum.a"}, []any{"ascending", "descending"}},
		{[]any{"datum.n.c", "x", "y"}, []any{"descending"}},
		{[]any{"bounds.x1", "datum.b"}, nil},
		{[]any{"toString", "x"}, nil},
		{[]any{"datum"}, nil},
		{[]any{"nope.deeper"}, nil},
	} {
		cs := compareFrom(c.fields, c.orders)
		if cs == nil || cs.roots == nil {
			t.Fatalf("%v: want a comparator over paths", c.fields)
		}
		r.Seed(11)
		a := mk(200)
		r.Seed(11)
		b := mk(200)
		v1, v2 := &runView{}, &runView{}
		v1.sortItemsByKeys(a, cs)
		v2.sortItemsByViews(b, cs.cmp)
		for i := range a {
			if a[i].Seq != b[i].Seq {
				t.Fatalf("%v: position %d holds item %d, a sort over views puts item %d there", c.fields, i, a[i].Seq, b[i].Seq)
			}
		}
	}
}
