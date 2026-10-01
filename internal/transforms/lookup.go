package transforms

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

func errUnknownOp(op string) error { return fmt.Errorf("unknown aggregate op %q", op) }

// LookupIndex maps string-coerced key values to tuples of a secondary
// dataset (vega-transforms' TupleIndex). When several tuples share a key the
// last one wins.
type LookupIndex struct {
	m    map[string]jsval.Value
	zone format.Zone
}

// NewLookupIndex indexes from by key.
func NewLookupIndex(ctx context.Context, from []jsval.Value, key Field) (*LookupIndex, error) {
	ix := &LookupIndex{m: make(map[string]jsval.Value, len(from)), zone: zoneOf(ctx)}
	for i, t := range from {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		ix.m[keyString(key.Apply(t), ix.zone)] = t
	}
	return ix, nil
}

// Get returns the tuple stored under the string form of k.
func (ix *LookupIndex) Get(k jsval.Value) (jsval.Value, bool) {
	v, ok := ix.m[keyString(k, ix.zone)]
	return v, ok
}

// LookupParams configures Lookup.
type LookupParams struct {
	Index *LookupIndex
	// Fields read the lookup key from each primary tuple.
	Fields []Field
	// Values, when non-empty, copy those fields of the matched tuple; otherwise
	// the whole matched tuple is stored.
	Values []Field
	// As names the outputs: one per Field without Values, Fields*Values with
	// them (for each field, all values). With Values and a single field it
	// defaults to the value accessors' names.
	As []string
	// Default is stored on a miss (upstream: null when unset).
	Default jsval.Value
}

// Lookup annotates each primary tuple, in place, with data from the secondary
// dataset. It returns an error for the parameter combinations upstream
// rejects.
func Lookup(ctx context.Context, data []jsval.Value, p LookupParams) ([]jsval.Value, error) {
	n, m := len(p.Fields), len(p.Values)
	def := p.Default
	if def.IsNullish() {
		def = jsval.Null
	}
	as := p.As
	if p.Index == nil {
		return nil, errors.New("lookup: missing index")
	}
	if m > 0 {
		if n > 1 && len(as) == 0 {
			return nil, errors.New("Multi-field lookup requires explicit \"as\" parameter.")
		}
		if len(as) > 0 && len(as) != n*m {
			return nil, errors.New("The \"as\" parameter has too few output field names.")
		}
		if len(as) == 0 {
			as = make([]string, m)
			for j, v := range p.Values {
				as[j] = v.Name
			}
		}
	} else if len(as) == 0 {
		return nil, errors.New("Missing output field names.")
	}
	for ti, t := range data {
		if err := poll(ctx, ti); err != nil {
			return nil, err
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		k := 0
		for i := 0; i < n; i++ {
			v, ok := p.Index.Get(p.Fields[i].Apply(t))
			if m == 0 {
				if !ok {
					v = def
				}
				if i < len(as) {
					o.Set(as[i], v)
				}
				continue
			}
			for j := 0; j < m; j++ {
				if ok {
					o.Set(as[k], p.Values[j].Apply(v))
				} else {
					o.Set(as[k], def)
				}
				k++
			}
		}
	}
	return data, nil
}
