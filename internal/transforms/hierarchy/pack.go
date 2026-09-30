package hierarchy

import (
	"context"
	"errors"
	"math"
)

// errPackBasis is d3's bare `throw new Error` when the enclosing-circle basis
// cannot be extended; it only happens with NaN or infinite radii.
var errPackBasis = errors.New("pack: cannot enclose circles (invalid radius)")

// lcg is d3-hierarchy's deterministic linear congruential generator, used to
// shuffle circles before computing enclosing circles.
type lcg struct{ s uint64 }

func newLCG() *lcg { return &lcg{s: 1} }

func (l *lcg) next() float64 {
	l.s = (1664525*l.s + 1013904223) % 4294967296
	return float64(l.s) / 4294967296
}

type circle struct{ x, y, r float64 }

func shuffle(cs []circle, rnd *lcg) {
	m := len(cs)
	for m > 0 {
		i := int(rnd.next() * float64(m))
		m--
		cs[m], cs[i] = cs[i], cs[m]
	}
}

// packEnclose is d3's packEncloseRandom: the smallest circle enclosing cs
// (Welzl-style, on a shuffled copy).
func packEnclose(ctx context.Context, in []circle, rnd *lcg) (circle, error) {
	cs := append([]circle(nil), in...)
	shuffle(cs, rnd)
	var basis []circle
	var e circle
	haveE := false
	i, steps := 0, 0
	for i < len(cs) {
		steps++
		if err := ctxErr(ctx, steps); err != nil {
			return circle{}, err
		}
		p := cs[i]
		if haveE && enclosesWeak(e, p) {
			i++
			continue
		}
		var err error
		basis, err = extendBasis(basis, p)
		if err != nil {
			return circle{}, err
		}
		e = encloseBasis(basis)
		haveE = true
		i = 0
	}
	return e, nil
}

func extendBasis(B []circle, p circle) ([]circle, error) {
	if enclosesWeakAll(p, B) {
		return []circle{p}, nil
	}
	for i := range B {
		if enclosesNot(p, B[i]) && enclosesWeakAll(encloseBasis2(B[i], p), B) {
			return []circle{B[i], p}, nil
		}
	}
	for i := 0; i < len(B)-1; i++ {
		for j := i + 1; j < len(B); j++ {
			if enclosesNot(encloseBasis2(B[i], B[j]), p) &&
				enclosesNot(encloseBasis2(B[i], p), B[j]) &&
				enclosesNot(encloseBasis2(B[j], p), B[i]) &&
				enclosesWeakAll(encloseBasis3(B[i], B[j], p), B) {
				return []circle{B[i], B[j], p}, nil
			}
		}
	}
	return nil, errPackBasis
}

func enclosesNot(a, b circle) bool {
	dr := a.r - b.r
	dx, dy := b.x-a.x, b.y-a.y
	return dr < 0 || float64(dr*dr) < float64(dx*dx)+float64(dy*dy)
}

func enclosesWeak(a, b circle) bool {
	dr := a.r - b.r + float64(math.Max(math.Max(a.r, b.r), 1)*1e-9)
	dx, dy := b.x-a.x, b.y-a.y
	return dr > 0 && float64(dr*dr) > float64(dx*dx)+float64(dy*dy)
}

func enclosesWeakAll(a circle, B []circle) bool {
	for _, b := range B {
		if !enclosesWeak(a, b) {
			return false
		}
	}
	return true
}

func encloseBasis(B []circle) circle {
	switch len(B) {
	case 1:
		return B[0]
	case 2:
		return encloseBasis2(B[0], B[1])
	default:
		return encloseBasis3(B[0], B[1], B[2])
	}
}

func encloseBasis2(a, b circle) circle {
	x21, y21, r21 := b.x-a.x, b.y-a.y, b.r-a.r
	l := math.Sqrt(float64(x21*x21) + float64(y21*y21))
	return circle{
		x: (a.x + b.x + float64(x21/l*r21)) / 2,
		y: (a.y + b.y + float64(y21/l*r21)) / 2,
		r: (l + a.r + b.r) / 2,
	}
}

