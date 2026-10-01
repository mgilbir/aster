package transforms

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// The adapters of the operators that derive, annotate or order tuples, beside aggregate and window
// (upstream_transforms_ops_test.go).

func (o *opCall) bin() {
	data, ok := pulseTuples(o.in, "add")
	f, okField := fieldFrom(o.params["field"])
	want, hasWant := o.out["add"]
	bins, hasBins := o.c.Value.(map[string]any)
	ext, okExt := o.params["extent"].([]any)
	if !okField || !okExt || len(ext) != 2 || !(ok && hasWant || hasBins) {
		o.Skip("bin pulses of another shape")
		return
	}
	p := BinParams{Field: f, NoInterval: o.params["interval"] == false}
	p.Config = BinConfig{
		Extent:  [2]float64{upstream.Number(ext[0]), upstream.Number(ext[1])},
		NoNice:  o.params["nice"] == false,
		MaxBins: upstream.Number(o.params["maxbins"]),
		Step:    upstream.Number(o.params["step"]),
	}
	if math.IsNaN(p.Config.MaxBins) {
		p.Config.MaxBins = 0
	}
	if math.IsNaN(p.Config.Step) {
		p.Config.Step = 0
	}
	b, err := NewBinner(p)
	if err != nil {
		o.CheckAgainst(o.c, nil, nil, true)
		return
	}
	if !ok || !hasWant {
		// Only the binning function came out, which the recording holds as its bounds.
		o.CheckAgainst(o.c, bins, map[string]any{"start": upstream.Enc(b.Start), "stop": upstream.Enc(b.Stop), "step": upstream.Enc(b.Step)}, false)
		return
	}
	o.checkTuples(want, data, b.Apply(o.ctx, data, p.NoInterval, p.As))
}

func (o *opCall) project() {
	var fields []Field
	if o.params["fields"] != nil {
		var ok bool
		if fields, ok = fieldsFrom(o.params["fields"]); !ok {
			o.Skip("project fields of another shape")
			return
		}
	}
	p := ProjectParams{Fields: fields, As: stringsFrom(o.params["as"])}
	want, got := map[string]any{}, map[string]any{}
	for _, k := range []string{"add", "mod", "rem"} {
		data, ok := pulseTuples(o.in, k)
		if !ok {
			continue
		}
		res, err := Project(o.ctx, data, p)
		if err != nil {
			o.CheckAgainst(o.c, nil, nil, true)
			return
		}
		want[k], got[k] = o.out[k], tupleList(res)
	}
	o.CheckAgainst(o.c, want, got, false)
}

func (o *opCall) extent() {
	f, ok := fieldFrom(o.params["field"])
	value, okValue := o.c.Value.([]any)
	if !ok || !okValue {
		o.Skip("extent pulses of another shape")
		return
	}
	data, _ := pulseTuples(o.in, "source")
	lo, hi, found, err := FieldExtent(o.ctx, data, f)
	if err != nil {
		o.CheckAgainst(o.c, nil, nil, true)
		return
	}
	got := []any{upstream.Undefined(), upstream.Undefined()}
	if found {
		got = upstream.Floats([]float64{lo, hi})
	}
	o.CheckAgainst(o.c, value, got, false)
}

func (o *opCall) impute() {
	data, ok := pulseTuples(o.in, "source")
	field, okField := fieldFrom(o.params["field"])
	key, okKey := fieldFrom(o.params["key"])
	want, okWant := o.c.Value.([]any)
	groupby, okGroup := fieldsFrom(o.params["groupby"])
	if o.params["groupby"] == nil {
		groupby, okGroup = nil, true
	}
	if !ok || !okField || !okKey || !okWant || !okGroup {
		o.Skip("impute pulses of another shape")
		return
	}
	p := ImputeParams{Field: field, Key: key, GroupBy: groupby, Value: upstream.ToValue(o.params["value"])}
	p.Method, _ = o.params["method"].(string)
	if kv, ok := o.params["keyvals"].([]any); ok {
		for _, k := range kv {
			p.KeyVals = append(p.KeyVals, upstream.ToValue(k))
		}
	}
	res, err := Impute(o.ctx, data, p)
	if err != nil {
		o.CheckAgainst(o.c, nil, nil, true)
		return
	}
	o.CheckAgainst(o.c, want, tupleList(res[len(data):]), false)
}

func (o *opCall) lookup() {
	data, ok := pulseTuples(o.in, "source")
	fields, okFields := fieldsFrom(o.params["fields"])
	index, okIndex := o.params["index"].(map[string]any)
	object, _ := index["object"].(map[string]any)
	want, hasWant := o.out["source"]
	if !ok || !okFields || !okIndex || !hasWant {
		o.Skip("lookup pulses with no tuples")
		return
	}
	// The index maps a key to a tuple; the tuples are indexed under the keys the recording gives them.
	keys := make(map[*jsval.Object]string, len(object))
	var from []jsval.Value
	for k, t := range object {
		v := upstream.ToValue(t)
		keys[v.ObjValue()] = k
		from = append(from, v)
	}
	byKey := Field{Get: func(t jsval.Value) jsval.Value { return jsval.Str(keys[t.ObjValue()]) }}
	ix, err := NewLookupIndex(o.ctx, from, byKey)
	if err != nil {
		o.CheckAgainst(o.c, nil, nil, true)
		return
	}
	p := LookupParams{Index: ix, Fields: fields, As: stringsFrom(o.params["as"])}
	if o.params["values"] != nil {
		if p.Values, ok = fieldsFrom(o.params["values"]); !ok {
			o.Skip("lookup values of another shape")
			return
		}
	}
	if d, ok := o.params["default"]; ok {
		p.Default = upstream.ToValue(d)
	}
	res, err := Lookup(o.ctx, data, p)
	o.checkTuples(want, res, err)
}

