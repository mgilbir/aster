package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ---- transform nodes that wrap one transform object ----

type xformNode struct {
	dfBase
	kind      string // window, joinaggregate, fold, extent, flatten, pivot, sample, density, quantile, regression, loess, lookup, impute
	transform *Value
	secondary Value // lookup
}

func newXform(parent dfNode, kind string, t Value) *xformNode {
	n := &xformNode{kind: kind, transform: &t}
	initNode(n, parent)
	return n
}

func (n *xformNode) tv() Value    { return *n.transform }
func (n *xformNode) setT(v Value) { *n.transform = v }

var xformHashPrefix = map[string]string{
	"window": "WindowTransform", "joinaggregate": "JoinAggregateTransform", "fold": "FoldTransform",
	"extent": "ExtentTransform", "flatten": "FlattenTransform", "pivot": "PivotTransform",
	"sample": "SampleTransform", "density": "DensityTransform", "quantile": "QuantileTransform",
	"regression": "RegressionTransform", "loess": "LoessTransform", "lookup": "Lookup", "impute": "Impute",
}

func (n *xformNode) clone() dfNode {
	c := &xformNode{kind: n.kind, secondary: n.secondary}
	v := duplicate(n.cc, n.tv())
	c.transform = &v
	initNode(c, nil)
	return c
}

func (n *xformNode) hash() string {
	if n.kind == "lookup" {
		return "Lookup " + hashOf(n.cc, mkv("transform", n.tv(), "secondary", n.secondary))
	}
	return xformHashPrefix[n.kind] + " " + hashOf(n.cc, n.tv())
}

func strItems(cc *compileCtx, v Value) []string {
	var out []string
	for _, x := range v.Items() {
		cc.work(1)
		out = append(out, x.AsString())
	}
	return out
}

// defaultNameV is getDefaultName: `as ?? vgField(def)`, the value as given.
func (n *xformNode) defaultNameV(def Value) Value {
	if a := def.Get("as"); !a.IsNullish() {
		return a
	}
	return jsval.Str(vgField(n.cc, def, fieldRefOption{}))
}

func (n *xformNode) defaultName(def Value) string {
	cc := n.cc

	if a := def.Get("as"); !a.IsNullish() {
		return a.AsString()
	}
	return vgField(cc, def, fieldRefOption{})
}

