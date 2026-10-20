package vega

import (
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
	"strconv"
)

// This file ports vega-crossfilter for a static evaluation: every tuple gets a
// bitmap with one bit per filtered dimension, set when the tuple falls outside
// that dimension's query range, and ResolveFilter keeps the tuples that pass
// every dimension it does not ignore.

// bitmaps is the crossfilter state a filter signal carries.
type bitmaps struct {
	data []jsval.Value
	curr []uint32
	// mask is the set of dimensions whose filter changed on the last run.
	mask uint32
	// obj is the operator's value as an expression sees it (one object for
	// the operator's life, as upstream's Bitmaps is).
	obj *jsval.Object
}

// exprValue is the crossfilter's value read through its signal.
func (b *bitmaps) exprValue() jsval.Value { return jsval.Obj(b.obj) }

func init() {
	transformFactories["crossfilter"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		obj := newBitmapsObject()
		return &bitmaps{obj: obj}, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			fields := p.fields("fields")
			query := p.list("query")
			if x := p.Get("query"); !paramNullish(x) && !paramIsArray(x) {
				// query.length and query[i] of a value that is no list: a
				// number or boolean has no length (no dimensions), a string
				// or an object has its own
				qv := paramJS(x)
				ln := jsval.ToNumber(jsval.Member(qv, "length"))
				query = nil
				if ln >= 1 {
					if ln > 30 {
						fail("crossfilter supports at most 30 dimensions")
					}
					for i := 0; i < int(ln); i++ {
						query = append(query, jsval.Member(qv, strconv.Itoa(i)))
					}
				}
			}
			m := len(query)
			if m > 30 {
				fail("crossfilter supports at most 30 dimensions")
			}
			b := &bitmaps{data: copyRows(n.g, pulse.tuples), curr: make([]uint32, len(pulse.tuples)), obj: obj}
			for i, t := range b.data {
				n.g.rows(i, len(b.data), 2)
				if o := jsval.Fields(t); o != nil {
					o.Set("_index", jsval.Int(i))
				}
			}
			for d := 0; d < m; d++ {
				// key = fields[i].fname, then Dimension's query[i].slice():
				// a missing field and a query that is no array or string
				// are TypeErrors
				if d >= len(fields) {
					fail("TypeError: Cannot read properties of undefined (reading 'fname')")
				}
				if fields[d].IsNil() { // a falsy field entry is a null accessor
					fail("TypeError: Cannot read properties of null (reading 'fname')")
				}
				// indices[key] finds Object.prototype's member for a field named
				// like one, a function that is no index: the first tuple
				// added fails in dim.bisect.
				if len(pulse.tuples) > 0 && expr.IsObjectPrototypeName(fields[d].Key()) {
					fail("TypeError: dim.bisect is not a function")
				}
				q := paramJS(query[d])
				switch {
				case q.IsNullish():
					fail("TypeError: Cannot read properties of %s (reading 'slice')", nullishName(q))
				case !q.IsArr() && !q.IsStr():
					fail("TypeError: query.slice is not a function")
				}
				lo, hi := jsval.Member(q, "0"), jsval.Member(q, "1")
				for k, t := range b.data {
					// an accessor (perhaps an expression) and two
					// comparisons a row and dimension
					n.g.rows(k, len(b.data), 4)
					v := fields[d].Apply(t)
					// values inside [lo, hi) keep the bit clear
					if !(transforms.GreaterEq(n.g.view.walk, v, lo) && transforms.Less(n.g.view.walk, v, hi)) {
						b.curr[k] |= 1 << uint(d)
					}
				}
			}
			b.mask = uint32(1)<<uint(m) - 1
			// CrossFilter.eval's `this.value.mask`: every dimension when it
			// added tuples, none otherwise
			if len(b.data) > 0 {
				obj.Set("mask", jsval.Num(float64(b.mask)))
			} else {
				obj.Set("mask", jsval.Num(0))
			}
			n.value = b
			// Upstream hands on the indices of the tuples that pass at
			// least one filter (CrossFilter.insert: curr[k] !== all), not
			// the tuples, which stay the pulse's source (crossfilter_pulse.go).
			// Its "all" is Bitmaps.all(), the mask of its bitmaps' width
			// (at least 8 bits), not of the dimensions: 0xff below 257.
			all := uint32(0xff)
			idx := make([]jsval.Value, 0, len(b.data))
			for k := range b.data {
				n.g.rows(k, len(b.data), 1)
				if b.curr[k] != all {
					idx = append(idx, jsval.Int(k))
				}
			}
			return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: true, tuples: idx, src: &pulseSource{tuples: b.data}}
		}), nil
	}

	transformFactories["resolvefilter"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			ignore := ignoreMask(p.Value("ignore"))
			b, _ := p.Get("filter").(*bitmaps)
			if b == nil {
				// bitmap = _.filter, mask = bitmap.mask: a value that is no
				// crossfilter's has no mask (undefined & ignore is 0, a
				// stop) unless it holds one, whose bitmap.data() is no
				// function; null and undefined have no properties.
				x := p.Get("filter")
				if paramNullish(x) {
					fail("TypeError: Cannot read properties of %s (reading 'mask')", nullishText(x))
				}
				v := paramJS(x)
				if jsmath.ToInt32(jsval.ToNumber(jsval.Member(v, "mask")))&int32(ignore) == 0 {
					return stopPulse
				}
				fail("TypeError: bitmap.data is not a function")
			}
			if b.mask&ignore == 0 {
				return stopPulse
			}
			// What the pulse adds are a crossfilter's indices: each that
			// passes is mapped to its tuple, `pass = k => !(curr[k] &
			// ignore) ? data[k] : null`. The source is kept, filtered lazily
			// by the tuples' _index (output.filter(SOURCE, ...)).
			out := make([]jsval.Value, 0, len(pulse.tuples))
			for j, k := range pulse.tuples {
				n.g.rows(j, len(pulse.tuples), 2)
				if i, ok := resolveIndex(b.curr, ignore, k); ok && i < len(b.data) {
					out = append(out, b.data[i])
				}
			}
			res := changedPulse(pulse, out)
			var prev func(jsval.Value) bool
			if pulse.src != nil {
				prev = pulse.src.filter
			}
			res.src = &pulseSource{tuples: pulse.rawSource()}
			res.src.filter = func(t jsval.Value) bool {
				if prev != nil && !prev(t) {
					return false
				}
				i, ok := resolveIndex(b.curr, ignore, t.Get("_index"))
				return ok && i < len(b.data)
			}
			return res
		}), nil
	}
}

