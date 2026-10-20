package hierarchy

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// meteredTree is a tree of n nodes, node i the child of node (i-1)/3.
func meteredTree(t *testing.T, n int) *Tree {
	t.Helper()
	nodes := make([]*Node, n)
	for i := range nodes {
		nodes[i] = &Node{Data: jsval.Obj(jsval.ObjectOf("v", jsval.Int(i%7+1)))}
		if i > 0 {
			p := nodes[(i-1)/3]
			p.Children = append(p.Children, nodes[i])
		}
	}
	m := budget.NewMeter(context.Background())
	if err := finish(&m, nodes[0]); err != nil {
		t.Fatal(err)
	}
	return &Tree{Root: nodes[0]}
}

// TestLayoutPassesAreMetered: the layouts' arithmetic passes (d3's tidy
// tree walks, cluster's two, partition's and treemap's tiling) visit every
// node, and a tree of MaxNodes takes seconds over them (0.8 to 1.3 s a
// layout of a million nodes, measured), so each node they visit is a step of
// work charged to the call, which polls its context: the deadline ends a
// layout wherever it is, not only between passes. Counted, not timed: the
// work a layout charges is at least its passes times the nodes.
func TestLayoutPassesAreMetered(t *testing.T) {
	const n = 30_000
	for _, c := range []struct {
		name   string
		passes int64 // node visits charged per node, at least
		run    func(context.Context, *Tree) error
	}{
		// six passes (five over the nodes in pre-order, firstWalk in
		// post-order) and the four fields written
		{"tidy", 10, func(ctx context.Context, tr *Tree) error {
			return LayoutTree(ctx, tr, TreeParams{Size: []float64{100, 100}})
		}},
		// two passes and the four fields
		{"cluster", 6, func(ctx context.Context, tr *Tree) error {
			return LayoutTree(ctx, tr, TreeParams{Method: "cluster", Size: []float64{100, 100}})
		}},
		// a node and its children tiled, the round, and the six fields
		{"partition", 9, func(ctx context.Context, tr *Tree) error {
			return LayoutPartition(ctx, tr, PartitionParams{Size: []float64{100, 100}, Round: true})
		}},
		{"treemap", 9, func(ctx context.Context, tr *Tree) error {
			return LayoutTreemap(ctx, tr, TreemapParams{Size: []float64{100, 100}, Round: true})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := meteredTree(t, n)
			l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
			if err := c.run(budget.WithLedger(context.Background(), l), tr); err != nil {
				t.Fatal(err)
			}
			// a meter hands its count to the ledger every 1,024 steps:
			// up to that much of each of the layout's few meters is unseen
			if got, want := l.WorkDone(), c.passes*n-8*1024; got < want {
				t.Errorf("%d steps of work over %d nodes; want at least %d", got, n, want)
			}
			// and the deadline ends it
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := c.run(ctx, meteredTree(t, n)); err == nil {
				t.Error("a cancelled layout succeeded")
			}
		})
	}
}
