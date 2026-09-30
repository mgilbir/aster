package vegalite

// sset is an insertion-ordered set of strings (JavaScript's Set<string>).
// A nil *sset stands for `undefined`, which upstream uses for "unknown".
type sset struct {
	items []string
	index map[string]struct{}
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
	}
	return c
}

// mapSet turns the set into a Go map (for the field-prefix helpers).
func (s *sset) asMap() map[string]bool {
	if s == nil {
		return nil
	}
	m := make(map[string]bool, len(s.items))
	for _, i := range s.items {
		m[i] = true
	}
	return m
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

// omap is an insertion-ordered string-keyed map.
type omap[V any] struct {
	keys []string
	m    map[string]V
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
		o.keys = insertJSKey(o.keys, k)
	}
	o.m[k] = v
}

// insertJSKey appends k, or places an array-index key among the leading index
// keys in ascending order (JavaScript's own-key order).
func insertJSKey(keys []string, k string) []string {
	n, ok := arrayIndex(k)
	if !ok {
		return append(keys, k)
	}
	pos := len(keys)
	for i, e := range keys {
		if m, isIdx := arrayIndex(e); !isIdx || m > n {
			pos = i
			break
		}
	}
	keys = append(keys, "")
	copy(keys[pos+1:], keys[pos:])
	keys[pos] = k
	return keys
}

func (o *omap[V]) del(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *omap[V]) len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

func (o *omap[V]) keyList() []string {
	if o == nil {
		return nil
	}
	return append([]string(nil), o.keys...)
}
