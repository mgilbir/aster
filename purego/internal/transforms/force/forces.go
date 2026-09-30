package force

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func clampIter(n int) int {
	if n > MaxForceIterations {
		return MaxForceIterations
	}
	return n
}

// Center translates all nodes so their centroid sits at (X, Y).
type Center struct{ X, Y float64 }

func (c Center) build() force { return &centerForce{Center: c} }

type centerForce struct {
	Center
	nodes []node
}

func (f *centerForce) initialize(s *sim) error { f.nodes = s.nodes; return nil }

func (f *centerForce) apply(float64) {
	n := len(f.nodes)
	var sx, sy float64
	for i := range f.nodes {
		sx += f.nodes[i].x
		sy += f.nodes[i].y
	}
	sx = (sx/float64(n) - f.X) // strength is always 1 in Vega
	sy = (sy/float64(n) - f.Y)
	for i := range f.nodes {
		f.nodes[i].x -= sx
		f.nodes[i].y -= sy
	}
}

// Collide pushes overlapping circles apart. Radius defaults to 1.
type Collide struct {
	Radius     Param
	Strength   float64
	Iterations int
}

// NewCollide returns a Collide as Vega effectively defaults it: radius 1,
// strength 1, one iteration. The transform's definition advertises a default
// strength of 0.7, but vega-parser does not apply sub-parameter defaults, so an
// omitted strength keeps d3-force's own default of 1.
func NewCollide() Collide {
	return Collide{Radius: Constant(1), Strength: 1, Iterations: 1}
}

func (c Collide) build() force { return &collideForce{cfg: c} }

type collideForce struct {
	cfg   Collide
	s     *sim
	radii []float64
	tree  quadtree
	xs    []float64
	ys    []float64
}

func (f *collideForce) initialize(s *sim) error {
	f.s = s
	f.radii = make([]float64, len(s.nodes))
	for i := range s.nodes {
		f.radii[i] = f.cfg.Radius.eval(s.vals[i])
	}
	f.xs = make([]float64, len(s.nodes))
	f.ys = make([]float64, len(s.nodes))
	return nil
}

func (f *collideForce) apply(float64) {
	nodes := f.s.nodes
	radii := f.radii
	strength := f.cfg.Strength
	var cur int32
	var xi, yi, ri, ri2 float64
	// prepare stores the largest radius below each cell so the visit can prune.
	prepare := func(q int32) {
		nd := &f.tree.nodes[q]
		if nd.leaf {
			nd.r = radii[nd.data]
			return
		}
		nd.r = 0
		for _, c := range nd.child {
			if c != 0 && f.tree.nodes[c].r > nd.r {
				nd.r = f.tree.nodes[c].r
			}
		}
	}
	apply := func(q int32, x0, y0, x1, y1 float64) bool {
		nd := &f.tree.nodes[q]
		rj := nd.r
		r := ri + rj
		if nd.leaf {
			// Only the head of a coincident chain takes part, as upstream.
			if nd.data > cur {
				d := &nodes[nd.data]
				x := xi - d.x - d.vx
				y := yi - d.y - d.vy
				l := float64(x*x) + float64(y*y)
				if l < r*r {
					if x == 0 {
						x = f.s.rng.jiggle()
						l += float64(x * x)
					}
					if y == 0 {
						y = f.s.rng.jiggle()
						l += float64(y * y)
					}
					l = math.Sqrt(l)
					l = (r - l) / l * strength
					rj = float64(rj * rj)
					r = rj / (ri2 + rj)
					x *= l
					y *= l
					nodes[cur].vx += float64(x * r)
					nodes[cur].vy += float64(y * r)
					r = 1 - r
					d.vx -= float64(x * r)
					d.vy -= float64(y * r)
				}
			}
			return false
		}
		return x0 > xi+r || x1 < xi-r || y0 > yi+r || y1 < yi-r
	}
	for k := 0; k < f.cfg.Iterations && k < MaxForceIterations; k++ {
		for i := range nodes {
			f.xs[i] = nodes[i].x + nodes[i].vx
			f.ys[i] = nodes[i].y + nodes[i].vy
		}
		f.tree.build(f.xs, f.ys)
		f.tree.visitAfter(prepare)
		for i := range nodes {
			if f.s.halt(i) {
				return
			}
			cur = int32(i)
			ri = radii[i]
			ri2 = float64(ri * ri)
			xi = nodes[i].x + nodes[i].vx
			yi = nodes[i].y + nodes[i].vy
			f.tree.visit(apply)
		}
	}
}

// NBody is the many-body (charge) force with Barnes-Hut approximation.
type NBody struct {
	Strength    Param
	Theta       float64
	DistanceMin float64
	DistanceMax float64 // +Inf when unset
}

// NewNBody returns an NBody with Vega's defaults (strength -30, theta 0.9,
// distanceMin 1, distanceMax unbounded).
func NewNBody() NBody {
	return NBody{Strength: Constant(-30), Theta: 0.9, DistanceMin: 1, DistanceMax: math.Inf(1)}
}

