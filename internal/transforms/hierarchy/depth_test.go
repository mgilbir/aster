package hierarchy

import (
	"runtime/debug"
	"testing"
)

// Binary tiling splits off one child at a time when every value is zero (and
// whenever one child holds half the remaining value). It recursed once per
// child; it must recurse into the smaller half only, so a parent with a
// million children needs a few dozen frames.
func TestTileBinaryRecursionIsLogarithmic(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(1 << 20))
	const n = 1_000_000
	parent := &Node{Children: make([]*Node, n)}
	for i := range parent.Children {
		parent.Children[i] = &Node{}
	}
	tileBinary(parent, 0, 0, 100, 100)
	// Every child got a box.
	for _, c := range parent.Children {
		if c.X1 < c.X0 || c.Y1 < c.Y0 {
			t.Fatalf("child without a box: %+v", c)
		}
	}
	// Skewed values: each child holds more than half of what remains.
	w := 1.0
	for i := range parent.Children[:1000] {
		parent.Children[i].Value = w
		w /= 2.1
	}
	parent.Children = parent.Children[:1000]
	parent.Value = 2
	tileBinary(parent, 0, 0, 100, 100)
}
