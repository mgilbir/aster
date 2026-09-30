package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// ---- transform nodes that wrap one transform object ----

type xformNode struct {
	dfBase
	kind      string // window, joinaggregate, fold, extent, flatten, pivot, sample, density, quantile, regression, loess, lookup, impute
	transform *Value
	secondary string // lookup
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
	v := deepClone(n.tv())
	c.transform = &v
	initNode(c, nil)
	return c
}

func (n *xformNode) hash() string {
	if n.kind == "lookup" {
		return "Lookup " + hashOf(mkv("transform", n.tv(), "secondary", n.secondary))
	}
	return xformHashPrefix[n.kind] + " " + hashOf(n.tv())
}

func strItems(v Value) []string {
	var out []string
	for _, x := range v.Items() {
		out = append(out, x.AsString())
	}
	return out
}

func (n *xformNode) defaultName(def Value) string {
	if a := def.Get("as"); !a.IsNullish() {
		return a.AsString()
	}
	return vgField(def, fieldRefOption{})
}

func (n *xformNode) dependentFields() *sset {
	t := n.tv()
	out := newSset()
	switch n.kind {
	case "window":
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
		for _, m := range t.Get("sort").Items() {
			out.add(m.Get("field").AsString())
		}
		for _, w := range t.Get("window").Items() {
			if f := w.Get("field"); !f.IsUndefined() {
				out.add(f.AsString())
			}
		}
	case "joinaggregate":
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
		for _, w := range t.Get("joinaggregate").Items() {
			if f := w.Get("field"); !f.IsUndefined() {
				out.add(f.AsString())
			}
		}
	case "fold":
		for _, f := range strItems(t.Get("fold")) {
			out.add(f)
		}
	case "extent":
		out.add(t.Get("extent").AsString())
	case "flatten":
		for _, f := range strItems(t.Get("flatten")) {
			out.add(f)
		}
	case "pivot":
		out.add(t.Get("pivot").AsString())
		out.add(t.Get("value").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	case "density":
		out.add(t.Get("density").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	case "quantile":
		out.add(t.Get("quantile").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	case "regression":
		out.add(t.Get("regression").AsString())
		out.add(t.Get("on").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	case "loess":
		out.add(t.Get("loess").AsString())
		out.add(t.Get("on").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	case "lookup":
		out.add(t.Get("lookup").AsString())
	case "impute":
		out.add(t.Get("impute").AsString())
		out.add(t.Get("key").AsString())
		for _, g := range strItems(t.Get("groupby")) {
			out.add(g)
		}
	}
	return out
}

func (n *xformNode) producedFields() *sset {
	t := n.tv()
	switch n.kind {
	case "window":
		s := newSset()
		for _, w := range t.Get("window").Items() {
			s.add(n.defaultName(w))
		}
		return s
	case "joinaggregate":
		s := newSset()
		for _, w := range t.Get("joinaggregate").Items() {
			s.add(n.defaultName(w))
		}
		return s
	case "fold", "flatten", "density", "quantile", "regression", "loess":
		return newSset(strItems(t.Get("as"))...)
	case "extent", "sample":
		return newSset()
	case "pivot":
		return nil
	case "lookup":
		if t.Get("as").IsTruthy() {
			return newSset(strItems(jsval.Arr(arrayOf(t.Get("as"))))...)
		}
		return newSset(strItems(t.Get("from").Get("fields"))...)
	case "impute":
		return newSset(t.Get("impute").AsString())
	}
	return newSset()
}

// addDimensions is defined for window, joinaggregate and pivot nodes.
func (n *xformNode) addDimensions(fields []string) {
	t := n.tv()
	var base []Value
	switch n.kind {
	case "window", "joinaggregate":
		base = t.Get("groupby").Items()
	case "pivot":
		base = arrayOf(t.Get("groupby"))
	default:
		return
	}
	seen := map[string]bool{}
	var out []Value
	for _, x := range base {
		if !seen[x.AsString()] {
			seen[x.AsString()] = true
			out = append(out, x)
		}
	}
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			out = append(out, jsval.Str(f))
		}
	}
	o := cloneObj(t.ObjValue())
	o.Set("groupby", jsval.Arr(out))
	n.setT(jsval.Obj(o))
}

func (n *xformNode) assemble() []Value {
	t := n.tv()
	switch n.kind {
	case "window":
		return []Value{n.assembleWindow()}
	case "joinaggregate":
		var fields, ops, as []Value
		for _, j := range t.Get("joinaggregate").Items() {
			ops = append(ops, j.Get("op"))
			as = append(as, jsval.Str(n.defaultName(j)))
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
		if !v5 {
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
	t := n.tv()
	var fields, ops, as, params []Value
	for _, w := range t.Get("window").Items() {
		ops = append(ops, w.Get("op"))
		as = append(as, jsval.Str(n.defaultName(w)))
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
			if !isAggregateOp(o) {
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
		for _, sf := range s.Items() {
			sortFields = append(sortFields, sf.Get("field"))
			sortOrder = append(sortOrder, coalesce(sf.Get("order"), jsval.Str("ascending")))
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
			start := coalesce(keyvals.Get("start"), jsval.Int(0))
			parts := []string{start.AsString(), keyvals.Get("stop").AsString()}
			if keyvals.Get("step").IsTruthy() {
				parts = append(parts, keyvals.Get("step").AsString())
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
	encoding := m.encoding
	xDef, yDef := encoding.Get("x"), encoding.Get("y")
	if isFieldDef(xDef) && isFieldDef(yDef) {
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
		groupbyFields := pathGroupingFields(m.mark(), encoding)
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

func newDensityNode(parent dfNode, t Value) dfNode {
	t = deepClone(t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("value")), coalesce(jsIndex(as, 1), jsval.Str("density"))))
	if v5 {
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
	t = deepClone(t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("key")), coalesce(jsIndex(as, 1), jsval.Str("value"))))
	return newXform(parent, "fold", jsval.Obj(o))
}

func newFlattenNode(parent dfNode, t Value) dfNode {
	t = deepClone(t)
	as := t.Get("as")
	var out []Value
	for i, f := range t.Get("flatten").Items() {
		out = append(out, coalesce(jsIndex(as, i), f))
	}
	o := cloneObj(t.ObjValue())
	o.Set("as", jsval.Arr(out))
	return newXform(parent, "flatten", jsval.Obj(o))
}

func newQuantileNode(parent dfNode, t Value) dfNode {
	t = deepClone(t)
	as := t.Get("as")
	o := cloneObj(t.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), jsval.Str("prob")), coalesce(jsIndex(as, 1), jsval.Str("value"))))
	return newXform(parent, "quantile", jsval.Obj(o))
}

func newRegressionNode(parent dfNode, t Value, kind string, field string) dfNode {
	t2 := deepClone(t)
	as := t2.Get("as")
	o := cloneObj(t2.ObjValue())
	o.Set("as", arr(coalesce(jsIndex(as, 0), t.Get("on")), coalesce(jsIndex(as, 1), t.Get(field))))
	return newXform(parent, kind, jsval.Obj(o))
}

func makeLookupNode(parent dfNode, m Model, t Value, counter int) dfNode {
	b := m.b()
	sources := b.comp.data
	from := t.Get("from")
	var fromOutput *outputNode
	var secondary string
	switch {
	case hasProperty(from, "data"):
		fromSource := findSource(from.Get("data"), sources.sources.items)
		if fromSource == nil {
			fromSource = newSourceNode(from.Get("data"))
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
		sel := b.trySelectionComponent(varName(selName))
		if sel == nil {
			throw("Lookups can only be performed on selection parameters. %q is a variable parameter.", selName)
		}
		fromOutput = sel.materialized
		if fromOutput == nil {
			throw("Cannot define and lookup the %q selection in the same view. Try moving the lookup into a second, layered view?", selName)
		}
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
	groupby            Value
	offset             string
	sort               Value
	facetby            []string
	stackby            []string
	impute             bool
	as                 []string
}

type stackNode struct {
	dfBase
	st *stackParams
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
	c.sort = deepClone(n.st.sort)
	return initNode(&stackNode{st: &c}, nil)
}

func (n *stackNode) addDimensions(fields []string) { n.st.facetby = append(n.st.facetby, fields...) }

func (n *stackNode) getGroupbyFields() []string {
	st := n.st
	if len(st.dimensionFieldDefs) > 0 {
		var out []string
		for _, d := range st.dimensionFieldDefs {
			if d.Get("bin").IsTruthy() {
				if st.impute {
					out = append(out, vgField(d, fieldRefOption{binSuffix: "mid"}))
				} else {
					out = append(out, vgField(d, fieldRefOption{}), vgField(d, fieldRefOption{binSuffix: "end"}))
				}
			} else {
				out = append(out, vgField(d, fieldRefOption{}))
			}
		}
		return out
	}
	return strItems(st.groupby)
}

func (n *stackNode) dependentFields() *sset {
	out := newSset(n.st.stackField)
	for _, f := range n.getGroupbyFields() {
		out.add(f)
	}
	for _, f := range n.st.facetby {
		out.add(f)
	}
	for _, f := range strItems(n.st.sort.Get("field")) {
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
	o.Set("stackField", jsval.Str(st.stackField))
	o.Set("groupby", st.groupby)
	o.Set("offset", jsval.Str(st.offset))
	o.Set("sort", st.sort)
	o.Set("facetby", strsVal(st.facetby))
	if st.stackby != nil {
		o.Set("stackby", strsVal(st.stackby))
	}
	if st.impute {
		o.Set("impute", jsval.True)
	}
	o.Set("as", strsVal(st.as))
	return "Stack " + hashOf(jsval.Obj(o))
}

func makeStackFromTransform(parent dfNode, t Value) *stackNode {
	stack, groupby, as := t.Get("stack"), t.Get("groupby"), t.Get("as")
	offset := "zero"
	if o := t.Get("offset"); !o.IsUndefined() {
		offset = o.AsString()
	}
	var sortFields, sortOrder []Value
	for _, sf := range t.Get("sort").Items() {
		sortFields = append(sortFields, sf.Get("field"))
		sortOrder = append(sortOrder, firstDefined(sf.Get("order"), jsval.Str("ascending")))
	}
	sort := mkv("field", jsval.Arr(sortFields), "order", jsval.Arr(sortOrder))
	var normalizedAs []string
	switch {
	case as.IsArr() && as.Len() > 1 && func() bool {
		for _, x := range as.Items() {
			if !x.IsStr() {
				return false
			}
		}
		return true
	}():
		normalizedAs = strItems(as)
	case as.IsStr():
		normalizedAs = []string{as.StrValue(), as.StrValue() + "_end"}
	default:
		normalizedAs = []string{stack.AsString() + "_start", stack.AsString() + "_end"}
	}
	return newStackNode(parent, &stackParams{
		stackField: stack.AsString(), groupby: groupby, offset: offset, sort: sort, as: normalizedAs,
	})
}

func makeStackFromEncoding(parent dfNode, m *unitModel) dfNode {
	sp := m.stack
	if sp == nil {
		return nil
	}
	var dims []Value
	for _, gc := range sp.groupbyChannels {
		if d := getFieldDef(m.encoding.Get(gc)); d.IsTruthy() {
			dims = append(dims, d)
		}
	}
	var stackby []string
	for _, by := range sp.stackBy {
		if f := vgField(by.fieldDef, fieldRefOption{}); f != "" {
			stackby = append(stackby, f)
		}
	}
	orderDef := m.encoding.Get("order")
	var sort Value
	if orderDef.IsArr() || isFieldDef(orderDef) {
		f, o := sortParams(orderDef, fieldRefOption{})
		sort = mkv("field", jsval.Arr(f), "order", jsval.Arr(o))
	} else {
		var sortOrder Value
		switch {
		case isOrderOnlyDef(orderDef):
			sortOrder = orderDef.Get("sort")
		case sp.fieldChannel == chY:
			sortOrder = jsval.Str("descending")
		default:
			sortOrder = jsval.Str("ascending")
		}
		var fields, orders []Value
		seen := map[string]bool{}
		for _, f := range stackby {
			if v5 || !seen[f] {
				seen[f] = true
				fields = append(fields, jsval.Str(f))
				orders = append(orders, sortOrder)
			}
		}
		sort = mkv("field", jsval.Arr(fields), "order", jsval.Arr(orders))
	}
	return newStackNode(parent, &stackParams{
		dimensionFieldDefs: dims, stackField: m.vgField(sp.fieldChannel, fieldRefOption{}),
		stackby: stackby, sort: sort, offset: sp.offset, impute: sp.impute,
		groupby: undef,
		as: []string{
			m.vgField(sp.fieldChannel, fieldRefOption{suffix: "start", forAs: true}),
			m.vgField(sp.fieldChannel, fieldRefOption{suffix: "end", forAs: true}),
		},
	})
}

func (n *stackNode) assemble() []Value {
	var out []Value
	st := n.st
	if st.impute {
		for _, d := range st.dimensionFieldDefs {
			bandPosition := 0.5
			if bp := d.Get("bandPosition"); bp.IsNum() {
				bandPosition = bp.NumValue()
			}
			if d.Get("bin").IsTruthy() {
				binStart := vgField(d, fieldRefOption{expr: "datum"})
				binEnd := vgField(d, fieldRefOption{expr: "datum", binSuffix: "end"})
				bpS := jsval.JSNumberString(bandPosition)
				expr := isValidFiniteNumberExpr(binStart) + " ? " + bpS + "*" + binStart + "+" + jsval.JSNumberString(1-bandPosition) + "*" + binEnd + " : " + binStart
				if v5 {
					expr = bpS + "*" + binStart + "+" + jsval.JSNumberString(1-bandPosition) + "*" + binEnd
				}
				out = append(out, mkv("type", "formula",
					"expr", expr,
					"as", vgField(d, fieldRefOption{binSuffix: "mid", forAs: true})))
			}
			gb := append(append([]string{}, st.stackby...), st.facetby...)
			out = append(out, mkv("type", "impute", "field", st.stackField, "groupby", strsVal(gb),
				"key", vgField(d, fieldRefOption{binSuffix: "mid"}), "method", "value", "value", 0))
		}
	}
	gb := append(n.getGroupbyFields(), st.facetby...)
	out = append(out, mkv("type", "stack", "groupby", strsVal(gb), "field", st.stackField, "sort", st.sort, "as", strsVal(st.as), "offset", st.offset))
	return out
}

// ---- geojson / geopoint ----

type geoJSONNode struct {
	dfBase
	fields  Value // array or undefined
	geojson string
	signal  string
}

func newGeoJSONNode(parent dfNode, fields Value, geojson, signal string) *geoJSONNode {
	return initNode(&geoJSONNode{fields: fields, geojson: geojson, signal: signal}, parent)
}

func (n *geoJSONNode) clone() dfNode {
	return initNode(&geoJSONNode{fields: deepClone(n.fields), geojson: n.geojson, signal: n.signal}, nil)
}
func (n *geoJSONNode) dependentFields() *sset {
	out := newSset()
	if n.geojson != "" {
		out.add(n.geojson)
	}
	for _, f := range n.fields.Items() {
		if f.IsStr() {
			out.add(f.StrValue())
		}
	}
	return out
}
func (n *geoJSONNode) producedFields() *sset { return newSset() }
func (n *geoJSONNode) hash() string {
	return "GeoJSON " + n.geojson + " " + n.signal + " " + hashOf(n.fields)
}
func (n *geoJSONNode) assemble() []Value {
	var out []Value
	if n.geojson != "" {
		out = append(out, mkv("type", "filter", "expr", `isValid(datum["`+n.geojson+`"])`))
	}
	o := mk("type", "geojson")
	if n.fields.IsTruthy() {
		o.Set("fields", n.fields)
	}
	if n.geojson != "" {
		o.Set("geojson", jsval.Str(n.geojson))
	}
	o.Set("signal", jsval.Str(n.signal))
	return append(out, jsval.Obj(o))
}

func geoPair(m *unitModel, coords [2]string) Value {
	pair := make([]Value, 2)
	for i, ch := range coords {
		def := getFieldOrDatumDef(m.encoding.Get(ch))
		switch {
		case isFieldDef(def):
			pair[i] = def.Get("field")
		case isDatumDef(def):
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
			parent = newGeoJSONNode(parent, pair, "", m.getName("geojson_"+jsval.JSNumberString(float64(counter))))
			counter++
		}
	}
	if m.channelHasField(chShape) {
		fd := m.typedFieldDef(chShape)
		if channelDefType(fd) == "geojson" {
			parent = newGeoJSONNode(parent, undef, fd.Get("field").AsString(), m.getName("geojson_"+jsval.JSNumberString(float64(counter))))
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
	return initNode(&geoPointNode{projection: n.projection, fields: deepClone(n.fields), as: append([]string(nil), n.as...)}, nil)
}
func (n *geoPointNode) dependentFields() *sset {
	out := newSset()
	for _, f := range n.fields.Items() {
		if f.IsStr() {
			out.add(f.StrValue())
		}
	}
	return out
}
func (n *geoPointNode) producedFields() *sset { return newSset(n.as...) }
func (n *geoPointNode) hash() string {
	return "Geopoint " + n.projection + " " + hashOf(n.fields) + " " + hashOf(strsVal(n.as))
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
	data       string
	channels   map[string]*facetChannelInfo
	childModel Model
}

func newFacetNode(parent dfNode, m *facetModel, name, data string) *facetNode {
	n := &facetNode{model: m, name: name, data: data, channels: map[string]*facetChannelInfo{}}
	initNode(n, parent)
	for _, channel := range facetChannels {
		fd := m.facet.Lookup(channel)
		if fd.IsTruthy() {
			bin, sort := fd.Get("bin"), fd.Get("sort")
			info := &facetChannelInfo{name: m.getName(channel + "_domain"), fields: []string{vgField(fd, fieldRefOption{})}}
			if isBinning(bin) {
				info.fields = append(info.fields, vgField(fd, fieldRefOption{binSuffix: "end"}))
			}
			if isSortField(sort) {
				info.sortField = sort
			} else if sort.IsArr() {
				info.sortIndexField = sortArrayIndexField(fd, channel, fieldRefOption{})
			}
			n.channels[channel] = info
		}
	}
	n.childModel = m.child
	return n
}

func (n *facetNode) clone() dfNode     { throw("Cannot clone node"); return nil }
func (n *facetNode) getSource() string { return n.name }

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
	return hashOf(jsval.Obj(o))
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
			if hasDiscreteDomain(typ) && isVgRangeStep(rng) {
				domain := assembleDomain(n.childModel, channel)
				if field := getFieldFromDomain(domain); field != "" {
					out[channel] = field
				}
			}
		}
	}
	return out
}

func (n *facetNode) assembleRowColumnHeaderData(channel, crossedDataName string, indep map[string]string) Value {
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
		op := coalesce(info.sortField.Get("op"), jsval.Str(defaultSortOp))
		fields = append(fields, info.sortField.Get("field"))
		ops = append(ops, op)
		as = append(as, jsval.Str(vgField(info.sortField, fieldRefOption{forAs: true})))
	} else if info.sortIndexField != "" {
		fields = append(fields, jsval.Str(info.sortIndexField))
		ops = append(ops, jsval.Str("max"))
		as = append(as, jsval.Str(info.sortIndexField))
	}
	source := n.data
	if crossedDataName != "" {
		source = crossedDataName
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