func (n NBody) build() force { return &nbodyForce{cfg: n} }

type nbodyForce struct {
	cfg       NBody
	s         *sim
	strengths []float64
	tree      quadtree
	xs, ys    []float64
}

func (f *nbodyForce) initialize(s *sim) error {
	f.s = s
	f.strengths = make([]float64, len(s.nodes))
	for i := range s.nodes {
		f.strengths[i] = f.cfg.Strength.eval(s.vals[i])
	}
	f.xs = make([]float64, len(s.nodes))
	f.ys = make([]float64, len(s.nodes))
	return nil
}

func (f *nbodyForce) apply(alpha float64) {
	nodes := f.s.nodes
	if len(nodes) == 0 {
		return
	}
	strengths := f.strengths
	tree := &f.tree
	distanceMin2 := f.cfg.DistanceMin * f.cfg.DistanceMin
	distanceMax2 := f.cfg.DistanceMax * f.cfg.DistanceMax
	theta2 := f.cfg.Theta * f.cfg.Theta
	rng := &f.s.rng
	for i := range nodes {
		f.xs[i], f.ys[i] = nodes[i].x, nodes[i].y
	}
	tree.build(f.xs, f.ys)
	tree.visitAfter(func(q int32) {
		nd := &tree.nodes[q]
		var strength float64
		if !nd.leaf {
			var weight, x, y float64
			for _, c := range nd.child {
				if c == 0 {
					continue
				}
				ch := &tree.nodes[c]
				if w := math.Abs(ch.value); w != 0 && !math.IsNaN(w) {
					strength += ch.value
					weight += w
					x += float64(w * ch.x)
					y += float64(w * ch.y)
				}
			}
			nd.x = x / weight
			nd.y = y / weight
		} else {
			nd.x = f.xs[nd.data]
			nd.y = f.ys[nd.data]
			for c := q; c != 0; c = tree.nodes[c].next {
				strength += strengths[tree.nodes[c].data]
			}
		}
		nd.value = strength
	})
	var cur int32
	self := &nodes[0]
	apply := func(q int32, x1, _, x2, _ float64) bool {
		nd := &tree.nodes[q]
		if nd.value == 0 || math.IsNaN(nd.value) {
			return true
		}
		x := nd.x - self.x
		y := nd.y - self.y
		w := x2 - x1
		l := float64(x*x) + float64(y*y)
		// Barnes-Hut: treat a far enough cell as a single body. Very close
		// nodes are limited by distanceMin; coincident ones get a random push.
		if w*w/theta2 < l {
			if l < distanceMax2 {
				if x == 0 {
					x = rng.jiggle()
					l += float64(x * x)
				}
				if y == 0 {
					y = rng.jiggle()
					l += float64(y * y)
				}
				if l < distanceMin2 {
					l = math.Sqrt(distanceMin2 * l)
				}
				self.vx += x * nd.value * alpha / l
				self.vy += y * nd.value * alpha / l
			}
			return true
		} else if !nd.leaf || l >= distanceMax2 {
			return false
		}
		if nd.data != cur || nd.next != 0 {
			if x == 0 {
				x = rng.jiggle()
				l += float64(x * x)
			}
			if y == 0 {
				y = rng.jiggle()
				l += float64(y * y)
			}
			if l < distanceMin2 {
				l = math.Sqrt(distanceMin2 * l)
			}
		}
		for c := q; c != 0; c = tree.nodes[c].next {
			if d := tree.nodes[c].data; d != cur {
				w = strengths[d] * alpha / l
				self.vx += float64(x * w)
				self.vy += float64(y * w)
			}
		}
		return false
	}
	for i := range nodes {
		if f.s.halt(i) {
			return
		}
		cur = int32(i)
		self = &nodes[i]
		tree.visit(apply)
	}
}

// Link is the spring force between linked nodes.
type Link struct {
	Links      []jsval.Value // tuples with source and target
	ID         Accessor      // node id; nil means the node's index
	Distance   Param
	Strength   *Param // nil: 1 / min(degree(source), degree(target))
	Iterations int
}

// NewLink returns a Link with Vega's defaults (distance 30, one iteration).
func NewLink(links []jsval.Value) Link {
	return Link{Links: links, Distance: Constant(30), Iterations: 1}
}

func (l Link) build() force { return &linkForce{cfg: l} }

type linkForce struct {
	cfg                  Link
	s                    *sim
	src, tgt             []int32
	strengths, distances []float64
	bias                 []float64
}

type idKey struct {
	kind jsval.Kind
	n    float64
	s    string
	o    *jsval.Object
}

// keyOf approximates JavaScript's Map key equality (SameValueZero).
func keyOf(v jsval.Value) idKey {
	k := idKey{kind: v.Kind()}
	switch v.Kind() {
	case jsval.KindNum, jsval.KindTimestamp, jsval.KindBool:
		k.n = v.NumValue()
		if math.IsNaN(k.n) {
			k.n, k.s = 0, "NaN"
		}
	case jsval.KindStr:
		k.s = v.StrValue()
	case jsval.KindObj:
		k.o = v.ObjValue()
	}
	return k
}

