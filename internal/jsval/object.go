package jsval

import (
	"fmt"
	"sync/atomic"

	ecma262 "github.com/mgilbir/goecma262"
)

// indexThreshold is the size at which an Object builds a hash index. Below it
// a linear scan over the keys is faster than hashing.
const indexThreshold = 32

// Object is an insertion-ordered string-keyed map, the shape of a JavaScript
// object literal. Setting an existing key overwrites it in place and keeps
// its position, as JavaScript does.
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
	if i := o.find(key); i >= 0 {
		o.vals[i] = v
		return
	}
	if o.keys == nil {
		o.keys, o.vals = make([]string, 0, 4), make([]Value, 0, 4)
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, v)
	if o.index != nil {
		o.index[key] = len(o.keys) - 1
	} else if len(o.keys) > indexThreshold {
		o.reindex()
	}
}

func (o *Object) reindex() {
	o.index = make(map[string]int, len(o.keys))
	for i, k := range o.keys {
		o.index[k] = i
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
		c.tid = o.tid
	}
	if len(c.keys) > indexThreshold {
		c.reindex()
	}
	return c
}

// Pattern is a compiled ECMA-262 regular expression, the value regexp() makes.
type Pattern struct {
	Source string
	Flags  string
	Re     *ecma262.Regexp
}

// NewPattern compiles source with JavaScript flags ("gimsuyvd").
func NewPattern(source, flags string) (*Pattern, error) {
	re, err := ecma262.CompileFlags(source, flags)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression /%s/%s: %w", source, flags, err)
	}
	return &Pattern{Source: source, Flags: flags, Re: re}, nil
}

// String is JavaScript's String(regexp): the literal a reader would write.
func (p *Pattern) String() string { return "/" + p.Source + "/" + p.Flags }