func (o *opCall) dotbin() {
	data, ok := pulseTuples(o.in, "source")
	field, okField := fieldFrom(o.params["field"])
	groupby, okGroup := fieldsFrom(o.params["groupby"])
	want, hasWant := o.out["source"]
	if !ok || !okField || !okGroup || !hasWant {
		o.Skip("dotbin pulses of another shape")
		return
	}
	p := DotBinParams{Field: field, GroupBy: groupby, Smooth: upstream.ToValue(o.params["smooth"]).IsTruthy()}
	p.As, _ = o.params["as"].(string)
	if v, ok := o.params["step"]; ok {
		p.Step = upstream.Number(v)
	}
	_, err := DotBinTuples(o.ctx, data, p)
	o.checkTuples(want, data, err)
}

func (o *opCall) kde() {
	data, ok := pulseTuples(o.in, "source")
	field, okField := fieldFrom(o.params["field"])
	groupby, okGroup := fieldsFrom(o.params["groupby"])
	want, hasWant := o.out["add"]
	if !ok || !okField || !okGroup || !hasWant {
		o.Skip("kde pulses of another shape")
		return
	}
	p := KDEParams{
		Field: field, GroupBy: groupby,
		Cumulative: upstream.ToValue(o.params["cumulative"]).IsTruthy(),
		Counts:     upstream.ToValue(o.params["counts"]).IsTruthy(),
	}
	p.Resolve, _ = o.params["resolve"].(string)
	for name, dst := range map[string]*float64{
		"steps": &p.Steps, "minsteps": &p.MinSteps, "maxsteps": &p.MaxSteps, "bandwidth": &p.Bandwidth,
	} {
		if v, ok := o.params[name]; ok {
			*dst = upstream.Number(v)
		}
	}
	res, err := KDE(o.ctx, data, p)
	o.checkTuples(want, res, err)
}

// filter replays the filters whose expression is a constant. The others are closures, which the
// recording holds only by the fields they read, and a filter that has seen pulses before emits the
// changes against the tuples it let through then.
func (o *opCall) filter() {
	expr, _ := o.params["expr"].(map[string]any)
	data, ok := pulseTuples(o.in, "add")
	if name, _ := expr["name"].(string); o.c.Sequence != 0 || !ok || (name != "true" && name != "false") {
		o.Skip("filters that are closures, or that have seen pulses before")
		return
	}
	res, err := Filter(o.ctx, data, func(jsval.Value) bool { return expr["name"] == "true" })
	want := o.out["add"]
	if want == nil {
		want = []any{}
	}
	o.checkTuples(want, res, err)
}

// sample replays the first pulse of a sample. Its draws follow the ones that made the tuples, which the
// recording pins to a generator whose state the last tuple gives away.
func (o *opCall) sample() {
	data, ok := pulseTuples(o.in, "add")
	want, hasWant := o.out["source"]
	size, okSize := o.params["size"].(float64)
	if !ok || !hasWant || !okSize || o.c.Sequence != 0 {
		o.Skip("samples that have seen pulses before")
		return
	}
	last, _ := data[len(data)-1].ObjValue().Get("v")
	res, err := Sample(o.ctx, data, int(size), LCG(math.Round(jsval.ToNumber(last)*2147483647)))
	o.checkTuples(want, res, err)
}

// collectState says what is known of the tuples a collect instance holds.
type collectState struct {
	seen   bool // an earlier pulse handed it tuples
	sorted bool // a sort was set at some point: compare(field, 'descending') records as the accessor of its field, and the order is lost
	lost   bool // a pulse was not recorded in full
}

// collect replays the first pulse of tuples a collect is handed. The later ones apply to the tuples it
// holds, which the recording identifies by object where the replay has only their value (and which
// the operators downstream modify in place).
func (o *opCall) collect() {
	st := o.collected[o.c.Instance]
	if st == nil || o.c.Sequence == 0 {
		st = new(collectState)
		o.collected[o.c.Instance] = st
	}
	st.sorted = st.sorted || o.params["sort"] != nil
	data, ok := pulseTuples(o.in, "add")
	want, hasWant := o.out["source"]
	switch {
	case len(o.in) == 0:
		o.Skip("collects of no tuples")
	case st.lost || st.seen:
		o.Skip("collects of tuples that earlier pulses left")
	case st.sorted:
		o.Skip("sorted collects, whose sort order the recording does not keep")
	case !ok || !hasWant || o.in["rem"] != nil || o.in["mod"] != nil:
		o.Skip("collect pulses of another shape")
	default:
		res, err := Collect(o.ctx, data, nil)
		o.checkTuples(want, res, err)
	}
	st.seen = st.seen || len(o.in) > 0
}
