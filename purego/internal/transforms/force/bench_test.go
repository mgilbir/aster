package force

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func benchNodes(n int) []jsval.Value {
	nodes := make([]jsval.Value, n)
	for i := range nodes {
		nodes[i] = jsval.Obj(jsval.ObjectOf("id", jsval.Int(i), "r", jsval.Num(3)))
	}
	return nodes
}

func BenchmarkSimulation1k(b *testing.B) {
	const n = 1000
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		// Run resolves link endpoints in place, so links are rebuilt per run.
		links := make([]jsval.Value, 0, n)
		for i := 1; i < n; i++ {
			links = append(links, jsval.Obj(jsval.ObjectOf("source", jsval.Int((i*7919)%i), "target", jsval.Int(i))))
		}
		nodes := benchNodes(n)
		b.StartTimer()
		p := DefaultParams()
		p.Static = true
		p.Iterations = 30
		c := NewCollide()
		c.Radius = Constant(3)
		p.Forces = []Force{Center{}, NewNBody(), NewLink(links), c}
		if err := Run(context.Background(), nodes, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNBody1k(b *testing.B) {
	p := DefaultParams()
	p.Static = true
	p.Iterations = 30
	p.Forces = []Force{NewNBody()}
	b.ReportAllocs()
	for b.Loop() {
		if err := Run(context.Background(), benchNodes(1000), p); err != nil {
			b.Fatal(err)
		}
	}
}
