package hierarchy

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// d3HierFacts is one node in the recorded encoding, without its links: the recorder expands a node's
// parent and children inconsistently (whichever it met first), so a tree is compared as the breadth-first
// list of its nodes (what descendants() returns), each with its number of children, which fixes the
// shape. withData adds what stratify decides (the datum and the id), else what treemap decides (the
// value and the rectangle).
func d3HierFacts(rec map[string]any, withData bool) map[string]any {
	keys := []string{"height", "depth", "data", "id"}
	if !withData {
		keys = []string{"height", "depth", "value", "x0", "y0", "x1", "y1"}
	}
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := rec[k]; ok {
			out[k] = v
		}
	}
	children, _ := rec["children"].([]any)
	out["children"] = float64(len(children))
	return out
}

// d3HierWant lists the nodes of a recorded answer: a node stands for the breadth-first list of its
// tree, a list for its own nodes.
func d3HierWant(rec any, withData bool) any {
	var out []any
	switch x := rec.(type) {
	case []any:
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, d3HierFacts(m, withData))
			}
		}
	case map[string]any:
		level := []map[string]any{x}
		for len(level) > 0 {
			var next []map[string]any
			for _, m := range level {
				out = append(out, d3HierFacts(m, withData))
				children, _ := m["children"].([]any)
				for _, c := range children {
					next = append(next, c.(map[string]any))
				}
			}
			level = next
		}
	}
	return out
}

// d3HierGot is d3HierWant for the engine's nodes.
func d3HierGot(nodes []*Node, withData bool) any {
	out := make([]any, len(nodes))
	for i, n := range nodes {
		m := map[string]any{"height": float64(n.Height), "depth": float64(n.Depth), "children": float64(len(n.Children))}
		if withData {
			m["data"] = upstream.FromValue(n.Data)
			if n.id != "" {
				m["id"] = n.id
			}
		} else {
			m["value"] = upstream.Enc(n.Value)
			m["x0"], m["y0"], m["x1"], m["y1"] = upstream.Enc(n.X0), upstream.Enc(n.Y0), upstream.Enc(n.X1), upstream.Enc(n.Y1)
		}
		out[i] = m
	}
	return out
}

// d3HierTuples is the array a stratify call is given: an array, or a Set.
func d3HierTuples(arg any) ([]jsval.Value, bool) {
	if m, ok := arg.(map[string]any); ok && m["$"] == "set" {
		arg = m["values"]
	}
	items, ok := arg.([]any)
	if !ok {
		return nil, false
	}
	out := make([]jsval.Value, len(items))
	for i, it := range items {
		out[i] = upstream.ToValue(it)
	}
	return out, true
}

// TestUpstreamD3HierarchyStratify replays d3-hierarchy's own tests of stratify (see internal/upstream):
// a recorded call is a stratifier configured by its chain (id, parentId; each a function the recording
// names, not the code of) applied to an array of rows, and its answer the root node. Stratify builds
// the engine's tree from the same rows, and the two trees are compared node by node (datum, id, depth,
// height, number of children), as are the errors d3 throws. The `path` option is d3-hierarchy 3.1's;
// vega's stratify has only id and parentId, so the engine has no counterpart of it.
func TestUpstreamD3HierarchyStratify(t *testing.T) {
	r := upstream.Start(t, "d3-hierarchy")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Fn != "stratify()" || c.Oversized != nil {
			continue
		}
		if c.Method != "" {
			r.Skip("the accessors of the stratifier (the engine takes them as arguments)")
			continue
		}
		key, parentKey := field("id"), field("parentId")
		tuples, usable := d3HierTuples(c.Arg(0))
		for _, s := range c.ChainSteps() {
			fn, _ := s.Args[0].(map[string]any)
			switch {
			case s.Method == "path":
				r.Skip("stratify.path, which vega's stratify lacks")
				usable = false
			case s.Method == "id" && fn["name"] == "foo":
				key = field("foo")
			case s.Method == "parentId" && fn["name"] == "foo":
				parentKey = field("foo")
			case s.Method == "parentId":
				// the flare test: the id with its last dotted part dropped, none for a top level one
				parentKey = func(v jsval.Value) jsval.Value {
					id := v.Get("id").AsString()
					if i := strings.LastIndex(id, "."); i >= 0 {
						return jsval.Str(id[:i])
					}
					return jsval.Null
				}
			default:
				r.Skip("a stratifier configured in a way the recording does not hold")
				usable = false
			}
		}
		switch {
		case !usable:
			continue
		case c.ArgsContain("function"):
			r.Skip("rows whose fields are functions (toString)")
			continue
		}
		tree, err := Stratify(tuples, key, parentKey)
		if err != nil {
			r.Check(c, nil, true)
			continue
		}
		r.CheckAgainst(c, d3HierWant(c.Result, true), d3HierGot(tree.Root.Descendants(), true), false)
	}
	r.Done(19)
}

