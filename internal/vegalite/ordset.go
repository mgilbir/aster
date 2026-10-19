package vegalite

import (
	"slices"
	"sort"

	"github.com/mgilbir/aster/internal/jsval"
)

// sset is an insertion-ordered set of values: JavaScript's Set, whose
// members upstream's types call strings but which holds whatever the spec
// gave (a transform's `as` of ["a"], a group-by of null). Members are told
// apart as a Set tells them apart, by SameValueZero (jsval.MapKey): "a" and
// ["a"] are two members, as are two arrays of the same text, and each is
// kept as it was added. A nil *sset stands for `undefined`, which upstream
// uses for "unknown".
type sset struct {
	items []Value
	// index is made once the set is past ssetScan members; below that a
	// scan of items is cheaper than a map, and most sets stay there.
	index map[jsval.InternKey]struct{}
}

// ssetOfValues is new Set(values): each member kept as it is given.
func ssetOfValues(values []Value) *sset {
	s := newSset()
	for _, v := range values {
		s.addValue(v)
	}
	return s
}

const ssetScan = 8

// newSset is new Set(items) of strings.
func newSset(items ...string) *sset {
	s := &sset{}
	if len(items) > 0 {
		s.items = make([]Value, 0, len(items))
	}
	for _, i := range items {
		s.add(i)
	}
	return s
}

// add is set.add(x) of a string.
func (s *sset) add(x string) { s.addValue(jsval.Str(x)) }

// addValue is set.add(v).
func (s *sset) addValue(v Value) {
	if s.hasValue(v) {
		return
	}
	s.items = append(s.items, v)
	switch {
	case s.index != nil:
		s.index[v.MapKey()] = struct{}{}
	case len(s.items) > ssetScan:
		s.index = make(map[jsval.InternKey]struct{}, 2*len(s.items))
		for _, i := range s.items {
			s.index[i.MapKey()] = struct{}{}
		}
	}
}

// has is set.has(x) of a string: only a member that is that string.
func (s *sset) has(x string) bool { return s.hasValue(jsval.Str(x)) }

// hasValue is set.has(v).
func (s *sset) hasValue(v Value) bool {
	if s == nil {
		return false
	}
	k := v.MapKey()
	if s.index != nil {
		_, ok := s.index[k]
		return ok
	}
	for _, i := range s.items {
		if i.MapKey() == k {
			return true
		}
	}
	return false
}

func (s *sset) size() int {
	if s == nil {
		return 0
	}
	return len(s.items)
}

// values are the members in insertion order, as they were added; the
// caller must not modify the slice.
func (s *sset) values() []Value {
	if s == nil {
		return nil
	}
	return s.items
}

func (s *sset) clone() *sset {
	c := newSset()
	if s != nil {
		for _, v := range s.items {
			c.addValue(v)
		}
	}
	return c
}

func setEqual(a, b *sset) bool {
	if a.size() != b.size() {
		return false
	}
	for _, x := range a.values() {
		if !b.hasValue(x) {
			return false
		}
	}
	return true
}

func hasIntersection(a, b *sset) bool {
	for _, x := range a.values() {
		if b.hasValue(x) {
			return true
		}
	}
	return false
}

// omap is an insertion-ordered string-keyed map with JavaScript's key order:
// array-index keys first, ascending, then the rest in insertion order.
//
// Keeping that order on every insert (upstream's own objects do) moves the
// keys after an index key each time, quadratic over a map's keys; deleting
// from the middle of the key list is linear too. So the list is kept lazily
// (C4): a new key is appended and a deleted one only leaves the value map,
// and the list is put in order the next time it is read, with the same
// result as ordering on every insert.
type omap[V any] struct {
	ks []string
	m  map[string]V
	// untidy says ks holds deleted keys or index keys out of order.
	untidy bool
	// unordered says the keys are never listed (newLookupOmap), so their
	// order is not kept.
	unordered bool
	// proto is what assigning "__proto__" made the object's prototype (see
	// jsRealm); the zero value is Object.prototype.
	proto *jsProto
}

// newOmap is an empty omap; its map is made on the first write, so one
// that stays empty (no legends, no selections) allocates only itself.
func newOmap[V any]() *omap[V] { return &omap[V]{} }

// newLookupOmap is an omap upstream only reads and writes by key (a
// selection's parsed projections, its hasField), never listing them: the
// same plain-object semantics, without the key list.
func newLookupOmap[V any]() *omap[V] { return &omap[V]{unordered: true} }

func (o *omap[V]) get(k string) (V, bool) {
	if o == nil {
		var z V
		return z, false
	}
	v, ok := o.m[k]
	return v, ok
}

func (o *omap[V]) lookup(k string) V {
	v, _ := o.get(k)
	return v
}

func (o *omap[V]) has(k string) bool { _, ok := o.get(k); return ok }

func (o *omap[V]) set(k string, v V) {
	if o.m == nil {
		o.m = map[string]V{}
	}
	if o.unordered {
		o.m[k] = v
		return
	}
	if _, ok := o.m[k]; !ok {
		if n, isIdx := arrayIndex(k); isIdx && !o.untidy && len(o.ks) > 0 {
			if m, lastIdx := arrayIndex(o.ks[len(o.ks)-1]); !lastIdx || m > n {
				o.untidy = true
			}
		}
		o.ks = append(o.ks, k)
	}
	o.m[k] = v
}

func (o *omap[V]) del(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	o.untidy = true
	// Compact once the deleted keys are most of the list, so its length
	// stays proportional to the live keys.
	if len(o.ks) > 32 && len(o.m) < len(o.ks)/2 {
		o.tidy()
	}
}

// tidy drops the deleted keys from the list and puts it in JavaScript's
// order, which is what ordering on every insert would have given: index keys
// ascending, then the others in the order they were (last) added.
func (o *omap[V]) tidy() {
	if !o.untidy {
		return
	}
	// A key's live entry is its last one: a key deleted and set again was
	// appended anew. Walk from the end, keeping the first entry of each
	// key still in m.
	seen := make(map[string]bool, len(o.m))
	live := make([]string, 0, len(o.m))
	var idx []string
	for i := len(o.ks) - 1; i >= 0; i-- {
		k := o.ks[i]
		if _, ok := o.m[k]; !ok || seen[k] {
			continue
		}
		seen[k] = true
		if _, isIdx := arrayIndex(k); isIdx {
			idx = append(idx, k)
		} else {
			live = append(live, k)
		}
	}
	slices.Reverse(live)
	if len(idx) > 0 {
		sort.Slice(idx, func(i, j int) bool {
			a, _ := arrayIndex(idx[i])
			b, _ := arrayIndex(idx[j])
			return a < b
		})
		live = append(idx, live...)
	}
	o.ks = live
	o.untidy = false
}

func (o *omap[V]) len() int {
	if o == nil {
		return 0
	}
	return len(o.m)
}

// keysView returns the keys without copying; the caller must not modify the
// omap while iterating or keep the slice.
func (o *omap[V]) keysView() []string {
	if o == nil {
		return nil
	}
	if o.unordered {
		panic("vegalite: the keys of a lookup omap are not kept")
	}
	o.tidy()
	return o.ks
}

func (o *omap[V]) keyList() []string {
	if o == nil {
		return nil
	}
	if o.unordered {
		panic("vegalite: the keys of a lookup omap are not kept")
	}
	o.tidy()
	return append([]string(nil), o.ks...)
}

// jsEntry is a key and its value.
type jsEntry struct {
	k string
	v Value
}
