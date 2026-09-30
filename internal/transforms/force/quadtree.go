package force

import "math"

// qnode is one cell of the quadtree. Nodes live in an arena and refer to each
// other by index (0 means "none"), so building a tree allocates nothing once
// the arena has grown to its working size.
type qnode struct {
	child [4]int32 // internal nodes: quadrants NW, NE, SW, SE
	next  int32    // leaf: next point coincident with this one
	data  int32    // leaf: index of the simulation node
	leaf  bool
	// Scratch values used by the forces (nbody: value/x/y, collide: r).
	value, x, y, r float64
}

type qframe struct {
	n              int32
	x0, y0, x1, y1 float64
}

// quadtree mirrors d3-quadtree over simulation nodes. The extent starts
// as an integer box and doubles to cover points, exactly as d3 does, so
// quadrant boundaries (and therefore Barnes-Hut groupings) match upstream.
type quadtree struct {
	nodes          []qnode
	root           int32
	x0, y0, x1, y1 float64
	xs, ys         []float64
	stack, post    []qframe
}

// maxQuadDepth bounds the split loop for points that differ by less than the
// floating point resolution of the current cell.
const maxQuadDepth = 1100

func (t *quadtree) reset() {
	t.nodes = append(t.nodes[:0], qnode{}) // index 0 is the nil node
	t.root = 0
	t.x0, t.y0, t.x1, t.y1 = math.NaN(), math.NaN(), math.NaN(), math.NaN()
}

func (t *quadtree) newNode(leaf bool, data int32) int32 {
	t.nodes = append(t.nodes, qnode{leaf: leaf, data: data})
	return int32(len(t.nodes) - 1)
}

