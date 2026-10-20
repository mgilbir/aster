package vega

import (
	"strconv"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
)

// This file implements the data-changing expression functions modify() and
// setdata() (vega-functions modify.js, data.js) on the context.

// changeSet is vega-dataflow's ChangeSet: the changes modify() requests of
// one data set during one propagation, and what setdata() replaces a data
// set with. Like upstream's it is by tuple id: a tuple named for removal
// removes that tuple only, however many rows hold equal fields; an insert of
// a tuple the data set holds already is no insert, and cancels a removal of
// it; a modification applies to a tuple the data set still holds (C33). What
// is recorded is charged to the call's ledger as it is recorded and given
// back once it is applied (C5): a change set is kept from the call of
// modify() that starts it to the end of the propagation.
type changeSet struct {
	stamp int
	rem   []jsval.Value            // tuples to remove (rem)
	remp  []func(jsval.Value) bool // removal predicates (remp)
	add   []jsval.Value            // tuples to insert (add)
	mod   []tupleMod               // modifications of tuples (mod)
	bytes int64                    // what the ledger was charged for it
}

type tupleMod struct {
	tuple jsval.Value
	key   string
	value jsval.Value
}

// changeEntryBytes is what an entry of a change set's lists holds (a Value),
// and changeModBytes and changePredBytes a modification and a predicate.
const (
	changeEntryBytes = valueBytes
	changeModBytes   = 64
	changePredBytes  = 64
)

// record charges the call's ledger for n bytes the change set is about to
// keep, ending the render past the call's memory.
func (v *runView) record(cs *changeSet, n int64) {
	if err := v.led.Alloc(n); err != nil {
		failErr(err)
	}
	cs.bytes += n
}

// recordValues is append(dst, items...) for a list of a change set: the
// growth of its capacity charged before it is made, and a step of work per
// 16 values copied.
func (v *runView) recordValues(cs *changeSet, dst, items []jsval.Value) []jsval.Value {
	v.g.work(len(items)/16 + 1)
	if need := len(dst) + len(items); need > cap(dst) {
		c := max(need, 2*cap(dst))
		v.record(cs, int64(c-cap(dst))*changeEntryBytes)
		grown := make([]jsval.Value, len(dst), c)
		copy(grown, dst)
		dst = grown
	}
	return append(dst, items...)
}

// release gives back what a change set was charged, once it is applied.
func (v *runView) release(cs *changeSet) {
	v.led.Free(cs.bytes)
	cs.bytes = 0
}

// truthyPredicate is vega-util's truthy, what `remove: true` and setdata()
// remove by: every tuple.
func truthyPredicate(jsval.Value) bool { return true }

// removePredicate is modify()'s removePredicate(props): a tuple matches when
// equalObject(props, tuple) holds. The comparison walks the values, with the
// call's Walker w.
func removePredicate(w jsval.Walker, props jsval.Value) func(jsval.Value) bool {
	return func(t jsval.Value) bool {
		m := w.Meter()
		eq := modifyEqualObject(&m, props, t, 0)
		m.Done()
		return eq
	}
}

// modifyEqual is modify.js's equal(a, b): `a === b`, or both NaN, or arrays
// of equal length and equal elements, or two objects (dates, regular
// expressions, functions and arrays are objects) where equalObject(a, b)
// holds. Each value visited is a step of the walk m meters, which ends the
// call past jsval.MaxValueDepth or the call's bounds.
func modifyEqual(m *jsval.WalkMeter, a, b jsval.Value, depth int) bool {
	m.Step(depth)
	if jsval.SameRef(a, b) {
		return true
	}
	if a.IsNum() && b.IsNum() && a.NumValue() != a.NumValue() && b.NumValue() != b.NumValue() {
		return true
	}
	if a.IsArr() {
		if !b.IsArr() || a.Len() != b.Len() {
			return false
		}
		for i, x := range a.Items() {
			if !modifyEqual(m, x, b.Index(i), depth+1) {
				return false
			}
		}
		return true
	}
	if isJSObject(a) && isJSObject(b) {
		return modifyEqualObject(m, a, b, depth)
	}
	return false
}

