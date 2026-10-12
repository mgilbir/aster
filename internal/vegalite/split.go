package vegalite

import (
	"sort"

	"github.com/mgilbir/aster/internal/jsval"
)

// withExplicit is upstream's Explicit<T>: a value plus whether the user wrote it.
type withExplicit struct {
	explicit bool
	value    Value
}

func makeExplicit(v Value) withExplicit { return withExplicit{true, v} }
func makeImplicit(v Value) withExplicit { return withExplicit{false, v} }

// split holds a component's properties, separated into user-specified
// (explicit) and derived (implicit) ones. combine() lists explicit keys first,
// which is why the order in which properties are set decides output order.
type split struct {
	explicit, implicit jsObj
}

func newSplit() *split { return &split{} }

// newSplitOf makes a split of two objects, whose keys it keeps in order.
func newSplitOf(explicit, implicit *Object) *split {
	s := &split{}
	s.explicit.init(explicit)
	s.implicit.init(implicit)
	return s
}

func (s *split) clone(cc *compileCtx) *split {
	c := &split{}
	s.explicit.deepCloneInto(cc, &c.explicit)
	s.implicit.deepCloneInto(cc, &c.implicit)
	return c
}

// combine is `{...explicit, ...implicit}`.
func (s *split) combine() *Object {
	s.explicit.tidy()
	s.implicit.tidy()
	o := jsval.NewObject(s.explicit.Len() + s.implicit.Len())
	reorder := false
	for _, e := range s.explicit.es {
		if !e.dead {
			o.Set(e.k, e.v)
		}
	}
	for _, e := range s.implicit.es {
		if e.dead {
			continue
		}
		if !reorder && !o.Has(e.k) {
			if n, ok := arrayIndex(e.k); ok && !appendKeepsJSOrder(o, n) {
				reorder = true
			}
		}
		o.Set(e.k, e.v)
	}
	if reorder {
		jsOrderKeys(nil, o)
	}
	return o
}

func (s *split) get(key string) Value {
	return firstDefined(s.explicit.Prop(key), s.implicit.Prop(key))
}

func (s *split) getWithExplicit(key string) withExplicit {
	if v := s.explicit.Prop(key); !v.IsUndefined() {
		return withExplicit{true, v}
	}
	if v := s.implicit.Prop(key); !v.IsUndefined() {
		return withExplicit{false, v}
	}
	return withExplicit{false, undef}
}

func (s *split) setWithExplicit(key string, w withExplicit) {
	if !w.value.IsUndefined() {
		s.set(key, w.value, w.explicit)
	}
}

// set is upstream's Split.set: delete the key from one side, set it on the
// other. Both are O(1) amortized (see jsObj), however many keys a split
// holds; split has many callers that do not carry the compilation, so it
// must not be slower than that (C4).
func (s *split) set(key string, v Value, explicit bool) {
	if explicit {
		s.implicit.Delete(key)
		s.explicit.Set(key, v)
	} else {
		s.explicit.Delete(key)
		s.implicit.Set(key, v)
	}
}

// jsObj is one side of a split: a JavaScript object under construction,
// with O(1) amortized Set and Delete. JavaScript orders an object's keys
// array indices first, ascending, then the rest by when they were added; a
// deleted key that is set again counts as added anew. Kept eagerly, that
// order costs a move of the later keys per insert and per delete. Here a set
// appends and a delete marks its entry dead, and the entries are put in
// order (tidy) before they are listed, with the same result.
type jsObj struct {
	es []jsObjEntry
	// at is the position of each live key in es, kept once es is past
	// jsObjIndexed entries; below that a scan is cheaper.
	at map[string]int
	// live is the number of live entries.
	live int
	// untidy says es holds dead entries or index keys out of order.
	untidy bool
	// proto is what assigning "__proto__" made the object's prototype (see
	// jsRealm).
	proto jsProto
}

type jsObjEntry struct {
	k    string
	v    Value
	dead bool
}

const jsObjIndexed = 32

// newJSObj makes a jsObj of o's keys, in o's order.
func newJSObj(o *Object) *jsObj {
	j := &jsObj{}
	j.init(o)
	return j
}

// init sets j to o's keys, in o's order.
func (j *jsObj) init(o *Object) {
	if o.Len() == 0 {
		return
	}
	j.es = make([]jsObjEntry, o.Len())
	for i := range j.es {
		j.es[i] = jsObjEntry{k: o.KeyAt(i), v: o.ValueAt(i)}
	}
	j.live = len(j.es)
	j.reindex()
}

func (j *jsObj) reindex() {
	if len(j.es) <= jsObjIndexed {
		j.at = nil
		return
	}
	j.at = make(map[string]int, j.live)
	for i, e := range j.es {
		if !e.dead {
			j.at[e.k] = i
		}
	}
}

func (j *jsObj) find(k string) int {
	if j.at != nil {
		if i, ok := j.at[k]; ok {
			return i
		}
		return -1
	}
	for i := range j.es {
		if !j.es[i].dead && j.es[i].k == k {
			return i
		}
	}
	return -1
}

// Len is the number of keys.
func (j *jsObj) Len() int { return j.live }

// Has reports whether key is present.
func (j *jsObj) Has(k string) bool { return j.find(k) >= 0 }

// Lookup is the key's own value, or undefined.
func (j *jsObj) Lookup(k string) Value {
	if i := j.find(k); i >= 0 {
		return j.es[i].v
	}
	return undef
}

