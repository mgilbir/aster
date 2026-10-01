package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// JoinAggregateParams configures JoinAggregate.
type JoinAggregateParams struct {
	GroupBy  []Field
	Measures []Measure // empty means a single count
	// Key optionally overrides the cell key computed from GroupBy.
	Key Field
	// Rand feeds ci0/ci1; nil means DefaultRand.
	Rand Rand
}

// JoinAggregate computes group aggregates and writes them into every input
// tuple (group-by fields first as upstream copies the whole cell tuple: the
// group-by values, then counts, then the other measures) and returns data.
func JoinAggregate(ctx context.Context, data []jsval.Value, p JoinAggregateParams) ([]jsval.Value, error) {
	measures := p.Measures
	if len(measures) == 0 {
		measures = []Measure{{Op: "count"}}
	}
	// joinaggregate's parameter definition has no aggregate_params, so upstream
	// never passes a decay to exponential here (its result is NaN).
	measures = append([]Measure(nil), measures...)
	for i := range measures {
		measures[i].Param = 0
	}
	ms, err := newMeasureSet(measures, p.Rand)
	if err != nil {
		return nil, err
	}
	ms.ctx = ctx
	zone := zoneOf(ctx)
	cellKey := KeyOf(zone, p.GroupBy...)
	if !p.Key.IsNil() {
		g := p.Key.Get
		cellKey = func(t jsval.Value) string { return keyString(g(t), zone) }
	}
	type joinCell struct {
		c     *cell
		tuple *jsval.Object
	}
	cells := map[string]*joinCell{}
	keys := make([]string, len(data))
	var order []*joinCell
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		k := cellKey(t)
		keys[i] = k
		jc := cells[k]
		if jc == nil {
			jc = &joinCell{c: ms.newCell(), tuple: jsval.NewObject(len(p.GroupBy) + len(ms.names))}
			for _, d := range p.GroupBy {
				jc.tuple.Set(d.Name, d.Get(t))
			}
			cells[k] = jc
			order = append(order, jc)
		}
		ms.add(jc.c, t)
		if ms.needStore {
			jc.c.data = append(jc.c.data, t)
		}
	}
	for i, jc := range order {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		ms.write(jc.c, jc.tuple)
		if ms.err != nil {
			return nil, ms.err
		}
	}
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		src := cells[keys[i]].tuple
		for j := 0; j < src.Len(); j++ {
			o.Set(src.KeyAt(j), src.ValueAt(j))
		}
	}
	return data, nil
}
