package vegalite

import (
	"slices"
	"sort"

	"github.com/mgilbir/aster/internal/jsval"
)

// sset is an insertion-ordered set of strings (JavaScript's Set<string>).
// A nil *sset stands for `undefined`, which upstream uses for "unknown".
type sset struct {
	items []string
	index map[string]struct{}
	// raw holds, by their text, the members that were added as values other
	// than strings (a transform's `as` of null): upstream's set holds the
	// values, and reading one as an access path differs from its text.
	raw map[string]Value
}

// addValue adds v, keeping it as it is when it is not a string.
func (s *sset) addValue(v Value) {
	k := v.AsString()
	if !v.IsStr() && !s.has(k) {
		if s.raw == nil {
			s.raw = map[string]Value{}
		}
		s.raw[k] = v
	}
	s.add(k)
}

// value is the member x as it was added.
func (s *sset) value(x string) Value {
	if v, ok := s.raw[x]; ok {
		return v
	}
	return jsval.Str(x)
}

// ssetOfValues is new Set(values): each member kept as it is given.
func ssetOfValues(values []Value) *sset {
	s := newSset()
	for _, v := range values {
		s.addValue(v)
	}
	return s
}

func newSset(items ...string) *sset {
	s := &sset{index: make(map[string]struct{}, len(items))}
	for _, i := range items {
		s.add(i)
	}
	return s
}

func (s *sset) add(x string) {
	if s.index == nil {
		s.index = map[string]struct{}{}
	}
	if _, ok := s.index[x]; ok {
		return
	}
	s.index[x] = struct{}{}
	s.items = append(s.items, x)
}

func (s *sset) has(x string) bool {
	if s == nil {
		return false
	}
	_, ok := s.index[x]
	return ok
}

func (s *sset) size() int {
	if s == nil {
		return 0
	}
	return len(s.items)
}

func (s *sset) list() []string {
	if s == nil {
		return nil
	}
	return s.items
}

func (s *sset) clone() *sset {
	c := newSset()
	if s != nil {
		for _, i := range s.items {
			c.add(i)
		}
		for k, v := range s.raw {
			if c.raw == nil {
				c.raw = map[string]Value{}
			}
			c.raw[k] = v
		}
	}
	return c
}

func setEqual(a, b *sset) bool {
	if a.size() != b.size() {
		return false
	}
	for _, x := range a.list() {
		if !b.has(x) {
			return false
		}
	}
	return true
}

func hasIntersection(a, b *sset) bool {
	for _, x := range a.list() {
		if b.has(x) {
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
	// proto is what assigning "__proto__" made the object's prototype (see
	// jsRealm); the zero value is Object.prototype.
	proto jsProto
}

func newOmap[V any]() *omap[V] { return &omap[V]{m: map[string]V{}} }

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
	o.tidy()
	return o.ks
}

func (o *omap[V]) keyList() []string {
	if o == nil {
		return nil
	}
	o.tidy()
	return append([]string(nil), o.ks...)
}

// jsEntry is a key and its value.
type jsEntry struct {
	k string
	v Value
}
