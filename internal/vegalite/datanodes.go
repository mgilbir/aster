package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Data flow node types — vega-lite/src/compile/data/*.ts.

// fieldDefModel is the part of unit and facet models that data nodes use.
type fieldDefModel interface {
	Model
	forEachFieldDef(f func(fd Value, channel string))
	fieldDefOf(channel string) Value
	channelHasFieldOf(channel string) bool
}

func (u *unitModel) fieldDefOf(channel string) Value  { return u.fieldDef(channel) }
func (u *unitModel) channelHasFieldOf(c string) bool  { return u.channelHasField(c) }
func (f *facetModel) fieldDefOf(channel string) Value { return f.fieldDef(channel) }
func (f *facetModel) channelHasFieldOf(c string) bool { return f.channelHasField(c) }

// reduceFieldDef folds over every field def in the mapping.
func reduceFieldDef[T any](m fieldDefModel, f func(acc T, fd Value, channel string) T, init T) T {
	acc := init
	m.forEachFieldDef(func(fd Value, channel string) { acc = f(acc, fd, channel) })
	return acc
}

// ---- ancestor parse ----

// ancestorParse tracks which fields ancestors already parse. Upstream's
// AncestorParse.clone() returns a plain Split with parseNothing assigned, so
// cloning a clone does not carry parseNothing on (plain marks such a clone).
type ancestorParse struct {
	*split
	parseNothing bool
	plain        bool
}

func newAncestorParse() *ancestorParse { return &ancestorParse{split: newSplit()} }

func (a *ancestorParse) cloneAP(cc *compileCtx) *ancestorParse {
	c := &ancestorParse{split: a.split.clone(cc), plain: true}
	if !a.plain {
		c.parseNothing = a.parseNothing
	}
	return c
}

// ---- parse node ----

type parseNode struct {
	dfBase
	parse *Object
}

func newParseNode(parent dfNode, parse *Object) *parseNode {
	return initNode(&parseNode{parse: parse}, parent)
}

func (n *parseNode) clone() dfNode {
	return initNode(&parseNode{parse: duplicate(n.cc, jsval.Obj(n.parse)).ObjValue()}, nil)
}
func (n *parseNode) hash() string { return "Parse " + hashOf(n.cc, jsval.Obj(n.parse)) }
func (n *parseNode) producedFields() *sset {
	return newSset(n.parse.Keys()...)
}
func (n *parseNode) dependentFields() *sset { return newSset(n.parse.Keys()...) }

func makeExplicitParse(parent dfNode, m Model, ap *ancestorParse) *parseNode {
	explicit := jsval.NewObject(0)
	data := m.b().data
	if p := data.Get("format").Get("parse"); !isGenerator(data) && p.IsObj() {
		explicit = p.ObjValue()
	} else if !isGenerator(data) && p.IsTruthy() {
		// A parse that is not an object is read through its keys: a
		// string's characters, an array's items.
		explicit = jsval.NewObject(0)
		for _, k := range jsKeys(p) {
			cc := ccOf(parent)
			cc.work(1)
			explicit.Set(k, jsProp(p, k))
		}
	}
	return parseNodeWithAncestors(parent, explicit, jsval.NewObject(0), ap)
}

// parseNodeWithAncestors builds a parse node for the fields not already parsed
// by an ancestor; it returns nil when nothing is left to parse.
func parseNodeWithAncestors(parent dfNode, explicit, implicit *Object, ap *ancestorParse) *parseNode {
	cc := ccOf(parent)
	for _, field := range append([]string(nil), implicit.Keys()...) {
		cc.work(1)
		parsedAs := ap.getWithExplicit(field)
		if !parsedAs.value.IsUndefined() {
			iv := implicit.Lookup(field)
			if parsedAs.explicit || jsval.Equal(parsedAs.value, iv) || (parsedAs.value.IsStr() && parsedAs.value.StrValue() == "derived") ||
				(iv.IsStr() && iv.StrValue() == "flatten") {
				implicit.Delete(field)
			} else {
				cc.warn(msgDifferentParse(field, iv, parsedAs.value))
			}
		}
	}
	for _, field := range append([]string(nil), explicit.Keys()...) {
		cc.work(1)
		parsedAs := ap.get(field)
		if !parsedAs.IsUndefined() {
			if jsval.Equal(parsedAs, explicit.Lookup(field)) {
				explicit.Delete(field)
			} else {
				cc.warn(msgDifferentParse(field, explicit.Lookup(field), parsedAs))
			}
		}
	}
	parse := newSplitOf(explicit, implicit)
	ap.copyAll(parse)
	p := jsval.NewObject(4)
	comb := parse.combine()
	for _, key := range comb.Keys() {
		cc.work(1)
		if val := parse.get(key); !val.IsNull() {
			jsSet(ccOf(parent), p, key, val)
		}
	}
	if p.Len() == 0 || ap.parseNothing {
		return nil
	}
	return newParseNode(parent, p)
}

func (n *parseNode) merge(other *parseNode) {
	n.parse = merged(jsval.Obj(n.parse), jsval.Obj(other.parse))
	other.remove()
}

func (n *parseNode) assembleFormatParse() *Object {
	cc := n.cc
	out := jsval.NewObject(4)
	for _, field := range n.parse.Keys() {
		cc.work(1)
		if accessPathDepth(field) == 1 {
			out.Set(field, n.parse.Lookup(field))
		}
	}
	return out
}

func unquote(pattern string) string {
	// pattern.slice(1, -1): a lone quote both starts and ends the pattern,
	// and leaves nothing.
	if len(pattern) >= 1 && ((pattern[0] == '\'' && pattern[len(pattern)-1] == '\'') || (pattern[0] == '"' && pattern[len(pattern)-1] == '"')) {
		if len(pattern) == 1 {
			return ""
		}
		return pattern[1 : len(pattern)-1]
	}
	return pattern
}

func parseExpressionFor(field, parse string) string {
	f := accessPathWithDatum(field, "datum")
	switch {
	case parse == "number":
		return "toNumber(" + f + ")"
	case parse == "boolean":
		return "toBoolean(" + f + ")"
	case parse == "string":
		return "toString(" + f + ")"
	case parse == "date":
		return "toDate(" + f + ")"
	case parse == "flatten":
		return f
	case strings.HasPrefix(parse, "date:"):
		return "timeParse(" + f + ",'" + unquote(parse[5:]) + "')"
	case strings.HasPrefix(parse, "utc:"):
		return "utcParse(" + f + ",'" + unquote(parse[4:]) + "')"
	}
	return ""
}

func (n *parseNode) assembleTransforms(onlyNested bool) []Value {
	cc := n.cc
	var out []Value
	for _, field := range n.parse.Keys() {
		cc.work(1)
		if onlyNested && accessPathDepth(field) <= 1 {
			continue
		}
		pv := n.parse.Lookup(field)
		if !pv.IsStr() {
			// parse.startsWith, past the comparisons with the known names.
			throw("parse.startsWith is not a function")
		}
		expr := parseExpressionFor(field, pv.StrValue())
		if expr == "" {
			cc.warn(msgUnrecognizedParse(pv.StrValue()))
			continue
		}
		out = append(out, mkv("type", "formula", "expr", expr, "as", removePathFromField(field)))
	}
	return out
}

// ---- identifier ----

type identifierNode struct{ dfBase }

func newIdentifierNode(parent dfNode) *identifierNode { return initNode(&identifierNode{}, parent) }
func (n *identifierNode) clone() dfNode               { return initNode(&identifierNode{}, nil) }
func (n *identifierNode) dependentFields() *sset      { return newSset() }
func (n *identifierNode) producedFields() *sset       { return newSset(selectionID) }
func (n *identifierNode) hash() string                { return "Identifier" }

// ---- graticule / sequence generators ----

type graticuleNode struct {
	dfBase
	params Value
}

func newGraticuleNode(parent dfNode, params Value) *graticuleNode {
	return initNode(&graticuleNode{params: params}, parent)
}
func (n *graticuleNode) clone() dfNode          { return initNode(&graticuleNode{params: n.params}, nil) }
func (n *graticuleNode) dependentFields() *sset { return newSset() }
func (n *graticuleNode) producedFields() *sset  { return nil }
func (n *graticuleNode) hash() string           { return "Graticule " + hashOf(n.cc, n.params) }
func (n *graticuleNode) assemble() Value {
	o := mk("type", "graticule")
	if !(n.params.IsBool() && n.params.BoolValue()) {
		spread(o, n.params)
	}
	return jsval.Obj(o)
}

type sequenceNode struct {
	dfBase
	params Value
}

func newSequenceNode(parent dfNode, params Value) *sequenceNode {
	return initNode(&sequenceNode{params: params}, parent)
}
func (n *sequenceNode) clone() dfNode          { return initNode(&sequenceNode{params: n.params}, nil) }
func (n *sequenceNode) dependentFields() *sset { return newSset() }
func (n *sequenceNode) producedFields() *sset {
	if as := n.params.Get("as"); as.IsTruthy() {
		return newSset(as.AsString())
	}
	return newSset("data")
}
func (n *sequenceNode) hash() string { return "Hash " + hashOf(n.cc, n.params) }
func (n *sequenceNode) assemble() Value {
	o := mk("type", "sequence")
	spread(o, n.params)
	return jsval.Obj(o)
}

func isDataSourceNode(n dfNode) bool {
	switch n.(type) {
	case *sourceNode, *graticuleNode, *sequenceNode:
		return true
	}
	return false
}

// ---- calculate ----

type calculateNode struct {
	dfBase
	transform Value
	dep       *sset
}

func newCalculateNode(parent dfNode, transform Value) *calculateNode {
	return initNode(&calculateNode{transform: transform, dep: getDependentFields(ccOf(parent), exprSource(transform.Get("calculate")))}, parent)
}
func (n *calculateNode) clone() dfNode {
	return initNode(&calculateNode{transform: duplicate(n.cc, n.transform), dep: getDependentFields(n.cc, n.transform.Get("calculate").AsString())}, nil)
}

// exprSource is an expression upstream hands to vega-expression's parser,
// which reads it with source.length and source.charCodeAt: anything but a
// string is a TypeError.
func exprSource(v Value) string {
	switch {
	case v.IsStr():
		return v.StrValue()
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'length')", nullishName(v))
	}
	throw("source.charCodeAt is not a function")
	return ""
}

func (n *calculateNode) producedFields() *sset {
	s := newSset()
	s.addValue(n.transform.Get("as"))
	return s
}
func (n *calculateNode) dependentFields() *sset { return n.dep }
func (n *calculateNode) hash() string           { return "Calculate " + hashOf(n.cc, n.transform) }
func (n *calculateNode) assemble() Value {
	return mkv("type", "formula", "expr", n.transform.Get("calculate"), "as", n.transform.Get("as"))
}

func sortArrayIndexField(cc *compileCtx, fd Value, channel string, opt fieldRefOption) string {
	opt.prefix = channel
	opt.suffix = "sort_index"
	return vgField(cc, fd, opt)
}

func parseAllCalculateForSortIndex(parent dfNode, m fieldDefModel) dfNode {
	cc := m.b().ctx

	m.forEachFieldDef(func(fd Value, channel string) {
		if !isScaleFieldDef(fd) {
			return
		}
		sort := fd.Get("sort")
		if isSortArray(sort) {
			var sb strings.Builder
			for i, sv := range sort.Items() {
				cc.work(1)
				pred := mkv("field", fd.Get("field"), "timeUnit", fd.Get("timeUnit"), "equal", sv)
				sb.WriteString(fieldFilterExpression(cc, pred, true))
				sb.WriteString(" ? " + jsval.JSNumberString(float64(i)) + " : ")
			}
			sb.WriteString(jsval.JSNumberString(float64(sort.Len())))
			parent = newCalculateNode(parent, mkv("calculate", sb.String(), "as", sortArrayIndexField(cc, fd, channel, fieldRefOption{forAs: true})))
		}
	})
	return parent
}

// ---- filter ----

type filterNode struct {
	dfBase
	model  Model
	filter Value
	expr   string
	dep    *sset
}

func newFilterNode(parent dfNode, m Model, filter Value) *filterNode {
	n := &filterNode{model: m, filter: filter}
	initNode(n, parent)
	n.expr = expression(m.b().ctx, m, filter, n)
	n.dep = getDependentFields(m.b().ctx, n.expr)
	return n
}
func (n *filterNode) clone() dfNode          { return newFilterNode(nil, n.model, duplicate(n.cc, n.filter)) }
func (n *filterNode) dependentFields() *sset { return n.dep }
func (n *filterNode) producedFields() *sset  { return newSset() }
func (n *filterNode) hash() string           { return "Filter " + n.expr }
func (n *filterNode) assemble() Value        { return mkv("type", "filter", "expr", n.expr) }

// expression renders a filter (a predicate composition) as a Vega expression.
func expression(cc *compileCtx, m Model, filterOp Value, node dfNode) string {
	return logicalExpr(cc, filterOp, func(pred Value) string {
		switch {
		case pred.IsStr():
			return pred.StrValue()
		case isSelectionPredicateOf(cc, pred):
			return parseSelectionPredicate(m, pred, node, "datum")
		}
		return fieldFilterExpression(cc, pred, true)
	}, 0)
}

// ---- filter invalid ----

type filterInvalidNode struct {
	dfBase
	filter *omap[Value]
}

func newFilterInvalidNode(parent dfNode, filter *omap[Value]) *filterInvalidNode {
	return initNode(&filterInvalidNode{filter: filter}, parent)
}
func (n *filterInvalidNode) clone() dfNode {
	cc := n.cc
	c := newOmap[Value]()
	for _, k := range n.filter.keysView() {
		cc.work(1)
		c.set(k, n.filter.m[k])
	}
	return initNode(&filterInvalidNode{filter: c}, nil)
}
func (n *filterInvalidNode) dependentFields() *sset { return newSset(n.filter.keysView()...) }
func (n *filterInvalidNode) producedFields() *sset  { return newSset() }
func (n *filterInvalidNode) hash() string {
	cc := n.cc
	o := jsval.NewObject(4)
	for _, k := range n.filter.keysView() {
		cc.work(1)
		o.Set(k, n.filter.m[k])
	}
	return "FilterInvalid " + hashOf(n.cc, jsval.Obj(o))
}

func makeFilterInvalid(parent dfNode, m *unitModel, marks, scales string) dfNode {
	cc := m.b().ctx

	if marks == "include-invalid-values" && scales == "include-invalid-values" {
		return nil
	}
	filter := reduceFieldDef(fieldDefModel(m), func(agg *omap[Value], fd Value, channel string) *omap[Value] {
		if isScaleChannel(cc, channel) {
			if sc := m.getScaleComponent(channel); sc != nil {
				mode := getScaleInvalidDataMode(m.markDef, m.config, channel, sc.get("type").AsString(), isCountingAggregateOp(fd.Get("aggregate")))
				if mode != "show" && mode != "always-valid" {
					agg.assign(fd.Get("field").AsString(), fd)
				}
			}
		}
		return agg
	}, newOmap[Value]())
	if filter.len() == 0 {
		return nil
	}
	return newFilterInvalidNode(parent, filter)
}

// makeFilterInvalid58 is Vega-Lite 5.8's FilterInvalidNode.make: fields of
// continuous scales are filtered when the mark's `invalid` is "filter".
func makeFilterInvalid58(parent dfNode, m *unitModel) dfNode {
	cc := m.b().ctx

	if invalid := getMarkPropOrConfigSimple("invalid", m.markDef, m.config); !(invalid.IsStr() && invalid.StrValue() == "filter") {
		return nil
	}
	filter := reduceFieldDef(fieldDefModel(m), func(agg *omap[Value], fd Value, channel string) *omap[Value] {
		if isScaleChannel(cc, channel) {
			if sc := m.getScaleComponent(channel); sc != nil {
				agg2 := fd.Get("aggregate")
				if hasContinuousDomain(sc.get("type").AsString()) && !(agg2.IsStr() && agg2.StrValue() == "count") && !isPathMarkName(m.mark()) {
					agg.assign(fd.Get("field").AsString(), fd)
				}
			}
		}
		return agg
	}, newOmap[Value]())
	if filter.len() == 0 {
		return nil
	}
	return newFilterInvalidNode(parent, filter)
}

func isValidFiniteNumberExpr(ref string) string {
	return "isValid(" + ref + ") && isFinite(+" + ref + ")"
}

func (n *filterInvalidNode) assemble() Value {
	cc := n.cc

	var filters []string
	for _, field := range n.filter.keysView() {
		cc.work(1)
		fd := n.filter.m[field]
		ref := vgField(cc, fd, fieldRefOption{expr: "datum"})
		switch channelDefType(fd) {
		case "temporal":
			filters = append(filters, "(isDate("+ref+") || (isValid("+ref+") && isFinite(+"+ref+")))")
		case "quantitative":
			if cc.v5 {
				filters = append(filters, "isValid("+ref+")", "isFinite(+"+ref+")")
			} else {
				filters = append(filters, isValidFiniteNumberExpr(ref))
			}
		}
	}
	if len(filters) > 0 {
		return mkv("type", "filter", "expr", strings.Join(filters, " && "))
	}
	return jsval.Null
}
