package transforms

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// opCall is one recorded call of a vega-transforms operator, with the pieces its adapter reads: the
// parameters and the pulse that went in and the one that came out (see TestUpstreamVegaTransforms).
type opCall struct {
	*upstream.Replay
	ctx             context.Context
	c               *upstream.Call
	params, in, out map[string]any
	collected       map[int]*collectState // by instance
}

// plumbing says why an operator with no counterpart in the engine is not replayed: it moves tuples
// between the operators of a dataflow, which the engine's one-shot functions have no use for.
var plumbing = map[string]string{
	"relay":      "a pass-through that only re-derives tuples (no engine counterpart)",
	"tupleindex": "a lookup index kept for other operators (no engine counterpart: the index is built by NewLookupIndex)",
	"values":     "a sorted list of distinct values kept for other operators (no engine counterpart)",
	"facet":      "a subflow per group of tuples (the engine has no subflows)",
	"load":       "a fetch of a URL (the engine's loaders are tested elsewhere)",
}

// fieldFrom decodes an accessor marker that reads one field.
func fieldFrom(v any) (Field, bool) {
	m, ok := v.(map[string]any)
	if !ok || m["$"] != "accessor" {
		return Field{}, false
	}
	fields, _ := m["fields"].([]any)
	if len(fields) != 1 {
		return Field{}, false
	}
	path, _ := fields[0].(string)
	f := FieldOf(path)
	if name, ok := m["name"].(string); ok {
		f.Name = name
	}
	return f, true
}

// arrayParam is a parameter declared as an array, which Vega also accepts as a single value.
func arrayParam(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case map[string]any:
		return []any{x}, true
	}
	return nil, false
}

// fieldsFrom decodes an array of accessor markers.
func fieldsFrom(v any) ([]Field, bool) {
	items, ok := arrayParam(v)
	if !ok {
		return nil, false
	}
	out := make([]Field, len(items))
	for i, it := range items {
		f, ok := fieldFrom(it)
		if !ok {
			return nil, false
		}
		out[i] = f
	}
	return out, true
}

// nullableFieldsFrom is fieldsFrom for an array in which null stands for no field (a count).
func nullableFieldsFrom(v any) ([]Field, bool) {
	items, ok := arrayParam(v)
	if !ok {
		return nil, false
	}
	out := make([]Field, len(items))
	for i, it := range items {
		if it == nil {
			continue
		}
		f, ok := fieldFrom(it)
		if !ok {
			return nil, false
		}
		out[i] = f
	}
	return out, true
}

// comparatorFrom decodes the sort of a compare(...) call, recorded as an accessor or a comparator.
func comparatorFrom(v any) (Comparator, bool) {
	m, ok := v.(map[string]any)
	if !ok || (m["$"] != "accessor" && m["$"] != "comparator") {
		return nil, false
	}
	paths, _ := m["fields"].([]any)
	orders, _ := m["orders"].([]any)
	var fields []Field
	var ords []string
	for i, p := range paths {
		s, _ := p.(string)
		fields = append(fields, FieldOf(s))
		o := Asc
		if i < len(orders) {
			o, _ = orders[i].(string)
		}
		ords = append(ords, o)
	}
	return CompareBy(fields, ords), true
}

// stringsFrom decodes an array of strings, in which null is the empty string.
func stringsFrom(v any) []string {
	items, _ := v.([]any)
	out := make([]string, len(items))
	for i, it := range items {
		out[i], _ = it.(string)
	}
	return out
}

// numbersFrom decodes an array of numbers, in which a missing or null entry is NaN.
func numbersFrom(v any, n int) []float64 {
	items, _ := v.([]any)
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
		if i < len(items) && items[i] != nil {
			out[i] = upstream.Number(items[i])
		}
	}
	return out
}

// pulseTuples decodes the first of the keys that the pulse has, as tuples.
func pulseTuples(m map[string]any, keys ...string) ([]jsval.Value, bool) {
	for _, k := range keys {
		if items, ok := m[k].([]any); ok {
			return tuplesOf(items), true
		}
	}
	return nil, false
}

// tuplesOf decodes recorded tuples.
func tuplesOf(items []any) []jsval.Value {
	vals := make([]jsval.Value, len(items))
	for i, it := range items {
		vals[i] = upstream.ToValue(it)
	}
	return vals
}

// checkTuples compares the tuples the engine produced with the recorded ones.
func (o *opCall) checkTuples(want any, res []jsval.Value, err error) {
	if err != nil {
		o.CheckAgainst(o.c, want, nil, true)
		return
	}
	o.CheckAgainst(o.c, want, tupleList(res), false)
}

// stateful skips a vector whose answer depends on the pulses the operator instance saw before it.
func (o *opCall) stateful() bool {
	if o.c.Sequence == 0 {
		return false
	}
	o.Skip("pulses against the state of earlier ones (" + o.c.Op + ")")
	return true
}

