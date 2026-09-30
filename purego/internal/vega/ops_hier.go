package vega

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/transforms"
	"github.com/mgilbir/aster/purego/internal/transforms/hierarchy"
)

func init() {
	tf := transformFactories

	tf["nest"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			keys := make([]hierarchy.Accessor, 0)
			for _, f := range p.fields("keys") {
				keys = append(keys, hierarchy.Accessor(f.Get))
			}
			tree, gen, err := hierarchy.Nest(pulse.tuples, keys, p.bool("generate"))
			if err != nil {
				failErr(err)
			}
			out := append(append(make([]jsval.Value, 0, len(pulse.tuples)+len(gen)), pulse.tuples...), gen...)
			res := changedPulse(pulse, out)
			res.tree = tree
			return res
		}), nil
	}

	tf["stratify"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			tree, err := hierarchy.Stratify(pulse.tuples,
				hierarchy.Accessor(p.field("key").Get), hierarchy.Accessor(p.field("parentKey").Get))
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
				common := hierarchy.Common{As: p.strs("as")}
				if f := p.field("field"); !f.IsNil() {
					common.Field = hierarchy.Accessor(f.Get)
				}
				if cs := p.compareSpec("sort"); cs != nil {
					common.Sort = nodeCompare(cs)
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
		return hierarchy.LayoutTree(n.g.ctx, t, hierarchy.TreeParams{
			Common: common, Method: p.str("method"), Size: p.nums("size"), NodeSize: p.nums("nodeSize"),
			UniformSeparation: p.has("separation") && !p.bool("separation"),
		})
	})
	tf["pack"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		pp := hierarchy.PackParams{Common: common, Size: p.nums("size"), Padding: optNum(p, "padding")}
		if f := p.field("radius"); !f.IsNil() {
			pp.Radius = hierarchy.NodeFieldPath(f.Name)
		}
		return hierarchy.LayoutPack(n.g.ctx, t, pp)
	})
	tf["partition"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		return hierarchy.LayoutPartition(n.g.ctx, t, hierarchy.PartitionParams{
			Common: common, Size: p.nums("size"), Padding: optNum(p, "padding"), Round: p.bool("round"),
		})
	})
	tf["treemap"] = layout(func(n *opNode, p *opParams, t *hierarchy.Tree, common hierarchy.Common) error {
		return hierarchy.LayoutTreemap(n.g.ctx, t, hierarchy.TreemapParams{
			Common: common, Method: p.str("method"), Ratio: optNum(p, "ratio"), Size: p.nums("size"),
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
			valid := make(map[*jsval.Object]struct{}, len(pulse.tuples))
			for _, t := range pulse.tuples {
				if o := t.ObjValue(); o != nil {
					valid[o] = struct{}{}
				}
			}
			links, err := hierarchy.TreeLinks(pulse.tree, func(t jsval.Value) bool {
				_, ok := valid[t.ObjValue()]
				return ok
			})
			if err != nil {
				failErr(err)
			}
			return changedPulse(pulse, links)
		}), nil
	}
}

// optNum reads a numeric parameter that only counts when the specification
// gave it.
func optNum(p *opParams, name string) *float64 {
	v := p.Value(name)
	if v.IsNullish() {
		return nil
	}
	f := jsval.ToNumber(v)
	return &f
}

// nodeCompare applies a compare parameter to hierarchy nodes: its field paths
// are read from the node ("value", "data.name").
func nodeCompare(cs *compareSpec) hierarchy.Compare {
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
			if c := transforms.Ascending(k.get(a), k.get(b)); c != 0 {
				return c * k.ord
			}
		}
		return 0
	}
}
