package vega

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms"
)

// factory instantiates the behaviour of an entry type: its initial value and
// either a transform (pulse-processing) or an update function (a value
// operator).
type factory func(c *rtContext, n *opNode, e *entry) (value any, tr transform, upd func(*opNode, *opParams) any)

// trFunc adapts a function to the transform interface.
type trFunc func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse

func (f trFunc) transform(n *opNode, p *opParams, pulse *flowPulse) *flowPulse { return f(n, p, pulse) }

var transformFactories = map[string]factory{}

func init() {
	transformFactories["proxy"] = facProxy
	transformFactories["field"] = facField
	transformFactories["key"] = facKey
	transformFactories["compare"] = facCompare
	transformFactories["expression"] = facExpression
	transformFactories["params"] = facParams
	transformFactories["collect"] = facCollect
	transformFactories["sieve"] = facSieve
	transformFactories["values"] = facValues
	transformFactories["extent"] = facExtent
	transformFactories["multiextent"] = facMultiExtent
	transformFactories["multivalues"] = facMultiValues
	transformFactories["tupleindex"] = facTupleIndex
	transformFactories["relay"] = facRelay
	transformFactories["load"] = facLoad
}

// changedPulse is a pulse announcing new tuples.
func changedPulse(in *flowPulse, tuples []jsval.Value) *flowPulse {
	return &flowPulse{stamp: in.stamp, encode: in.encode, changed: true, tuples: tuples}
}

// barePulse is a pulse without source data (Pulse.fork(NO_SOURCE)).
func barePulse(in *flowPulse) *flowPulse {
	return &flowPulse{stamp: in.stamp, encode: in.encode, changed: true}
}

// ingestTuple is vega-dataflow's ingest: objects are tuples, anything else is
// wrapped as {data: value}. Objects are copied so that transforms that write
// fields into tuples never touch the specification they came from.
func ingestTuple(v jsval.Value) jsval.Value { return ingestTupleWith(nil, v) }

// ingestTupleWith is ingestTuple drawing the copy from cl, for a loop that
// ingests many rows; a nil cl copies each row on its own.
func ingestTupleWith(cl *jsval.Cloner, v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindObj:
		src := v.ObjValue()
		var o *jsval.Object
		if cl != nil {
			o = cl.Clone(src)
		} else {
			o = src.Clone()
		}
		o.SetTupleID(src.TupleID()) // ingest keeps the id a tuple already has
		o.EnsureTupleID()
		return jsval.Obj(o)
	case jsval.KindArr, jsval.KindTimestamp:
		return v
	}
	o := jsval.ObjectOf("data", v)
	o.EnsureTupleID()
	return jsval.Obj(o)
}

// -- value operators -----------------------------------------------------------

func facProxy(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		n.value = p.Get("value")
		if p.Modified("value") {
			return barePulse(pulse)
		}
		return stopPulse
	}), nil
}

func facField(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified() {
			return n.value
		}
		name := p.Value("name")
		as, _ := asString(p.Get("as"))
		if name.IsArr() {
			out := make([]transforms.Field, 0, name.Len())
			for _, f := range name.Items() {
				out = append(out, fieldAccessor(f.AsString(), ""))
			}
			return out
		}
		return fieldAccessor(name.AsString(), as)
	}
}

func asString(x any) (string, bool) {
	switch v := x.(type) {
	case jsval.Value:
		if v.IsNullish() {
			return "", false
		}
		return v.AsString(), true
	case string:
		return v, true
	}
	return "", false
}

func facKey(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified() {
			return n.value
		}
		var fields []string
		if arr, ok := p.Get("fields").([]any); ok {
			for _, f := range arr {
				if s, ok := asString(f); ok {
					fields = append(fields, s)
				}
			}
		}
		return makeKeyFn(c.view.zone, fields, p.Value("flat").IsTruthy())
	}
}

// makeKeyFn is vega-util's key(fields, flat): the string forms of the field
// values joined with '|' (a date as in the view's zone z). With flat, the names
// are literal property names.
func makeKeyFn(z format.Zone, fields []string, flat bool) transforms.KeyFunc {
	fs := make([]transforms.Field, len(fields))
	for i, f := range fields {
		if flat {
			fs[i] = flatField(f)
		} else {
			segs, err := jsval.SplitFieldPath(f)
			if err != nil {
				fail("%s", err.Error())
			}
			if len(segs) == 0 {
				// The runtime compiles the accessor of a path with no step to
				// `return _[];`, which does not parse. Unlike a field
				// parameter, a key has no guard for the empty name.
				fail("Unexpected token ']'")
			}
			fs[i] = transforms.FieldOf(f)
		}
	}
	return transforms.KeyOf(z, fs...)
}

