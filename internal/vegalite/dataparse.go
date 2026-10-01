package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Building the data flow graph for a model — vega-lite/src/compile/data/parse.ts.

// sourceList is the list of data sources shared by every model of one compile
// (upstream shares a single array).
type sourceList struct{ items []dfNode }

type dataComponent struct {
	sources             *sourceList
	outputNodes         map[string]sourceGetter
	outputNodeRefCounts map[string]int
	isFaceted           bool

	raw, main, preFilterInvalid, postFilterInvalid *outputNode
	facetRoot                                      *facetNode
	ancestorParse                                  *ancestorParse
}

func findSource(data Value, sources []dfNode) *sourceNode {
	for _, other := range sources {
		os, ok := other.(*sourceNode)
		if !ok {
			continue
		}
		if data.Get("name").IsTruthy() && os.hasName() && data.Get("name").AsString() != os.name {
			continue
		}
		formatMesh := data.Get("format").Get("mesh")
		if os.data == nil {
			// A data object that is none of inline, url, sphere or named
			// (`{}`) leaves the source without data.
			throw("Cannot read properties of undefined (reading 'format')")
		}
		otherData := jsval.Obj(os.data)
		otherFeature := otherData.Get("format").Get("feature")
		if formatMesh.IsTruthy() && otherFeature.IsTruthy() {
			continue
		}
		formatFeature := data.Get("format").Get("feature")
		if (formatFeature.IsTruthy() || otherFeature.IsTruthy()) && !strictEq(formatFeature, otherFeature) {
			continue
		}
		otherMesh := otherData.Get("format").Get("mesh")
		if (formatMesh.IsTruthy() || otherMesh.IsTruthy()) && !strictEq(formatMesh, otherMesh) {
			continue
		}
		switch {
		case isInlineData(data) && isInlineData(otherData):
			if deepEqual(data.Get("values"), otherData.Get("values")) {
				return os
			}
		case isUrlData(data) && isUrlData(otherData):
			if strictEq(data.Get("url"), otherData.Get("url")) {
				return os
			}
		case isNamedData(data):
			if strictEq(data.Get("name"), jsval.Str(os.name)) {
				return os
			}
		}
	}
	return nil
}

// strictEq is JavaScript's === for the values in a data spec (NaN is not equal to itself).
func strictEq(a, b Value) bool {
	if a.IsObj() || a.IsArr() {
		return jsval.SameRef(a, b)
	}
	if a.IsNum() && b.IsNum() {
		return a.NumValue() == b.NumValue()
	}
	return jsval.Equal(a, b)
}

func parseRoot(m Model, sources *sourceList) dfNode {
	cc := m.b().ctx

	b := m.b()
	// `model.data || !model.parent`: a data of null, 0 or "" on a child is no
	// data of its own, and is not the null that gives the root an empty source.
	if b.data.IsTruthy() || b.parent == nil {
		if b.data.IsNull() {
			s := newSourceNode(cc, mkv("values", jsval.Arr(nil)))
			sources.items = append(sources.items, s)
			return s
		}
		if existing := findSource(b.data, sources.items); existing != nil {
			if !isGenerator(b.data) {
				existing.data.Set("format", jsval.Obj(mergeDeepFormat(b.data.Get("format"), existing.data.Lookup("format"))))
			}
			if !existing.hasName() && b.data.Get("name").IsTruthy() {
				existing.name = b.data.Get("name").AsString()
			}
			return existing
		}
		s := newSourceNode(cc, b.data)
		sources.items = append(sources.items, s)
		return s
	}
	pd := b.parent.b().comp.data
	if pd.facetRoot != nil {
		return pd.facetRoot
	}
	return pd.main
}

// mergeDeepFormat is mergeDeep({}, a, b): b wins, objects merge recursively.
func mergeDeepFormat(a, b Value) *Object {
	dest := jsval.NewObject(4)
	for _, src := range []Value{a, b} {
		mergeDeepInto(dest, src)
	}
	return dest
}

