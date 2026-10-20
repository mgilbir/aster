package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
	"github.com/mgilbir/aster/internal/transforms/hierarchy"
)

func init() {
	tf := transformFactories

	tf["nest"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			keys := make([]hierarchy.Accessor, 0)
			for _, f := range p.fields("keys") {
				if f.IsNil() && len(pulse.tuples) > 0 {
					// a null accessor is called as key(value) at its depth
					fail("TypeError: key is not a function")
				}
				keys = append(keys, hierarchy.Accessor(f.Get))
			}
			tree, gen, err := hierarchy.Nest(n.g.ctx, pulse.tuples, keys, transforms.KeyStringer(n.g.ctx), p.bool("generate"))
			if err != nil {
				failErr(err)
			}
			n.g.view.holdCreated(n, 0, gen) // the generated parent nodes
			out := append(append(make([]jsval.Value, 0, len(pulse.tuples)+len(gen)), pulse.tuples...), gen...)
			res := changedPulse(pulse, out)
			res.tree = tree
			return res
		}), nil
	}

	tf["stratify"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			if pulse.noSource {
				fail("Stratify transform requires an upstream data source.")
			}
			key, parentKey := p.field("key"), p.field("parentKey")
			// A null accessor (a falsy field) is called as a function:
			// d3's stratify calls id and parentId on every tuple, and an
			// empty input is looked up by key.
			switch {
			case len(pulse.tuples) == 0 && key.IsNil():
				fail("TypeError: filter is not a function")
			case len(pulse.tuples) > 0 && key.IsNil():
				fail("TypeError: currentId is not a function")
			case len(pulse.tuples) > 0 && parentKey.IsNil():
				fail("TypeError: currentParentId is not a function")
			}
			tree, err := hierarchy.Stratify(n.g.ctx, pulse.tuples,
				hierarchy.Accessor(key.Get), hierarchy.Accessor(parentKey.Get),
				transforms.KeyStringer(n.g.ctx))
			if err != nil {
				failErr(err)
			}
			res := changedPulse(pulse, append([]jsval.Value(nil), pulse.tuples...))
			res.tree = tree
			return res
		}), nil
	}

	layout := func(run func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error) factory {
		return func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
			return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
				if pulse.tree == nil {
					fail("hierarchy layout transform requires a backing tree data source")
				}
				common := hierarchy.Common{As: p.asList(hierarchyFields[e.typ]...), AsExact: true}
				if f := p.field("field"); !f.IsNil() {
					common.Field = hierarchy.Accessor(f.Get)
				}
				if cs := p.compareSpec("sort"); cs != nil {
					common.Sort = nodeCompare(n.g.view.walk, cs)
				}
				if err := run(n, p, pulse.tree, common); err != nil {
					failErr(err)
				}
				out := *pulse
				out.changed = true
				return &out
			}), nil
		}
	}

	tf["tree"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		// Tree.layout: m = method || 'tidy', one of Layouts' own keys
		m, _ := p.orValue(jsval.Str("tidy"), "method").PropertyKey(64)
		if m != "tidy" && m != "cluster" {
			fail("Unrecognized Tree layout method: %s", m)
		}
		return hierarchy.LayoutTree(n.g.ctx, t, hierarchy.TreeParams{
			Common: common, Method: m, Size: layoutSize(p, "size"), NodeSize: layoutSize(p, "nodeSize"),
			UniformSeparation: strictFalse(p.Get("separation")), // _.separation !== false
		})
	})
	tf["pack"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		pp := hierarchy.PackParams{Common: common, Size: layoutSize(p, "size"), Padding: optNum(p, "padding")}
		if f := p.field("radius"); !f.IsNil() {
			pp.Radius = hierarchy.NodeFieldPath(f.Name)
		}
		return hierarchy.LayoutPack(n.g.ctx, t, pp)
	})
	tf["partition"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		return hierarchy.LayoutPartition(n.g.ctx, t, hierarchy.PartitionParams{
			Common: common, Size: layoutSize(p, "size"), Padding: optNum(p, "padding"), Round: p.bool("round"),
		})
	})
	tf["treemap"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		return hierarchy.LayoutTreemap(n.g.ctx, t, hierarchy.TreemapParams{
			Common: common, Method: treemapMethod(p), Ratio: optNum(p, "ratio"), Size: layoutSize(p, "size"),
			Round: p.bool("round"), Padding: optNum(p, "padding"), PaddingInner: optNum(p, "paddingInner"),
			PaddingOuter: optNum(p, "paddingOuter"), PaddingTop: optNum(p, "paddingTop"),
			PaddingRight: optNum(p, "paddingRight"), PaddingBottom: optNum(p, "paddingBottom"),
			PaddingLeft: optNum(p, "paddingLeft"),
		})
	})

	tf["treelinks"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			if pulse.tree == nil {
				fail("TreeLinks transform requires a tree data source.")
			}
			// `lut[tupleid(t)] = 1` over the pulse's tuples: a node is
			// valid when its tuple's id is among them.
			valid := make(map[uint64]struct{}, len(pulse.tuples))
			for i, t := range pulse.tuples {
				n.g.rows(i, len(pulse.tuples), 2)
				if id := n.g.view.tupleKey(t); id != 0 {
					valid[id] = struct{}{}
				}
			}
			links, err := hierarchy.TreeLinks(n.g.ctx, pulse.tree, func(t jsval.Value) bool {
				if t.IsNullish() {
					return false
				}
				_, ok := valid[n.g.view.tupleKey(t)]
				return ok
			})
			if err != nil {
				failErr(err)
			}
			n.g.view.holdCreated(n, 0, links)
			return changedPulse(pulse, links)
		}), nil
	}
}

