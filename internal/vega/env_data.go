package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// This file implements the data-changing expression functions modify() and
// setdata() (vega-functions modify.js, data.js) on the context.

// pendingChange accumulates the changes modify() requests on one data set
// during one propagation.
type pendingChange struct {
	stamp   int
	removes []func(jsval.Value) bool
	inserts []jsval.Value
	mods    []tupleMod
}

type tupleMod struct {
	tuple jsval.Value
	key   string
	value jsval.Value
}

// propsMatch is vega's removePredicate: every property of props equals that of
// the tuple.
func propsMatch(props jsval.Value) func(jsval.Value) bool {
	return func(t jsval.Value) bool { return equalObjects(props, t, 0) }
}

// looseEqualDepth is looseEqual for values nested depth levels deep. A value
// built at run time can nest deeper than any parsed document, so the descent
// stops (reporting unequal) at jsval.MaxValueDepth.
func looseEqualDepth(a, b jsval.Value, depth int) bool {
	if depth > jsval.MaxValueDepth {
		return false
	}
	if a.Kind() == b.Kind() {
		switch a.Kind() {
		case jsval.KindArr:
			if a.Len() != b.Len() {
				return false
			}
			for i, x := range a.Items() {
				if !looseEqualDepth(x, b.Index(i), depth+1) {
					return false
				}
			}
			return true
		case jsval.KindObj:
			return equalObjects(a, b, depth)
		}
		return jsval.Equal(a, b)
	}
	return false
}

func equalObjects(a, b jsval.Value, depth int) bool {
	o := a.ObjValue()
	if o == nil {
		return false
	}
	for i := 0; i < o.Len(); i++ {
		if !looseEqualDepth(o.ValueAt(i), b.Get(o.KeyAt(i)), depth+1) {
			return false
		}
	}
	return true
}

// Modify implements expr.DataWriter.
func (c *rtContext) Modify(name string, insert, remove, toggle, modify, values jsval.Value) jsval.Value {
	roles := c.dataRoles(name)
	input := roles["input"]
	if input == nil {
		return jsval.Num(0)
	}
	cur, _ := input.value.([]jsval.Value)
	if !(len(cur) > 0 || insert.IsTruthy() || toggle.IsTruthy()) {
		return jsval.Num(0)
	}
	v := c.view
	pc := v.pending[input]
	if pc == nil || pc.stamp < c.g.clock {
		pc = &pendingChange{stamp: c.g.clock}
		if v.pending == nil {
			v.pending = map[*opNode]*pendingChange{}
		}
		v.pending[input] = pc
		node := input
		c.g.runAfter(func(g *flowGraph) {
			v.applyChanges(node, v.pending[node])
			delete(v.pending, node)
		}, true, 1)
	}
	if remove.IsTruthy() {
		switch {
		case remove.IsBool():
			pc.removes = append(pc.removes, func(jsval.Value) bool { return true })
		case remove.IsArr():
			set := map[jsval.Value]struct{}{}
			for _, t := range remove.Items() {
				set[t] = struct{}{}
			}
			pc.removes = append(pc.removes, func(t jsval.Value) bool { _, ok := set[t]; return ok })
		case remove.IsObj():
			// an object is a tuple when it is one of the data set's tuples,
			// otherwise a property filter
			t := remove
			pc.removes = append(pc.removes, func(x jsval.Value) bool {
				if x == t {
					return true
				}
				return propsMatch(remove)(x)
			})
		}
	}
	if insert.IsTruthy() {
		pc.inserts = append(pc.inserts, arrayOf(insert)...)
	}
	if toggle.IsTruthy() {
		pred := propsMatch(toggle)
		found := false
		for _, t := range cur {
			if pred(t) {
				found = true
				break
			}
		}
		if found {
			pc.removes = append(pc.removes, pred)
		} else {
			pc.inserts = append(pc.inserts, arrayOf(toggle)...)
		}
	}
	if modify.IsTruthy() {
		if vo := values.ObjValue(); vo != nil {
			for i := 0; i < vo.Len(); i++ {
				pc.mods = append(pc.mods, tupleMod{tuple: modify, key: vo.KeyAt(i), value: vo.ValueAt(i)})
			}
		}
	}
	return jsval.Num(1)
}

// applyChanges applies a change set to a data set's input operator and runs
// the dataflow.
func (v *runView) applyChanges(input *opNode, pc *pendingChange) {
	if pc == nil {
		return
	}
	cur, _ := input.value.([]jsval.Value)
	out := make([]jsval.Value, 0, len(cur)+len(pc.inserts))
	for _, t := range cur {
		removed := false
		for _, r := range pc.removes {
			if r(t) {
				removed = true
				break
			}
		}
		if !removed {
			out = append(out, t)
		}
	}
	for _, t := range pc.inserts {
		out = append(out, ingestTuple(t))
	}
	for _, m := range pc.mods {
		if o := m.tuple.ObjValue(); o != nil {
			o.Set(m.key, m.value)
		}
	}
	input.value = out
	v.g.pulseInput(input, &flowPulse{tuples: out, changed: true})
	// A trigger that keeps firing would nest evaluations without end.
	v.reruns++
	if v.reruns > v.limits.MaxReruns {
		failLimit("data triggers did not settle after %d re-evaluations", v.limits.MaxReruns)
	}
	if err := v.g.run(""); err != nil {
		failErr(err)
	}
}

// SetData implements expr.DataWriter: replace the contents of a data set.
func (c *rtContext) SetData(name string, tuples jsval.Value) jsval.Value {
	input := c.dataRoles(name)["input"]
	if input == nil {
		return jsval.Num(0)
	}
	items := arrayOf(tuples)
	out := make([]jsval.Value, len(items))
	for i, t := range items {
		out[i] = ingestTuple(t)
	}
	input.value = out
	c.g.pulseInput(input, &flowPulse{tuples: out, changed: true})
	return jsval.Num(1)
}

// UnitCounts implements expr.UnitIndexProvider: the number of tuples each
// selection unit contributed, from the store's `index:unit` index.
func (c *rtContext) UnitCounts(name string) (map[string]int, bool) {
	n := c.dataNode(name, "index:unit")
	if n == nil {
		return nil, false
	}
	idx, _ := n.value.(*tupleIndex)
	if idx == nil {
		return nil, false
	}
	out := make(map[string]int, len(idx.m))
	for k, t := range idx.m {
		out[k] = int(jsval.ToNumber(t.Get("count")))
	}
	return out, true
}