func mergeDeepInto(dest *Object, src Value) {
	if !src.IsObj() {
		return
	}
	so := src.ObjValue()
	for i := 0; i < so.Len(); i++ {
		writeConfig(dest, so.KeyAt(i), so.ValueAt(i), true, nil)
	}
}

func parseTransformArray(head dfNode, m Model, ap *ancestorParse) dfNode {
	cc := m.b().ctx

	if cc != nil {
		if cc.transforms += len(m.b().transforms); cc.transforms > maxTransforms {
			exceeded("too many data transforms (limit %d)", maxTransforms)
		}
	}
	lookupCounter := 0
	for _, t := range m.b().transforms {
		cc.check()
		derivedType := ""
		var transformNode dfNode
		switch {
		case hasProperty(t, "calculate"):
			n := newCalculateNode(head, t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "filter"):
			implicit := getImplicitFromFilterTransform(t)
			pn := parseNodeWithAncestors(head, jsval.NewObject(0), implicit, ap)
			if pn != nil {
				head = pn
			}
			head = newFilterNode(head, m, t.Get("filter"))
		case hasProperty(t, "bin"):
			n := makeBinFromTransform(head, t, m)
			head, transformNode, derivedType = n, n, "number"
		case hasProperty(t, "timeUnit"):
			derivedType = "date"
			field := t.Get("field").AsString()
			if ap.getWithExplicit(field).value.IsUndefined() {
				head = newParseNode(head, mk(field, derivedType))
				ap.set(field, jsval.Str(derivedType), false)
			}
			n := makeTimeUnitFromTransform(cc, head, t)
			head, transformNode = n, n
		case hasProperty(t, "aggregate"):
			n := makeAggregateFromTransform(cc, head, t)
			if n != nil {
				head, transformNode = n, n
			} else {
				head = nil
			}
			derivedType = "number"
			if requiresSelectionID(m) {
				head = newIdentifierNode(head)
			}
		case hasProperty(t, "lookup"):
			n := makeLookupNode(head, m, t, lookupCounter)
			lookupCounter++
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "window"):
			n := newXform(head, "window", t)
			head, transformNode, derivedType = n, n, "number"
		case hasProperty(t, "joinaggregate"):
			n := newXform(head, "joinaggregate", t)
			head, transformNode, derivedType = n, n, "number"
		case hasProperty(t, "stack"):
			n := makeStackFromTransform(head, t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "fold"):
			n := newFoldNode(head, t)
			head, transformNode, derivedType = n, n, "derived"
		case !cc.v5 && hasProperty(t, "extent") && !hasProperty(t, "density") && !hasProperty(t, "regression"):
			n := newXform(head, "extent", deepClone(t))
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "flatten"):
			n := newFlattenNode(head, t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "pivot"):
			n := newXform(head, "pivot", t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "sample"):
			head = newXform(head, "sample", t)
		case hasProperty(t, "impute"):
			n := newXform(head, "impute", t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "density"):
			n := newDensityNode(cc, head, t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "quantile"):
			n := newQuantileNode(head, t)
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "regression"):
			n := newRegressionNode(head, t, "regression", "regression")
			head, transformNode, derivedType = n, n, "derived"
		case hasProperty(t, "loess"):
			n := newRegressionNode(head, t, "loess", "loess")
			head, transformNode, derivedType = n, n, "derived"
		default:
			continue
		}
		if transformNode != nil && derivedType != "" {
			for _, field := range transformNode.producedFields().list() {
				ap.set(field, jsval.Str(derivedType), false)
			}
		}
	}
	return head
}