// build indexes the points (xs[i], ys[i]) for i in [0,len(xs)). Points with a
// non-finite coordinate are ignored: d3 skips NaN, and an infinite coordinate
// would make its extent-doubling loop run forever.
func (t *quadtree) build(xs, ys []float64) {
	t.reset()
	t.xs, t.ys = xs, ys
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for i := range xs {
		x, y := xs[i], ys[i]
		if !finite(x) || !finite(y) {
			continue
		}
		x0, x1 = math.Min(x0, x), math.Max(x1, x)
		y0, y1 = math.Min(y0, y), math.Max(y1, y)
	}
	if x0 > x1 || y0 > y1 {
		return
	}
	t.cover(x0, y0)
	t.cover(x1, y1)
	for i := range xs {
		if finite(xs[i]) && finite(ys[i]) {
			t.add(int32(i))
		}
	}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (t *quadtree) cover(x, y float64) {
	x0, y0, x1, y1 := t.x0, t.y0, t.x1, t.y1
	if math.IsNaN(x0) {
		x0 = math.Floor(x)
		x1 = x0 + 1
		y0 = math.Floor(y)
		y1 = y0 + 1
	} else {
		z := x1 - x0
		if z == 0 {
			z = 1
		}
		node := t.root
		for x0 > x || x >= x1 || y0 > y || y >= y1 {
			i := b2i(y < y0)<<1 | b2i(x < x0)
			parent := t.newNode(false, -1)
			t.nodes[parent].child[i] = node
			node = parent
			z *= 2
			switch i {
			case 0:
				x1, y1 = x0+z, y0+z
			case 1:
				x0, y1 = x1-z, y0+z
			case 2:
				x1, y0 = x0+z, y1-z
			case 3:
				x0, y0 = x1-z, y1-z
			}
		}
		if t.root != 0 && !t.nodes[t.root].leaf {
			t.root = node
		}
	}
	t.x0, t.y0, t.x1, t.y1 = x0, y0, x1, y1
}

func (t *quadtree) add(d int32) {
	x, y := t.xs[d], t.ys[d]
	leaf := t.newNode(true, d)
	node := t.root
	if node == 0 {
		t.root = leaf
		return
	}
	x0, y0, x1, y1 := t.x0, t.y0, t.x1, t.y1
	var parent int32
	var i int
	for !t.nodes[node].leaf {
		xm, ym := (x0+x1)/2, (y0+y1)/2
		right, bottom := x >= xm, y >= ym
		if right {
			x0 = xm
		} else {
			x1 = xm
		}
		if bottom {
			y0 = ym
		} else {
			y1 = ym
		}
		i = b2i(bottom)<<1 | b2i(right)
		parent = node
		node = t.nodes[parent].child[i]
		if node == 0 {
			t.nodes[parent].child[i] = leaf
			return
		}
	}
	xp, yp := t.xs[t.nodes[node].data], t.ys[t.nodes[node].data]
	if x == xp && y == yp {
		t.chain(leaf, node, parent, i)
		return
	}
	for depth := 0; ; depth++ {
		np := t.newNode(false, -1)
		if parent != 0 {
			t.nodes[parent].child[i] = np
		} else {
			t.root = np
		}
		parent = np
		xm, ym := (x0+x1)/2, (y0+y1)/2
		if x >= xm {
			x0 = xm
		} else {
			x1 = xm
		}
		if y >= ym {
			y0 = ym
		} else {
			y1 = ym
		}
		i = b2i(y >= ym)<<1 | b2i(x >= xm)
		j := b2i(yp >= ym)<<1 | b2i(xp >= xm)
		if i != j {
			t.nodes[parent].child[j] = node
			t.nodes[parent].child[i] = leaf
			return
		}
		if depth > maxQuadDepth {
			// Indistinguishable at this resolution: treat as coincident.
			t.nodes[parent].child[i] = leaf
			t.nodes[leaf].next = node
			return
		}
	}
}

// chain makes leaf the new head of node's coincident list.
func (t *quadtree) chain(leaf, node, parent int32, i int) {
	t.nodes[leaf].next = node
	if parent != 0 {
		t.nodes[parent].child[i] = leaf
	} else {
		t.root = leaf
	}
}

// visit walks the tree pre-order, quadrants in NW,NE,SW,SE order. When cb
// returns true the node's children are skipped.
func (t *quadtree) visit(cb func(n int32, x0, y0, x1, y1 float64) bool) {
	if t.root == 0 {
		return
	}
	st := append(t.stack[:0], qframe{t.root, t.x0, t.y0, t.x1, t.y1})
	for len(st) > 0 {
		q := st[len(st)-1]
		st = st[:len(st)-1]
		nd := &t.nodes[q.n]
		if !cb(q.n, q.x0, q.y0, q.x1, q.y1) && !nd.leaf {
			xm, ym := (q.x0+q.x1)/2, (q.y0+q.y1)/2
			ch := nd.child // copy: cb may not grow the arena, but stay safe
			if c := ch[3]; c != 0 {
				st = append(st, qframe{c, xm, ym, q.x1, q.y1})
			}
			if c := ch[2]; c != 0 {
				st = append(st, qframe{c, q.x0, ym, xm, q.y1})
			}
			if c := ch[1]; c != 0 {
				st = append(st, qframe{c, xm, q.y0, q.x1, ym})
			}
			if c := ch[0]; c != 0 {
				st = append(st, qframe{c, q.x0, q.y0, xm, ym})
			}
		}
	}
	t.stack = st
}

// visitAfter calls cb on every node after all of its descendants.
func (t *quadtree) visitAfter(cb func(n int32)) {
	if t.root == 0 {
		return
	}
	st := append(t.stack[:0], qframe{n: t.root})
	post := t.post[:0]
	for len(st) > 0 {
		q := st[len(st)-1]
		st = st[:len(st)-1]
		nd := &t.nodes[q.n]
		if !nd.leaf {
			for _, c := range nd.child {
				if c != 0 {
					st = append(st, qframe{n: c})
				}
			}
		}
		post = append(post, q)
	}
	for k := len(post) - 1; k >= 0; k-- {
		cb(post[k].n)
	}
	t.stack, t.post = st, post
}
