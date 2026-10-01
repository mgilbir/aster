package hierarchy

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamVegaHierarchy replays vega-hierarchy's own tests of nest and stratify (see
// internal/upstream). The recording is one call of the operator per vector: its parameters, the
// tuples of the pulse that went in and the tree the operator kept as its value. A pass that removes
// tuples (sequence above 0) is handed the tuples that remain, as the operator rebuilds its tree from
// all of them. The trees are compared by what each node holds: its tuple, depth, height, stratify id
// and children; the links to parents and the tuple lookup table are implied by those.
func TestUpstreamVegaHierarchy(t *testing.T) {
	r := upstream.Start(t, "vega-hierarchy")
	field := func(v any) (Accessor, bool) {
		m, ok := v.(map[string]any)
		if !ok || m["$"] != "accessor" {
			return nil, false
		}
		fields, _ := m["fields"].([]any)
		if len(fields) != 1 {
			return nil, false
		}
		name, _ := fields[0].(string)
		return func(d jsval.Value) jsval.Value { return d.Get(name) }, true
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || (c.Op != "nest" && c.Op != "stratify") {
			continue
		}
		in, _ := c.Input.(map[string]any)
		params, _ := c.Params.(map[string]any)
		items, ok := in["source"].([]any)
		if !ok {
			// a pass over no tuples at all carries no pulse fields
			items = []any{}
		}
		data := make([]jsval.Value, len(items))
		for j, it := range items {
			data[j] = upstream.ToValue(it)
		}
		var tree *Tree
		var err error
		switch c.Op {
		case "nest":
			keyList, _ := params["keys"].([]any)
			keys := make([]Accessor, len(keyList))
			valid := true
			for j, k := range keyList {
				if keys[j], ok = field(k); !ok {
					valid = false
				}
			}
			if !valid {
				r.Skip("nest keys that are not field accessors")
				continue
			}
			tree, _, err = Nest(data, keys, false)
		case "stratify":
			key, okKey := field(params["key"])
			parentKey, okParent := field(params["parentKey"])
			if !okKey || !okParent {
				r.Skip("stratify keys that are not field accessors")
				continue
			}
			tree, err = Stratify(data, key, parentKey)
		}
		if err != nil {
			r.CheckAgainst(c, vegaHierTree(c.Value), nil, true)
			continue
		}
		r.CheckAgainst(c, vegaHierTree(c.Value), vegaHierNode(tree.Root), false)
	}
	r.Done(5)
}

// vegaHierNode encodes a node and its subtree in the shape vegaHierTree reduces a recorded one to.
func vegaHierNode(n *Node) any {
	m := map[string]any{
		"data":   upstream.FromValue(n.Data),
		"depth":  upstream.Enc(float64(n.Depth)),
		"height": upstream.Enc(float64(n.Height)),
	}
	if n.id != "" {
		m["id"] = n.id
	}
	if len(n.Children) > 0 {
		kids := make([]any, len(n.Children))
		for i, k := range n.Children {
			kids[i] = vegaHierNode(k)
		}
		m["children"] = kids
	}
	return m
}

// vegaHierTree reduces a recorded node (an instance of d3's Node, with parent links and, on the
// root, the lookup table) to the fields vegaHierNode encodes.
func vegaHierTree(v any) any {
	n, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	m := map[string]any{"data": n["data"], "depth": n["depth"], "height": n["height"]}
	if id, ok := n["id"]; ok {
		m["id"] = id
	}
	if kids, ok := n["children"].([]any); ok {
		out := make([]any, len(kids))
		for i, k := range kids {
			out[i] = vegaHierTree(k)
		}
		m["children"] = out
	}
	return m
}