// resolvePasses is ResolveFilter's `pass(t._index)`: `curr[k]` and `data[k]`
// are property reads, so k is an index only when String(k) is one ("1",
// and -0, which prints "0"; not 1.5, "01" or NaN); any other key reads
// undefined and the tuple does not pass. _index is the crossfilter's own,
// unless a transform rewrote it.
func resolvePasses(curr []uint32, ignore uint32, index jsval.Value) bool {
	_, ok := resolveIndex(curr, ignore, index)
	return ok
}

// resolveIndex is resolvePasses with the index it passes.
func resolveIndex(curr []uint32, ignore uint32, index jsval.Value) (int, bool) {
	// index is a tuple's _index or a key of the index map: an array index
	// (at most ten digits) or nothing.
	key, ok := index.PropertyKey(16)
	if !ok {
		return 0, false
	}
	k, ok := parseArrayIndex(key)
	return k, ok && k < len(curr) && curr[k]&ignore == 0
}

// ignoreMask is ResolveFilter's `~(_.ignore || 0)`: the bits of the filters
// not ignored. The complement works on ToInt32 of the number, which a Go
// conversion to uint32 is not for a negative or huge value.
func ignoreMask(v jsval.Value) uint32 {
	return ^jsmath.ToUint32(jsval.ToNumber(orZeroV(v)))
}
