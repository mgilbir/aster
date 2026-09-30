package vega

import (
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
			b := &bitmaps{data: append([]jsval.Value(nil), pulse.tuples...), curr: make([]uint32, len(pulse.tuples))}
			for i, t := range b.data {
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
					v := fields[d].Apply(t)
					// values inside [lo, hi) keep the bit clear
					if !(transforms.GreaterEq(v, lo) && transforms.Less(v, hi)) {
						b.curr[k] |= 1 << uint(d)
					}
				}
			}
			b.mask = uint32(1)<<uint(m) - 1
			n.value = b
			return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: true, tuples: pulse.tuples}
		}), nil
	}

	transformFactories["resolvefilter"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			ignore := ^uint32(jsval.ToNumber(orZeroV(p.Value("ignore"))))
			b, _ := p.Get("filter").(*bitmaps)
			if b == nil {
				fail("resolvefilter requires a crossfilter")
			}
			if b.mask&ignore == 0 {
				return stopPulse
			}
			out := make([]jsval.Value, 0, len(pulse.tuples))
			for _, t := range pulse.tuples {
				k := int(jsval.ToNumber(t.Get("_index")))
				if k >= 0 && k < len(b.curr) && b.curr[k]&ignore == 0 {
					out = append(out, t)
				}
			}
			return changedPulse(pulse, out)
		}), nil
	}
}