func (n *xformNode) dependentFields() *sset {
	cc := n.cc
	t := n.tv()
	out := newSset()
	switch n.kind {
	case "window":
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
		// (this.transform.sort ?? []).forEach((m) => out.add(m.field))
		var sorts []Value
		if s := t.Get("sort"); !s.IsNullish() {
			sorts = listOf(s, "(this.transform.sort ?? [])", "forEach")
		}
		for _, m := range sorts {
			cc.work(1)
			out.addValue(jsGet(m, "field"))
		}
		for _, w := range t.Get("window").Items() {
			cc.work(1)
			if f := w.Get("field"); !f.IsUndefined() {
				out.add(f.AsString())
			}
		}
	case "joinaggregate":
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
		for _, w := range t.Get("joinaggregate").Items() {
			cc.work(1)
			if f := w.Get("field"); !f.IsUndefined() {
				out.add(f.AsString())
			}
		}
	case "fold":
		for _, f := range strItems(n.cc, t.Get("fold")) {
			cc.work(1)
			out.add(f)
		}
	case "extent":
		out.add(t.Get("extent").AsString())
	case "flatten":
		for _, f := range strItems(n.cc, t.Get("flatten")) {
			cc.work(1)
			out.add(f)
		}
	case "pivot":
		out.add(t.Get("pivot").AsString())
		out.add(t.Get("value").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	case "density":
		out.add(t.Get("density").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	case "quantile":
		out.add(t.Get("quantile").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	case "regression":
		out.add(t.Get("regression").AsString())
		out.add(t.Get("on").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	case "loess":
		out.add(t.Get("loess").AsString())
		out.add(t.Get("on").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	case "lookup":
		out.add(t.Get("lookup").AsString())
	case "impute":
		out.add(t.Get("impute").AsString())
		out.add(t.Get("key").AsString())
		for _, g := range strItems(n.cc, t.Get("groupby")) {
			cc.work(1)
			out.add(g)
		}
	}
	return out
}

func (n *xformNode) producedFields() *sset {
	cc := n.cc
	t := n.tv()
	switch n.kind {
	case "window":
		s := newSset()
		for _, w := range listOf(t.Get("window"), "this.transform.window", "map") {
			cc.work(1)
			// getDefaultName: as ?? vgField(...), the as as it is given.
			if a := w.Get("as"); !a.IsNullish() && !a.IsStr() {
				s.addValue(a)
				continue
			}
			s.add(n.defaultName(w))
		}
		return s
	case "joinaggregate":
		s := newSset()
		for _, w := range listOf(t.Get("joinaggregate"), "this.transform.joinaggregate", "map") {
			cc.work(1)
			// getDefaultName: as ?? vgField(...), the as as it is given.
			if a := w.Get("as"); !a.IsNullish() && !a.IsStr() {
				s.addValue(a)
				continue
			}
			s.add(n.defaultName(w))
		}
		return s
	case "fold", "flatten", "density", "quantile", "regression", "loess":
		// new Set(this.transform.as): the names as given (the constructor
		// made `as` an array).
		return ssetOfValues(t.Get("as").Items())
	case "extent", "sample":
		return newSset()
	case "pivot":
		return nil
	case "lookup":
		if t.Get("as").IsTruthy() {
			return ssetOfValues(arrayOf(t.Get("as")))
		}
		// new Set(this.transform.from.fields)
		return ssetOfValues(iterateNew(t.Get("from").Get("fields")))
	case "impute":
		return ssetOfValues([]Value{t.Get("impute")})
	}
	return newSset()
}

// addDimensions is defined for window, joinaggregate and pivot nodes.
// groupbyConcat is what unique() iterates of groupby.concat(fields): an
// array's items and the fields; a string's characters, as String.prototype
// .concat appends the fields' text to it ("1e999" and ["rev"] make
// "1e999rev"); anything else has no concat.
func groupbyConcat(g Value, fields []string) []Value {
	switch {
	case g.IsArr():
		out := append([]Value(nil), g.Items()...)
		for _, f := range fields {
			out = append(out, jsval.Str(f))
		}
		return out
	case g.IsStr():
		return iterate(jsval.Str(g.StrValue()+strings.Join(fields, ",")), "")
	}
	throw("this.transform.groupby.concat is not a function")
	return nil
}

func (n *xformNode) addDimensions(fields []string) {
	cc := n.cc
	t := n.tv()
	var base []Value
	switch n.kind {
	case "window", "joinaggregate":
		// `this.transform.groupby.concat(fields)`: without a groupby that is
		// a TypeError (pivot alone guards with `?? []`).
		switch g := t.Get("groupby"); {
		case g.IsUndefined():
			throw("Cannot read properties of undefined (reading 'concat')")
		case g.IsNull():
			throw("Cannot read properties of null (reading 'concat')")
		default:
			base = groupbyConcat(g, fields)
		}
	case "pivot":
		g := t.Get("groupby")
		if g.IsNullish() {
			g = jsval.Arr(nil) // (groupby ?? [])
		}
		base = groupbyConcat(g, fields)
	default:
		return
	}
	// unique(groupby.concat(fields), (d) => d)
	seen := map[string]bool{}
	var out []Value
	for _, x := range base {
		cc.work(1)
		if !jsSeen(cc, seen, x.AsString()) {
			seen[x.AsString()] = true
			out = append(out, x)
		}
	}
	o := cloneObj(t.ObjValue())
	o.Set("groupby", jsval.Arr(out))
	n.setT(jsval.Obj(o))
}

func (n *xformNode) assemble() []Value {
	cc := n.cc

	t := n.tv()
	switch n.kind {
	case "window":
		return []Value{n.assembleWindow()}
	case "joinaggregate":
		var fields, ops, as []Value
		for _, j := range t.Get("joinaggregate").Items() {
			cc.work(1)
			ops = append(ops, j.Get("op"))
			as = append(as, n.defaultNameV(j))
			if j.Get("field").IsUndefined() {
				fields = append(fields, jsval.Null)
			} else {
				fields = append(fields, j.Get("field"))
			}
		}
		o := mk("type", "joinaggregate", "as", jsval.Arr(as), "ops", jsval.Arr(ops), "fields", jsval.Arr(fields))
		if g := t.Get("groupby"); !g.IsUndefined() {
			o.Set("groupby", g)
		}
		return []Value{jsval.Obj(o)}
	case "fold":
		return []Value{mkv("type", "fold", "fields", t.Get("fold"), "as", t.Get("as"))}
	case "extent":
		return []Value{mkv("type", "extent", "field", t.Get("extent"), "signal", t.Get("param"))}
	case "flatten":
		return []Value{mkv("type", "flatten", "fields", t.Get("flatten"), "as", t.Get("as"))}
	case "pivot":
		o := mk("type", "pivot", "field", t.Get("pivot"), "value", t.Get("value"))
		for _, k := range []string{"limit", "op", "groupby"} {
			if v := t.Get(k); !v.IsUndefined() {
				o.Set(k, v)
			}
		}
		return []Value{jsval.Obj(o)}
	case "sample":
		return []Value{mkv("type", "sample", "size", t.Get("sample"))}
	case "density":
		o := mk("type", "kde", "field", t.Get("density"))
		spread(o, jsval.Obj(omit(t, "density")))
		if !cc.v5 {
			o.Set("resolve", t.Get("resolve"))
		}
		return []Value{jsval.Obj(o)}
	case "quantile":
		o := mk("type", "quantile", "field", t.Get("quantile"))
		spread(o, jsval.Obj(omit(t, "quantile")))
		return []Value{jsval.Obj(o)}
	case "regression":
		o := mk("type", "regression", "x", t.Get("on"), "y", t.Get("regression"))
		spread(o, jsval.Obj(omit(t, "regression", "on")))
		return []Value{jsval.Obj(o)}
	case "loess":
		o := mk("type", "loess", "x", t.Get("on"), "y", t.Get("loess"))
		spread(o, jsval.Obj(omit(t, "loess", "on")))
		return []Value{jsval.Obj(o)}
	case "lookup":
		return []Value{n.assembleLookup()}
	case "impute":
		return n.assembleImpute()
	}
	return nil
}

func (n *xformNode) assembleWindow() Value {
	cc := n.cc

	t := n.tv()
	var fields, ops, as, params []Value
	for _, w := range t.Get("window").Items() {
		cc.work(1)
		ops = append(ops, w.Get("op"))
		as = append(as, n.defaultNameV(w))
		if p := w.Get("param"); p.IsUndefined() {
			params = append(params, jsval.Null)
		} else {
			params = append(params, p)
		}
		if f := w.Get("field"); f.IsUndefined() {
			fields = append(fields, jsval.Null)
		} else {
			fields = append(fields, f)
		}
	}
	frame, groupby := t.Get("frame"), t.Get("groupby")
	if frame.IsArr() && frame.Index(0).IsNull() && frame.Index(1).IsNull() {
		all := true
		for _, o := range ops {
			cc.work(1)
			if !isAggregateOp(cc, o) {
				all = false
			}
		}
		if all {
			o := mk("type", "joinaggregate", "as", jsval.Arr(as), "ops", jsval.Arr(ops), "fields", jsval.Arr(fields))
			if !groupby.IsUndefined() {
				o.Set("groupby", groupby)
			}
			return jsval.Obj(o)
		}
	}
	var sortFields, sortOrder []Value
	if s := t.Get("sort"); !s.IsUndefined() {
		for _, sf := range iterate(s, "this.transform.sort") {
			cc.work(1)
			sortFields = append(sortFields, jsGet(sf, "field"))
			sortOrder = append(sortOrder, coalesce(jsGet(sf, "order"), jsval.Str("ascending")))
		}
	}
	sort := mkv("field", jsval.Arr(sortFields), "order", jsval.Arr(sortOrder))
	o := mk("type", "window", "params", jsval.Arr(params), "as", jsval.Arr(as), "ops", jsval.Arr(ops), "fields", jsval.Arr(fields), "sort", sort)
	if ip := t.Get("ignorePeers"); !ip.IsUndefined() {
		o.Set("ignorePeers", ip)
	}
	if !groupby.IsUndefined() {
		o.Set("groupby", groupby)
	}
	if !frame.IsUndefined() {
		o.Set("frame", frame)
	}
	return jsval.Obj(o)
}

func (n *xformNode) assembleLookup() Value {
	t := n.tv()
	var foreign *Object
	if t.Get("from").Get("fields").IsTruthy() {
		foreign = mk("values", t.Get("from").Get("fields"))
		if t.Get("as").IsTruthy() {
			foreign.Set("as", jsval.Arr(arrayOf(t.Get("as"))))
		}
	} else {
		asName := t.Get("as")
		if !asName.IsStr() {
			n.cc.warn(msgNoFieldsNeedsAs)
			asName = jsval.Str("_lookup")
		}
		foreign = mk("as", arr(asName))
	}
	o := mk("type", "lookup", "from", n.secondary, "key", t.Get("from").Get("key"), "fields", arr(t.Get("lookup")))
	spread(o, jsval.Obj(foreign))
	if d := t.Get("default"); d.IsTruthy() {
		o.Set("default", d)
	}
	return jsval.Obj(o)
}

func (n *xformNode) assembleImpute() []Value {
	t := n.tv()
	impute, key, keyvals, method, groupby, value := t.Get("impute"), t.Get("key"), t.Get("keyvals"), t.Get("method"), t.Get("groupby"), t.Get("value")
	frame := t.Get("frame")
	if frame.IsUndefined() {
		frame = arr(nil, nil)
	}
	it := mk("type", "impute", "field", impute, "key", key)
	if keyvals.IsTruthy() {
		if hasProperty(keyvals, "stop") {
			// const {start = 0, stop, step} = keyvals (the default for an
			// undefined start only), then [start, stop, ...].join(','),
			// which writes null as "".
			start := keyvals.Get("start")
			if start.IsUndefined() {
				start = jsval.Int(0)
			}
			parts := []string{joinItem(start), joinItem(keyvals.Get("stop"))}
			if keyvals.Get("step").IsTruthy() {
				parts = append(parts, joinItem(keyvals.Get("step")))
			}
			it.Set("keyvals", mkv("signal", "sequence("+strings.Join(parts, ",")+")"))
		} else {
			it.Set("keyvals", keyvals)
		}
	}
	it.Set("method", jsval.Str("value"))
	if groupby.IsTruthy() {
		it.Set("groupby", groupby)
	}
	if !method.IsTruthy() || (method.IsStr() && method.StrValue() == "value") {
		it.Set("value", value)
	} else {
		it.Set("value", jsval.Null)
	}
	if method.IsTruthy() && !(method.IsStr() && method.StrValue() == "value") {
		derive := mk("type", "window", "as", arr("imputed_"+impute.AsString()+"_value"), "ops", arr(method), "fields", arr(impute), "frame", frame, "ignorePeers", false)
		if groupby.IsTruthy() {
			derive.Set("groupby", groupby)
		}
		replace := mkv("type", "formula", "expr", "datum."+impute.AsString()+" === null ? datum.imputed_"+impute.AsString()+"_value : datum."+impute.AsString(), "as", impute)
		return []Value{jsval.Obj(it), jsval.Obj(derive), replace}
	}
	return []Value{jsval.Obj(it)}
}

func makeImputeFromEncoding(parent dfNode, m *unitModel) dfNode {
	cc := m.b().ctx

	encoding := m.encoding
	xDef, yDef := encoding.Get("x"), encoding.Get("y")
	if isFieldDef(cc, xDef) && isFieldDef(cc, yDef) {
		var imputed, keyDef Value
		switch {
		case xDef.Get("impute").IsTruthy():
			imputed, keyDef = xDef, yDef
		case yDef.Get("impute").IsTruthy():
			imputed, keyDef = yDef, xDef
		default:
			return nil
		}
		imp := imputed.Get("impute")
		method, value, frame, keyvals := imp.Get("method"), imp.Get("value"), imp.Get("frame"), imp.Get("keyvals")
		groupbyFields := pathGroupingFields(cc, m.mark(), encoding)
		o := mk("impute", imputed.Get("field"), "key", keyDef.Get("field"))
		if method.IsTruthy() {
			o.Set("method", method)
		}
		if !value.IsUndefined() {
			o.Set("value", value)
		}
		if frame.IsTruthy() {
			o.Set("frame", frame)
		}
		if !keyvals.IsUndefined() {
			o.Set("keyvals", keyvals)
		}
		if len(groupbyFields) > 0 {
			o.Set("groupby", strsVal(groupbyFields))
		}
		return newXform(parent, "impute", jsval.Obj(o))
	}
	return nil
}

// ---- transform constructors that normalize their input ----

func newDensityNode(cc *compileCtx, parent dfNode, t Value) dfNode {
	t = duplicate(ccOf(parent), t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("value")), coalesce(jsIndex(as, 1), jsval.Str("density"))))
	if cc.v5 {
		// 5.8 has no `resolve`; grouped densities default to 200 steps.
		if t.Get("groupby").IsTruthy() && t.Get("minsteps").IsNullish() && t.Get("maxsteps").IsNullish() && t.Get("steps").IsNullish() {
			o.Set("steps", jsval.Int(200))
		}
	} else {
		o.Set("resolve", coalesce(t.Get("resolve"), jsval.Str("shared")))
	}
	return newXform(parent, "density", jsval.Obj(o))
}

func newFoldNode(parent dfNode, t Value) dfNode {
	t = duplicate(ccOf(parent), t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("key")), coalesce(jsIndex(as, 1), jsval.Str("value"))))
	return newXform(parent, "fold", jsval.Obj(o))
}

func newFlattenNode(parent dfNode, t Value) dfNode {
	cc := ccOf(parent)
	t = duplicate(ccOf(parent), t)
	as := t.Get("as")
	var out []Value
	// as[i]: a string's units are made once (jsIndex makes them per read).
	var units []uint16
	if as.IsStr() {
		units = utf16Len(as.StrValue())
	}
	for i, f := range listOf(t.Get("flatten"), "flatten", "map") {
		cc.work(1)
		var a Value
		if as.IsStr() {
			a = unitAt(units, i)
		} else {
			a = jsIndex(as, i)
		}
		out = append(out, coalesce(a, f))
	}
	o := cloneObj(t.ObjValue())
	o.Set("as", jsval.Arr(out))
	return newXform(parent, "flatten", jsval.Obj(o))
}

func newQuantileNode(parent dfNode, t Value) dfNode {
	t = duplicate(ccOf(parent), t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("prob")), coalesce(jsIndex(as, 1), jsval.Str("value"))))
	return newXform(parent, "quantile", jsval.Obj(o))
}