func encloseBasis3(a, b, c circle) circle {
	x1, y1, r1 := a.x, a.y, a.r
	x2, y2, r2 := b.x, b.y, b.r
	x3, y3, r3 := c.x, c.y, c.r
	a2, a3 := x1-x2, x1-x3
	b2, b3 := y1-y2, y1-y3
	c2, c3 := r2-r1, r3-r1
	d1 := float64(x1*x1) + float64(y1*y1) - float64(r1*r1)
	d2 := d1 - float64(x2*x2) - float64(y2*y2) + float64(r2*r2)
	d3 := d1 - float64(x3*x3) - float64(y3*y3) + float64(r3*r3)
	ab := float64(a3*b2) - float64(a2*b3)
	xa := (float64(b2*d3)-float64(b3*d2))/(ab*2) - x1
	xb := (float64(b3*c2) - float64(b2*c3)) / ab
	ya := (float64(a3*d2)-float64(a2*d3))/(ab*2) - y1
	yb := (float64(a2*c3) - float64(a3*c2)) / ab
	A := float64(xb*xb) + float64(yb*yb) - 1
	B := 2 * (r1 + float64(xa*xb) + float64(ya*yb))
	C := float64(xa*xa) + float64(ya*ya) - float64(r1*r1)
	var r float64
	if math.Abs(A) > 1e-6 {
		r = -((B + math.Sqrt(float64(B*B)-float64(4*A*C))) / (2 * A))
	} else {
		r = -(C / B)
	}
	return circle{x: x1 + xa + float64(xb*r), y: y1 + ya + float64(yb*r), r: r}
}

// chain is a node of the front-chain, a circular doubly linked list.
type chain struct {
	c        *Node
	next     *chain
	previous *chain
}

func place(b, a, c *Node) {
	dx, dy := b.X-a.X, b.Y-a.Y
	d2 := float64(dx*dx) + float64(dy*dy)
	if d2 != 0 {
		a2 := a.R + c.R
		a2 = float64(a2 * a2)
		b2 := b.R + c.R
		b2 = float64(b2 * b2)
		if a2 > b2 {
			x := (d2 + b2 - a2) / (2 * d2)
			y := math.Sqrt(math.Max(0, b2/d2-float64(x*x)))
			c.X = b.X - float64(x*dx) - float64(y*dy)
			c.Y = b.Y - float64(x*dy) + float64(y*dx)
		} else {
			x := (d2 + a2 - b2) / (2 * d2)
			y := math.Sqrt(math.Max(0, a2/d2-float64(x*x)))
			c.X = a.X + float64(x*dx) - float64(y*dy)
			c.Y = a.Y + float64(x*dy) + float64(y*dx)
		}
	} else {
		c.X = a.X + c.R
		c.Y = a.Y
	}
}

func intersects(a, b *Node) bool {
	dr := a.R + b.R - 1e-6
	dx, dy := b.X-a.X, b.Y-a.Y
	return dr > 0 && float64(dr*dr) > float64(dx*dx)+float64(dy*dy)
}

func score(n *chain) float64 {
	a, b := n.c, n.next.c
	ab := a.R + b.R
	dx := (float64(a.X*b.R) + float64(b.X*a.R)) / ab
	dy := (float64(a.Y*b.R) + float64(b.Y*a.R)) / ab
	return float64(dx*dx) + float64(dy*dy)
}