// isJSObject is vega-util's isObject, `_ === Object(_)`: anything but a
// primitive.
func isJSObject(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindObj, jsval.KindArr, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

// modifyEqualObject is modify.js's equalObject(a, b): `for (const key in a)
// if (!equal(a[key], b[key])) return false`. A for-in loop finds the
// members of an object, the indexes of an array or of a string, and nothing
// on a number, a boolean, a date, a regular expression or a function: so a
// `remove: 1` matches every tuple, as upstream's does.
func modifyEqualObject(m *jsval.WalkMeter, a, b jsval.Value, depth int) bool {
	switch a.Kind() {
	case jsval.KindObj:
		if jsval.IsFunction(a) {
			return true
		}
		o := a.ObjValue()
		for i := 0; i < o.Len(); i++ {
			if !modifyEqual(m, o.ValueAt(i), modifyProp(b, o.KeyAt(i)), depth+1) {
				return false
			}
		}
	case jsval.KindArr:
		for i, x := range a.Items() {
			if !modifyEqual(m, x, modifyProp(b, strconv.Itoa(i)), depth+1) {
				return false
			}
		}
	case jsval.KindStr:
		// The indexes of a string are its UTF-16 code units.
		u, _ := jsval.ToArrayLike(a)
		for i := range u.Len() {
			if !modifyEqual(m, u.At(i), modifyProp(b, strconv.FormatInt(i, 10)), depth+1) {
				return false
			}
		}
	}
	return true
}

// modifyProp is the property read b[key] of the values modify() compares: an
// object's member (or what it inherits), an array's or a string's element
// or length; undefined otherwise.
func modifyProp(b jsval.Value, key string) jsval.Value {
	switch b.Kind() {
	case jsval.KindObj:
		return b.ObjValue().Prop(key)
	case jsval.KindArr:
		if key == "length" {
			return jsval.Int(b.Len())
		}
		if i, ok := parseArrayIndex(key); ok && i < b.Len() {
			return b.Index(i)
		}
	case jsval.KindStr:
		u, _ := jsval.ToArrayLike(b)
		if key == "length" {
			return jsval.Num(float64(u.Len()))
		}
		if i, ok := parseArrayIndex(key); ok {
			return u.At(int64(i))
		}
	}
	return jsval.Undefined
}

// dataInput is `this.context.data[name].input` of modify() and setdata(),
// which read it with no check that the data set exists: an unknown name is
// the TypeError of reading 'input' of undefined (a name only Object.prototype
// has reads a function, whose input is undefined: the next read fails).
func (c *rtContext) dataInput(name string) *opNode {
	roles := c.dataRoles(name)
	if roles == nil {
		if jsval.ObjectPrototypeHas(name) {
			fail("TypeError: Cannot read properties of undefined (reading 'value')")
		}
		fail("TypeError: Cannot read properties of undefined (reading 'input')")
	}
	input := roles["input"]
	if input == nil {
		fail("TypeError: Cannot read properties of undefined (reading 'value')")
	}
	return input
}

// Modify implements expr.DataWriter: vega-functions' modify(name, insert,
// remove, toggle, modify, values).
func (c *rtContext) Modify(name string, insert, remove, toggle, modify, values jsval.Value) jsval.Value {
	input := c.dataInput(name)
	cur, _ := input.value.([]jsval.Value)
	if !(len(cur) > 0 || insert.IsTruthy() || toggle.IsTruthy()) {
		return jsval.Num(0)
	}
	v := c.view
	cs := v.pending[input]
	if cs == nil || cs.stamp < c.g.clock {
		cs = &changeSet{stamp: c.g.clock}
		if v.pending == nil {
			v.pending = map[*opNode]*changeSet{}
		}
		v.pending[input] = cs
		// The callback applies the change set it was made for, as
		// upstream's closes over `changes`: a later run may start another
		// one for this data set before this one is applied.
		node, changes := input, cs
		c.g.runAfter(func(g *flowGraph) {
			if v.pending[node] == changes {
				delete(v.pending, node)
			}
			v.applyChanges(node, changes)
		}, true, 1)
	}
	if remove.IsTruthy() {
		switch {
		case remove.IsBool():
			// remove === true
			v.record(cs, changePredBytes)
			cs.remp = append(cs.remp, truthyPredicate)
		case remove.IsArr() || c.IsTuple(remove):
			cs.rem = v.recordValues(cs, cs.rem, arrayOf(remove))
		default:
			v.record(cs, changePredBytes)
			cs.remp = append(cs.remp, removePredicate(c.g.walk, remove))
		}
	}
	if insert.IsTruthy() {
		cs.add = v.recordValues(cs, cs.add, arrayOf(insert))
	}
	if toggle.IsTruthy() {
		pred := removePredicate(c.g.walk, toggle)
		found := false
		for i, t := range cur {
			c.g.rows(i, len(cur), 1)
			if pred(t) {
				found = true
				break
			}
		}
		if found {
			v.record(cs, changePredBytes)
			cs.remp = append(cs.remp, pred)
		} else {
			cs.add = v.recordValues(cs, cs.add, arrayOf(toggle))
		}
	}
	if modify.IsTruthy() {
		if jsval.IsFunction(modify) {
			panic(&expr.Error{Msg: "modify parameter must be a data tuple, not a function"})
		}
		// for (key in values): the members of an object, the indexes of
		// an array; a primitive's indexes are those of a string.
		switch values.Kind() {
		case jsval.KindObj:
			if !jsval.IsFunction(values) {
				vo := values.ObjValue()
				c.g.work(vo.Len()/16 + 1)
				v.record(cs, int64(vo.Len())*changeModBytes)
				for i := 0; i < vo.Len(); i++ {
					cs.mod = append(cs.mod, tupleMod{tuple: modify, key: vo.KeyAt(i), value: vo.ValueAt(i)})
				}
			}
		case jsval.KindArr:
			c.g.work(values.Len()/16 + 1)
			v.record(cs, int64(values.Len())*changeModBytes)
			for i, x := range values.Items() {
				cs.mod = append(cs.mod, tupleMod{tuple: modify, key: strconv.Itoa(i), value: x})
			}
		case jsval.KindStr:
			u, _ := jsval.ToArrayLike(values)
			c.g.work(int(u.Len()/16) + 1)
			v.record(cs, u.Len()*changeModBytes)
			for i := range u.Len() {
				cs.mod = append(cs.mod, tupleMod{tuple: modify, key: strconv.FormatInt(i, 10), value: u.At(i)})
			}
		}
	}
	return jsval.Num(1)
}

// tupleKey is the key a change set files a value under: vega-dataflow's
// tupleid(t). A data tuple's is its id, a scenegraph item's (items are
// tuples upstream) its sequence, set apart from the data's ids; anything
// else has none, and is filed under JavaScript's `undefined` key, 0, which
// all such values share (upstream's `cur[undefined]`). Reading the id of
// null or undefined is upstream's TypeError.
func (v *runView) tupleKey(t jsval.Value) uint64 {
	if t.IsNullish() {
		of := "undefined"
		if t.IsNull() {
			of = "null"
		}
		fail("TypeError: Cannot read properties of %s (reading 'Symbol(vega_id)')", of)
	}
	o := jsval.Tuple(t)
	if o == nil {
		return 0
	}
	if id := o.TupleID(); id != 0 {
		return uint64(id)
	}
	if it := v.itemOf(t); it != nil {
		return 1<<32 | it.Seq
	}
	return 0
}

// changeTuples is ChangeSet.pulse: the tuples a data set holds once cs is
// applied to tuples, those it keeps in their order and then those it
// inserts, ingested; the modifications are made on the tuples they name.
// The tuples are held by input, its data set's own rows, before the
// inserted ones are made.
func (v *runView) changeTuples(input *opNode, tuples []jsval.Value, cs *changeSet) []jsval.Value {
	g := v.g
	// cur: 1 a tuple held, -1 one to remove; a key not in the map is
	// upstream's undefined entry.
	cur := make(map[uint64]int8, len(tuples)+len(cs.add))
	for i, t := range tuples {
		g.rows(i, len(tuples), 2)
		cur[v.tupleKey(t)] = 1
	}
	for i, t := range cs.rem {
		g.rows(i, len(cs.rem), 2)
		cur[v.tupleKey(t)] = -1
	}
	for _, f := range cs.remp {
		for i, t := range tuples {
			g.rows(i, len(tuples), 2)
			if f(t) {
				cur[v.tupleKey(t)] = -1
			}
		}
	}
	var add []jsval.Value
	for i, t := range cs.add {
		g.rows(i, len(cs.add), 2)
		id := v.tupleKey(t)
		if cur[id] != 0 {
			// the tuple is there already: flagged for both add and
			// remove, the two cancel
			cur[id] = 1
		} else {
			add = append(add, t)
		}
	}
	out := make([]jsval.Value, 0, len(tuples)+len(add))
	for i, t := range tuples {
		g.rows(i, len(tuples), 2)
		if cur[v.tupleKey(t)] > 0 {
			out = append(out, t)
		}
	}
	for i, m := range cs.mod {
		g.rows(i, len(cs.mod), 2)
		if cur[v.tupleKey(m.tuple)] <= 0 {
			continue
		}
		o := jsval.Fields(m.tuple)
		if o == nil {
			// `t[f] = v` on a primitive, in a module's strict code
			kind := "number"
			switch m.tuple.Kind() {
			case jsval.KindStr:
				kind = "string"
			case jsval.KindBool:
				kind = "boolean"
			}
			fail("TypeError: Cannot create property '%s' on %s '%s'", m.key, kind, jsval.StringPrefix(m.tuple, jsval.MessageBytes))
		}
		o.Set(m.key, m.value)
	}
	// The data set now holds what is left and what is inserted, the
	// inserted rows charged before they are copied in.
	v.holdSrc(input, int64(len(out)+len(add)), v.rowExcess(out)+v.rowExcess(add))
	for i, t := range add {
		g.rows(i, len(add), 2)
		out = append(out, ingestTuple(t))
	}
	return out
}

// applyChanges applies a change set to a data set's input operator and runs
// the dataflow.
func (v *runView) applyChanges(input *opNode, cs *changeSet) {
	if cs == nil {
		return
	}
	defer v.release(cs)
	cur, _ := input.value.([]jsval.Value)
	out := v.changeTuples(input, cur, cs)
	input.value = out
	v.g.pulseInput(input, &flowPulse{tuples: out, changed: true})
	// Triggers that keep changing a data set (one whose trigger reads a
	// signal the change updates) loop without end upstream. Each data set
	// may be changed so many times in one run; independent data sets do
	// not share the allowance.
	if v.triggerRuns == nil {
		v.triggerRuns = map[*opNode]int{}
	}
	v.triggerRuns[input]++
	if v.limits.MaxReruns >= 0 && v.triggerRuns[input] > v.limits.MaxReruns {
		failLimit("the triggers of a data set did not settle after %d re-evaluations", v.limits.MaxReruns)
	}
	v.rerun("")
}

// SetData implements expr.DataWriter: setdata(name, tuples) replaces the
// contents of a data set, as upstream's `changeset().remove(truthy)
// .insert(tuples)`: a tuple the data set holds and the list names again
// stays where it is, once; the others are appended in the list's order.
func (c *rtContext) SetData(name string, tuples jsval.Value) jsval.Value {
	input := c.dataInput(name)
	cur, _ := input.value.([]jsval.Value)
	cs := &changeSet{remp: []func(jsval.Value) bool{truthyPredicate}, add: arrayOf(tuples)}
	out := c.view.changeTuples(input, cur, cs)
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
	// A map of the units, made at each call: charged as an array of them.
	c.view.chargeItems(2 * len(idx.m))
	out := make(map[string]int, len(idx.m))
	for k, t := range idx.m {
		out[k] = clampInt(jsval.ToNumber(t.Get("count")))
	}
	return out, true
}