func newRegressionNode(parent dfNode, t Value, kind string, field string) dfNode {
	t2 := duplicate(ccOf(parent), t)
	as := t2.Get("as")
	o := cloneObj(t2.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), t.Get("on")), coalesce(jsIndex(as, 1), t.Get(field))))
	return newXform(parent, kind, jsval.Obj(o))
}

func makeLookupNode(parent dfNode, m Model, t Value, counter int) dfNode {
	cc := m.b().ctx

	b := m.b()
	sources := b.comp.data
	from := t.Get("from")
	var fromOutput *outputNode
	var secondary Value
	switch {
	case hasProperty(from, "data"):
		fromSource := findSource(cc, from.Get("data"), sources.sources.items)
		if fromSource == nil {
			fromSource = newSourceNode(cc, from.Get("data"))
			sources.sources.items = append(sources.sources.items, fromSource)
		}
		name := b.getName("lookup_" + jsval.JSNumberString(float64(counter)))
		fromOutput = newOutputNode(fromSource, name, dsLookup, sources.outputNodeRefCounts)
		sources.outputNodes[name] = fromOutput
	case hasProperty(from, "param"):
		selName := from.Get("param").AsString()
		o := mk("as", selName)
		spread(o, t)
		t = jsval.Obj(o)
		sel, junk := b.trySelectionComponent(varNameOf(from.Get("param")))
		if sel == nil && !junk {
			throw("Lookups can only be performed on selection parameters. %q is a variable parameter.", selName)
		}
		if sel != nil {
			fromOutput = sel.materialized
		}
		if fromOutput == nil {
			throw("Cannot define and lookup the %q selection in the same view. Try moving the lookup into a second, layered view?", selName)
		}
	default:
		throw("Invalid lookup `from` %s: expected an object with a `data` or a `param` property.", stringify(cc, from))
	}
	secondary = fromOutput.getSource()
	n := newXform(parent, "lookup", t)
	n.secondary = secondary
	return n
}

