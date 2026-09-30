package layout

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func BenchmarkOverlapParity(b *testing.B) {
	for i := 0; i < b.N; i++ {
		m := labels(2000, 10, -6)
		Overlap(m, OverlapParams{Method: jsval.True, Order: "datum.index"})
	}
}

func BenchmarkGridLayout(b *testing.B) {
	items := make([]*scene.Item, 200)
	for i := range items {
		it := &scene.Item{Width: scene.N(30), Height: scene.N(20)}
		it.Bounds.Set(-5, -5, 35, 25)
		items[i] = it
	}
	opt := &gridOptions{alignCol: each, alignRow: each, padCol: 5, padRow: 5, columns: 10, centerCol: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gridLayout(items, opt)
	}
}
