package vegalite

import (
	"sort"

	"github.com/mgilbir/aster/internal/jsval"
)

// parseMap is a ParseNode's parse object (field to parse type) in a form
// that merges in time linear in the smaller side. Upstream's merge is
// `this._parse = {...this._parse, ...other.parse}`: a copy of both, and
// MoveParseUp merges a parse node into its parent at each step of a chain,
// the parent (one field) absorbing the child (all the fields below it), so
// a chain of n filters copied n²/2 fields, and kept every absorbed copy
// reachable until the pass ended (C9: audit 2026-10-18). Here the fields
// are a linked list, so the larger side is kept and the smaller one's
// fields are put before it (or after it) in place; the object the rest of
// the compiler reads is made from the list on demand, in the order the
// copies would have had.
//
// The list keeps the order of the object it was made from until a merge;
// a merge's result is in JavaScript's order (array-index keys first,
// ascending, then the others in list order), as the spread's is.
type parseMap struct {
	head, tail *parseEntry
	at         map[string]*parseEntry
	// jsOrder says the object lists array-index keys first (a merge made
	// it); before a merge it is in the list's order.
	jsOrder bool
	// obj is the object made from the list, until the list changes.
	obj *Object
	// The prefixes of the fields' paths, for fieldIntersection (made when
	// first asked for, kept as fields are added, dropped when one is
	// deleted); bad counts the fields whose path does not split.
	prefixes map[prefixKey]prefixAt
	bad      int
	// indexed is what the prefixes were charged, given back when they are
	// dropped.
	indexed int64
}

type parseEntry struct {
	k          string
	v          Value
	prev, next *parseEntry
	pieces     []string // the field's path, once prefixes is made
}

// prefixAt is a field path's prefix: the entry and its last piece.
type prefixAt struct {
	e     *parseEntry
	piece int
}

// parseEntryBytes is what one field of a parseMap costs: the entry, its map
// slot, and its prefixes once they are indexed.
const parseEntryBytes = 96

func newParseMap(cc *compileCtx, o *Object) *parseMap {
	cc.alloc(int64(o.Len()) * parseEntryBytes)
	m := &parseMap{at: make(map[string]*parseEntry, o.Len())}
	for i := 0; i < o.Len(); i++ {
		cc.work(1)
		m.pushBack(&parseEntry{k: o.KeyAt(i), v: o.ValueAt(i)})
	}
	m.obj = o
	return m
}

func (m *parseMap) len() int { return len(m.at) }

func (m *parseMap) pushBack(e *parseEntry) {
	e.prev, e.next = m.tail, nil
	if m.tail != nil {
		m.tail.next = e
	} else {
		m.head = e
	}
	m.tail = e
	m.at[e.k] = e
}

func (m *parseMap) pushFront(e *parseEntry) {
	e.prev, e.next = nil, m.head
	if m.head != nil {
		m.head.prev = e
	} else {
		m.tail = e
	}
	m.head = e
	m.at[e.k] = e
}

func (m *parseMap) unlink(e *parseEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		m.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		m.tail = e.prev
	}
	e.prev, e.next = nil, nil
	delete(m.at, e.k)
}

// object is the parse as an object, in the order upstream's has.
func (m *parseMap) object(cc *compileCtx) *Object {
	if m.obj != nil {
		return m.obj
	}
	o := jsval.NewObject(m.len())
	type idx struct {
		n uint32
		e *parseEntry
	}
	var ids []idx
	for e := m.head; e != nil; e = e.next {
		cc.work(1)
		if m.jsOrder {
			if n, ok := arrayIndex(e.k); ok {
				ids = append(ids, idx{n, e})
				continue
			}
		}
		o.Set(e.k, e.v)
	}
	if len(ids) > 0 {
		sort.Slice(ids, func(i, j int) bool { return ids[i].n < ids[j].n })
		rest := o
		o = jsval.NewObject(m.len())
		for _, x := range ids {
			o.Set(x.e.k, x.e.v)
		}
		for i := 0; i < rest.Len(); i++ {
			o.Set(rest.KeyAt(i), rest.ValueAt(i))
		}
	}
	m.obj = o
	return o
}