// ---- stack ----

type stackParams struct {
	dimensionFieldDefs []Value
	stackField         string
	// stackFieldV is the stacked field as upstream holds it (a transform's
	// `stack` of any type); unset, it is stackField.
	stackFieldV Value
	groupby     Value
	offset      Value // as upstream holds it, of whatever type
	sort        Value
	facetby     []string
	stackby     []string
	impute      bool
	as          []string
}

type stackNode struct {
	dfBase
	st *stackParams
	// assembling says getGroupbyFields is read by assemble (a spread)
	// rather than by dependentFields (a forEach), for upstream's message.
	assembling bool
}

func (st *stackParams) stackFieldValue() Value {
	if !st.stackFieldV.IsUndefined() {
		return st.stackFieldV
	}
	return jsval.Str(st.stackField)
}

func newStackNode(parent dfNode, st *stackParams) *stackNode {
	return initNode(&stackNode{st: st}, parent)
}

func (n *stackNode) clone() dfNode {
	c := *n.st
	c.dimensionFieldDefs = append([]Value(nil), n.st.dimensionFieldDefs...)
	c.facetby = append([]string(nil), n.st.facetby...)
	c.stackby = append([]string(nil), n.st.stackby...)
	c.as = append([]string(nil), n.st.as...)
	c.sort = duplicate(n.cc, n.st.sort)
	return initNode(&stackNode{st: &c}, nil)
}