func (f *linkForce) initialize(s *sim) error {
	f.s = s
	links := f.cfg.Links
	n, m := len(s.nodes), len(links)
	byID := make(map[idKey]int32, n)
	for i := range s.nodes {
		var id jsval.Value
		if f.cfg.ID != nil {
			id = f.cfg.ID(s.vals[i])
		} else {
			id = jsval.Int(i)
		}
		byID[keyOf(id)] = int32(i)
	}
	byObj := map[*jsval.Object]int32{}
	resolve := func(v jsval.Value) (int32, bool) {
		if v.IsObj() {
			if len(byObj) == 0 {
				for i, o := range s.objs {
					byObj[o] = int32(i)
				}
			}
			i, ok := byObj[v.ObjValue()]
			return i, ok
		}
		i, ok := byID[keyOf(v)]
		return i, ok
	}
	f.src, f.tgt = make([]int32, m), make([]int32, m)
	count := make([]float64, n)
	for i, lv := range links {
		if !lv.IsObj() {
			return fmt.Errorf("force: link %d is not an object", i)
		}
		lo := lv.ObjValue()
		lo.Set("index", jsval.Int(i))
		for _, end := range [2]struct {
			key string
			dst *int32
		}{{"source", &f.src[i]}, {"target", &f.tgt[i]}} {
			v := lo.Lookup(end.key)
			idx, ok := resolve(v)
			if !ok {
				// Same message as d3's `find`.
				return fmt.Errorf("node not found: %s", v.AsString())
			}
			*end.dst = idx
			if !v.IsObj() {
				lo.Set(end.key, s.vals[idx])
			}
			count[idx]++
		}
	}
	f.bias = make([]float64, m)
	f.strengths = make([]float64, m)
	f.distances = make([]float64, m)
	for i, lv := range links {
		cs, ct := count[f.src[i]], count[f.tgt[i]]
		f.bias[i] = cs / (cs + ct)
		if f.cfg.Strength != nil {
			f.strengths[i] = f.cfg.Strength.eval(lv)
		} else {
			f.strengths[i] = 1 / math.Min(cs, ct)
		}
		f.distances[i] = f.cfg.Distance.eval(lv)
	}
	return nil
}

func (f *linkForce) apply(alpha float64) {
	nodes := f.s.nodes
	rng := &f.s.rng
	for k := 0; k < f.cfg.Iterations && k < MaxForceIterations; k++ {
		for i := range f.src {
			if f.s.halt(i) {
				return
			}
			source, target := &nodes[f.src[i]], &nodes[f.tgt[i]]
			// `|| jiggle`: both zero and NaN trigger the random nudge.
			x := target.x + target.vx - source.x - source.vx
			if x == 0 || math.IsNaN(x) {
				x = rng.jiggle()
			}
			y := target.y + target.vy - source.y - source.vy
			if y == 0 || math.IsNaN(y) {
				y = rng.jiggle()
			}
			l := math.Sqrt(float64(x*x) + float64(y*y))
			l = (l - f.distances[i]) / l * alpha * f.strengths[i]
			x *= l
			y *= l
			b := f.bias[i]
			target.vx -= float64(x * b)
			target.vy -= float64(y * b)
			b = 1 - b
			source.vx += float64(x * b)
			source.vy += float64(y * b)
		}
	}
}

// X pulls each node's x toward a target position.
type X struct {
	Strength float64
	X        Param // target position
}

// NewX returns an X force with Vega's default strength 0.1 and target 0.
func NewX() X { return X{Strength: 0.1} }

func (x X) build() force { return &axisForce{strength: x.Strength, target: x.X} }

// Y pulls each node's y toward a target position.
type Y struct {
	Strength float64
	Y        Param
}

// NewY returns a Y force with Vega's default strength 0.1 and target 0.
func NewY() Y { return Y{Strength: 0.1} }

func (y Y) build() force { return &axisForce{strength: y.Strength, target: y.Y, isY: true} }

type axisForce struct {
	strength float64
	target   Param
	isY      bool
	nodes    []node
	z, str   []float64
}

func (f *axisForce) initialize(s *sim) error {
	f.nodes = s.nodes
	f.z = make([]float64, len(s.nodes))
	f.str = make([]float64, len(s.nodes))
	for i := range s.nodes {
		f.z[i] = f.target.eval(s.vals[i])
		if !math.IsNaN(f.z[i]) {
			f.str[i] = f.strength
		}
	}
	return nil
}

func (f *axisForce) apply(alpha float64) {
	if f.isY {
		for i := range f.nodes {
			nd := &f.nodes[i]
			nd.vy += float64((f.z[i] - nd.y) * f.str[i] * alpha)
		}
		return
	}
	for i := range f.nodes {
		nd := &f.nodes[i]
		nd.vx += float64((f.z[i] - nd.x) * f.str[i] * alpha)
	}
}
