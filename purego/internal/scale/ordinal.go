package scale

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// ordKey is the identity of a domain value in d3's InternMap: primitives by
// SameValueZero, and objects by their valueOf() (a Date is its epoch number).
type ordKey struct {
	kind jsval.Kind
	n    float64
	s    string
}

// keyOf returns the map key of v. ok is false for values interned by
// reference (arrays, objects, patterns), which live in a side list.
func keyOf(v jsval.Value) (ordKey, bool) {
	switch v.Kind() {
	case jsval.KindNum, jsval.KindTimestamp:
		n := v.NumValue()
		if n != n {
			// NaN never equals itself as a struct field, so it gets a fixed key.
			return ordKey{kind: jsval.KindNum, s: "NaN"}, true
		}
		if n == 0 {
			n = 0 // -0 and +0 are the same key (SameValueZero)
		}
		return ordKey{kind: jsval.KindNum, n: n}, true
	case jsval.KindStr:
		return ordKey{kind: jsval.KindStr, s: v.StrValue()}, true
	case jsval.KindBool:
		return ordKey{kind: jsval.KindBool, n: v.NumValue()}, true
	case jsval.KindNull, jsval.KindUndefined:
		return ordKey{kind: v.Kind()}, true
	}
	return ordKey{}, false
}

// Ordinal is d3.scaleOrdinal: an ordered domain of distinct values mapped
// cyclically onto a range. By default its domain grows implicitly: applying the
// scale to an unseen value appends it (d3's `implicit` unknown).
type Ordinal struct {
	meta
	domain   []jsval.Value
	index    map[ordKey]int
	refs     []int // indexes in domain of by-reference values
	rng      []jsval.Value
	unknown  jsval.Value
	implicit bool
}

// NewOrdinal is d3.scaleOrdinal with an empty domain and range.
func NewOrdinal() *Ordinal {
	return &Ordinal{index: map[ordKey]int{}, implicit: true}
}

func (o *Ordinal) lookup(v jsval.Value) (int, bool) {
	if k, ok := keyOf(v); ok {
		i, found := o.index[k]
		return i, found
	}
	for _, i := range o.refs {
		if jsval.SameRef(o.domain[i], v) {
			return i, true
		}
	}
	return 0, false
}

func (o *Ordinal) add(v jsval.Value) int {
	i := len(o.domain)
	o.domain = append(o.domain, v)
	if k, ok := keyOf(v); ok {
		o.index[k] = i
	} else {
		o.refs = append(o.refs, i)
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
	o.index = make(map[ordKey]int, len(d))
	o.refs = o.refs[:0]
	for _, v := range d {
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
	n.SetDomain(o.domain)
	n.rng = cloneValues(o.rng)
	n.unknown, n.implicit = o.unknown, o.implicit
	return n
}

var (
	_ Scale     = (*Ordinal)(nil)
	_ Unknowner = (*Ordinal)(nil)
)