func (n *stackNode) addDimensions(fields []string) { n.st.facetby = append(n.st.facetby, fields...) }

func (n *stackNode) getGroupbyFields() []string {
	vs := n.getGroupbyFieldsV()
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.AsString()
	}
	return out
}

// getGroupbyFieldsV is getGroupbyFields with a transform's group-by entries
// as given (a null stays null in the output).
func (n *stackNode) getGroupbyFieldsV() []Value {
	cc := n.cc

	st := n.st
	if len(st.dimensionFieldDefs) > 0 {
		var out []Value
		for _, d := range st.dimensionFieldDefs {
			cc.work(1)
			if d.Get("bin").IsTruthy() {
				if st.impute {
					out = append(out, jsval.Str(vgField(cc, d, fieldRefOption{binSuffix: "mid"})))
				} else {
					out = append(out, jsval.Str(vgField(cc, d, fieldRefOption{})), jsval.Str(vgField(cc, d, fieldRefOption{binSuffix: "end"})))
				}
			} else {
				out = append(out, jsval.Str(vgField(cc, d, fieldRefOption{})))
			}
		}
		return out
	}
	// groupby ?? [], which the callers iterate: a value that is not an
	// array is upstream's TypeError, as forEach or as a spread.
	if g := st.groupby; !g.IsNullish() && !g.IsArr() {
		if !n.assembling {
			throw("this.getGroupbyFields(...).forEach is not a function")
		}
		if !g.IsStr() {
			throw("this.getGroupbyFields is not a function or its return value is not iterable")
		}
		return iterate(g, "") // a spread of a string: its characters
	}
	return st.groupby.Items()
}

func (n *stackNode) dependentFields() *sset {
	cc := n.cc
	out := newSset()
	out.addValue(n.st.stackFieldValue())
	for _, f := range n.getGroupbyFields() {
		cc.work(1)
		out.add(f)
	}
	for _, f := range n.st.facetby {
		out.add(f)
	}
	for _, f := range strItems(n.cc, n.st.sort.Get("field")) {
		cc.work(1)
		out.add(f)
	}
	return out
}

func (n *stackNode) producedFields() *sset { return newSset(n.st.as...) }

func (n *stackNode) hash() string {
	st := n.st
	dims := make([]Value, len(st.dimensionFieldDefs))
	copy(dims, st.dimensionFieldDefs)
	o := jsval.NewObject(8)
	o.Set("dimensionFieldDefs", jsval.Arr(dims))
	o.Set("stackField", st.stackFieldValue())
	o.Set("groupby", st.groupby)
	o.Set("offset", st.offset)
	o.Set("sort", st.sort)
	o.Set("facetby", strsVal(st.facetby))
	if st.stackby != nil {
		o.Set("stackby", strsVal(st.stackby))
	}
	if st.impute {
		o.Set("impute", jsval.True)
	}
	o.Set("as", strsVal(st.as))
	return "Stack " + hashOf(n.cc, jsval.Obj(o))
}

func makeStackFromTransform(parent dfNode, t Value) *stackNode {
	cc := ccOf(parent)
	stack, groupby, as := t.Get("stack"), t.Get("groupby"), t.Get("as")
	offset := jsval.Str("zero")
	if o := t.Get("offset"); !o.IsUndefined() {
		offset = o
	}
	// for (const sortField of stackTransform.sort): sortField.field and
	// .order are read without a guard.
	var sortFields, sortOrder []Value
	if s := t.Get("sort"); !s.IsUndefined() {
		for _, sf := range iterate(s, "stackTransform.sort") {
			cc.work(1)
			sortFields = append(sortFields, jsGet(sf, "field"))
			sortOrder = append(sortOrder, firstDefined(jsGet(sf, "order"), jsval.Str("ascending")))
		}
	}
	sort := mkv("field", jsval.Arr(sortFields), "order", jsval.Arr(sortOrder))
	var normalizedAs []string
	switch {
	case as.IsArr() && as.Len() > 1 && func() bool {
		for _, x := range as.Items() {
			cc.work(1)
			if !x.IsStr() {
				return false
			}
		}
		return true
	}():
		normalizedAs = strItems(ccOf(parent), as)
	case as.IsStr():
		normalizedAs = []string{as.StrValue(), as.StrValue() + "_end"}
	default:
		normalizedAs = []string{stack.AsString() + "_start", stack.AsString() + "_end"}
	}
	return newStackNode(parent, &stackParams{
		stackField: stack.AsString(), stackFieldV: stack, groupby: groupby, offset: offset, sort: sort, as: normalizedAs,
	})
}