// deleteFunc deletes the fields del says to, in place (as upstream's
// `delete parse[field]`).
func (m *parseMap) deleteFunc(cc *compileCtx, del func(k string) bool) {
	for e := m.head; e != nil; {
		cc.work(1)
		next := e.next
		if del(e.k) {
			m.unlink(e)
			m.obj = nil
			m.dropIndex(cc)
		}
		e = next
	}
}

// mergeFrom makes m `{...m, ...other}`: m's fields first, in m's order,
// then other's that m lacks; other's value wherever both have the field.
// The larger list is kept and the smaller's fields are moved to it, so a
// merge costs the smaller. other is left empty.
func (m *parseMap) mergeFrom(cc *compileCtx, other *parseMap) {
	m.jsOrder, m.obj = true, nil
	if m.len() >= other.len() {
		for e := other.head; e != nil; {
			cc.work(1)
			next := e.next
			if mine := m.at[e.k]; mine != nil {
				mine.v = e.v
			} else {
				other.unlink(e)
				m.pushBack(e)
				m.index(cc, e)
			}
			e = next
		}
		*other = parseMap{at: map[string]*parseEntry{}}
		return
	}
	// Keep other's list: m's fields go before it, in m's order (the last
	// first, each to the front), a field both have moving to the front
	// with other's value.
	for e := m.tail; e != nil; {
		cc.work(1)
		prev := e.prev
		if theirs := other.at[e.k]; theirs != nil {
			other.unlink(theirs)
			other.pushFront(theirs)
		} else {
			m.unlink(e)
			other.pushFront(e)
			other.index(cc, e)
		}
		e = prev
	}
	*m, *other = *other, parseMap{at: map[string]*parseEntry{}}
	m.jsOrder, m.obj = true, nil
}

// dropIndex drops m's index of prefixes (a field was deleted), giving back
// what it was charged.
func (m *parseMap) dropIndex(cc *compileCtx) {
	m.prefixes = nil
	cc.free(m.indexed)
	m.indexed = 0
}

// index adds e's prefixes to m's index, if m has one.
func (m *parseMap) index(cc *compileCtx, e *parseEntry) {
	if m.prefixes == nil {
		return
	}
	pieces, err := jsval.SplitFieldPath(e.k)
	if err != nil {
		m.bad++
		e.pieces = nil
		return
	}
	e.pieces = pieces
	cc.alloc(int64(len(pieces)) * prefixEntryBytes)
	m.indexed += int64(len(pieces)) * prefixEntryBytes
	forEachPrefix(cc, pieces, func(k prefixKey, i int) bool {
		if _, ok := m.prefixes[k]; !ok {
			m.prefixes[k] = prefixAt{e, i}
		}
		return true
	})
}

// prefixIndex is the index of m's fields' prefixes (see fieldIntersection),
// made the first time it is asked for and kept as fields are added.
func (m *parseMap) prefixIndex(cc *compileCtx) map[prefixKey]prefixAt {
	if m.prefixes != nil {
		return m.prefixes
	}
	m.prefixes, m.bad = make(map[prefixKey]prefixAt, m.len()), 0
	for e := m.head; e != nil; e = e.next {
		m.index(cc, e)
	}
	return m.prefixes
}

// fieldIntersectsParse is fieldIntersection(a, pn.dependentFields()) for a
// parse node, the question MoveParseUp asks at each step a parse node takes
// up a chain: with the node's own index of its fields' prefixes, kept as
// fields merge in, it costs a's prefixes, not the parse's fields.
func fieldIntersectsParse(cc *compileCtx, a *sset, pn *parseNode) bool {
	if a == nil {
		return true
	}
	pa, na := splitMembers(cc, a)
	index := pn.pm.prefixIndex(cc)
	if pn.pm.bad > 0 {
		// A field upstream's splitAccessPath throws on: the general form
		// throws at the same field.
		return fieldIntersection(cc, a, pn.dependentFields())
	}
	if na == 0 {
		return false
	}
	for _, pieces := range pa {
		found, collided := false, false
		forEachPrefix(cc, pieces, func(k prefixKey, i int) bool {
			at, ok := index[k]
			if !ok {
				return true
			}
			if wrappedPrefix(cc, at.e.pieces[:at.piece+1]) == wrappedPrefix(cc, pieces[:i+1]) {
				found = true
				return false
			}
			collided = true
			return true
		})
		if found {
			return true
		}
		if collided {
			return fieldIntersection(cc, a, pn.dependentFields())
		}
	}
	return false
}