func getImplicitFromFilterTransform(t Value) *Object {
	implicit := jsval.NewObject(2)
	forEachLeaf(t.Get("filter"), func(filter Value) {
		if !isFieldPredicate(filter) {
			return
		}
		val := jsval.Null
		switch {
		case isFieldEqualPredicate(filter):
			val = signalRefOrValue(filter.Get("equal"))
		case isFieldLTEPredicate(filter):
			val = signalRefOrValue(filter.Get("lte"))
		case isFieldLTPredicate(filter):
			val = signalRefOrValue(filter.Get("lt"))
		case isFieldGTPredicate(filter):
			val = signalRefOrValue(filter.Get("gt"))
		case isFieldGTEPredicate(filter):
			val = signalRefOrValue(filter.Get("gte"))
		case isFieldRangePredicate(filter):
			val = filter.Get("range").Index(0)
		case isFieldOneOfPredicate(filter):
			oneOf := filter.Get("oneOf")
			if !oneOf.IsTruthy() {
				oneOf = filter.Get("in")
			}
			val = oneOf.Index(0)
		}
		field := filter.Get("field").AsString()
		if val.IsTruthy() {
			switch {
			case isDateTime(val):
				jsSet(implicit, field, jsval.Str("date"))
			case val.IsNum():
				jsSet(implicit, field, jsval.Str("number"))
			case val.IsStr():
				jsSet(implicit, field, jsval.Str("string"))
			}
		}
		if filter.Get("timeUnit").IsTruthy() {
			jsSet(implicit, field, jsval.Str("date"))
		}
	}, 0)
	return implicit
}

func getImplicitFromEncoding(m Model) *Object {
	cc := m.b().ctx

	implicit := jsval.NewObject(4)
	add := func(fd Value) {
		field := fd.Get("field").AsString()
		switch {
		case isFieldOrDatumDefForTimeFormat(cc, fd):
			jsSet(implicit, field, jsval.Str("date"))
		case channelDefType(fd) == "quantitative" && isMinMaxOp(fd.Get("aggregate")):
			jsSet(implicit, field, jsval.Str("number"))
		case accessPathDepth(field) > 1:
			if !implicit.Has(field) {
				jsSet(implicit, field, jsval.Str("flatten"))
			}
		case isScaleFieldDef(fd) && isSortField(cc, fd.Get("sort")) && accessPathDepth(fd.Get("sort").Get("field").AsString()) > 1:
			sf := fd.Get("sort").Get("field").AsString()
			if !implicit.Has(sf) {
				jsSet(implicit, sf, jsval.Str("flatten"))
			}
		}
	}
	if fm, ok := m.(fieldDefModel); ok {
		fm.forEachFieldDef(func(fd Value, channel string) {
			if isTypedFieldDef(fd) {
				add(fd)
			} else {
				mainChannel := getMainRangeChannel(channel)
				mainFieldDef := fm.fieldDefOf(mainChannel)
				// `mainFieldDef.type` of a secondary channel (x2) whose main
				// channel (x) is not encoded.
				switch {
				case mainFieldDef.IsUndefined():
					throw("Cannot read properties of undefined (reading 'type')")
				case mainFieldDef.IsNull():
					throw("Cannot read properties of null (reading 'type')")
				}
				o := cloneObj(fd.ObjValue())
				o.Set("type", mainFieldDef.Get("type"))
				add(jsval.Obj(o))
			}
		})
	}
	if u := asUnit(m); u != nil {
		if isPathMarkName(u.mark()) && !u.encoding.Get("order").IsTruthy() {
			dim := chX
			if u.markDef.Get("orient").AsString() == "horizontal" {
				dim = chY
			}
			dd := u.encoding.Get(dim)
			if isFieldDef(cc, dd) && channelDefType(dd) == "quantitative" && !implicit.Has(dd.Get("field").AsString()) {
				jsSet(implicit, dd.Get("field").AsString(), jsval.Str("number"))
			}
		}
	}
	return implicit
}

func getImplicitFromSelection(m Model) *Object {
	implicit := jsval.NewObject(2)
	if u := asUnit(m); u != nil && u.comp.selection != nil {
		for _, name := range u.comp.selection.keyList() {
			sel := u.comp.selection.lookup(name)
			for _, proj := range sel.project.items {
				if proj.channel == "" && accessPathDepth(proj.field) > 1 {
					jsSet(implicit, proj.field, jsval.Str("flatten"))
				}
			}
		}
	}
	return implicit
}