func makeStackFromEncoding(parent dfNode, m *unitModel) dfNode {
	cc := m.b().ctx

	sp := m.stack
	if sp == nil {
		return nil
	}
	var dims []Value
	for _, gc := range sp.groupbyChannels {
		if d := getFieldDef(cc, m.encoding.Get(gc)); d.IsTruthy() {
			dims = append(dims, d)
		}
	}
	var stackby []string
	for _, by := range sp.stackBy {
		cc.work(1)
		if f := vgField(cc, by.fieldDef, fieldRefOption{}); f != "" {
			stackby = append(stackby, f)
		}
	}
	orderDef := m.encoding.Get("order")
	var sort Value
	if orderDef.IsArr() || isFieldDef(cc, orderDef) {
		f, o := sortParams(cc, orderDef, fieldRefOption{})
		sort = mkv("field", jsval.Arr(f), "order", jsval.Arr(o))
	} else {
		var sortOrder Value
		switch {
		case isOrderOnlyDef(cc, orderDef):
			sortOrder = orderDef.Get("sort")
		case sp.fieldChannel == chY:
			sortOrder = jsval.Str("descending")
		default:
			sortOrder = jsval.Str("ascending")
		}
		var fields, orders []Value
		seen := map[string]bool{}
		for _, f := range stackby {
			cc.work(1)
			if cc.v5 || !seen[f] {
				seen[f] = true
				fields = append(fields, jsval.Str(f))
				orders = append(orders, sortOrder)
			}
		}
		sort = mkv("field", jsval.Arr(fields), "order", jsval.Arr(orders))
	}
	return newStackNode(parent, &stackParams{
		dimensionFieldDefs: dims, stackField: m.vgField(sp.fieldChannel, fieldRefOption{}),
		stackby: stackby, sort: sort, offset: sp.offsetV, impute: sp.impute,
		groupby: undef,
		as: []string{
			m.vgField(sp.fieldChannel, fieldRefOption{suffix: "start", forAs: true}),
			m.vgField(sp.fieldChannel, fieldRefOption{suffix: "end", forAs: true}),
		},
	})
}

func (n *stackNode) assemble() []Value {
	cc := n.cc

	var out []Value
	st := n.st
	if st.impute {
		for _, d := range st.dimensionFieldDefs {
			cc.work(1)
			// const {bandPosition = 0.5} = dimensionFieldDef, then
			// `${bandPosition}*…+${1 - bandPosition}*…`: JavaScript's
			// arithmetic and text for a band position of any type.
			bp := d.Get("bandPosition")
			if bp.IsUndefined() {
				bp = jsval.Num(0.5)
			}
			if d.Get("bin").IsTruthy() {
				binStart := vgField(cc, d, fieldRefOption{expr: "datum"})
				binEnd := vgField(cc, d, fieldRefOption{expr: "datum", binSuffix: "end"})
				bpS := bp.AsString()
				oneMinus := jsval.JSNumberString(1 - jsval.ToNumber(bp))
				expr := isValidFiniteNumberExpr(binStart) + " ? " + bpS + "*" + binStart + "+" + oneMinus + "*" + binEnd + " : " + binStart
				if cc.v5 {
					expr = bpS + "*" + binStart + "+" + oneMinus + "*" + binEnd
				}
				out = append(out, mkv("type", "formula",
					"expr", expr,
					"as", vgField(cc, d, fieldRefOption{binSuffix: "mid", forAs: true})))
			}
			gb := append(append([]string{}, st.stackby...), st.facetby...)
			out = append(out, mkv("type", "impute", "field", st.stackFieldValue(), "groupby", strsVal(gb),
				"key", vgField(cc, d, fieldRefOption{binSuffix: "mid"}), "method", "value", "value", 0))
		}
	}
	n.assembling = true
	gb := n.getGroupbyFieldsV()
	for _, f := range st.facetby {
		gb = append(gb, jsval.Str(f))
	}
	out = append(out, mkv("type", "stack", "groupby", jsval.Arr(gb), "field", st.stackFieldValue(), "sort", st.sort, "as", strsVal(st.as), "offset", st.offset))
	return out
}

// ---- geojson / geopoint ----

type geoJSONNode struct {
	dfBase
	fields  Value // the coordinate pair, or null for a shape (hash reads "null")
	geojson Value // the shape field as given; null for coordinates
	signal  string
}

func newGeoJSONNode(parent dfNode, fields Value, geojson Value, signal string) *geoJSONNode {
	return initNode(&geoJSONNode{fields: fields, geojson: geojson, signal: signal}, parent)
}

func (n *geoJSONNode) clone() dfNode {
	return initNode(&geoJSONNode{fields: duplicate(n.cc, n.fields), geojson: n.geojson, signal: n.signal}, nil)
}
func (n *geoJSONNode) dependentFields() *sset {
	cc := n.cc
	out := newSset()
	if n.geojson.IsTruthy() {
		out.addValue(n.geojson)
	}
	for _, f := range n.fields.Items() {
		cc.work(1)
		if f.IsStr() {
			out.add(f.StrValue())
		}
	}
	return out
}
func (n *geoJSONNode) producedFields() *sset { return newSset() }
func (n *geoJSONNode) hash() string {
	return "GeoJSON " + n.geojson.AsString() + " " + n.signal + " " + hashOf(n.cc, n.fields)
}
func (n *geoJSONNode) assemble() []Value {
	var out []Value
	if n.geojson.IsTruthy() {
		out = append(out, mkv("type", "filter", "expr", `isValid(datum["`+n.geojson.AsString()+`"])`))
	}
	o := mk("type", "geojson")
	if n.fields.IsTruthy() {
		o.Set("fields", n.fields)
	}
	if n.geojson.IsTruthy() {
		o.Set("geojson", n.geojson)
	}
	o.Set("signal", jsval.Str(n.signal))
	return append(out, jsval.Obj(o))
}

func geoPair(m *unitModel, coords [2]string) Value {
	cc := m.b().ctx

	pair := make([]Value, 2)
	for i, ch := range coords {
		def := getFieldOrDatumDef(cc, m.encoding.Get(ch))
		switch {
		case isFieldDef(cc, def):
			pair[i] = def.Get("field")
		case isDatumDefOf(m.b().ctx, def):
			pair[i] = mkv("expr", def.Get("datum").AsString())
		case isValueDef(def):
			pair[i] = mkv("expr", def.Get("value").AsString())
		default:
			pair[i] = undef
		}
	}
	return jsval.Arr(pair)
}

