package jsval

import (
	"fmt"

	ecma262 "github.com/mgilbir/goecma262"
)

// indexThreshold is the size at which an Object builds a hash index. Below it
// a linear scan over the keys is faster than hashing.
const indexThreshold = 12

// Object is an insertion-ordered string-keyed map, the shape of a JavaScript
// object literal. Setting an existing key overwrites it in place and keeps
// its position, as JavaScript does.
//
// An Object is not safe for concurrent mutation.
type Object struct {
	keys  []string
	vals  []Value
	index map[string]int
}

// NewObject makes an empty object with room for n keys.
func NewObject(n int) *Object {
	return &Object{keys: make([]string, 0, n), vals: make([]Value, 0, n)}
}

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
	c := &Object{
		keys: append(make([]string, 0, o.Len()+1), o.Keys()...),
		vals: make([]Value, 0, o.Len()+1),
	}
	if o != nil {
		c.vals = append(c.vals, o.vals...)
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
