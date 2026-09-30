package vega

// smallMap is an insertion-ordered string-keyed map for the handful of entries
// an operator's parameters have. It costs one slice instead of a hash table
// and keeps definition order for free; past smallMapIndexed entries it builds
// an index. The zero value is an empty map. Not safe for concurrent use.
type smallMap[V any] struct {
	ents []smallEnt[V]
	idx  map[string]int
}

type smallEnt[V any] struct {
	key string
	val V
}

const smallMapIndexed = 16

func (m *smallMap[V]) find(k string) int {
	if m.idx != nil {
		if i, ok := m.idx[k]; ok {
			return i
		}
		return -1
	}
	for i := range m.ents {
		if m.ents[i].key == k {
			return i
		}
	}
	return -1
}

func (m *smallMap[V]) lookup(k string) (V, bool) {
	if i := m.find(k); i >= 0 {
		return m.ents[i].val, true
	}
	var zero V
	return zero, false
}

// at is the value at k, or the zero value.
func (m *smallMap[V]) at(k string) V {
	v, _ := m.lookup(k)
	return v
}

func (m *smallMap[V]) has(k string) bool { return m.find(k) >= 0 }

func (m *smallMap[V]) len() int { return len(m.ents) }

func (m *smallMap[V]) keyAt(i int) string { return m.ents[i].key }
func (m *smallMap[V]) valAt(i int) V      { return m.ents[i].val }

// set stores v under k and reports the entry's index and whether it is new.
func (m *smallMap[V]) set(k string, v V) (int, bool) {
	if i := m.find(k); i >= 0 {
		m.ents[i].val = v
		return i, false
	}
	if m.ents == nil {
		m.grow(4)
	}
	m.ents = append(m.ents, smallEnt[V]{k, v})
	i := len(m.ents) - 1
	if m.idx != nil {
		m.idx[k] = i
	} else if len(m.ents) > smallMapIndexed {
		m.idx = make(map[string]int, 2*len(m.ents))
		for j := range m.ents {
			m.idx[m.ents[j].key] = j
		}
	}
	return i, true
}

// delete removes k, keeping the order of the rest.
func (m *smallMap[V]) delete(k string) {
	i := m.find(k)
	if i < 0 {
		return
	}
	m.ents = append(m.ents[:i], m.ents[i+1:]...)
	if m.idx != nil {
		delete(m.idx, k)
		for j := i; j < len(m.ents); j++ {
			m.idx[m.ents[j].key] = j
		}
	}
}

// grow makes room for n entries up front.
func (m *smallMap[V]) grow(n int) {
	if cap(m.ents) < n {
		m.ents = append(make([]smallEnt[V], 0, n), m.ents...)
	}
}
