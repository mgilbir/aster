package vega

import (
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
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
}

func init() {
	transformFactories["crossfilter"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return &bitmaps{}, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			fields := p.fields("fields")
			query := p.list("query")
			m := len(query)
			if m > 30 {
				fail("crossfilter supports at most 30 dimensions")
			}
			b := &bitmaps{data: copyRows(n.g, pulse.tuples), curr: make([]uint32, len(pulse.tuples))}
			for i, t := range b.data {
				n.g.rows(i, len(b.data), 2)
				if o := t.ObjValue(); o != nil {
					o.Set("_index", jsval.Int(i))
				}
			}
			for d := 0; d < m; d++ {
				if d >= len(fields) {
					break
				}
				q := toValue(query[d])
				lo, hi := q.Index(0), q.Index(1)
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
				fail("resolvefilter requires a crossfilter")
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
