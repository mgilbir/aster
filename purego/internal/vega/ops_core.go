package vega

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
	"github.com/mgilbir/aster/purego/internal/transforms"
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
func ingestTuple(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindObj:
		return jsval.Obj(v.ObjValue().Clone())
	case jsval.KindArr, jsval.KindTimestamp:
		return v
	}
	return jsval.Obj(jsval.ObjectOf("data", v))
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
		return makeKeyFn(fields, p.Value("flat").IsTruthy())
	}
}

// makeKeyFn is vega-util's key(fields, flat): the string forms of the field
// values joined with '|'. With flat, the names are literal property names.
func makeKeyFn(fields []string, flat bool) transforms.KeyFunc {
	fs := make([]transforms.Field, len(fields))
	for i, f := range fields {
		if flat {
			fs[i] = flatField(f)
		} else {
			fs[i] = transforms.FieldOf(f)
		}
	}
	return transforms.KeyOf(fs...)
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
	return makeKeyFn(fields, k.flat)
}

// compareSpec is a resolved comparator: the function over tuples, and the field
// paths and orders it was built from (hierarchy transforms apply the same
// ordering to tree nodes).
type compareSpec struct {
	cmp    transforms.Comparator
	fields []string
	orders []string
}

// compareFrom builds a comparator from field specs (paths or accessors) and
// order strings; nil when no field remains.
func compareFrom(fields []any, orders []any) *compareSpec {
	fs := make([]transforms.Field, 0, len(fields))
	ord := make([]string, 0, len(fields))
	names := make([]string, 0, len(fields))
	for i, f := range fields {
		var fld transforms.Field
		switch v := f.(type) {
		case jsval.Value:
			if v.IsNullish() {
				continue
			}
			fld = transforms.FieldOf(v.AsString())
		case transforms.Field:
			fld = v
		case string:
			fld = transforms.FieldOf(v)
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
	return &compareSpec{cmp: cmp, fields: names, orders: ord}
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
			list = append(make([]jsval.Value, 0, len(src)), src...)
		}
		if cmp != nil && mod {
			transforms.SortTuples(list, cmp)
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
				transforms.SortTuples(src, cmp)
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
		live := make(map[*jsval.Object]struct{}, len(src))
		for i, t := range src {
			o := t.ObjValue()
			if o == nil {
				out[i] = t
				continue
			}
			live[o] = struct{}{}
			d, ok := lut[o]
			if !ok {
				d = jsval.Obj(o.Clone())
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
		for i := range data {
			data[i] = ingestTuple(data[i])
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
