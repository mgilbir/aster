package transforms

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/jsval"
)

func errUnknownOp(op string) error { return fmt.Errorf("unknown aggregate op %q", op) }

// LookupIndex maps string-coerced key values to tuples of a secondary
// dataset (vega-transforms' TupleIndex). When several tuples share a key the
// last one wins.
type LookupIndex struct {
	m    map[string]jsval.Value
	keys Keys
}

// NewLookupIndex indexes from by key.
func NewLookupIndex(ctx context.Context, from []jsval.Value, key Field) (*LookupIndex, error) {
	ix := &LookupIndex{m: make(map[string]jsval.Value, len(from)), keys: keysOf(ctx)}
	for i, t := range from {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		if k := ix.keys.String(key.Apply(t)); k != ProtoKey {
			ix.m[k] = t
		}
	}
	return ix, nil
}

// ProtoKey is the one key a plain JavaScript object cannot hold as a
// property: `obj['__proto__'] = v` sets the object's prototype instead (and
// not even that for a non-object v), and hasOwnProperty(obj, '__proto__')
// stays false. A map upstream keeps in a plain object (vega-util's fastmap
// included) never finds an entry stored under it.
const ProtoKey = "__proto__"

// Get returns the tuple stored under the string form of k.
func (ix *LookupIndex) Get(k jsval.Value) (jsval.Value, bool) {
	v, ok := ix.m[ix.keys.String(k)]
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
	// Exact reports parameters the runtime resolved as upstream reads them:
	// ValuesGiven chooses the values branch (a truthy values, [] too), As
	// holds every output name (n*m of them, or n), and a null Field or
	// Value is the TypeError of calling it, raised for the first tuple that
	// does.
	Exact       bool
	ValuesGiven bool
}

// errNotAFunction is the TypeError of calling a null accessor.
func errNotAFunction(what string) error {
	return &jsval.Thrown{Name: "TypeError", Msg: what + " is not a function"}
}

// lookupExact is Lookup for Exact parameters.
func lookupExact(ctx context.Context, data []jsval.Value, p LookupParams) ([]jsval.Value, error) {
	def := p.Default
	if def.IsNullish() {
		def = jsval.Null
	}
	n, m := len(p.Fields), len(p.Values)
	at := func(k int) string {
		if k < len(p.As) {
			return p.As[k]
		}
		return "undefined"
	}
	for ti, t := range data {
		if err := poll(ctx, ti); err != nil {
			return nil, err
		}
		k := 0
		for i := 0; i < n; i++ {
			if p.Fields[i].IsNil() {
				return nil, errNotAFunction("keys[i]")
			}
			v, ok := p.Index.Get(p.Fields[i].Apply(t))
			if !p.ValuesGiven {
				if !ok {
					v = def
				}
				if o := jsval.FieldsFor(t, at(i)); o != nil {
					o.Set(at(i), v)
				}
				continue
			}
			for j := 0; j < m; j, k = j+1, k+1 {
				x := def
				if ok {
					if p.Values[j].IsNil() {
						return nil, errNotAFunction("values[j]")
					}
					x = p.Values[j].Apply(v)
				}
				if o := jsval.FieldsFor(t, at(k)); o != nil {
					o.Set(at(k), x)
				}
			}
		}
	}
	return data, nil
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
	if p.Exact {
		return lookupExact(ctx, data, p)
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
				as[j] = v.Key()
			}
		}
	} else if len(as) == 0 {
		return nil, errors.New("Missing output field names.")
	}
	for ti, t := range data {
		if err := poll(ctx, ti); err != nil {
			return nil, err
		}
		o := jsval.Fields(t)
		if o == nil && len(as) > 0 {
			// The first write, `t[as[0]] = ...`, throws into a primitive.
			o = jsval.FieldsFor(t, as[0])
		}
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