// packSiblings is d3's packSiblingsRandom: it places the circles (nodes'
// X, Y, R) tangent to each other around the origin and returns the radius of
// the enclosing circle centred there.
func packSiblings(ctx context.Context, circles []*Node, rnd *lcg) (float64, error) {
	n := len(circles)
	if n == 0 {
		return 0, nil
	}
	a := circles[0]
	a.X, a.Y = 0, 0
	if n == 1 {
		return a.R, nil
	}
	b := circles[1]
	a.X = -b.R
	b.X = a.R
	b.Y = 0
	if n == 2 {
		return a.R + b.R, nil
	}
	c := circles[2]
	place(b, a, c)

	an, bn, cn := &chain{c: a}, &chain{c: b}, &chain{c: c}
	an.next, cn.previous = bn, bn
	bn.next, an.previous = cn, cn
	cn.next, bn.previous = an, an

	steps := 0
pack:
	for i := 3; i < n; i++ {
		steps++
		if err := ctxErr(ctx, steps); err != nil {
			return 0, err
		}
		c = circles[i]
		place(an.c, bn.c, c)
		cn = &chain{c: c}

		// Find the closest intersecting circle on the front-chain, if any;
		// closeness is linear distance along the chain.
		j, k := bn.next, an.previous
		sj, sk := bn.c.R, an.c.R
		for {
			if sj <= sk {
				if intersects(j.c, cn.c) {
					bn = j
					an.next, bn.previous = bn, an
					i-- // the loop increment retries this circle
					continue pack
				}
				sj += j.c.R
				j = j.next
			} else {
				if intersects(k.c, cn.c) {
					an = k
					an.next, bn.previous = bn, an
					i--
					continue pack
				}
				sk += k.c.R
				k = k.previous
			}
			if j == k.next {
				break
			}
		}

		cn.previous, cn.next = an, bn
		an.next, bn.previous = cn, cn
		bn = cn

		aa := score(an)
		for x := cn.next; x != bn; x = x.next {
			if ca := score(x); ca < aa {
				an, aa = x, ca
			}
		}
		bn = an.next
	}

	front := []circle{{bn.c.X, bn.c.Y, bn.c.R}}
	for x := bn.next; x != bn; x = x.next {
		front = append(front, circle{x.c.X, x.c.Y, x.c.R})
	}
	e, err := packEnclose(ctx, front, rnd)
	if err != nil {
		return 0, err
	}
	for _, ci := range circles {
		ci.X -= e.x
		ci.Y -= e.y
	}
	return e.r, nil
}

// pack is d3.pack(): sizes leaves, packs siblings bottom-up and scales the
// result to size.
func pack(ctx context.Context, root *Node, radius func(*Node) float64, dx, dy, padding float64) error {
	rnd := newLCG()
	root.X, root.Y = dx/2, dy/2
	var err error
	step := 0
	packChildren := func(pad, k float64) {
		root.eachAfter(func(n *Node) {
			if err != nil || len(n.Children) == 0 {
				return
			}
			step++
			if err = ctxErr(ctx, step); err != nil {
				return
			}
			r := pad * k
			if r != r {
				r = 0
			}
			if r != 0 {
				for _, c := range n.Children {
					c.R += r
				}
			}
			var e float64
			e, err = packSiblings(ctx, n.Children, rnd)
			if r != 0 {
				for _, c := range n.Children {
					c.R -= r
				}
			}
			n.R = e + r
		})
	}
	translate := func(k float64) {
		root.eachBefore(func(n *Node) {
			n.R *= k
			if p := n.Parent; p != nil {
				n.X = p.X + float64(k*n.X)
				n.Y = p.Y + float64(k*n.Y)
			}
		})
	}
	setLeaf := func(f func(*Node) float64) {
		root.eachBefore(func(n *Node) {
			if len(n.Children) == 0 {
				r := f(n)
				if r != r {
					r = 0
				}
				n.R = math.Max(0, r)
			}
		})
	}
	if radius != nil {
		setLeaf(radius)
		packChildren(padding, 0.5)
		if err != nil {
			return err
		}
		translate(1)
	} else {
		setLeaf(func(n *Node) float64 { return math.Sqrt(n.Value) })
		packChildren(0, 1)
		if err != nil {
			return err
		}
		packChildren(padding, root.R/math.Min(dx, dy))
		if err != nil {
			return err
		}
		translate(math.Min(dx, dy) / (2 * root.R))
	}
	return err
}
