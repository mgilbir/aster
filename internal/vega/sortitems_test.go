package vega

import (
	"context"
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
	} {
		cs := compareFrom(jsval.Walker{}, c.fields, c.orders, true)
		if cs == nil || cs.roots == nil {
			t.Fatalf("%v: want a comparator over paths", c.fields)
		}
		r.Seed(11)
		a := mk(200)
		r.Seed(11)
		b := mk(200)
		v1, v2 := &runView{g: newGraph(context.Background())}, &runView{g: newGraph(context.Background())}
		v1.sortItemsByKeys(a, cs)
		v2.sortItemsByViews(b, cs.cmp)
		for i := range a {
			if a[i].Seq != b[i].Seq {
				t.Fatalf("%v: position %d holds item %d, a sort over views puts item %d there", c.fields, i, a[i].Seq, b[i].Seq)
			}
		}
	}
}

// A compare field is read as a member chain: a path through a root the item
// does not hold is the TypeError of reading a property of undefined, from
// the sort over keys as from the sort over views, however many items.
func TestSortItemsByKeysThrowsAtAMissingStep(t *testing.T) {
	cs := compareFrom(jsval.Walker{}, []any{"nope.deeper"}, nil, true)
	if cs == nil || cs.roots == nil {
		t.Fatal("want a comparator over paths")
	}
	mk := func(n int) []*scene.Item {
		items := make([]*scene.Item, n)
		for i := range items {
			items[i] = &scene.Item{Seq: uint64(i + 1), Bounds: scene.NewBounds(), Datum: jsval.Obj(jsval.NewObject(0))}
		}
		return items
	}
	throws := func(f func()) (msg string) {
		defer func() {
			if r := recover(); r != nil {
				th, ok := r.(*jsval.Thrown)
				if !ok {
					panic(r)
				}
				msg = th.Msg
			}
		}()
		f()
		return ""
	}
	byKeys := throws(func() { (&runView{g: newGraph(context.Background())}).sortItemsByKeys(mk(3), cs) })
	byViews := throws(func() { (&runView{g: newGraph(context.Background())}).sortItemsByViews(mk(3), cs.cmp) })
	if byKeys == "" || byKeys != byViews {
		t.Errorf("sort over keys throws %q, over views %q", byKeys, byViews)
	}
	if msg := throws(func() { (&runView{g: newGraph(context.Background())}).sortItemsByKeys(mk(1), cs) }); msg != "" {
		t.Errorf("one item compares nothing, yet the sort threw %q", msg)
	}
}