// optNum reads a numeric parameter that only counts when the specification
// gave it.
// optNum is a numeric layout parameter as setParams hands it on when
// `p in _` (null too): +x, nil when absent.
func optNum(p *opParams, name string) *float64 {
	x := p.Get(name)
	if x == nil {
		return nil
	}
	v, _ := paramValueOf(x)
	f := jsval.ToNumber(v)
	return &f
}

// layoutSize is a size or nodeSize parameter as d3's layout.size(x) reads
// it when `p in _`: [+x[0], +x[1]], a TypeError for null; nil when absent.
func layoutSize(p *opParams, name string) []float64 {
	x := p.Get(name)
	if x == nil {
		return nil
	}
	if paramNullish(x) {
		fail("TypeError: Cannot read properties of %s (reading '0')", nullishText(x))
	}
	return []float64{jsval.ToNumber(paramElem(x, 0)), jsval.ToNumber(paramElem(x, 1))}
}

// treemapMethod is Treemap's x.method(_.method), when `'method' in _`:
// one of Tiles' own keys, by the text the value names.
func treemapMethod(p *opParams) string {
	x := p.Get("method")
	if x == nil {
		return ""
	}
	v, _ := paramValueOf(x)
	m, _ := v.PropertyKey(64)
	switch m {
	case "binary", "dice", "slice", "slicedice", "squarify", "resquarify":
		return m
	}
	fail("Unrecognized Treemap layout method: %s", jsval.StringPrefix(v, jsval.MessageBytes))
	return ""
}

// hierarchyFields are each layout's output fields (its `fields`), which
// `_.as || fields` names.
var hierarchyFields = map[string][]string{
	"tree":      {"x", "y", "depth", "children"},
	"pack":      {"x", "y", "r", "depth", "children"},
	"partition": {"x0", "y0", "x1", "y1", "depth", "children"},
	"treemap":   {"x0", "y0", "x1", "y1", "depth", "children"},
}

// nodeCompare applies a compare parameter to hierarchy nodes: its field paths
// are read from the node ("value", "data.name").
func nodeCompare(w jsval.Walker, cs *compareSpec) hierarchy.Compare {
	type key struct {
		get hierarchy.NodeField
		ord int
	}
	keys := make([]key, len(cs.fields))
	for i, f := range cs.fields {
		k := key{get: hierarchy.NodeFieldPath(f), ord: 1}
		if i < len(cs.orders) && cs.orders[i] == transforms.Desc {
			k.ord = -1
		}
		keys[i] = k
	}
	return func(a, b *hierarchy.Node) int {
		for _, k := range keys {
			if c := transforms.Ascending(w, k.get(a), k.get(b)); c != 0 {
				return c * k.ord
			}
		}
		return 0
	}
}