func makeOutputNode(t dataSourceType, m Model, head dfNode) *outputNode {
	d := m.b().comp.data
	name := m.b().getDataName(t)
	node := newOutputNode(head, name, t, d.outputNodeRefCounts)
	d.outputNodes[name] = node
	return node
}

// parseDataFor builds the graph for model m and returns its data component.
func parseDataFor(m Model) *dataComponent {
	cc := m.b().ctx

	b := m.b()
	dc := b.comp.data
	head := parseRoot(m, dc.sources)
	data := b.data
	newData := data.IsTruthy() && (isGenerator(data) || isUrlData(data) || isInlineData(data))
	var ap *ancestorParse
	if !newData && b.parent != nil {
		ap = b.parent.b().comp.data.ancestorParse.cloneAP()
	} else {
		ap = newAncestorParse()
	}
	if isGenerator(data) {
		switch {
		case isSequenceGenerator(data):
			head = newSequenceNode(head, data.Get("sequence"))
		case isGraticuleGenerator(data):
			head = newGraticuleNode(head, data.Get("graticule"))
		}
		ap.parseNothing = true
	} else if data.Get("format").IsObj() && data.Get("format").ObjValue().Has("parse") && data.Get("format").Get("parse").IsNull() {
		ap.parseNothing = true
	}
	if pn := makeExplicitParse(head, m, ap); pn != nil {
		head = pn
	}
	head = newIdentifierNode(head)
	parentIsLayer := b.parent != nil && isLayerModel(b.parent)
	u := asUnit(m)
	fm, isFM := m.(fieldDefModel)
	if isFM {
		if parentIsLayer {
			if n := makeBinFromEncoding(head, fm); n != nil {
				head = n
			}
		}
	}
	if len(b.transforms) > 0 {
		head = parseTransformArray(head, m, ap)
	}
	implicit := merged(jsval.Obj(getImplicitFromSelection(m)), jsval.Obj(getImplicitFromEncoding(m)))
	if pn := parseNodeWithAncestors(head, jsval.NewObject(0), implicit, ap); pn != nil {
		head = pn
	}
	if u != nil {
		head = parseAllGeoJSON(head, u)
		head = parseAllGeoPoint(head, u)
	}
	if isFM {
		if !parentIsLayer {
			if n := makeBinFromEncoding(head, fm); n != nil {
				head = n
			}
		}
		if n := makeTimeUnitFromEncoding(head, fm); n != nil {
			head = n
		}
		head = parseAllCalculateForSortIndex(head, fm)
	}
	raw := makeOutputNode(dsRaw, m, head)
	head = raw
	if u != nil {
		if agg := makeAggregateFromEncoding(head, u); agg != nil {
			head = agg
			if requiresSelectionID(m) {
				head = newIdentifierNode(head)
			}
		}
		if n := makeImputeFromEncoding(head, u); n != nil {
			head = n
		}
		if n := makeStackFromEncoding(head, u); n != nil {
			head = n
		}
	}
	var preFilterInvalid *outputNode
	var postFilterInvalid *outputNode
	var marksMode, scalesMode string
	if u != nil && cc.v5 {
		// Vega-Lite 5.8: only `invalid: filter` filters, before the main source.
		if n := makeFilterInvalid58(head, u); n != nil {
			head = n
		}
	} else if u != nil {
		invalid := getMarkPropOrConfigSimple("invalid", u.markDef, u.config)
		marksMode, scalesMode = getDataSourcesForHandlingInvalidValues(invalid, isPathMarkName(u.mark()))
		if marksMode != scalesMode && scalesMode == "include-invalid-values" {
			preFilterInvalid = makeOutputNode(dsPreFilterInvalid, m, head)
			head = preFilterInvalid
		}
		if marksMode == "exclude-invalid-values" {
			if n := makeFilterInvalid(head, u, marksMode, scalesMode); n != nil {
				head = n
			}
		}
	}
	main := makeOutputNode(dsMain, m, head)
	head = main
	if u != nil && marksMode != "" {
		if marksMode == "include-invalid-values" && scalesMode == "exclude-invalid-values" {
			if n := makeFilterInvalid(head, u, marksMode, scalesMode); n != nil {
				head = n
			}
			postFilterInvalid = makeOutputNode(dsPostFilterInvalid, m, head)
			head = postFilterInvalid
		}
	}
	if u != nil {
		materializeSelections(u, main)
	}
	var facetRoot *facetNode
	if f, ok := m.(*facetModel); ok {
		facetName := f.getName("facet")
		if j := makeJoinAggregateFromFacet(cc, head, f.facet); j != nil {
			head = j
		}
		facetRoot = newFacetNode(head, f, facetName, main.getSource())
		dc.outputNodes[facetName] = facetRoot
	}
	out := *dc
	out.raw, out.main, out.facetRoot, out.ancestorParse = raw, main, facetRoot, ap
	out.preFilterInvalid, out.postFilterInvalid = preFilterInvalid, postFilterInvalid
	return &out
}

