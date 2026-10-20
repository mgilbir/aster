package jsval

import (
	"slices"
	"sort"
	"sync/atomic"
)

// indexThreshold is the size at which an Object builds a hash index. Below it
// a linear scan over the keys is faster than hashing.
const indexThreshold = 32

// Object is a string-keyed map in JavaScript's property order, the shape of a
// JavaScript object: the keys that are array indexes ("0" to "4294967294",
// written canonically) first, in ascending order, then the others in the
// order they were added (OrdinaryOwnPropertyKeys). Setting an existing key
// overwrites it in place and keeps its position, as JavaScript does.
//
// An Object is not safe for concurrent mutation.
type Object struct {
	keys  []string
	vals  []Value
	index map[string]int
	// str, when set, is the object's toString (d3 colour objects print as
	// "rgb(…)"); plain objects print as "[object Object]".
	str func(*Object) string
	// host is an opaque payload of the embedding program, for the objects
	// that stand for a host object (a canvas) rather than plain data. Two
	// objects with a payload are equal only when they are the same object.
	host any
	// tid is the tuple id (vega-dataflow's tupleid): 0 until the object is
	// ingested as a data tuple.
	tid uint32
	// nidx is how many of the keys, the leading ones, are array indexes.
	nidx uint32
}

// tupleCounter is vega-dataflow's TUPLE_ID: ids grow in ingestion order.
var tupleCounter atomic.Uint32

// TupleID is the object's tuple id, 0 when it is not a tuple.
func (o *Object) TupleID() uint32 { return o.tid }

// EnsureTupleID gives the object the next tuple id unless it has one, as
// vega-dataflow's ingest does, and reports the id.
func (o *Object) EnsureTupleID() uint32 {
	if o.tid == 0 {
		o.tid = tupleCounter.Add(1)
	}
	return o.tid
}

// SetTupleID sets the tuple id.
func (o *Object) SetTupleID(id uint32) { o.tid = id }

// A CopyHook is a host payload that is told when its object is copied
// (Clone, a Cloner's copy), and answers the payload of the copy: the
// embedding program's way to follow an object it tracks through the copies
// the engine makes of it.
type CopyHook interface {
	Copied(dst *Object) any
}

// SetHost attaches an opaque host payload to the object.
func (o *Object) SetHost(h any) { o.host = h }

// Host returns the payload attached with SetHost, or nil.
func (o *Object) Host() any { return o.host }

// SetStringer sets the function String(v) uses for this object.
func (o *Object) SetStringer(f func(*Object) string) { o.str = f }

