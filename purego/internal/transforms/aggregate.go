package transforms

import (
	"context"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// AggregateParams configures Aggregate (vega-transforms' aggregate).
type AggregateParams struct {
	// GroupBy are the dimension accessors; output tuples carry them under
	// their accessor names.
	GroupBy []Field
	// Measures are the aggregations (upstream's fields/ops/aggregate_params/as
	// arrays zipped). Empty means a single count.
	Measures []Measure
	// Cross generates the full cross product of the group-by values,
	// including empty cells (ignored for fewer than two dimensions). Upstream's
	// `drop` parameter only matters when tuples are removed incrementally, so a
	// one-shot Aggregate has no use for it: empty cells exist only under Cross,
	// and Cross keeps them.
	Cross bool
	// Key optionally overrides the cell key computed from GroupBy.
	Key Field
	// Rand feeds the bootstrap of ci0/ci1; nil means DefaultRand.
	Rand Rand
}

// aggCell is one output group.
type aggCell struct {
	tuple *jsval.Object
	c     *cell
}

// Aggregate is the group-by aggregation. Output tuples are new objects, one
// per group in order of first appearance (cross-generated cells last), each
// holding the group-by fields, then counts, then the other measures.
func Aggregate(ctx context.Context, data []jsval.Value, p AggregateParams) ([]jsval.Value, error) {
	measures := p.Measures
	if len(measures) == 0 {
		measures = []Measure{{Op: "count"}}
	}
	ms, err := newMeasureSet(measures, p.Rand)
	if err != nil {
		return nil, err
	}
	dims := p.GroupBy
	dnames := make([]string, len(dims))
	for i, d := range dims {
		dnames[i] = d.Name
	}
	// The default key is built in a reusable buffer so that finding an
	// existing cell does not allocate; a custom Key field is used as is.
	keyApp := keyAppender(dims)
	if !p.Key.IsNil() {
		g := p.Key.Get
		keyApp = func(dst []byte, t jsval.Value) []byte { return appendKeyValue(dst, g(t)) }
	}
	var keyBuf []byte

	cells := make(map[string]*aggCell)
	var order []*aggCell
	newCell := func(key string, t jsval.Value) *aggCell {
		tuple := jsval.NewObject(len(dims) + len(ms.names))
		for i, d := range dims {
			tuple.Set(dnames[i], d.Get(t))
		}
		ac := &aggCell{tuple: tuple, c: ms.newCell()}
		cells[key] = ac
		order = append(order, ac)
		return ac
	}

	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		keyBuf = keyApp(keyBuf[:0], t)
		ac := cells[string(keyBuf)]
		if ac == nil {
			ac = newCell(string(keyBuf), t)
		}
		ms.add(ac.c, t)
		if ms.needStore {
			ac.c.data = append(ac.c.data, t)
		}
	}

	if p.Cross && len(dims) > 1 {
		if err := crossCells(ctx, dims, dnames, cells, order, newCell); err != nil {
			return nil, err
		}
	}

	out := make([]jsval.Value, len(order))
	for i, ac := range order {
		ms.write(ac.c, ac.tuple)
		out[i] = jsval.Obj(ac.tuple)
	}
	return out, nil
}

// crossCells adds an empty cell for every combination of the group-by values
// seen that has no cell yet (upstream Aggregate.cross). Domain values are kept
// in JavaScript object-key order: integer-like strings ascending, then the
// rest in first-seen order.
func crossCells(ctx context.Context, dims []Field, dnames []string, cells map[string]*aggCell,
	order []*aggCell, newCell func(string, jsval.Value) *aggCell) error {
	n := len(dims)
	type domain struct {
		keys []string
		vals map[string]jsval.Value
	}
	doms := make([]domain, n)
	for i := range doms {
		doms[i].vals = map[string]jsval.Value{}
	}
	total := 1
	for _, ac := range order {
		for i := 0; i < n; i++ {
			v := ac.tuple.Lookup(dnames[i])
			k := v.AsString()
			if _, ok := doms[i].vals[k]; !ok {
				doms[i].keys = append(doms[i].keys, k)
			}
			doms[i].vals[k] = v
		}
	}
	for i := range doms {
		doms[i].keys = OrderedKeys(doms[i].keys)
		total *= max(len(doms[i].keys), 1)
		if total > MaxGroupCells {
			return limitErr("aggregate cross cells", total, MaxGroupCells)
		}
	}
	// The scratch tuple is shared across the recursion exactly as upstream's:
	// cell tuples are copied from it by reading the dimension accessors.
	scratch := jsval.NewObject(n)
	scratchV := jsval.Obj(scratch)
	count := 0
	var gen func(base string, idx int) error
	gen = func(base string, idx int) error {
		d := doms[idx]
		for _, k := range d.keys {
			key := k
			if base != "" {
				key = base + "|" + k
			}
			scratch.Set(dnames[idx], d.vals[k])
			if idx+1 < n {
				if err := gen(key, idx+1); err != nil {
					return err
				}
			} else {
				if err := poll(ctx, count); err != nil {
					return err
				}
				count++
				if _, ok := cells[key]; !ok {
					newCell(key, scratchV)
				}
			}
		}
		return nil
	}
	return gen("", 0)
}