func parseAllGeoJSON(parent dfNode, m *unitModel) dfNode {
	if p := m.comp.projection; p != nil && !p.isFit() {
		return parent
	}
	counter := 0
	for _, coords := range [][2]string{{chLongitude, chLatitude}, {chLongitude2, chLatitude2}} {
		pair := geoPair(m, coords)
		if pair.Index(0).IsTruthy() || pair.Index(1).IsTruthy() {
			parent = newGeoJSONNode(parent, pair, jsval.Null, m.getName("geojson_"+jsval.JSNumberString(float64(counter))))
			counter++
		}
	}
	if m.channelHasField(chShape) {
		fd := m.typedFieldDef(chShape)
		if channelDefType(fd) == "geojson" {
			parent = newGeoJSONNode(parent, jsval.Null, fd.Get("field"), m.getName("geojson_"+jsval.JSNumberString(float64(counter))))
		}
	}
	return parent
}

type geoPointNode struct {
	dfBase
	projection string
	fields     Value
	as         []string
}

func newGeoPointNode(parent dfNode, projection string, fields Value, as []string) *geoPointNode {
	return initNode(&geoPointNode{projection: projection, fields: fields, as: as}, parent)
}
func (n *geoPointNode) clone() dfNode {
	return initNode(&geoPointNode{projection: n.projection, fields: duplicate(n.cc, n.fields), as: append([]string(nil), n.as...)}, nil)
}
func (n *geoPointNode) dependentFields() *sset {
	cc := n.cc
	out := newSset()
	for _, f := range n.fields.Items() {
		cc.work(1)
		if f.IsStr() {
			out.add(f.StrValue())
		}
	}
	return out
}
func (n *geoPointNode) producedFields() *sset { return newSset(n.as...) }
func (n *geoPointNode) hash() string {
	return "Geopoint " + n.projection + " " + hashOf(n.cc, n.fields) + " " + hashOf(n.cc, strsVal(n.as))
}
func (n *geoPointNode) assemble() Value {
	return mkv("type", "geopoint", "projection", n.projection, "fields", n.fields, "as", strsVal(n.as))
}

func parseAllGeoPoint(parent dfNode, m *unitModel) dfNode {
	proj := m.projectionName(false)
	if proj == "" {
		return parent
	}
	for _, coords := range [][2]string{{chLongitude, chLatitude}, {chLongitude2, chLatitude2}} {
		pair := geoPair(m, coords)
		suffix := ""
		if coords[0] == chLongitude2 {
			suffix = "2"
		}
		if pair.Index(0).IsTruthy() || pair.Index(1).IsTruthy() {
			parent = newGeoPointNode(parent, proj, pair, []string{m.getName("x" + suffix), m.getName("y" + suffix)})
		}
	}
	return parent
}

// ---- facet node ----

type facetChannelInfo struct {
	name           string
	fields         []string
	sortField      Value
	sortIndexField string
}

type facetNode struct {
	dfBase
	model      *facetModel
	name       string
	data       Value // the facet's source, as upstream holds it
	channels   map[string]*facetChannelInfo
	childModel Model
}

func newFacetNode(parent dfNode, m *facetModel, name string, data Value) *facetNode {
	cc := m.b().ctx

	n := &facetNode{model: m, name: name, data: data, channels: map[string]*facetChannelInfo{}}
	initNode(n, parent)
	for _, channel := range facetChannels {
		fd := m.facet.Lookup(channel)
		if fd.IsTruthy() {
			bin, sort := fd.Get("bin"), fd.Get("sort")
			info := &facetChannelInfo{name: m.getName(channel + "_domain"), fields: []string{vgField(cc, fd, fieldRefOption{})}}
			if isBinning(bin) {
				info.fields = append(info.fields, vgField(cc, fd, fieldRefOption{binSuffix: "end"}))
			}
			if isSortField(cc, sort) {
				info.sortField = sort
			} else if sort.IsArr() {
				info.sortIndexField = sortArrayIndexField(cc, fd, channel, fieldRefOption{})
			}
			n.channels[channel] = info
		}
	}
	n.childModel = m.child
	return n
}

func (n *facetNode) clone() dfNode    { throw("Cannot clone node"); return nil }
func (n *facetNode) getSource() Value { return jsval.Str(n.name) }

func (n *facetNode) facetHash(info *facetChannelInfo) string {
	o := jsval.NewObject(4)
	o.Set("name", jsval.Str(info.name))
	o.Set("fields", strsVal(info.fields))
	if info.sortField.IsTruthy() {
		o.Set("sortField", info.sortField)
	}
	if info.sortIndexField != "" {
		o.Set("sortIndexField", jsval.Str(info.sortIndexField))
	}
	return hashOf(n.cc, jsval.Obj(o))
}

func (n *facetNode) hash() string {
	out := "Facet"
	for _, ch := range facetChannels {
		if info := n.channels[ch]; info != nil {
			out += " " + ch[:1] + ":" + n.facetHash(info)
		}
	}
	return out
}

func (n *facetNode) fields() []string {
	var f []string
	for _, ch := range facetChannels {
		if info := n.channels[ch]; info != nil {
			f = append(f, info.fields...)
		}
	}
	return f
}

