package hierarchy

import "math"

// separation gives the spacing between adjacent nodes.
type separation func(a, b *Node) float64

func defaultSeparation(a, b *Node) float64 {
	if a.Parent == b.Parent {
		return 1
	}
	return 2
}

func unitSeparation(a, b *Node) float64 { return 1 }

// tidyNode is the auxiliary node of the Buchheim et al. tidy tree algorithm.
type tidyNode struct {
	n        *Node
	parent   *tidyNode
	children []*tidyNode
	ancDef   *tidyNode // default ancestor (A)
	anc      *tidyNode // ancestor (a)
	thread   *tidyNode // t
	z, m     float64   // prelim, mod
	c, s     float64   // change, shift
	i        int       // number among siblings
}

type sizing struct {
	dx, dy   float64
	nodeSize bool
}

func (t *tidyNode) nextLeft() *tidyNode {
	if t.children != nil {
		return t.children[0]
	}
	return t.thread
}

func (t *tidyNode) nextRight() *tidyNode {
	if t.children != nil {
		return t.children[len(t.children)-1]
	}
	return t.thread
}

// tidy is d3.tree(): Reingold-Tilford layout in linear time.
func tidy(root *Node, sep separation, sz sizing) {
	pre := make([]*Node, 0, 16)
	root.eachBefore(func(n *Node) { pre = append(pre, n) })
	for _, n := range pre {
		n.tn = &tidyNode{n: n}
	}
	for _, n := range pre {
		t := n.tn
		t.anc = t
		if len(n.Children) > 0 {
			t.children = make([]*tidyNode, len(n.Children))
			for i, c := range n.Children {
				c.tn.i = i
				c.tn.parent = t
				t.children[i] = c.tn
			}
		}
	}
	t := root.tn
	phantom := &tidyNode{}
	phantom.anc = phantom
	phantom.children = []*tidyNode{t}
	t.parent = phantom

	post := root.postOrder()
	for _, n := range post {
		firstWalk(n.tn, sep)
	}
	phantom.m = -t.z
	for _, n := range pre {
		v := n.tn
		v.n.X = v.z + v.parent.m
		v.m += v.parent.m
	}

	if sz.nodeSize {
		for _, n := range pre {
			n.X *= sz.dx
			n.Y = float64(n.Depth) * sz.dy
		}
	} else {
		left, right, bottom := root, root, root
		for _, n := range pre {
			if n.X < left.X {
				left = n
			}
			if n.X > right.X {
				right = n
			}
			if n.Depth > bottom.Depth {
				bottom = n
			}
		}
		s := 1.0
		if left != right {
			s = sep(left, right) / 2
		}
		tx := s - left.X
		kx := sz.dx / (right.X + s + tx)
		by := float64(bottom.Depth)
		if by == 0 {
			by = 1
		}
		ky := sz.dy / by
		for _, n := range pre {
			n.X = (n.X + tx) * kx
			n.Y = float64(n.Depth) * ky
		}
	}
	for _, n := range pre {
		n.tn = nil
	}
}

func firstWalk(v *tidyNode, sep separation) {
	siblings := v.parent.children
	var w *tidyNode
	if v.i > 0 {
		w = siblings[v.i-1]
	}
	if v.children != nil {
		executeShifts(v)
		midpoint := (v.children[0].z + v.children[len(v.children)-1].z) / 2
		if w != nil {
			v.z = w.z + sep(v.n, w.n)
			v.m = v.z - midpoint
		} else {
			v.z = midpoint
		}
	} else if w != nil {
		v.z = w.z + sep(v.n, w.n)
	}
	anc := v.parent.ancDef
	if anc == nil {
		anc = siblings[0]
	}
	v.parent.ancDef = apportion(v, w, anc, sep)
}

func executeShifts(v *tidyNode) {
	shift, change := 0.0, 0.0
	for i := len(v.children) - 1; i >= 0; i-- {
		w := v.children[i]
		w.z += shift
		w.m += shift
		change += w.c
		shift += w.s + change
	}
}

func moveSubtree(wm, wp *tidyNode, shift float64) {
	change := shift / float64(wp.i-wm.i)
	wp.c -= change
	wp.s += shift
	wm.c += change
	wp.z += shift
	wp.m += shift
}

func nextAncestor(vim, v, ancestor *tidyNode) *tidyNode {
	if vim.anc.parent == v.parent {
		return vim.anc
	}
	return ancestor
}

func apportion(v, w, ancestor *tidyNode, sep separation) *tidyNode {
	if w == nil {
		return ancestor
	}
	vip, vop, vim := v, v, w
	vom := vip.parent.children[0]
	sip, sop, sim, som := vip.m, vop.m, vim.m, vom.m
	for {
		vim = vim.nextRight()
		vip = vip.nextLeft()
		if vim == nil || vip == nil {
			break
		}
		vom = vom.nextLeft()
		vop = vop.nextRight()
		vop.anc = v
		shift := vim.z + sim - vip.z - sip + sep(vim.n, vip.n)
		if shift > 0 {
			moveSubtree(nextAncestor(vim, v, ancestor), v, shift)
			sip += shift
			sop += shift
		}
		sim += vim.m
		sip += vip.m
		som += vom.m
		sop += vop.m
	}
	if vim != nil && vop.nextRight() == nil {
		vop.thread = vim
		vop.m += sim - sop
	}
	if vip != nil && vom.nextLeft() == nil {
		vom.thread = vip
		vom.m += sip - som
		ancestor = v
	}
	return ancestor
}

// cluster is d3.cluster(): the dendrogram layout, all leaves at one depth.
func cluster(root *Node, sep separation, sz sizing) {
	var previous *Node
	x := 0.0
	root.eachAfter(func(n *Node) {
		if len(n.Children) > 0 {
			sum, maxY := 0.0, 0.0
			for _, c := range n.Children {
				sum += c.X
				maxY = math.Max(maxY, c.Y)
			}
			n.X = sum / float64(len(n.Children))
			n.Y = 1 + maxY
		} else {
			if previous != nil {
				x += sep(n, previous)
				n.X = x
			} else {
				n.X = 0
			}
			n.Y = 0
			previous = n
		}
	})
	left, right := root, root
	for len(left.Children) > 0 {
		left = left.Children[0]
	}
	for len(right.Children) > 0 {
		right = right.Children[len(right.Children)-1]
	}
	x0 := left.X - sep(left, right)/2
	x1 := right.X + sep(right, left)/2
	root.eachAfter(func(n *Node) {
		if sz.nodeSize {
			n.X = (n.X - root.X) * sz.dx
			n.Y = (root.Y - n.Y) * sz.dy
			return
		}
		ny := 1.0
		if truthy(root.Y) {
			ny = n.Y / root.Y
		}
		n.X = (n.X - x0) / (x1 - x0) * sz.dx
		n.Y = (1 - ny) * sz.dy
	})
}
