package vega

import (
	"context"
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// A first DataJoin run, which tells new tuples apart by id, joins as a run
// through the index does: the same items entering, updating and leaving, in
// the same order, over the runs that follow.
func TestDataJoinFirstRunMatchesIndex(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	v := newView(context.Background(), Options{}, jsval.Undefined)
	p := newParams()
	for round := 0; round < 200; round++ {
		_, tr, _ := facDataJoin(nil, nil, nil)
		fast := &opNode{g: v.g}
		indexed := &opNode{g: v.g, value: &joinMap{objs: map[*jsval.Object]*joinEntry{}, vals: map[jsval.Key]*joinEntry{}, strs: map[string]*joinEntry{}}}
		items := map[*scene.Item]*scene.Item{} // indexed item -> fast item
		for pi, src := range tuplePulses(r, 4) {
			want := tr.transform(indexed, p, &flowPulse{tuples: src})
			got := tr.transform(fast, p, &flowPulse{tuples: src})
			for _, l := range []struct {
				name      string
				want, got []*scene.Item
			}{{"add", want.add, got.add}, {"mod", want.mod, got.mod}, {"rem", want.rem, got.rem}} {
				if len(l.got) != len(l.want) {
					t.Fatalf("round %d pulse %d: %d %s items, want %d", round, pi, len(l.got), l.name, len(l.want))
				}
				for i, w := range l.want {
					g := l.got[i]
					if c, ok := items[w]; ok && c != g || !ok && l.name != "add" {
						t.Fatalf("round %d pulse %d: %s item %d is another item", round, pi, l.name, i)
					}
					items[w] = g
					if g.Datum.Key() != w.Datum.Key() {
						t.Fatalf("round %d pulse %d: %s item %d has another datum", round, pi, l.name, i)
					}
				}
			}
		}
	}
}