// NewObject makes an empty object with room for n keys.
func NewObject(n int) *Object {
	// Objects of a few keys are allocated together with their key and value
	// arrays: one allocation instead of three, sized exactly.
	switch {
	case n <= 0:
		return &Object{}
	case n == 1:
		x := &objectN1{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 2:
		x := &objectN2{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 3:
		x := &objectN3{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 4:
		x := &objectN4{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 5:
		x := &objectN5{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 6:
		x := &objectN6{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 7:
		x := &objectN7{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 8:
		x := &objectN8{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 9:
		x := &objectN9{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 10:
		x := &objectN10{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 11:
		x := &objectN11{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n == 12:
		x := &objectN12{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case n <= 16:
		x := &objectN16{}
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	}
	return &Object{keys: make([]string, 0, n), vals: make([]Value, 0, n)}
}

type (
	objectN1 struct {
		Object
		kbuf [1]string
		vbuf [1]Value
	}
	objectN2 struct {
		Object
		kbuf [2]string
		vbuf [2]Value
	}
	objectN3 struct {
		Object
		kbuf [3]string
		vbuf [3]Value
	}
	objectN4 struct {
		Object
		kbuf [4]string
		vbuf [4]Value
	}
	objectN5 struct {
		Object
		kbuf [5]string
		vbuf [5]Value
	}
	objectN6 struct {
		Object
		kbuf [6]string
		vbuf [6]Value
	}
	objectN7 struct {
		Object
		kbuf [7]string
		vbuf [7]Value
	}
	objectN8 struct {
		Object
		kbuf [8]string
		vbuf [8]Value
	}
	objectN9 struct {
		Object
		kbuf [9]string
		vbuf [9]Value
	}
	objectN10 struct {
		Object
		kbuf [10]string
		vbuf [10]Value
	}
	objectN11 struct {
		Object
		kbuf [11]string
		vbuf [11]Value
	}
	objectN12 struct {
		Object
		kbuf [12]string
		vbuf [12]Value
	}
	objectN16 struct {
		Object
		kbuf [16]string
		vbuf [16]Value
	}
)

// ObjectOf builds an object from alternating key, value pairs.
func ObjectOf(kv ...any) *Object {
	if len(kv)%2 != 0 {
		panic("jsval.ObjectOf: odd number of arguments")
	}
	o := NewObject(len(kv) / 2)
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1].(Value))
	}
	return o
}

// Len is the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

func (o *Object) find(key string) int {
	if o == nil {
		return -1
	}
	if o.index != nil {
		if i, ok := o.index[key]; ok {
			return i
		}
		return -1
	}
	for i, k := range o.keys {
		if k == key {
			return i
		}
	}
	return -1
}

// Get returns the value at key and whether it is present.
func (o *Object) Get(key string) (Value, bool) {
	i := o.find(key)
	if i < 0 {
		return Undefined, false
	}
	return o.vals[i], true
}

// Lookup returns the value at key, or Undefined.
func (o *Object) Lookup(key string) Value {
	v, _ := o.Get(key)
	return v
}

// Has reports whether key is present (even when its value is Undefined).
func (o *Object) Has(key string) bool { return o.find(key) >= 0 }

// Set inserts or overwrites key.
func (o *Object) Set(key string, v Value) {
	if o.host != nil {
		// The functions Inherited and NativeFunction answer are shared by
		// every caller in the process, so nothing may be written on them.
		if _, ok := o.host.(nativeFunction); ok {
			return
		}
	}
	if i := o.find(key); i >= 0 {
		o.vals[i] = v
		return
	}
	if o.keys == nil {
		o.keys, o.vals = make([]string, 0, 4), make([]Value, 0, 4)
	}
	if n, ok := ArrayIndex(key); ok {
		o.insertIndex(key, n, v)
		return
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, v)
	if o.index != nil {
		o.index[key] = len(o.keys) - 1
	} else if len(o.keys) > indexThreshold {
		o.reindex()
	}
}

// insertIndex adds the array-index key key (of value n) among the leading
// index keys, in ascending order.
func (o *Object) insertIndex(key string, n uint32, v Value) {
	i := sort.Search(int(o.nidx), func(i int) bool {
		m, _ := ArrayIndex(o.keys[i])
		return m > n
	})
	o.keys = slices.Insert(o.keys, i, key)
	o.vals = slices.Insert(o.vals, i, v)
	o.nidx++
	switch {
	case o.index != nil:
		// The keys from i on moved one place: only those are updated, so
		// keys added in JavaScript's order cost O(1) each.
		for j := i; j < len(o.keys); j++ {
			o.index[o.keys[j]] = j
		}
	case len(o.keys) > indexThreshold:
		o.reindex()
	}
}

// setUnordered is Set without the key order, for a builder that orders the
// object once it is complete (Order): keys added one at a time in any order
// cost O(1) each, where Set moves the keys after an array index it adds.
func (o *Object) setUnordered(key string, v Value) {
	if i := o.find(key); i >= 0 {
		o.vals[i] = v
		return
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, v)
	if o.index != nil {
		o.index[key] = len(o.keys) - 1
	} else if len(o.keys) > indexThreshold {
		o.reindex()
	}
}

// order puts the keys of an object built with setUnordered in JavaScript's
// order: the array indexes first, ascending, then the rest as they were
// added. It is O(n) when there is no array index, else O(n log n).
func (o *Object) order() {
	type kv struct {
		n uint32
		k string
		v Value
	}
	var idx []kv
	for i, k := range o.keys {
		if n, ok := ArrayIndex(k); ok {
			idx = append(idx, kv{n, k, o.vals[i]})
		}
	}
	if len(idx) == 0 {
		o.nidx = 0
		return
	}
	sort.Slice(idx, func(a, b int) bool { return idx[a].n < idx[b].n })
	j := len(o.keys)
	// The other keys keep their order, behind the indexes: move them from
	// the end back.
	for i := len(o.keys) - 1; i >= 0; i-- {
		if _, ok := ArrayIndex(o.keys[i]); !ok {
			j--
			o.keys[j], o.vals[j] = o.keys[i], o.vals[i]
		}
	}
	for i, e := range idx {
		o.keys[i], o.vals[i] = e.k, e.v
	}
	o.nidx = uint32(len(idx))
	if o.index != nil {
		o.reindex()
	}
}

// ArrayIndex reports whether key is an array index as JavaScript orders an
// object's keys by: the canonical decimal text (no sign, no leading zero) of
// an integer from 0 to 2^32 - 2, and its value.
func ArrayIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 10 || key[0] < '0' || key[0] > '9' || key[0] == '0' && len(key) > 1 {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	if n >= 1<<32-1 {
		return 0, false
	}
	return uint32(n), true
}

func (o *Object) reindex() {
	o.index = make(map[string]int, len(o.keys))
	for i, k := range o.keys {
		o.index[k] = i
	}
}

// DeleteFunc removes every entry for which del reports true, preserving the
// order of the rest, in one pass: deleting n keys one at a time moves the
// keys after each, which is quadratic in a large object. del sees each entry
// once, in order, and must not change the object.
func (o *Object) DeleteFunc(del func(key string, v Value) bool) {
	if o == nil {
		return
	}
	j, nidx := 0, uint32(0)
	for i := range o.keys {
		if del(o.keys[i], o.vals[i]) {
			continue
		}
		if i < int(o.nidx) {
			nidx++
		}
		o.keys[j], o.vals[j] = o.keys[i], o.vals[i]
		j++
	}
	if j == len(o.keys) {
		return
	}
	o.nidx = nidx
	clear(o.keys[j:])
	clear(o.vals[j:])
	o.keys, o.vals = o.keys[:j], o.vals[:j]
	if o.index != nil {
		if j <= indexThreshold {
			o.index = nil
		} else {
			o.reindex()
		}
	}
}

// Delete removes key, preserving the order of the rest.
func (o *Object) Delete(key string) {
	i := o.find(key)
	if i < 0 {
		return
	}
	o.keys = append(o.keys[:i], o.keys[i+1:]...)
	o.vals = append(o.vals[:i], o.vals[i+1:]...)
	if i < int(o.nidx) {
		o.nidx--
	}
	if o.index == nil {
		return
	}
	if len(o.keys) <= indexThreshold {
		o.index = nil
		return
	}
	// Only the keys after the deleted one moved; shift their positions
	// rather than rehashing every key.
	delete(o.index, key)
	for j := i; j < len(o.keys); j++ {
		o.index[o.keys[j]] = j
	}
}

// Keys returns the keys in order. The slice is shared; do not modify it.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

// KeyAt and ValueAt address the i-th entry in insertion order.
func (o *Object) KeyAt(i int) string   { return o.keys[i] }
func (o *Object) ValueAt(i int) Value  { return o.vals[i] }
func (o *Object) SetAt(i int, v Value) { o.vals[i] = v }

// All iterates over the entries in insertion order.
func (o *Object) All() func(yield func(string, Value) bool) {
	return func(yield func(string, Value) bool) {
		if o == nil {
			return
		}
		for i := range o.keys {
			if !yield(o.keys[i], o.vals[i]) {
				return
			}
		}
	}
}

// Clone makes a shallow copy.
func (o *Object) Clone() *Object {
	// Room for one more key, which the transforms that add a field use; a
	// small object is one allocation, as NewObject makes it.
	c := NewObject(o.Len() + 1)
	if o != nil {
		c.keys = append(c.keys, o.keys...)
		c.vals = append(c.vals, o.vals...)
		c.str = o.str
		c.host = o.host
		if h, ok := c.host.(CopyHook); ok {
			c.host = h.Copied(c)
		}
		c.tid = o.tid
		c.nidx = o.nidx
	}
	if len(c.keys) > indexThreshold {
		c.reindex()
	}
	return c
}