// Prop is the property read j[k] (see jsval.Object.Prop and jsRealm).
func (j *jsObj) Prop(k string) Value {
	if i := j.find(k); i >= 0 {
		return j.es[i].v
	}
	if j.proto.set {
		return (&jsRealm{hosts: map[string]*jsHost{}}).inherited(j.proto, k).value()
	}
	if k == "__proto__" {
		return jsval.Obj(jsval.NewObject(0)) // Object.prototype
	}
	v, _ := jsval.Inherited(k)
	return v
}

// Set is the assignment j[k] = v: to "__proto__" the object does not hold
// as its own (JSON.parse makes one), it replaces the prototype (an object or
// null) or does nothing (see jsRealm).
func (j *jsObj) Set(k string, v Value) {
	if i := j.find(k); i >= 0 {
		j.es[i].v = v
		return
	}
	if k == "__proto__" {
		j.proto = protoAssigned(j.proto, v)
		return
	}
	if n, ok := arrayIndex(k); ok && !j.untidy && len(j.es) > 0 {
		if m, lastIdx := arrayIndex(j.es[len(j.es)-1].k); !lastIdx || m > n {
			j.untidy = true
		}
	}
	j.es = append(j.es, jsObjEntry{k: k, v: v})
	j.live++
	if j.at != nil {
		j.at[k] = len(j.es) - 1
	} else if len(j.es) > jsObjIndexed {
		j.reindex()
	}
}

// Delete is `delete j[k]`.
func (j *jsObj) Delete(k string) {
	i := j.find(k)
	if i < 0 {
		return
	}
	j.es[i] = jsObjEntry{k: k, dead: true}
	j.live--
	j.untidy = true
	if j.at != nil {
		delete(j.at, k)
	}
	// Compact once the dead entries are most of the list.
	if len(j.es) > 2*jsObjIndexed && j.live < len(j.es)/2 {
		j.tidy()
	}
}

// tidy drops the dead entries and puts the keys in JavaScript's order.
func (j *jsObj) tidy() {
	if !j.untidy {
		return
	}
	type idxEntry struct {
		n uint32
		e jsObjEntry
	}
	var idx []idxEntry
	rest := make([]jsObjEntry, 0, j.live)
	for _, e := range j.es {
		if e.dead {
			continue
		}
		if n, ok := arrayIndex(e.k); ok {
			idx = append(idx, idxEntry{n, e})
		} else {
			rest = append(rest, e)
		}
	}
	es := rest
	if len(idx) > 0 {
		sort.Slice(idx, func(a, b int) bool { return idx[a].n < idx[b].n })
		es = make([]jsObjEntry, 0, j.live)
		for _, x := range idx {
			es = append(es, x.e)
		}
		es = append(es, rest...)
	}
	j.es = es
	j.untidy = false
	j.reindex()
}

// deepCloneInto sets c to structuredClone of j: its live entries in
// JavaScript's order, each value cloned.
func (j *jsObj) deepCloneInto(cc *compileCtx, c *jsObj) {
	j.tidy()
	v5 := cc != nil && cc.v5
	if v5 {
		// 5.8's duplicate is the clone package: it keeps the prototype and
		// leaves out the keys Object.prototype holds (skipsInClone58);
		// structuredClone (6.4) does neither.
		c.proto = j.proto
	}
	if len(j.es) == 0 {
		return
	}
	c.es = make([]jsObjEntry, 0, len(j.es))
	for _, e := range j.es {
		if v5 && skipsInClone58(cc, e.k) {
			continue
		}
		c.es = append(c.es, jsObjEntry{k: e.k, v: duplicate(cc, e.v)})
	}
	c.live = len(c.es)
	c.reindex()
}

// object is the keys and values as a new object, in JavaScript's order.
func (j *jsObj) object() *Object {
	j.tidy()
	o := jsval.NewObject(j.live)
	for _, e := range j.es {
		o.Set(e.k, e.v)
	}
	return o
}

func (s *split) copyKeyFromObject(key string, o Value) {
	if v := o.Get(key); !v.IsUndefined() {
		s.set(key, v, true)
	}
}

func (s *split) copyAll(o *split) {
	comb := o.combine()
	for _, k := range comb.Keys() {
		s.setWithExplicit(k, o.getWithExplicit(k))
	}
}

// tieBreaker resolves two conflicting explicit/implicit values.
type tieBreaker func(cc *compileCtx, v1, v2 withExplicit, property, propertyOf string) withExplicit

// defaultTieBreaker keeps v1, warning when both values were specified.
func defaultTieBreaker(cc *compileCtx, v1, v2 withExplicit, property, propertyOf string) withExplicit {
	if v1.explicit && v2.explicit {
		cc.warn(msgMergeConflictingProperty(cc, property, propertyOf, v1.value, v2.value))
	}
	return v1
}

func tieBreakByComparing(compare func(a, b Value) int) tieBreaker {
	return func(cc *compileCtx, v1, v2 withExplicit, property, propertyOf string) withExplicit {
		diff := compare(v1.value, v2.value)
		if diff > 0 {
			return v1
		} else if diff < 0 {
			return v2
		}
		return defaultTieBreaker(cc, v1, v2, property, propertyOf)
	}
}

// mergeValuesWithExplicit merges two values of one property: explicit beats
// implicit, equal values collapse, otherwise the tie breaker decides. v1 may be
// nil (absent).
func mergeValuesWithExplicit(cc *compileCtx, v1 *withExplicit, v2 withExplicit, property, propertyOf string, tb tieBreaker) withExplicit {
	if tb == nil {
		tb = defaultTieBreaker
	}
	if v1 == nil || v1.value.IsUndefined() {
		return v2
	}
	switch {
	case v1.explicit && !v2.explicit:
		return *v1
	case v2.explicit && !v1.explicit:
		return v2
	case deepEqual(v1.value, v2.value):
		return *v1
	}
	return tb(cc, *v1, v2, property, propertyOf)
}
