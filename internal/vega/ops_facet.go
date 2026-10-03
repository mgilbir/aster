package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
)

func init() {
	transformFactories["facet"] = facFacet
	transformFactories["prefacet"] = facPreFacet
}

// subflowMaker instantiates a facet cell's operators and returns the root
// operator that receives the cell's tuples.
type subflowMaker func(key string, parent jsval.Value) *opNode

// makeSubflow resolves a $subflow parameter: each call forks the context and
// instantiates the sub-scope's operators in it, with the `parent` signal bound
// to the datum of the enclosing group.
func (c *rtContext) makeSubflow(spec *flowSpec) subflowMaker {
	return func(key string, parent jsval.Value) *opNode {
		sub := c.fork()
		sub.parse(spec)
		root := sub.get(spec.operators[0])
		if p := sub.signal("parent"); p != nil {
			p.value = parent
		}
		return root
	}
}

// aggCells is the value of an aggregate operator whose output faceted group
// marks read: the output tuple of each group by key.
type aggCells struct {
	byKey map[string]jsval.Value
	// Cells are indexed on first use: most aggregates never parent a facet.
	in, out []jsval.Value
	key     transforms.KeyFunc
}

// get returns the output tuple of the group with the given key.
func (a *aggCells) get(k string) jsval.Value {
	if a.byKey == nil {
		a.byKey = make(map[string]jsval.Value, len(a.out))
		for i, k := range firstKeys(a.in, a.key) {
			if i < len(a.out) {
				a.byKey[k] = a.out[i]
			}
		}
		a.in = nil
	}
	return a.byKey[k]
}

// cell is one facet subflow: a source operator that hands the cell's tuples to
// the instantiated operators.
type cell struct {
	src    *opNode
	tuples []jsval.Value
}

// facetState is the state shared by Facet and PreFacet.
type facetState struct {
	flows  map[any]*cell
	order  []any
	active []*opNode
}

func (s *facetState) subflow(n *opNode, k any, make subflowMaker, key string, parent jsval.Value, pulse *flowPulse) *cell {
	c := s.flows[k]
	if c == nil {
		g := n.g
		g.view.cells++
		if g.view.cells > g.view.limits.MaxSubflows {
			failLimit("more than %d facet cells", g.view.limits.MaxSubflows)
		}
		sf := g.add("subflow", nil)
		sf.ctx = n.ctx
		c = &cell{src: sf}
		sfc := c
		sf.tr = trFunc(func(_ *opNode, _ *opParams, in *flowPulse) *flowPulse {
			return &flowPulse{stamp: in.stamp, encode: in.encode, changed: true, tuples: sfc.tuples}
		})
		root := make(key, parent)
		root.source = []*opNode{sf}
		sf.addTarget(root)
		s.flows[k] = c
		s.order = append(s.order, k)
	}
	return c
}

func (s *facetState) activate(c *cell) { s.active = append(s.active, c.src) }

func newFacetNode(n *opNode) *facetState {
	st := &facetState{flows: map[any]*cell{}}
	n.dynTargs = &st.active
	return st
}

func facFacet(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	st := newFacetNode(n)
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		keyFn, _ := p.Get("key").(transforms.KeyFunc)
		flow, _ := p.Get("subflow").(subflowMaker)
		if keyFn == nil || flow == nil {
			fail("facet requires a key and a subflow")
		}
		cells, _ := p.Get("group").(*aggCells)
		st.active = st.active[:0]

		groups := transforms.GroupByKey(pulse.tuples, keyFn)
		seen := make(map[any]struct{}, len(groups))
		for _, g := range groups {
			var parent jsval.Value
			if cells != nil {
				parent = cells.get(g.Key)
			}
			cl := st.subflow(n, g.Key, flow, g.Key, parent, pulse)
			cl.tuples = g.Tuples
			st.activate(cl)
			seen[g.Key] = struct{}{}
		}
		// Cells whose tuples are all gone still run, to retire their items.
		for _, k := range st.order {
			if _, ok := seen[k]; ok {
				continue
			}
			cl := st.flows[k]
			if len(cl.tuples) > 0 {
				cl.tuples = nil
				st.activate(cl)
			}
		}
		return pulse
	}), nil
}

func facPreFacet(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	st := newFacetNode(n)
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		flow, _ := p.Get("subflow").(subflowMaker)
		if flow == nil {
			fail("prefacet requires a subflow")
		}
		field, hasField := p.Get("field").(transforms.Field)
		if p.Modified("field") && n.stamp > 1 && hasField {
			// upstream: "PreFacet does not support field modification"
			_ = field
		}
		st.active = st.active[:0]
		seen := make(map[any]struct{}, len(pulse.tuples))
		for _, t := range pulse.tuples {
			cl := st.subflow(n, t.Key(), flow, "", t, pulse)
			if hasField && !field.IsNil() {
				arr := field.Apply(t)
				items := arr.Items()
				tuples := make([]jsval.Value, len(items))
				for i, it := range items {
					tuples[i] = ingestTuple(it)
				}
				cl.tuples = tuples
			} else {
				cl.tuples = []jsval.Value{t}
			}
			st.activate(cl)
			seen[t.Key()] = struct{}{}
		}
		for _, k := range st.order {
			if _, ok := seen[k]; ok {
				continue
			}
			cl := st.flows[k]
			if len(cl.tuples) > 0 {
				cl.tuples = nil
				st.activate(cl)
			}
		}
		return pulse
	}), nil
}