// d3HierTree rebuilds the tree a recorded node stands for, as treemap is given it: the shape, and the
// values the test summed. A leaf's datum carries its value (the Field of the layout sums them up
// again, so the internal values are checked too) and an internal node's none. The rows resquarify
// cached on a node (_squarify) are restored from the recording.
func d3HierTree(rec map[string]any) *Tree {
	var build func(m map[string]any) *Node
	build = func(m map[string]any) *Node {
		n := &Node{}
		kids, _ := m["children"].([]any)
		for _, k := range kids {
			n.Children = append(n.Children, build(k.(map[string]any)))
		}
		own := 0.0
		if len(kids) == 0 {
			own = upstream.Number(m["value"])
		}
		d := jsval.NewObject(1)
		d.Set("value", jsval.Num(own))
		n.Data = jsval.Obj(d)
		rows, _ := m["_squarify"].([]any)
		off := 0
		for _, row := range rows {
			rm := row.(map[string]any)
			count := len(rm["children"].([]any))
			n.sq = append(n.sq, &sqRow{value: upstream.Number(rm["value"]), dice: rm["dice"] == true, children: n.Children[off : off+count]})
			n.sqR = phi
			off += count
		}
		return n
	}
	root := build(rec)
	if finish(root) != nil {
		return nil
	}
	return &Tree{Root: root}
}

// d3HierTreemapParams is the layout a treemap's chain configures. ok is false for a chain the engine
// cannot be given (a padding function, a tile the recording does not name).
func d3HierTreemapParams(c *upstream.Call) (p TreemapParams, ok bool) {
	p.Common.Field = field("value")
	methods := map[string]string{
		"treemapBinary": "binary", "treemapDice": "dice", "treemapSlice": "slice", "treemapSliceDice": "slicedice",
		"treemapSquarify": "squarify", "treemapResquarify": "resquarify",
	}
	for _, s := range c.ChainSteps() {
		switch s.Method {
		case "size":
			for _, v := range s.Args[0].([]any) {
				p.Size = append(p.Size, upstream.Number(v))
			}
		case "round":
			p.Round = s.Args[0] == true
		case "padding":
			p.Padding = Float(upstream.Number(s.Args[0]))
		case "paddingInner":
			p.PaddingInner = Float(upstream.Number(s.Args[0]))
		case "paddingOuter":
			p.PaddingOuter = Float(upstream.Number(s.Args[0]))
		case "tile":
			origin, _ := upstream.IsFunction(s.Args[0])
			if name, exported := origin["export"].(string); exported && methods[name] != "" {
				p.Method = methods[name]
			} else if name, made := origin["from"].(string); made && strings.HasSuffix(name, ".ratio") && methods[strings.TrimSuffix(name, ".ratio")] != "" {
				p.Method = methods[strings.TrimSuffix(name, ".ratio")]
				p.Ratio = Float(upstream.Number(origin["args"].([]any)[0]))
			} else {
				return p, false
			}
		default:
			return p, false
		}
	}
	return p, true
}

// TestUpstreamD3HierarchyTreemap replays d3-hierarchy's own tests of treemap (see internal/upstream):
// a recorded call is a treemap configured by its chain (size, round, the paddings, tile) applied to a
// hierarchy that is summed and sorted already, and its answer that hierarchy laid out, or the list of
// its nodes (descendants()). The recorded node is rebuilt as a Tree whose leaves carry the values and
// LayoutTreemap lays it out with the same parameters (the tile functions become its method and ratio);
// the nodes are compared by their rectangle, value, depth, height and number of children. The tile
// functions on their own return undefined, their answer is in the children they move, which the
// recording does not keep, so only their use through treemap is replayed.
func TestUpstreamD3HierarchyTreemap(t *testing.T) {
	r := upstream.Start(t, "d3-hierarchy")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		switch {
		case c.Fn == "treemap()" && c.Oversized != nil:
			r.Skip("arguments too large to record")
			continue
		case strings.HasPrefix(c.Fn, "treemap") && c.Fn != "treemap()":
			r.Skip("tile functions on their own, and the functions made by treemap and the tiles")
			continue
		case c.Fn != "treemap()":
			continue
		}
		var arg any
		switch {
		case c.Method == "" && len(c.Via) == 0:
			arg = c.Arg(0)
		case c.Method == "descendants" && len(c.Via) == 1:
			arg = c.ViaSteps()[0].Args[0]
		default:
			r.Skip("the accessors of the treemap (the engine takes them as parameters)")
			continue
		}
		rec, isNode := arg.(map[string]any)
		p, ok := d3HierTreemapParams(c)
		if !isNode || !ok {
			r.Skip("a treemap or hierarchy the recording does not hold")
			continue
		}
		tree := d3HierTree(rec)
		if tree == nil || LayoutTreemap(context.Background(), tree, p) != nil {
			r.Check(c, nil, true)
			continue
		}
		r.CheckAgainst(c, d3HierWant(c.Result, false), d3HierGot(tree.Root.Descendants(), false), false)
	}
	r.Done(17)
}