func (o *opCall) aggregate() {
	groupby, okGroup := fieldsFrom(o.params["groupby"])
	fields, okFields := nullableFieldsFrom(o.params["fields"])
	if o.params["groupby"] == nil {
		groupby, okGroup = nil, true
	}
	if o.params["fields"] == nil {
		fields, okFields = nil, true
	}
	if !okGroup || !okFields || o.params["drop"] != nil {
		o.Skip("aggregate parameters of another shape")
		return
	}
	p := AggregateParams{GroupBy: groupby, Cross: upstream.ToValue(o.params["cross"]).IsTruthy()}
	ops := stringsFrom(o.params["ops"])
	as := stringsFrom(o.params["as"])
	aggParams := numbersFrom(o.params["aggregate_params"], len(ops))
	for i, op := range ops {
		m := Measure{Op: op}
		if i < len(fields) {
			m.Field = fields[i]
		}
		if i < len(as) {
			m.As = as[i]
		}
		if !math.IsNaN(aggParams[i]) {
			m.Param = aggParams[i]
		}
		p.Measures = append(p.Measures, m)
	}
	if o.c.Sequence > 0 {
		switch {
		case p.Cross && len(p.GroupBy) > 1:
			o.Skip("aggregate cross products, which keep the values of tuples since removed")
		case slices.Contains(ops, "exponential") || slices.Contains(ops, "exponentialb"):
			o.Skip("aggregate exponential averages, which a removal does not invert")
		default:
			o.aggregateState(p)
		}
		return
	}
	data, ok := pulseTuples(o.in, "add")
	want, hasWant := o.out["add"]
	if !ok {
		o.Skip("aggregates whose input pulse the recording does not hold")
		return
	}
	if !hasWant || len(o.in) != 2 || len(o.out) != 1 {
		o.Skip("aggregate pulses of another shape")
		return
	}
	res, err := Aggregate(o.ctx, data, p)
	o.checkTuples(want, res, err)
}

// aggregateState replays an aggregate that has seen pulses before: the pulse it emits (which cells were
// added, modified or removed) depends on those pulses, but the cells it holds afterwards are the
// aggregate of the tuples that remain, which the engine computes in one go. The recording holds the
// cells in a JavaScript object, so the comparison is of the sets of tuples, not of their order.
func (o *opCall) aggregateState(p AggregateParams) {
	source, ok := pulseTuples(o.in, "source")
	cells, okCells := o.c.Value.(map[string]any)
	if !ok || !okCells {
		o.Skip("aggregate pulses of another shape")
		return
	}
	var want []any
	for _, cell := range cells {
		m, _ := cell.(map[string]any)
		if num, _ := m["num"].(float64); num > 0 {
			want = append(want, m["tuple"])
		}
	}
	res, err := Aggregate(o.ctx, source, p)
	if err != nil {
		o.CheckAgainst(o.c, nil, nil, true)
		return
	}
	got := tupleList(res)
	sortByText(want)
	sortByText(got)
	o.CheckAgainst(o.c, want, got, false)
}

// sortByText orders recorded values by their JSON text.
func sortByText(vs []any) {
	text := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	slices.SortFunc(vs, func(a, b any) int { return strings.Compare(text(a), text(b)) })
}

func (o *opCall) window() {
	data, ok := pulseTuples(o.in, "source")
	fields, okFields := nullableFieldsFrom(o.params["fields"])
	groupby, okGroup := fieldsFrom(o.params["groupby"])
	if o.params["fields"] == nil {
		fields, okFields = nil, true
	}
	if o.params["groupby"] == nil {
		groupby, okGroup = nil, true
	}
	want, hasWant := o.out["source"]
	if !ok || !okFields || !okGroup || !hasWant {
		o.Skip("window pulses of another shape")
		return
	}
	p := WindowParams{GroupBy: groupby}
	p.IgnorePeers = upstream.ToValue(o.params["ignorePeers"]).IsTruthy()
	if s, present := o.params["sort"]; present {
		if p.Sort, ok = comparatorFrom(s); !ok {
			o.Skip("window sort that is not a compare(...)")
			return
		}
	}
	if frame, ok := o.params["frame"].([]any); ok && len(frame) == 2 {
		for _, b := range frame {
			if b == nil {
				p.Frame = append(p.Frame, FrameBound{Unbounded: true})
			} else {
				p.Frame = append(p.Frame, FrameBound{Offset: int(upstream.Number(b))})
			}
		}
	}
	ops := stringsFrom(o.params["ops"])
	as := stringsFrom(o.params["as"])
	winParams := numbersFrom(o.params["params"], len(ops))
	aggParams := numbersFrom(o.params["aggregate_params"], len(ops))
	for i, op := range ops {
		s := WindowOpSpec{Op: op, Param: winParams[i]}
		if i < len(fields) {
			s.Field = fields[i]
		}
		if i < len(as) {
			s.As = as[i]
		}
		if !math.IsNaN(aggParams[i]) {
			s.AggParam = aggParams[i]
		}
		p.Ops = append(p.Ops, s)
	}
	res, err := Window(o.ctx, data, p)
	o.checkTuples(want, res, err)
}
