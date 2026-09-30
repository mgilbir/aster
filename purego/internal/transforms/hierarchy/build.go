package hierarchy

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Accessor reads a field of a tuple.
type Accessor func(jsval.Value) jsval.Value

// maxNestKeys bounds the number of nest levels.
const maxNestKeys = 256

// Nest groups tuples into a tree by the given key fields, outermost first.
//
// Leaves are the input tuples; every internal node's Data is a new object
// {key, values} (the root has only {values}). When generate is true those
// internal tuples are also returned, breadth-first, so the caller can add
// them to the output dataset as vega's Nest does.
func Nest(tuples []jsval.Value, keys []Accessor, generate bool) (*Tree, []jsval.Value, error) {
	if len(keys) > maxNestKeys {
		return nil, nil, fmt.Errorf("nest: too many keys (%d)", len(keys))
	}
	if len(tuples) > MaxNodes {
		return nil, nil, ErrTooLarge
	}
	rootNode := &Node{}
	rootNode.Children, rootNode.Data = nestLevel(tuples, keys, 0, jsval.Undefined, false)
	if err := finish(rootNode); err != nil {
		return nil, nil, err
	}
	t := &Tree{Root: rootNode}
	var gen []jsval.Value
	if generate {
		rootNode.each(func(n *Node) {
			if len(n.Children) > 0 {
				gen = append(gen, n.Data)
			}
		})
	}
	return t, gen, nil
}

// nestLevel builds the children of one group and the group's own datum.
func nestLevel(items []jsval.Value, keys []Accessor, depth int, key jsval.Value, hasKey bool) ([]*Node, jsval.Value) {
	var nodes []*Node
	var values []jsval.Value
	if depth >= len(keys) {
		nodes = make([]*Node, len(items))
		for i, it := range items {
			nodes[i] = &Node{Data: it}
		}
		values = items
	} else {
		get := keys[depth]
		index := make(map[string]int)
		var groups [][]jsval.Value
		var names []string
		for _, it := range items {
			k := get(it).AsString()
			gi, ok := index[k]
			if !ok {
				gi = len(groups)
				index[k] = gi
				groups = append(groups, nil)
				names = append(names, k)
			}
			groups[gi] = append(groups[gi], it)
		}
		for _, gi := range jsKeyOrder(names) {
			kids, data := nestLevel(groups[gi], keys, depth+1, jsval.Str(names[gi]), true)
			nodes = append(nodes, &Node{Data: data, Children: kids})
			values = append(values, data)
		}
	}
	o := jsval.NewObject(2)
	if hasKey {
		o.Set("key", key)
	}
	o.Set("values", jsval.Arr(values))
	return nodes, jsval.Obj(o)
}

// jsKeyOrder returns indices into names in the order a JavaScript for-in loop
// over an object with those keys would visit them: canonical array-index
// keys ("0", "17", not "017") ascending numerically, then the rest in
// insertion order. vega's nest groups through a plain object, so this shows
// up in the output order of numeric keys such as years.
func jsKeyOrder(names []string) []int {
	type idx struct {
		i int
		n uint64
	}
	var num []idx
	var rest []int
	for i, s := range names {
		if n, ok := arrayIndex(s); ok {
			num = append(num, idx{i, n})
		} else {
			rest = append(rest, i)
		}
	}
	if len(num) == 0 {
		out := make([]int, len(names))
		for i := range out {
			out[i] = i
		}
		return out
	}
	sort.Slice(num, func(a, b int) bool { return num[a].n < num[b].n })
	out := make([]int, 0, len(names))
	for _, x := range num {
		out = append(out, x.i)
	}
	return append(out, rest...)
}

func arrayIndex(s string) (uint64, bool) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n >= 4294967295 {
		return 0, false
	}
	return n, true
}

// Errors reported by Stratify, with d3-hierarchy's messages.
var (
	ErrNoRoot        = errors.New("no root")
	ErrMultipleRoots = errors.New("multiple roots")
	ErrCycle         = errors.New("cycle")
)

// Stratify builds a tree from tuples linked by id and parent id fields.
//
// As in d3, an id or parent id that is null, undefined or the empty string
// counts as absent: a tuple without a parent id is a root and must be unique.
// Duplicate ids are only an error when some tuple names one as its parent.
// An empty input yields a single root with an empty object as datum.
func Stratify(tuples []jsval.Value, key, parentKey Accessor) (*Tree, error) {
	if len(tuples) == 0 {
		root := &Node{Data: jsval.Obj(jsval.NewObject(0))}
		t := &Tree{Root: root, byKey: map[string]*Node{}}
		t.byKey[key(root.Data).AsString()] = root
		return t, nil
	}
	if len(tuples) > MaxNodes {
		return nil, ErrTooLarge
	}
	n := len(tuples)
	nodes := make([]*Node, n)
	parentID := make([]string, n)
	byKey := make(map[string]*Node, n)
	ambiguous := make(map[string]bool)
	for i, d := range tuples {
		node := &Node{Data: d}
		nodes[i] = node
		if v := key(d); !v.IsNullish() {
			if s := v.AsString(); s != "" {
				node.id = s
				if _, dup := byKey[s]; dup {
					ambiguous[s] = true
				} else {
					byKey[s] = node
				}
			}
		}
		if v := parentKey(d); !v.IsNullish() {
			parentID[i] = v.AsString()
		}
	}
	var root *Node
	for i, node := range nodes {
		if pid := parentID[i]; pid != "" {
			parent := byKey[pid]
			if parent == nil {
				return nil, fmt.Errorf("missing: %s", pid)
			}
			if ambiguous[pid] {
				return nil, fmt.Errorf("ambiguous: %s", pid)
			}
			parent.Children = append(parent.Children, node)
			node.Parent = parent
		} else {
			if root != nil {
				return nil, ErrMultipleRoots
			}
			root = node
		}
	}
	if root == nil {
		return nil, ErrNoRoot
	}
	// Nodes unreachable from the root are part of a cycle.
	reached := 0
	tooDeep := false
	root.Depth = 0
	root.eachBefore(func(nd *Node) {
		reached++
		if nd.Depth > MaxDepth {
			tooDeep = true
			return
		}
		for _, c := range nd.Children {
			c.Depth = nd.Depth + 1
		}
	})
	if tooDeep {
		return nil, ErrTooLarge
	}
	if reached < n {
		return nil, ErrCycle
	}
	computeHeights(root)
	t := &Tree{Root: root, byKey: make(map[string]*Node, n)}
	// vega registers every node under String(key(tuple)) (so a missing key
	// becomes "undefined"); on duplicates the last one visited wins.
	root.each(func(nd *Node) { t.byKey[key(nd.Data).AsString()] = nd })
	return t, nil
}
