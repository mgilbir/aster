package scale

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Ordinal is d3.scaleOrdinal: an ordered domain of distinct values mapped
// cyclically onto a range. By default its domain grows implicitly: applying the
// scale to an unseen value appends it (d3's `implicit` unknown).
type Ordinal struct {
	meta
	domain []jsval.Value
	// index is d3's InternMap: each value's position in domain by its
	// jsval.InternKey (a Date by its time value, -0 as 0, NaN as NaN, an
	// array or object by reference), found in O(1) whatever it holds.
	index map[jsval.InternKey]int
	// strs is index for the strings, which most domains are: a map keyed by
	// the string alone hashes less.
	strs map[string]int
	rng  []jsval.Value
	// probes counts the domain entries lookups compare with. Nothing reads
	// it but the tests, which check that interning is O(1) a value by
	// counting at n and 2n instead of timing it.
	probes   int64
	unknown  jsval.Value
	implicit bool
}

// NewOrdinal is d3.scaleOrdinal with an empty domain and range.
func NewOrdinal() *Ordinal {
	return &Ordinal{index: map[jsval.InternKey]int{}, strs: map[string]int{}, implicit: true}
}

func (o *Ordinal) lookup(v jsval.Value) (int, bool) {
	o.probes++
	if s, ok := v.Strict(); ok {
		i, found := o.strs[s]
		return i, found
	}
	i, found := o.index[v.InternKey()]
	return i, found
}

func (o *Ordinal) add(v jsval.Value) int {
	i := len(o.domain)
	o.domain = append(o.domain, v)
	if s, ok := v.Strict(); ok {
		o.strs[s] = i
	} else {
		o.index[v.InternKey()] = i
	}
	return i
}

// Apply is scale(d). With an implicit domain an unseen value is appended.
func (o *Ordinal) Apply(d jsval.Value) jsval.Value {
	i, ok := o.lookup(d)
	if !ok {
		if !o.implicit {
			return o.unknown
		}
		i = o.add(d)
	}
	if len(o.rng) == 0 {
		return jsval.Undefined
	}
	return o.rng[i%len(o.rng)]
}

// IndexOf returns the position of d in the domain without extending it.
func (o *Ordinal) IndexOf(d jsval.Value) (int, bool) { return o.lookup(d) }

// Domain returns a copy of the domain.
func (o *Ordinal) Domain() []jsval.Value { return cloneValues(o.domain) }

// SetDomain replaces the domain, dropping duplicates (first occurrence wins).
func (o *Ordinal) SetDomain(d []jsval.Value) {
	o.domain = make([]jsval.Value, 0, len(d))
	o.index = map[jsval.InternKey]int{}
	o.strs = make(map[string]int, len(d))
	for i, v := range d {
		o.charge(i, len(d))
		if _, ok := o.lookup(v); ok {
			continue
		}
		o.add(v)
	}
}

// Range returns a copy of the range.
func (o *Ordinal) Range() []jsval.Value { return cloneValues(o.rng) }

// SetRange replaces the range.
func (o *Ordinal) SetRange(r []jsval.Value) { o.rng = cloneValues(r) }

// Unknown returns the explicit unknown value (undefined when implicit).
func (o *Ordinal) Unknown() jsval.Value { return o.unknown }

// SetUnknown sets an explicit unknown value and turns implicit domain growth off.
func (o *Ordinal) SetUnknown(v jsval.Value) { o.unknown, o.implicit = v, false }

// Implicit reports whether unseen values extend the domain.
func (o *Ordinal) Implicit() bool { return o.implicit }

// SetImplicit is scale.unknown(d3.scaleImplicit): unseen values extend the domain.
func (o *Ordinal) SetImplicit() { o.unknown, o.implicit = jsval.Undefined, true }

// Copy is d3's ordinal copy: ordinal(domain, range).unknown(unknown).
func (o *Ordinal) Copy() Scale {
	n := NewOrdinal()
	n.typ = o.typ
	n.walk = o.walk
	n.SetDomain(o.domain)
	n.rng = cloneValues(o.rng)
	n.unknown, n.implicit = o.unknown, o.implicit
	return n
}

var (
	_ Scale     = (*Ordinal)(nil)
	_ Unknowner = (*Ordinal)(nil)
)
