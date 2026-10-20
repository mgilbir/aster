package hierarchy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
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
//
// str is String(v), which keys the groups (upstream's `key(value) + ”`): a
// date writes to the second in the view's zone, so two dates within one
// second share a group. nil means jsval's AsString, dates as epoch
// milliseconds.
//
// Each tuple read at each level is a step of work charged to ctx's ledger,
// which polls ctx (ADR 0010): a key accessor can be an expression.
func Nest(ctx context.Context, tuples []jsval.Value, keys []Accessor, str func(jsval.Value) string, generate bool) (*Tree, []jsval.Value, error) {
	if str == nil {
		str = jsval.Value.AsString
	}
	if len(keys) > maxNestKeys {
		return nil, nil, fmt.Errorf("%w: nest: too many keys (%d)", budget.ErrLimit, len(keys))
	}
	if len(tuples) > MaxNodes {
		return nil, nil, ErrTooLarge
	}
	m := budget.NewMeter(ctx)
	rootNode := &Node{}
	var err error
	rootNode.Children, rootNode.Data, err = nestLevel(&m, tuples, keys, str, 0, jsval.Undefined, false)
	if err != nil {
		return nil, nil, err
	}
	if err := valuesChildren(&m, rootNode); err != nil {
		return nil, nil, err
	}
	if err := finish(&m, rootNode); err != nil {
		return nil, nil, err
	}
	t := &Tree{Root: rootNode}
	var gen []jsval.Value
	if generate {
		rootNode.each(func(n *Node) {
			if err == nil {
				err = m.Work(1)
			}
			if len(n.Children) > 0 {
				gen = append(gen, n.Data)
			}
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return t, gen, nil
}

// nestLevel builds the children of one group and the group's own datum.
func nestLevel(m *budget.Meter, items []jsval.Value, keys []Accessor, str func(jsval.Value) string, depth int, key jsval.Value, hasKey bool) ([]*Node, jsval.Value, error) {
	var nodes []*Node
	var values []jsval.Value
	if err := m.Work(1); err != nil {
		return nil, jsval.Undefined, err
	}
	if depth >= len(keys) {
		if err := m.Work(int64(len(items))); err != nil {
			return nil, jsval.Undefined, err
		}
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
			if err := m.Work(1); err != nil {
				return nil, jsval.Undefined, err
			}
			k := str(get(it))
			gi, ok := index[k]
			if !ok && jsval.ObjectPrototypeHas(k) {
				// `values = valuesByKey[keyValue]` of a plain object finds
				// Object.prototype's member of that name
				return nil, jsval.Undefined, &jsval.Thrown{Name: "TypeError", Msg: "values.push is not a function"}
			}
			if !ok {
				if jsval.ObjectPrototypeHas(k) {
					// valuesByKey[keyValue] of a plain object is the member
					// Object.prototype has under the name: no array.
					panic(&jsval.Thrown{Name: "TypeError", Msg: "values.push is not a function"})
				}
				gi = len(groups)
				index[k] = gi
				groups = append(groups, nil)
				names = append(names, k)
			}
			groups[gi] = append(groups[gi], it)
		}
		for _, gi := range jsKeyOrder(names) {
			kids, data, err := nestLevel(m, groups[gi], keys, str, depth+1, jsval.Str(names[gi]), true)
			if err != nil {
				return nil, jsval.Undefined, err
			}
			nodes = append(nodes, &Node{Data: data, Children: kids})
			values = append(values, data)
		}
	}
	o := jsval.NewObject(2)
	if hasKey {
		o.Set("key", key)
	}
	o.Set("values", jsval.Arr(values))
	return nodes, jsval.Obj(o), nil
}

// valuesChildren gives the leaves of a nest the children d3-hierarchy finds
// in them: vega builds the tree with hierarchy(root, n => n.values), which
// reads `values` of every node's datum, an input tuple's too. A tuple with
// a `values` member has the elements of Array.from(values) as children (a
// string's code points, an array-like's members), each read the same way
// in turn; a datum that is null or undefined is the TypeError of reading
// its `values`. Each node made is a step of work, and the tree stays within
// MaxNodes as it is built: shared arrays in the data cannot make it grow
// past the bound.
func valuesChildren(m *budget.Meter, root *Node) error {
	count := 0
	var err error
	root.eachBefore(func(n *Node) {
		count++
		if err != nil {
			return
		}
		if count > MaxNodes {
			err = ErrTooLarge
			return
		}
		if n.Children != nil {
			return // an internal node the nest made: its values are its children
		}
		d := n.Data
		if d.IsNullish() {
			err = &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + d.Kind().String() + " (reading 'values')"}
			return
		}
		v := jsval.Undefined
		if o := d.ObjValue(); o != nil {
			v = o.Prop("values")
		}
		if !v.IsTruthy() {
			return
		}
		kids, e := arrayFrom(v, MaxNodes-count)
		if e != nil {
			err = e
			return
		}
		if e := m.Work(int64(1 + len(kids))); e != nil {
			err = e
			return
		}
		if len(kids) == 0 {
			return
		}
		n.Children = make([]*Node, len(kids))
		for i, k := range kids {
			n.Children[i] = &Node{Data: k}
		}
	})
	return err
}

// arrayFrom is Array.from(v) of a truthy v: an array's elements, a string's
// code points (a lone surrogate on its own), an object's array-like members,
// and nothing for a number, boolean or date. More than room elements is
// ErrTooLarge, before any is read.
func arrayFrom(v jsval.Value, room int) ([]jsval.Value, error) {
	switch v.Kind() {
	case jsval.KindArr:
		if v.Len() > room {
			return nil, ErrTooLarge
		}
		return v.Items(), nil
	case jsval.KindStr:
		s := v.StrValue()
		if len(s) > room { // at least one byte a code point
			n := utf8.RuneCountInString(s)
			if n > room {
				return nil, ErrTooLarge
			}
		}
		var out []jsval.Value
		for len(s) > 0 {
			n := wtf8Len(s)
			out = append(out, jsval.Str(s[:n]))
			s = s[n:]
		}
		return out, nil
	case jsval.KindObj:
		a, _ := jsval.ToArrayLike(v)
		if a.Len() > int64(room) {
			return nil, ErrTooLarge
		}
		out := make([]jsval.Value, a.Len())
		for i := range out {
			out[i] = a.At(int64(i))
		}
		return out, nil
	}
	return nil, nil
}

// wtf8Len is the length in bytes of the code point s starts with: a UTF-8
// sequence, or the three bytes of a lone surrogate, or one invalid byte.
func wtf8Len(s string) int {
	if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && s[2] >= 0x80 && s[2] <= 0xBF {
		return 3
	}
	_, n := utf8.DecodeRuneInString(s)
	return n
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
//
// str is String(v), which ids and lookups go through (d3's `id += ""`,
// vega's lookup object); nil means jsval's AsString (see Nest).
//
// Each tuple read and each node linked is a step of work charged to ctx's
// ledger, which polls ctx (ADR 0010).
func Stratify(ctx context.Context, tuples []jsval.Value, key, parentKey Accessor, str func(jsval.Value) string) (*Tree, error) {
	if str == nil {
		str = jsval.Value.AsString
	}
	if len(tuples) == 0 {
		root := &Node{Data: jsval.Obj(jsval.NewObject(0))}
		t := &Tree{Root: root, byKey: map[string]*Node{}}
		t.byKey[str(key(root.Data))] = root
		return t, nil
	}
	if len(tuples) > MaxNodes {
		return nil, ErrTooLarge
	}
	m := budget.NewMeter(ctx)
	n := len(tuples)
	nodes := make([]*Node, n)
	parentID := make([]string, n)
	byKey := make(map[string]*Node, n)
	ambiguous := make(map[string]bool)
	for i, d := range tuples {
		if err := m.Work(2); err != nil {
			return nil, err
		}
		node := &Node{Data: d}
		nodes[i] = node
		if v := key(d); !v.IsNullish() {
			if s := str(v); s != "" {
				node.id = s
				if _, dup := byKey[s]; dup {
					ambiguous[s] = true
				} else {
					byKey[s] = node
				}
			}
		}
		if v := parentKey(d); !v.IsNullish() {
			parentID[i] = str(v)
		}
	}
	var root *Node
	for i, node := range nodes {
		if err := m.Work(1); err != nil {
			return nil, err
		}
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
	var err error
	root.Depth = 0
	root.eachBefore(func(nd *Node) {
		if err == nil {
			err = m.Work(1)
		}
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
	if err != nil {
		return nil, err
	}
	if reached < n {
		return nil, ErrCycle
	}
	if err := computeHeights(&m, root); err != nil {
		return nil, err
	}
	t := &Tree{Root: root, byKey: make(map[string]*Node, n)}
	// vega registers every node under String(key(tuple)) (so a missing key
	// becomes "undefined"); on duplicates the last one visited wins.
	root.each(func(nd *Node) {
		if err == nil {
			err = m.Work(2)
		}
		t.byKey[str(key(nd.Data))] = nd
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}
