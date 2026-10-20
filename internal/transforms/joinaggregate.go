package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// JoinAggregateParams configures JoinAggregate.
type JoinAggregateParams struct {
	GroupBy  []Field
	Measures []Measure // nil means a single count; empty, none
	// Key optionally overrides the cell key computed from GroupBy.
	Key Field
	// Rand feeds ci0/ci1; nil means DefaultRand.
	Rand Rand
	// Into, when not nil, are the tuples the aggregates are written into
	// (upstream visits the pulse's source to write them, which is not what
	// it adds after a crossfilter). A tuple of a group data has none of is
	// upstream's TypeError reading .tuple of undefined.
	Into []jsval.Value
}

// JoinAggregate computes group aggregates and writes them into every input
// tuple (group-by fields first as upstream copies the whole cell tuple: the
// group-by values, then counts, then the other measures) and returns data.
func JoinAggregate(ctx context.Context, data []jsval.Value, p JoinAggregateParams) ([]jsval.Value, error) {
	measures := p.Measures
	if measures == nil {
		measures = []Measure{{Op: "count"}}
	}
	// joinaggregate's parameter definition has no aggregate_params, so upstream
	// never passes a decay to exponential here (its result is NaN).
	measures = append([]Measure(nil), measures...)
	for i := range measures {
		measures[i].Param = math.NaN()
	}
	ms, err := newMeasureSet(measures, p.Rand)
	if err != nil {
		return nil, err
	}
	ms.setContext(ctx)
	kf := keysOf(ctx)
	cellKey := KeyOf(kf, p.GroupBy...)
	if !p.Key.IsNil() {
		g := p.Key.Get
		cellKey = func(t jsval.Value) string { return kf.String(g(t)) }
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
				jc.tuple.Set(d.Key(), d.Get(t))
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
	into := data
	if p.Into != nil {
		into = p.Into
	}
	for i, t := range into {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		k := ""
		if p.Into == nil {
			k = keys[i]
		} else {
			k = cellKey(t)
		}
		jc := cells[k]
		if jc == nil {
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of undefined (reading 'tuple')"}
		}
		src := jc.tuple
		if src.Len() == 0 {
			continue
		}
		o := jsval.FieldsFor(t, src.KeyAt(0))
		for j := 0; j < src.Len(); j++ {
			o.Set(src.KeyAt(j), src.ValueAt(j))
		}
	}
	return data, nil
}