// flatField reads a property by literal name (backslash escapes removed).
func flatField(name string) transforms.Field {
	var b []byte
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+1 < len(name) {
			i++
		}
		b = append(b, name[i])
	}
	n := string(b)
	return transforms.NamedField(n, []string{n}, func(t jsval.Value) jsval.Value { return propOf(t, jsval.Str(n)) })
}

func (c *rtContext) keyFn(k pKey) transforms.KeyFunc {
	var fields []string
	for _, f := range k.fields {
		if v, ok := f.(jsval.Value); ok {
			fields = append(fields, v.AsString())
		}
	}
	return makeKeyFn(c.view.zone, fields, k.flat)
}

// compareSpec is a resolved comparator: the function over tuples, and the field
// paths and orders it was built from (hierarchy transforms apply the same
// ordering to tree nodes).
type compareSpec struct {
	cmp    transforms.Comparator
	fields []string
	orders []string
	// keys are the accessors cmp is built from and roots the first segment of
	// each one's path; roots is nil unless every field is a plain path with a
	// segment, so a caller can read the keys off a partial view of a tuple.
	keys  []transforms.Field
	roots []string
	// rest reads what follows the root of each path.
	rest []transforms.Accessor
}

// compareFrom builds a comparator from field specs (paths or accessors) and
// order strings; nil when no field remains.
func compareFrom(fields []any, orders []any) *compareSpec {
	fs := make([]transforms.Field, 0, len(fields))
	ord := make([]string, 0, len(fields))
	names := make([]string, 0, len(fields))
	roots := make([]string, 0, len(fields))
	rest := make([]transforms.Accessor, 0, len(fields))
	rootsOK := true
	for i, f := range fields {
		var fld transforms.Field
		switch v := f.(type) {
		case jsval.Value:
			if v.IsNullish() {
				continue
			}
			fld = transforms.FieldOf(v.AsString())
			rootsOK = appendRoot(&roots, &rest, v.AsString()) && rootsOK
		case transforms.Field:
			fld = v
			rootsOK = false
		case string:
			fld = transforms.FieldOf(v)
			rootsOK = appendRoot(&roots, &rest, v) && rootsOK
		default:
			continue
		}
		o := ""
		if i < len(orders) {
			o, _ = asString(orders[i])
		}
		fs, ord, names = append(fs, fld), append(ord, o), append(names, fld.Name)
	}
	cmp := transforms.CompareBy(fs, ord)
	if cmp == nil {
		return nil
	}
	cs := &compareSpec{cmp: cmp, fields: names, orders: ord}
	if rootsOK {
		cs.keys, cs.roots, cs.rest = fs, roots, rest
	}
	return cs
}

// appendRoot adds the first segment of a field path to roots, and an accessor
// of the rest to rest; it reports whether the path has a first segment.
func appendRoot(roots *[]string, rest *[]transforms.Accessor, path string) bool {
	segs := jsval.ParseFieldPath(path)
	if len(segs) == 0 {
		*roots = append(*roots, "")
		*rest = append(*rest, nil)
		return false
	}
	*roots = append(*roots, segs[0])
	*rest = append(*rest, transforms.PathAccessor(segs[1:]))
	return true
}

func (c *rtContext) compareFn(cmp pCompare) *compareSpec {
	fields := make([]any, len(cmp.fields))
	for i, f := range cmp.fields {
		fields[i] = f
	}
	orders := make([]any, len(cmp.orders))
	for i, o := range cmp.orders {
		orders[i] = o
	}
	return compareFrom(fields, orders)
}

func facCompare(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified() {
			return n.value
		}
		asList := func(x any) []any {
			switch v := x.(type) {
			case []any:
				return v
			case nil:
				return nil
			case jsval.Value:
				if v.IsArr() {
					out := make([]any, 0, v.Len())
					for _, it := range v.Items() {
						out = append(out, it)
					}
					return out
				}
				if v.IsNullish() {
					return nil
				}
			}
			return []any{x}
		}
		if cs := compareFrom(asList(p.Get("fields")), asList(p.Get("orders"))); cs != nil {
			return cs
		}
		// upstream returns null (no ordering): keep a non-nil marker so the
		// value counts as computed
		return &compareSpec{}
	}
}

func facExpression(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	n.modified = true
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified("expr") {
			return n.value
		}
		b, _ := p.Get("expr").(*boundExpr)
		if b == nil {
			return nil
		}
		return b.accessor()
	}
}

