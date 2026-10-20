package layout

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// The numeric fast path of gridLayout must lay out exactly what the
// JavaScript-valued one does when every input is a number.
func TestGridLayoutPathsAgree(t *testing.T) {
	mk := func() []*scene.Item {
		items := make([]*scene.Item, 7)
		for i := range items {
			it := &scene.Item{Width: scene.N(float64(20 + 3*i)), Height: scene.N(float64(10 + i))}
			it.Bounds.Set(float64(-i), -2, float64(25+2*i), float64(12+i))
			items[i] = it
		}
		return items
	}
	for _, align := range []string{each, all, "none"} {
		for _, flushB := range []string{flush, "full"} {
			opt := &gridOptions{alignCol: align, alignRow: align, padCol: jsval.Num(5), padRow: jsval.Num(3),
				columns: 3, centerCol: true, centerRow: true, bounds: flushB,
				anchor: gridAnchor{x: 7, y: 4, column: middle, row: end}}
			a, b := mk(), mk()
			ba := gridLayoutNum(a, opt, 5, 3)
			bb, err := gridLayoutJS(b, opt)
			if err != nil {
				t.Fatal(err)
			}
			if ba != bb {
				t.Errorf("%s/%s: bounds %v, JS path %v", align, flushB, ba, bb)
			}
			for i := range a {
				if a[i].X != b[i].X || a[i].Y != b[i].Y || a[i].Bounds != b[i].Bounds {
					t.Errorf("%s/%s: item %d: (%v,%v) %v, JS path (%v,%v) %v", align, flushB, i,
						a[i].X.Val(), a[i].Y.Val(), a[i].Bounds, b[i].X.Val(), b[i].Y.Val(), b[i].Bounds)
				}
			}
		}
	}
}
