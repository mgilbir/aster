// Package hierarchy implements the tree transforms of vega-hierarchy on top of
// the layout algorithms of d3-hierarchy: nest, stratify, tree (tidy and
// cluster), pack, partition, treemap and treelinks.
//
// A [Tree] is built from tuples by [Nest] or [Stratify]; the layout functions
// then compute geometry on its [Node]s and write the result back into the
// tuples (Node.Data) under the field names vega-hierarchy uses, exactly as the
// upstream transforms do.
package hierarchy

import (
	"context"
	"fmt"
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jssort"

	"github.com/mgilbir/aster/internal/jsval"
)

// Limits on spec-driven work. They are far above any real chart.
const (
	// MaxDepth bounds the depth of a tree.
	MaxDepth = 10000
	// MaxNodes bounds the number of nodes of a tree.
	MaxNodes = 5_000_000
)

// ErrTooLarge is returned when a hierarchy exceeds MaxDepth or MaxNodes.
var ErrTooLarge = fmt.Errorf("hierarchy: tree too deep or too large: %w", budget.ErrLimit)

// Node is a d3-hierarchy node. Layout results live in the geometry fields;
// which of them a transform fills depends on the layout.
type Node struct {
	Data     jsval.Value // the tuple this node stands for
	Parent   *Node
	Children []*Node // nil for leaves
	Depth    int
	Height   int
	Value    float64 // sum or count, see Sum and Count

	X, Y, R        float64 // tree, cluster and pack layouts
	X0, Y0, X1, Y1 float64 // treemap and partition layouts

	id  string // stratify id
	tn  *tidyNode
	sq  []*sqRow // resquarify's cached rows
	sqR float64  // the ratio sq was computed with
}

// Tree is a hierarchy plus the lookup tables vega-hierarchy attaches to it.
type Tree struct {
	Root  *Node
	byKey map[string]*Node
	byObj map[*jsval.Object]*Node
}

// NodeByKey returns the node whose stratify key is key (Stratify trees).
func (t *Tree) NodeByKey(key string) *Node { return t.byKey[key] }

// NodeOf returns the node for a tuple, or nil. Tuple identity is the object
// pointer, as everywhere in this engine.
func (t *Tree) NodeOf(tuple jsval.Value) *Node {
	if !tuple.IsObj() {
		return nil
	}
	if t.byObj == nil {
		t.byObj = make(map[*jsval.Object]*Node)
		t.Root.eachBefore(func(n *Node) {
			if n.Data.IsObj() {
				t.byObj[n.Data.ObjValue()] = n
			}
		})
	}
	return t.byObj[tuple.ObjValue()]
}

// Ancestors returns the node followed by its ancestors up to the root.
func (n *Node) Ancestors() []*Node {
	var out []*Node
	for ; n != nil; n = n.Parent {
		out = append(out, n)
	}
	return out
}

// Path returns the shortest path from n to other through their common
// ancestor, as d3's node.path.
func (n *Node) Path(other *Node) []*Node {
	a, b := n, other
	ancestor := commonAncestor(a, b)
	out := []*Node{a}
	for a != ancestor {
		a = a.Parent
		out = append(out, a)
	}
	k := len(out)
	for b != ancestor {
		out = append(out, b)
		b = b.Parent
	}
	// the b side was collected bottom-up; reverse it
	for i, j := k, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func commonAncestor(a, b *Node) *Node {
	if a == b {
		return a
	}
	aNodes, bNodes := a.Ancestors(), b.Ancestors()
	c := (*Node)(nil)
	for i, j := len(aNodes)-1, len(bNodes)-1; i >= 0 && j >= 0 && aNodes[i] == bNodes[j]; i, j = i-1, j-1 {
		c = aNodes[i]
	}
	return c
}

// eachBefore visits nodes in pre-order (parents first, children left to right).
func (n *Node) eachBefore(fn func(*Node)) {
	stack := []*Node{n}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fn(node)
		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, node.Children[i])
		}
	}
}

// postOrder lists the nodes children-first, siblings left to right, which is
// the order of d3's eachAfter.
func (n *Node) postOrder() []*Node {
	stack := []*Node{n}
	var next []*Node
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		next = append(next, node)
		stack = append(stack, node.Children...)
	}
	for i, j := 0, len(next)-1; i < j; i, j = i+1, j-1 {
		next[i], next[j] = next[j], next[i]
	}
	return next
}

// eachAfter visits nodes children first.
func (n *Node) eachAfter(fn func(*Node)) {
	for _, node := range n.postOrder() {
		fn(node)
	}
}

// each visits nodes breadth-first (d3's node.each and iterator order).
func (n *Node) each(fn func(*Node)) {
	level := []*Node{n}
	for len(level) > 0 {
		var next []*Node
		for _, node := range level {
			fn(node)
			next = append(next, node.Children...)
		}
		level = next
	}
}

// Descendants lists the tree below (and including) n breadth-first.
func (n *Node) Descendants() []*Node {
	var out []*Node
	n.each(func(x *Node) { out = append(out, x) })
	return out
}

// sum sets Value to value(node) plus the values of the children.
func (n *Node) sum(value func(*Node) float64) {
	n.eachAfter(func(node *Node) {
		s := value(node)
		for _, c := range node.Children {
			s += c.Value
		}
		node.Value = s
	})
}

// count sets Value to the number of leaves below each node.
func (n *Node) count() {
	n.eachAfter(func(node *Node) {
		if len(node.Children) == 0 {
			node.Value = 1
			return
		}
		s := 0.0
		for i := len(node.Children) - 1; i >= 0; i-- {
			s += node.Children[i].Value
		}
		node.Value = s
	})
}

// sortChildren orders every child list with cmp (a stable sort, as
// Array.prototype.sort is).
func (n *Node) sortChildren(cmp func(a, b *Node) int) {
	n.eachBefore(func(node *Node) {
		if len(node.Children) > 1 {
			c := node.Children
			jssort.Sort(c, cmp)
		}
	})
}

// finish fills Parent, Depth and Height from the Children lists, enforcing
// the size limits. Root's Parent is left as it is found (nil).
func finish(root *Node) error {
	count := 0
	var overflow bool
	root.Depth = 0
	root.Parent = nil
	root.eachBefore(func(n *Node) {
		count++
		if count > MaxNodes || n.Depth > MaxDepth {
			overflow = true
			return
		}
		for _, c := range n.Children {
			c.Parent = n
			c.Depth = n.Depth + 1
		}
	})
	if overflow {
		return ErrTooLarge
	}
	computeHeights(root)
	return nil
}

func computeHeights(root *Node) {
	root.eachBefore(func(n *Node) { n.Height = 0 })
	for _, n := range root.postOrder() {
		if p := n.Parent; p != nil && p.Height < n.Height+1 {
			p.Height = n.Height + 1
		}
	}
}

func ctxErr(ctx context.Context, i int) error {
	if i&1023 == 0 {
		return ctx.Err()
	}
	return nil
}

// truthy is JavaScript truthiness of a number.
func truthy(f float64) bool { return f != 0 && f == f }