func facParams(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		n.modified = p.Modified()
		n.value = p
		return barePulse(pulse)
	}), nil
}

// -- tuple collections ---------------------------------------------------------

func facCollect(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return []jsval.Value(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		cmp := p.comparator("sort")
		mod := pulse.changed || (cmp != nil && p.Modified("sort"))
		list, _ := n.value.([]jsval.Value)
		if pulse.changed {
			src := pulse.tuples
			if len(pulse.multi) > 0 {
				src = concatTuples(pulse.multi)
			}
			if cmp == nil {
				list = collectIncremental(list, src)
			} else {
				list = append(make([]jsval.Value, 0, len(src)), src...)
			}
		}
		if cmp != nil && mod {
			transforms.SortTuples(list, transforms.StableComparator(cmp))
		}
		n.value = list
		n.modified = mod
		tree := pulse.tree
		if !pulse.changed {
			tree = n.tree
		}
		n.tree = tree
		return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: mod, tuples: list, tree: tree}
	}), nil
}

// collectIncremental is what Collect does with the tuples that pass through it
// when it does not sort: tuples already collected and still present keep their
// place, those that left are dropped, and those that arrived are appended in
// the order they came (SortedList.data without a comparator: the surviving
// data, then the added tuples). A tuple is the same one when it is the same
// object, which is what upstream's tuple id stands for; a filter that lets a
// tuple through again after it was withheld thus appends it rather than
// putting it back where it was.
//
// The engine's operators hand over whole data sets rather than add, remove
// and modify lists, so which tuples left and which arrived is read off the
// difference. That needs distinct objects: anything else (a repeated or a
// non-object tuple) is taken as a new set in the order it came.
func collectIncremental(list, src []jsval.Value) []jsval.Value {
	if len(list) == 0 || sameObjects(list, src) {
		return append(make([]jsval.Value, 0, len(src)), src...)
	}
	incoming := make(map[*jsval.Object]struct{}, len(src))
	for _, t := range src {
		if !t.IsObj() {
			return append(make([]jsval.Value, 0, len(src)), src...)
		}
		o := t.ObjValue()
		if _, dup := incoming[o]; dup {
			return append(make([]jsval.Value, 0, len(src)), src...)
		}
		incoming[o] = struct{}{}
	}
	out := make([]jsval.Value, 0, len(src))
	kept := make(map[*jsval.Object]struct{}, len(list))
	for _, t := range list {
		if !t.IsObj() {
			return append(make([]jsval.Value, 0, len(src)), src...)
		}
		o := t.ObjValue()
		if _, dup := kept[o]; dup {
			return append(make([]jsval.Value, 0, len(src)), src...)
		}
		if _, ok := incoming[o]; ok {
			out = append(out, t)
			kept[o] = struct{}{}
		}
	}
	for _, t := range src {
		if _, ok := kept[t.ObjValue()]; !ok {
			out = append(out, t)
		}
	}
	return out
}

// sameObjects reports whether a and b are the same objects in the same order,
// for which collectIncremental's answer is b, whatever the sets hold.
func sameObjects(a, b []jsval.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		o := a[i].ObjValue()
		if o == nil || o != b[i].ObjValue() {
			return false
		}
	}
	return true
}

func concatTuples(lists [][]jsval.Value) []jsval.Value {
	n := 0
	for _, l := range lists {
		n += len(l)
	}
	out := make([]jsval.Value, 0, n)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func facSieve(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	n.modified = true // always treated as modified
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		if pulse.items != nil {
			n.value = pulse.items
		} else {
			n.value = pulse.tuples
		}
		n.tree = pulse.tree
		if pulse.changed {
			return barePulse(pulse)
		}
		return stopPulse
	}), nil
}

func facValues(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		if n.value == nil || p.Modified("field", "sort") || pulse.changed {
			src := pulse.tuples
			f, _ := p.Get("field").(transforms.Field)
			if cmp := p.comparator("sort"); cmp != nil {
				src = append([]jsval.Value(nil), src...)
				transforms.SortTuples(src, transforms.StableComparator(cmp))
			}
			out := make([]jsval.Value, len(src))
			for i, t := range src {
				out[i] = f.Apply(t)
			}
			n.value = jsval.Arr(out)
		}
		return nil
	}), nil
}