func (n *facetNode) dependentFields() *sset {
	dep := newSset(n.fields()...)
	for _, ch := range facetChannels {
		if info := n.channels[ch]; info != nil {
			if info.sortField.IsTruthy() {
				dep.add(info.sortField.Get("field").AsString())
			}
			if info.sortIndexField != "" {
				dep.add(info.sortIndexField)
			}
		}
	}
	return dep
}
func (n *facetNode) producedFields() *sset { return newSset() }

func (n *facetNode) getChildIndependentFieldsWithStep() map[string]string {
	out := map[string]string{}
	for _, channel := range positionScaleChannels {
		cs, ok := n.childModel.b().comp.scales.get(channel)
		if ok && cs != nil && !cs.merged {
			typ, rng := cs.get("type").AsString(), cs.get("range")
			if hasDiscreteDomain(typ) && isVgRangeStep(n.cc, rng) {
				domain := assembleDomain(n.childModel, channel)
				if field := getFieldFromDomain(n.cc, domain); field != "" {
					out[channel] = field
				} else {
					n.cc.warn(msgUnknownField(channel))
				}
			}
		}
	}
	return out
}

func (n *facetNode) assembleRowColumnHeaderData(channel, crossedDataName string, indep map[string]string) Value {
	cc := n.cc

	childChannel := map[string]string{"row": "y", "column": "x"}[channel]
	var fields, ops, as []Value
	if childChannel != "" && indep != nil && indep[childChannel] != "" {
		if crossedDataName != "" {
			fields = append(fields, jsval.Str("distinct_"+indep[childChannel]))
			ops = append(ops, jsval.Str("max"))
		} else {
			fields = append(fields, jsval.Str(indep[childChannel]))
			ops = append(ops, jsval.Str("distinct"))
		}
		as = append(as, jsval.Str("distinct_"+indep[childChannel]))
	}
	info := n.channels[channel]
	if info.sortField.IsTruthy() {
		op := firstDefined(info.sortField.Get("op"), jsval.Str(defaultSortOp)) // {op = DEFAULT_SORT_OP}: undefined only
		fields = append(fields, info.sortField.Get("field"))
		ops = append(ops, op)
		as = append(as, jsval.Str(vgField(cc, info.sortField, fieldRefOption{forAs: true})))
	} else if info.sortIndexField != "" {
		fields = append(fields, jsval.Str(info.sortIndexField))
		ops = append(ops, jsval.Str("max"))
		as = append(as, jsval.Str(info.sortIndexField))
	}
	source := n.data
	if crossedDataName != "" {
		source = jsval.Str(crossedDataName)
	}
	agg := mk("type", "aggregate", "groupby", strsVal(info.fields))
	if len(fields) > 0 {
		agg.Set("fields", jsval.Arr(fields))
		agg.Set("ops", jsval.Arr(ops))
		agg.Set("as", jsval.Arr(as))
	}
	return mkv("name", info.name, "source", source, "transform", arr(jsval.Obj(agg)))
}

func (n *facetNode) assembleFacetHeaderData(indep map[string]string) []Value {
	columns := n.model.layout.Lookup("columns")
	layoutHeaders := n.model.comp.layoutHeaders
	var data []Value
	hasShared := map[string]bool{}
	facetName := n.channels["facet"]
	for _, headerChannel := range headerChannels {
		for _, headerType := range headerTypes {
			for _, h := range layoutHeaders[headerChannel].get(headerType) {
				if h != nil && len(h.axes) > 0 {
					hasShared[headerChannel] = true
					break
				}
			}
		}
		if hasShared[headerChannel] {
			cardinality := `length(data("` + facetName.name + `"))`
			var stop Value
			if headerChannel == "row" {
				if columns.IsTruthy() {
					stop = sig("ceil(" + cardinality + " / " + columns.AsString() + ")")
				} else {
					stop = jsval.Int(1)
				}
			} else if columns.IsTruthy() {
				stop = sig("min(" + cardinality + ", " + columns.AsString() + ")")
			} else {
				stop = sig(cardinality)
			}
			data = append(data, mkv("name", facetName.name+"_"+headerChannel, "transform", arr(mkv("type", "sequence", "start", 0, "stop", stop))))
		}
	}
	if hasShared["row"] || hasShared["column"] {
		data = append([]Value{n.assembleRowColumnHeaderData("facet", "", indep)}, data...)
	}
	return data
}

func (n *facetNode) assemble() []Value {
	var data []Value
	crossedDataName := ""
	indep := n.getChildIndependentFieldsWithStep()
	column, row, facet := n.channels["column"], n.channels["row"], n.channels["facet"]
	if column != nil && row != nil && (indep["x"] != "" || indep["y"] != "") {
		crossedDataName = "cross_" + column.name + "_" + row.name
		var fields, ops []Value
		if indep["x"] != "" {
			fields = append(fields, jsval.Str(indep["x"]))
		}
		if indep["y"] != "" {
			fields = append(fields, jsval.Str(indep["y"]))
		}
		for range fields {
			ops = append(ops, jsval.Str("distinct"))
		}
		data = append(data, mkv("name", crossedDataName, "source", n.data, "transform", arr(
			mkv("type", "aggregate", "groupby", strsVal(n.fields()), "fields", jsval.Arr(fields), "ops", jsval.Arr(ops)))))
	}
	for _, ch := range []string{"column", "row"} {
		if n.channels[ch] != nil {
			data = append(data, n.assembleRowColumnHeaderData(ch, crossedDataName, indep))
		}
	}
	if facet != nil {
		data = append(data, n.assembleFacetHeaderData(indep)...)
	}
	return data
}