func makeJoinAggregateFromFacet(cc *compileCtx, parent dfNode, facet *Object) dfNode {
	row, column := facet.Lookup("row"), facet.Lookup("column")
	if row.IsTruthy() && column.IsTruthy() {
		var newParent dfNode
		for _, fd := range []Value{row, column} {
			if isSortField(cc, fd.Get("sort")) {
				sort := fd.Get("sort")
				field := sort.Get("field")
				op := coalesce(sort.Get("op"), jsval.Str(defaultSortOp))
				n := newXform(parent, "joinaggregate", mkv(
					"joinaggregate", arr(mkv("op", op, "field", field, "as", facetSortFieldName(cc, fd, sort, fieldRefOption{forAs: true}))),
					"groupby", arr(vgField(cc, fd, fieldRefOption{})),
				))
				parent, newParent = n, n
			}
		}
		return newParent
	}
	return nil
}

// ---- invalid data modes ----

func normalizeInvalidDataMode(mode Value, isPath bool) string {
	switch {
	case mode.IsUndefined() || (mode.IsStr() && mode.StrValue() == "break-paths-show-path-domains"):
		if isPath {
			return "break-paths-show-domains"
		}
		return "filter"
	case mode.IsNull():
		return "show"
	}
	return mode.AsString()
}

func getDataSourcesForHandlingInvalidValues(invalid Value, isPath bool) (marks, scales string) {
	switch normalizeInvalidDataMode(invalid, isPath) {
	case "filter":
		return "exclude-invalid-values", "exclude-invalid-values"
	case "break-paths-show-domains":
		if isPath {
			marks = "include-invalid-values"
		} else {
			marks = "exclude-invalid-values"
		}
		return marks, "include-invalid-values"
	case "break-paths-filter-domains":
		if isPath {
			marks = "include-invalid-values"
		} else {
			marks = "exclude-invalid-values"
		}
		return marks, "exclude-invalid-values"
	case "show":
		return "include-invalid-values", "include-invalid-values"
	}
	return "", ""
}

func getScaleDataSourceForHandlingInvalidValues(invalid Value, isPath bool) dataSourceType {
	marks, scales := getDataSourcesForHandlingInvalidValues(invalid, isPath)
	if marks == scales {
		return dsMain
	}
	if scales == "include-invalid-values" {
		return dsPreFilterInvalid
	}
	return dsPostFilterInvalid
}

func getScaleInvalidDataMode(markDef, config Value, scaleChannel, scaleType string, isCountAggregate bool) string {
	if scaleType == "" || !hasContinuousDomain(scaleType) || isCountAggregate {
		return "always-valid"
	}
	invalidMode := normalizeInvalidDataMode(getMarkPropOrConfigSimple("invalid", markDef, config), isPathMarkName(markDef.Get("type").AsString()))
	if !config.Get("scale").Get("invalid").Get(scaleChannel).IsUndefined() {
		return "show"
	}
	return invalidMode
}

func shouldBreakPath(mode string) bool {
	return mode == "break-paths-filter-domains" || mode == "break-paths-show-domains"
}