func facExtent(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return jsval.ArrOf(jsval.Undefined, jsval.Undefined), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		f, _ := p.Get("field").(transforms.Field)
		lo, hi, ok, err := transforms.FieldExtent(n.g.ctx, pulse.tuples, f)
		if err != nil {
			failErr(err)
		}
		if !ok {
			n.g.warn("Infinite extent for field \"" + f.Name + "\"")
			n.value = jsval.ArrOf(jsval.Undefined, jsval.Undefined)
		} else {
			n.value = jsval.ArrOf(jsval.Num(lo), jsval.Num(hi))
		}
		return nil
	}), nil
}

func facMultiExtent(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified() {
			return n.value
		}
		lo, hi := jsval.Num(math.Inf(1)), jsval.Num(math.Inf(-1))
		if exts, ok := p.Get("extents").([]any); ok {
			for _, x := range exts {
				ev, _ := x.(jsval.Value)
				if transforms.Less(ev.Index(0), lo) {
					lo = ev.Index(0)
				}
				if transforms.Greater(ev.Index(1), hi) {
					hi = ev.Index(1)
				}
			}
		}
		return jsval.ArrOf(lo, hi)
	}
}

func facMultiValues(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, nil, func(n *opNode, p *opParams) any {
		if n.value != nil && !p.Modified() {
			return n.value
		}
		var out []jsval.Value
		if vals, ok := p.Get("values").([]any); ok {
			for _, x := range vals {
				v, _ := x.(jsval.Value)
				out = append(out, v.Items()...)
			}
		}
		return jsval.Arr(out)
	}
}

// tupleIndex maps a field value (string-coerced) to a tuple: the lookup index
// of a Lookup transform, the counts behind indata(), and the per-unit counts
// of a selection store.
type tupleIndex struct {
	lookup *transforms.LookupIndex
	m      map[string]jsval.Value
}

func facTupleIndex(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return (*tupleIndex)(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		f, _ := p.Get("field").(transforms.Field)
		tuples := pulse.tuples
		if pulse.items != nil {
			// A named mark's data are its items ("reactive geometry").
			tuples = n.g.view.itemTuples(pulse.items)
		}
		lookup, err := transforms.NewLookupIndex(n.g.ctx, tuples, f)
		if err != nil {
			failErr(err)
		}
		idx := &tupleIndex{lookup: lookup, m: make(map[string]jsval.Value, len(tuples))}
		for _, t := range tuples {
			idx.m[f.Apply(t).AsString()] = t
		}
		n.value = idx
		n.modified = true
		return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: pulse.changed, tuples: pulse.tuples}
	}), nil
}

// facRelay relays a data stream between pipelines; with `derive` it hands on
// copies, so that transforms downstream do not write into the source tuples.
func facRelay(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	lut := map[*jsval.Object]jsval.Value{}
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		src := pulse.tuples
		if len(pulse.multi) > 0 {
			src = concatTuples(pulse.multi)
		}
		if !p.Value("derive").IsTruthy() {
			return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: true, tuples: src, tree: pulse.tree}
		}
		out := make([]jsval.Value, len(src))
		if len(lut) == 0 {
			lut = make(map[*jsval.Object]jsval.Value, len(src))
		}
		live := make(map[*jsval.Object]struct{}, len(src))
		var cl jsval.Cloner
		for i, t := range src {
			o := t.ObjValue()
			if o == nil {
				out[i] = t
				continue
			}
			live[o] = struct{}{}
			d, ok := lut[o]
			if !ok {
				d = jsval.Obj(cl.Clone(o))
				lut[o] = d
			} else {
				do := d.ObjValue()
				for j := 0; j < o.Len(); j++ {
					do.Set(o.KeyAt(j), o.ValueAt(j))
				}
			}
			out[i] = d
		}
		for o := range lut {
			if _, ok := live[o]; !ok {
				delete(lut, o)
			}
		}
		return &flowPulse{stamp: pulse.stamp, encode: pulse.encode, changed: true, tuples: out}
	}), nil
}

func facLoad(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return []jsval.Value(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		format := p.Value("format")
		var data []jsval.Value
		if v := p.Value("values"); !v.IsUndefined() && p.Has("values") {
			data = c.view.parseValues(v, format)
		} else {
			data = c.view.request(p.Value("url"), format)
		}
		var cl jsval.Cloner
		for i := range data {
			data[i] = ingestTupleWith(&cl, data[i])
		}
		n.value = data
		return changedPulse(pulse, data)
	}), nil
}

// itemTuples exposes scenegraph items to expressions and to marks derived from
// marks ("reactive geometry"): each item is presented as a tuple carrying its
// properties, its datum and its bounds.
func (v *runView) itemTuples(items []*scene.Item) []jsval.Value {
	out := make([]jsval.Value, len(items))
	for i, it := range items {
		out[i] = v.itemTuple(it)
	}
	return out
}
