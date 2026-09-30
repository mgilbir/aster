package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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

func (a *ancestorParse) cloneAP() *ancestorParse {
	c := &ancestorParse{split: a.split.clone(), plain: true}
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
	return initNode(&parseNode{parse: deepClone(jsval.Obj(n.parse)).ObjValue()}, nil)
}
func (n *parseNode) hash() string { return "Parse " + hashOf(jsval.Obj(n.parse)) }
func (n *parseNode) producedFields() *sset {
	return newSset(n.parse.Keys()...)
}
func (n *parseNode) dependentFields() *sset { return newSset(n.parse.Keys()...) }

func makeExplicitParse(parent dfNode, m Model, ap *ancestorParse) *parseNode {
	explicit := jsval.NewObject(0)
	data := m.b().data
	if !isGenerator(data) && data.Get("format").Get("parse").IsObj() {
		explicit = data.Get("format").Get("parse").ObjValue()
	}
	return parseNodeWithAncestors(parent, explicit, jsval.NewObject(0), ap)
}

// parseNodeWithAncestors builds a parse node for the fields not already parsed
// by an ancestor; it returns nil when nothing is left to parse.
func parseNodeWithAncestors(parent dfNode, explicit, implicit *Object, ap *ancestorParse) *parseNode {
	for _, field := range append([]string(nil), implicit.Keys()...) {
		parsedAs := ap.getWithExplicit(field)
		if !parsedAs.value.IsUndefined() {
			iv := implicit.Lookup(field)
			if parsedAs.explicit || jsval.Equal(parsedAs.value, iv) || (parsedAs.value.IsStr() && parsedAs.value.StrValue() == "derived") ||
				(iv.IsStr() && iv.StrValue() == "flatten") {
				implicit.Delete(field)
			}
		}
	}
	for _, field := range append([]string(nil), explicit.Keys()...) {
		parsedAs := ap.get(field)
		if !parsedAs.IsUndefined() {
			if jsval.Equal(parsedAs, explicit.Lookup(field)) {
				explicit.Delete(field)
			}
		}
	}
	parse := &split{explicit, implicit}
	ap.copyAll(parse)
	p := jsval.NewObject(4)
	comb := parse.combine()
	for _, key := range comb.Keys() {
		if val := parse.get(key); !val.IsNull() {
			jsSet(p, key, val)
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
	out := jsval.NewObject(4)
	for _, field := range n.parse.Keys() {
		if accessPathDepth(field) == 1 {
			out.Set(field, n.parse.Lookup(field))
		}
	}
	return out
}

func unquote(pattern string) string {
	if len(pattern) >= 2 && ((pattern[0] == '\'' && pattern[len(pattern)-1] == '\'') || (pattern[0] == '"' && pattern[len(pattern)-1] == '"')) {
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
	var out []Value
	for _, field := range n.parse.Keys() {
		if onlyNested && accessPathDepth(field) <= 1 {
			continue
		}
		expr := parseExpressionFor(field, n.parse.Lookup(field).AsString())
		if expr == "" {
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
func (n *graticuleNode) hash() string           { return "Graticule " + hashOf(n.params) }
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
func (n *sequenceNode) hash() string { return "Hash " + hashOf(n.params) }
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
	return initNode(&calculateNode{transform: transform, dep: getDependentFields(transform.Get("calculate").AsString())}, parent)
}
func (n *calculateNode) clone() dfNode {
	return initNode(&calculateNode{transform: deepClone(n.transform), dep: getDependentFields(n.transform.Get("calculate").AsString())}, nil)
}
func (n *calculateNode) producedFields() *sset  { return newSset(n.transform.Get("as").AsString()) }
func (n *calculateNode) dependentFields() *sset { return n.dep }
func (n *calculateNode) hash() string           { return "Calculate " + hashOf(n.transform) }
func (n *calculateNode) assemble() Value {
	return mkv("type", "formula", "expr", n.transform.Get("calculate"), "as", n.transform.Get("as"))
}

func sortArrayIndexField(fd Value, channel string, opt fieldRefOption) string {
	opt.prefix = channel
	opt.suffix = "sort_index"
	return vgField(fd, opt)
}

func parseAllCalculateForSortIndex(parent dfNode, m fieldDefModel) dfNode {
	m.forEachFieldDef(func(fd Value, channel string) {
		if !isScaleFieldDef(fd) {
			return
		}
		sort := fd.Get("sort")
		if isSortArray(sort) {
			var sb strings.Builder
			for i, sv := range sort.Items() {
				pred := mkv("field", fd.Get("field"), "timeUnit", fd.Get("timeUnit"), "equal", sv)
				sb.WriteString(fieldFilterExpression(pred, true))
				sb.WriteString(" ? " + jsval.JSNumberString(float64(i)) + " : ")
			}
			sb.WriteString(jsval.JSNumberString(float64(sort.Len())))
			parent = newCalculateNode(parent, mkv("calculate", sb.String(), "as", sortArrayIndexField(fd, channel, fieldRefOption{forAs: true})))
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
	n.expr = expression(m, filter, n)
	n.dep = getDependentFields(n.expr)
	return n
}
func (n *filterNode) clone() dfNode          { return newFilterNode(nil, n.model, deepClone(n.filter)) }
func (n *filterNode) dependentFields() *sset { return n.dep }
func (n *filterNode) producedFields() *sset  { return newSset() }
func (n *filterNode) hash() string           { return "Filter " + n.expr }
func (n *filterNode) assemble() Value        { return mkv("type", "filter", "expr", n.expr) }

// expression renders a filter (a predicate composition) as a Vega expression.
func expression(m Model, filterOp Value, node dfNode) string {
	return logicalExpr(filterOp, func(pred Value) string {
		switch {
		case pred.IsStr():
			return pred.StrValue()
		case isSelectionPredicate(pred):
			return parseSelectionPredicate(m, pred, node, "datum")
		}
		return fieldFilterExpression(pred, true)
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
	c := newOmap[Value]()
	for _, k := range n.filter.keys {
		c.set(k, n.filter.m[k])
	}
	return initNode(&filterInvalidNode{filter: c}, nil)
}
func (n *filterInvalidNode) dependentFields() *sset { return newSset(n.filter.keys...) }
func (n *filterInvalidNode) producedFields() *sset  { return newSset() }
func (n *filterInvalidNode) hash() string {
	o := jsval.NewObject(4)
	for _, k := range n.filter.keys {
		o.Set(k, n.filter.m[k])
	}
	return "FilterInvalid " + hashOf(jsval.Obj(o))
}

func makeFilterInvalid(parent dfNode, m *unitModel, marks, scales string) dfNode {
	if marks == "include-invalid-values" && scales == "include-invalid-values" {
		return nil
	}
	filter := reduceFieldDef(fieldDefModel(m), func(agg *omap[Value], fd Value, channel string) *omap[Value] {
		if isScaleChannel(channel) {
			if sc := m.getScaleComponent(channel); sc != nil {
				mode := getScaleInvalidDataMode(m.markDef, m.config, channel, sc.get("type").AsString(), isCountingAggregateOp(fd.Get("aggregate")))
				if mode != "show" && mode != "always-valid" {
					agg.set(fd.Get("field").AsString(), fd)
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
	if invalid := getMarkPropOrConfigSimple("invalid", m.markDef, m.config); !(invalid.IsStr() && invalid.StrValue() == "filter") {
		return nil
	}
	filter := reduceFieldDef(fieldDefModel(m), func(agg *omap[Value], fd Value, channel string) *omap[Value] {
		if isScaleChannel(channel) {
			if sc := m.getScaleComponent(channel); sc != nil {
				agg2 := fd.Get("aggregate")
				if hasContinuousDomain(sc.get("type").AsString()) && !(agg2.IsStr() && agg2.StrValue() == "count") && !isPathMarkName(m.mark()) {
					agg.set(fd.Get("field").AsString(), fd)
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
	var filters []string
	for _, field := range n.filter.keys {
		fd := n.filter.m[field]
		ref := vgField(fd, fieldRefOption{expr: "datum"})
		switch channelDefType(fd) {
		case "temporal":
			filters = append(filters, "(isDate("+ref+") || (isValid("+ref+") && isFinite(+"+ref+")))")
		case "quantitative":
			if v5 {
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
