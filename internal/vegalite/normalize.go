package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Normalization: vega-lite/src/normalize/*. A specification is rewritten into
// unit, layer, facet and concat specs only: composite marks expand into
// layers, row/column encodings into facets, repeats into concats, and the
// legacy `selection` property into params.

type selectionState struct {
	selections []Value
}

// normParams is upstream's NormalizerParams. It is passed by pointer and
// copied (shallowly) wherever upstream spreads it.
type normParams struct {
	cc               *compileCtx
	config           Value
	parentEncoding   Value
	parentProjection Value
	repeater         Value
	repeaterPrefix   string
	path             []string

	emptySelections     map[string]bool
	selectionPredicates map[string][]*Object
	sel                 *selectionState
	depth               int
}

func (p *normParams) clone() *normParams {
	c := *p
	return &c
}

func (p *normParams) deeper() *normParams {
	c := *p
	c.depth++
	if c.depth > maxDepth {
		panic(compileError{errDepth.Error()})
	}
	return &c
}

// mapperImpl is the virtual interface of upstream's SpecMapper.
type mapperImpl interface {
	mapTop(spec Value, p *normParams) Value
	mapFacet(spec Value, p *normParams) Value
	mapRepeat(spec Value, p *normParams) Value
	mapHConcat(spec Value, p *normParams) Value
	mapVConcat(spec Value, p *normParams) Value
	mapConcat(spec Value, p *normParams) Value
	mapLayerOrUnit(spec Value, p *normParams) Value
	mapLayer(spec Value, p *normParams) Value
	mapUnit(spec Value, p *normParams) Value
}

// baseMapper holds SpecMapper's default behaviour; self dispatches virtually.
type baseMapper struct{ self mapperImpl }

func (b *baseMapper) mapTop(spec Value, p *normParams) Value {
	p = p.deeper()
	switch {
	case isFacetSpec(spec):
		return b.self.mapFacet(spec, p)
	case isRepeatSpec(spec):
		return b.self.mapRepeat(spec, p)
	case isHConcatSpec(spec):
		return b.self.mapHConcat(spec, p)
	case isVConcatSpec(spec):
		return b.self.mapVConcat(spec, p)
	case isConcatSpec(spec):
		return b.self.mapConcat(spec, p)
	}
	return b.self.mapLayerOrUnit(spec, p)
}

func (b *baseMapper) mapLayerOrUnit(spec Value, p *normParams) Value {
	switch {
	case isLayerSpec(spec):
		return b.self.mapLayer(spec, p)
	case isUnitSpec(spec):
		return b.self.mapUnit(spec, p)
	}
	throw("Invalid spec: %s", stringify(spec))
	return undef
}

func (b *baseMapper) mapLayer(spec Value, p *normParams) Value {
	o := cloneObj(spec.ObjValue())
	o.Set("layer", mapVals(spec.Get("layer"), func(s Value) Value { return b.self.mapLayerOrUnit(s, p.deeper()) }))
	return jsval.Obj(o)
}

func (b *baseMapper) mapHConcat(spec Value, p *normParams) Value {
	o := cloneObj(spec.ObjValue())
	o.Set("hconcat", mapVals(spec.Get("hconcat"), func(s Value) Value { return b.self.mapTop(s, p) }))
	return jsval.Obj(o)
}

func (b *baseMapper) mapVConcat(spec Value, p *normParams) Value {
	o := cloneObj(spec.ObjValue())
	o.Set("vconcat", mapVals(spec.Get("vconcat"), func(s Value) Value { return b.self.mapTop(s, p) }))
	return jsval.Obj(o)
}

func (b *baseMapper) mapConcat(spec Value, p *normParams) Value {
	rest := omit(spec, "concat")
	rest.Set("concat", mapVals(spec.Get("concat"), func(s Value) Value { return b.self.mapTop(s, p) }))
	return jsval.Obj(rest)
}

func (b *baseMapper) mapFacet(spec Value, p *normParams) Value {
	o := cloneObj(spec.ObjValue())
	o.Set("spec", b.self.mapTop(spec.Get("spec"), p))
	return jsval.Obj(o)
}

func (b *baseMapper) mapRepeat(spec Value, p *normParams) Value {
	o := cloneObj(spec.ObjValue())
	o.Set("spec", b.self.mapTop(spec.Get("spec"), p))
	return jsval.Obj(o)
}

// ---- entry point ----

// normalize returns the normalized spec (with an autosize object when it is not Vega's default).
func normalize(cc *compileCtx, spec Value, config Value) Value {
	p := &normParams{cc: cc, config: config, sel: &selectionState{}}
	compat := newSelectionCompatNormalizer()
	core := newCoreNormalizer(cc)
	top := newTopLevelSelectionsNormalizer()
	s := compat.mapTop(spec, p)
	s = core.mapTop(s, p)
	s = top.mapTop(s, p)
	autosize := normalizeAutoSize(s, mkv("width", spec.Get("width"), "height", spec.Get("height"), "autosize", spec.Get("autosize")), config)
	o := cloneObj(s.ObjValue())
	if autosize.IsTruthy() {
		o.Set("autosize", autosize)
	}
	return jsval.Obj(o)
}

func normalizeAutoSizeValue(v Value) Value {
	if v.IsStr() {
		return mkv("type", v)
	}
	return coalesce(v, mkv())
}

func normalizeAutoSize(spec Value, sizeInfo Value, config Value) Value {
	width, height := sizeInfo.Get("width"), sizeInfo.Get("height")
	isFitCompatible := isUnitSpec(spec) || isLayerSpec(spec)
	autosizeDefault := jsval.NewObject(2)
	isContainer := func(v Value) bool { return v.IsStr() && v.StrValue() == "container" }
	// A container width/height is discarded for specs that cannot fit (only a warning upstream).
	if isFitCompatible {
		switch {
		case isContainer(width) && isContainer(height):
			autosizeDefault.Set("type", jsval.Str("fit"))
			autosizeDefault.Set("contains", jsval.Str("padding"))
		case isContainer(width):
			autosizeDefault.Set("type", jsval.Str("fit-x"))
			autosizeDefault.Set("contains", jsval.Str("padding"))
		case isContainer(height):
			autosizeDefault.Set("type", jsval.Str("fit-y"))
			autosizeDefault.Set("contains", jsval.Str("padding"))
		}
	}
	autosize := mk("type", "pad")
	spread(autosize, jsval.Obj(autosizeDefault))
	if config.IsTruthy() {
		spread(autosize, normalizeAutoSizeValue(config.Get("autosize")))
	}
	spread(autosize, normalizeAutoSizeValue(spec.Get("autosize")))
	if autosize.Lookup("type").IsStr() && autosize.Lookup("type").StrValue() == "fit" && !isFitCompatible {
		autosize.Set("type", jsval.Str("pad"))
	}
	if deepEqual(jsval.Obj(autosize), mkv("type", "pad")) {
		return undef
	}
	return jsval.Obj(autosize)
}

// ---- selection compatibility (legacy `selection` -> params) ----

type selectionCompatNormalizer struct{ baseMapper }

func newSelectionCompatNormalizer() *selectionCompatNormalizer {
	n := &selectionCompatNormalizer{}
	n.self = n
	return n
}

func (n *selectionCompatNormalizer) mapTop(spec Value, p *normParams) Value {
	if p.emptySelections == nil {
		p.emptySelections = map[string]bool{}
	}
	if p.selectionPredicates == nil {
		p.selectionPredicates = map[string][]*Object{}
	}
	spec = normalizeTransforms(spec, p)
	return n.baseMapper.mapTop(spec, p)
}

func (n *selectionCompatNormalizer) mapLayerOrUnit(spec Value, p *normParams) Value {
	spec = normalizeTransforms(spec, p)
	if enc := spec.Get("encoding"); enc.IsTruthy() {
		out := jsval.NewObject(enc.Len())
		for _, ch := range keysOf(enc) {
			out.Set(ch, normalizeChannelDefCompat(enc.Get(ch), p))
		}
		o := cloneObj(spec.ObjValue())
		o.Set("encoding", jsval.Obj(out))
		spec = jsval.Obj(o)
	}
	return n.baseMapper.mapLayerOrUnit(spec, p)
}

func (n *selectionCompatNormalizer) mapUnit(spec Value, p *normParams) Value {
	selection := spec.Get("selection")
	if !selection.IsTruthy() {
		return spec
	}
	rest := omit(spec, "selection")
	var params []Value
	for _, name := range keysOf(selection) {
		selDef := selection.Get(name)
		value, bind, empty := selDef.Get("init"), selDef.Get("bind"), selDef.Get("empty")
		sel := omit(selDef, "init", "bind", "empty")
		if t := sel.Lookup("type"); t.IsStr() {
			switch t.StrValue() {
			case "single":
				sel.Set("type", jsval.Str("point"))
				sel.Set("toggle", jsval.False)
			case "multi":
				sel.Set("type", jsval.Str("point"))
			}
		}
		emptyOK := !(empty.IsStr() && empty.StrValue() == "none")
		p.emptySelections[name] = emptyOK
		for _, pred := range p.selectionPredicates[name] {
			pred.Set("empty", jsval.Bool(emptyOK))
		}
		params = append(params, mkv("name", name, "value", value, "select", jsval.Obj(sel), "bind", bind))
	}
	rest.Set("params", jsval.Arr(params))
	return jsval.Obj(rest)
}

func normalizeTransforms(spec Value, p *normParams) Value {
	tx := spec.Get("transform")
	if !tx.IsTruthy() {
		return spec
	}
	rest := omit(spec, "transform")
	rest.Set("transform", mapVals(tx, func(t Value) Value {
		switch {
		case hasProperty(t, "filter"):
			return mkv("filter", normalizePredicateCompat(t, p))
		case hasProperty(t, "bin") && isObject(t.Get("bin")):
			o := cloneObj(t.ObjValue())
			o.Set("bin", normalizeBinExtent(t.Get("bin")))
			return jsval.Obj(o)
		case hasProperty(t, "lookup"):
			from := t.Get("from")
			if param := from.Get("selection"); param.IsTruthy() {
				o := cloneObj(t.ObjValue())
				newFrom := mk("param", param)
				spread(newFrom, jsval.Obj(omit(from, "selection")))
				o.Set("from", jsval.Obj(newFrom))
				return jsval.Obj(o)
			}
		}
		return t
	}))
	return jsval.Obj(rest)
}

func normalizeChannelDefCompat(obj Value, p *normParams) Value {
	cc := p.cc

	if !obj.IsObj() {
		return obj
	}
	enc := deepClone(obj)
	eo := enc.ObjValue()
	if isFieldDef(cc, enc) && isObject(enc.Get("bin")) {
		eo.Set("bin", normalizeBinExtent(enc.Get("bin")))
	}
	if isScaleFieldDef(enc) && enc.Get("scale").Get("domain").Get("selection").IsTruthy() {
		domain := enc.Get("scale").Get("domain")
		param := domain.Get("selection")
		rest := omit(domain, "selection")
		d := cloneObj(rest)
		if param.IsTruthy() {
			d.Set("param", param)
		}
		enc.Get("scale").ObjValue().Set("domain", jsval.Obj(d))
	}
	if isConditionalDef(enc) {
		cond := enc.Get("condition")
		if cond.IsArr() {
			eo.Set("condition", mapVals(cond, func(c Value) Value {
				if c.Get("param").IsTruthy() {
					return c
				}
				o := omit(c, "selection", "param", "test")
				o.Set("test", normalizePredicateCompat(c, p))
				return jsval.Obj(o)
			}))
		} else {
			nc := normalizeChannelDefCompat(cond, p)
			if nc.Get("param").IsTruthy() {
				eo.Set("condition", cond)
			} else {
				o := omit(nc, "selection", "param", "test")
				o.Set("test", normalizePredicateCompat(cond, p))
				eo.Set("condition", jsval.Obj(o))
			}
		}
	}
	return enc
}

func normalizeBinExtent(bin Value) Value {
	ext := bin.Get("extent")
	if ext.Get("selection").IsTruthy() {
		param := ext.Get("selection")
		e := omit(ext, "selection")
		e.Set("param", param)
		o := cloneObj(bin.ObjValue())
		o.Set("extent", jsval.Obj(e))
		return jsval.Obj(o)
	}
	return bin
}

func normalizePredicateCompat(op Value, p *normParams) Value {
	normSel := func(o Value) Value {
		return normalizeLogicalComposition(o, func(param Value) Value {
			name := param.AsString()
			empty, ok := p.emptySelections[name]
			if !ok {
				empty = true
			}
			pred := mk("param", param, "empty", empty)
			p.selectionPredicates[name] = append(p.selectionPredicates[name], pred)
			return jsval.Obj(pred)
		}, 0)
	}
	if op.Get("selection").IsTruthy() {
		return normSel(op.Get("selection"))
	}
	src := op.Get("test")
	if !src.IsTruthy() {
		src = op.Get("filter")
	}
	return normalizeLogicalComposition(src, func(o Value) Value {
		if o.Get("selection").IsTruthy() {
			return normSel(o.Get("selection"))
		}
		return o
	}, 0)
}

// ---- top-level selections ----

type topLevelSelectionsNormalizer struct{ baseMapper }

func newTopLevelSelectionsNormalizer() *topLevelSelectionsNormalizer {
	n := &topLevelSelectionsNormalizer{}
	n.self = n
	return n
}

func addSpecNameToParams(spec Value, p *normParams) *normParams {
	if spec.Get("name").IsTruthy() {
		c := p.clone()
		c.path = append(append([]string(nil), p.path...), spec.Get("name").AsString())
		return c
	}
	return p
}

func (n *topLevelSelectionsNormalizer) mapTop(spec Value, p *normParams) Value {
	if params := spec.Get("params"); params.IsTruthy() && !isUnitSpec(spec) {
		var rest []Value
		for _, param := range params.Items() {
			if isSelectionParameter(param) {
				p.sel.selections = append(p.sel.selections, param)
			} else {
				rest = append(rest, param)
			}
		}
		spec.ObjValue().Set("params", jsval.Arr(rest))
	}
	return n.baseMapper.mapTop(spec, p)
}

func (n *topLevelSelectionsNormalizer) mapUnit(spec Value, p *normParams) Value {
	selections := p.sel.selections
	if len(selections) == 0 {
		return spec
	}
	path := append(append([]string(nil), p.path...), spec.Get("name").AsString())
	var params []Value
	specName := spec.Get("name")
	for _, sel := range selections {
		views := sel.Get("views")
		if !views.IsTruthy() || views.Len() == 0 {
			params = append(params, sel)
			continue
		}
		for _, view := range views.Items() {
			switch {
			case view.IsStr():
				if (specName.IsStr() && view.StrValue() == specName.StrValue()) || contains(path, view.StrValue()) {
					params = append(params, sel)
				}
			case view.IsArr():
				prev := -1
				ok := true
				for i, v := range view.Items() {
					idx := indexOfStr(path, v.AsString())
					if idx == -1 || (i > 0 && idx <= prev) {
						ok = false
						break
					}
					prev = idx
				}
				if ok {
					params = append(params, sel)
				}
			}
		}
	}
	if len(params) > 0 {
		spec.ObjValue().Set("params", jsval.Arr(params))
	}
	return spec
}

func indexOfStr(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func (n *topLevelSelectionsNormalizer) mapFacet(spec Value, p *normParams) Value {
	return n.baseMapper.mapFacet(spec, addSpecNameToParams(spec, p))
}
func (n *topLevelSelectionsNormalizer) mapRepeat(spec Value, p *normParams) Value {
	return n.baseMapper.mapRepeat(spec, addSpecNameToParams(spec, p))
}
func (n *topLevelSelectionsNormalizer) mapHConcat(spec Value, p *normParams) Value {
	return n.baseMapper.mapHConcat(spec, addSpecNameToParams(spec, p))
}
func (n *topLevelSelectionsNormalizer) mapVConcat(spec Value, p *normParams) Value {
	return n.baseMapper.mapVConcat(spec, addSpecNameToParams(spec, p))
}
func (n *topLevelSelectionsNormalizer) mapLayer(spec Value, p *normParams) Value {
	return n.baseMapper.mapLayer(spec, addSpecNameToParams(spec, p))
}

// ---- core normalizer ----

type nonFacetUnitNormalizer interface {
	hasMatchingType(spec Value, p *normParams) bool
	run(spec Value, p *normParams, normalize func(Value, *normParams) Value) Value
}

type coreNormalizer struct {
	baseMapper
	unitNormalizers []nonFacetUnitNormalizer
}

func newCoreNormalizer(cc *compileCtx) *coreNormalizer {
	n := &coreNormalizer{}
	n.self = n
	n.unitNormalizers = []nonFacetUnitNormalizer{
		compositeNormalizer{"boxplot", normalizeBoxPlot},
		compositeNormalizer{"errorbar", normalizeErrorBar},
		compositeNormalizer{"errorband", normalizeErrorBand},
		pathOverlayNormalizer{},
		ruleForRangedLineNormalizer{},
	}
	return n
}

func (n *coreNormalizer) mapTop(spec Value, p *normParams) Value {
	cc := p.cc

	if isUnitSpec(spec) {
		enc := spec.Get("encoding")
		if channelHasField(cc, enc, chRow) || channelHasField(cc, enc, chColumn) || channelHasField(cc, enc, chFacet) {
			return n.mapFacetedUnit(spec, p.deeper())
		}
	}
	return n.baseMapper.mapTop(spec, p)
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "_")
}

func (n *coreNormalizer) mapUnit(spec Value, p *normParams) Value {
	cc := p.cc

	encoding := replaceRepeaterInEncoding(cc, spec.Get("encoding"), p.repeater)
	swre := cloneObj(spec.ObjValue())
	if spec.Get("name").IsTruthy() {
		swre.Set("name", jsval.Str(joinNonEmpty(p.repeaterPrefix, spec.Get("name").AsString())))
	}
	if encoding.IsTruthy() {
		swre.Set("encoding", encoding)
	}
	sv := jsval.Obj(swre)
	if p.parentEncoding.IsTruthy() || p.parentProjection.IsTruthy() {
		return n.mapUnitWithParentEncodingOrProjection(sv, p)
	}
	for _, un := range n.unitNormalizers {
		if un.hasMatchingType(sv, p) {
			return un.run(sv, p, func(s Value, pp *normParams) Value { return n.mapLayerOrUnit(s, pp) })
		}
	}
	return sv
}

func (n *coreNormalizer) mapRepeat(spec Value, p *normParams) Value {
	if isLayerRepeatSpec(spec) {
		return n.mapLayerRepeat(spec, p)
	}
	return n.mapNonLayerRepeat(spec, p)
}

func (n *coreNormalizer) mapLayerRepeat(spec Value, p *normParams) Value {
	repeat, childSpec := spec.Get("repeat"), spec.Get("spec")
	rest := omit(spec, "repeat", "spec")
	row, column, layer := repeat.Get("row"), repeat.Get("column"), repeat.Get("layer")
	repeater := p.repeater
	if !repeater.IsTruthy() {
		repeater = mkv()
	}
	if row.IsTruthy() || column.IsTruthy() {
		newRepeat := jsval.NewObject(2)
		if row.IsTruthy() {
			newRepeat.Set("row", row)
		}
		if column.IsTruthy() {
			newRepeat.Set("column", column)
		}
		ns := cloneObj(spec.ObjValue())
		ns.Set("repeat", jsval.Obj(newRepeat))
		ns.Set("spec", mkv("repeat", mkv("layer", layer), "spec", childSpec))
		return n.mapRepeat(jsval.Obj(ns), p)
	}
	items := layer.Items()
	out := make([]Value, len(items))
	for i, layerValue := range items {
		childRepeater := cloneObj(repeater.ObjValue())
		childRepeater.Set("layer", layerValue)
		prefix := ""
		if childSpec.Get("name").IsTruthy() {
			prefix = childSpec.Get("name").AsString() + "_"
		}
		childName := prefix + p.repeaterPrefix + "child__layer_" + varName(layerValue.AsString())
		cp := p.deeper()
		cp.repeater = jsval.Obj(childRepeater)
		cp.repeaterPrefix = childName
		child := n.mapLayerOrUnit(childSpec, cp)
		child.ObjValue().Set("name", jsval.Str(childName))
		out[i] = child
	}
	ro := cloneObj(rest)
	ro.Set("layer", jsval.Arr(out))
	return jsval.Obj(ro)
}

func (n *coreNormalizer) mapNonLayerRepeat(spec Value, p *normParams) Value {
	repeat, childSpec, data := spec.Get("repeat"), spec.Get("spec"), spec.Get("data")
	remaining := omit(spec, "repeat", "spec", "data")
	if !repeat.IsArr() && spec.Get("columns").IsTruthy() {
		spec = jsval.Obj(omit(spec, "columns"))
		remaining = omit(spec, "repeat", "spec", "data")
	}
	repeater := p.repeater
	if !repeater.IsTruthy() {
		repeater = mkv()
	}
	// row/column/repeat value lists: the specified list, or a single (possibly undefined) inherited value.
	single := func(v Value) []Value { return []Value{v} }
	var rows, cols, repeatValues []Value
	if !repeat.IsArr() && repeat.Get("row").IsTruthy() {
		rows = repeat.Get("row").Items()
	} else {
		rows = single(repeater.Get("row"))
	}
	if !repeat.IsArr() && repeat.Get("column").IsTruthy() {
		cols = repeat.Get("column").Items()
	} else {
		cols = single(repeater.Get("column"))
	}
	if repeat.IsArr() && repeat.IsTruthy() {
		repeatValues = repeat.Items()
	} else {
		repeatValues = single(repeater.Get("repeat"))
	}
	if len(repeatValues)*len(rows)*len(cols) > maxRepeatChildren {
		throw("repeat would create %d views (limit %d)", len(repeatValues)*len(rows)*len(cols), maxRepeatChildren)
	}
	var concat []Value
	for _, repeatValue := range repeatValues {
		for _, rowValue := range rows {
			for _, columnValue := range cols {
				childRepeater := mk("repeat", repeatValue, "row", rowValue, "column", columnValue, "layer", repeater.Get("layer"))
				pre := ""
				if childSpec.Get("name").IsTruthy() {
					pre = childSpec.Get("name").AsString() + "_"
				}
				var suffix string
				if repeat.IsArr() {
					suffix = varName(repeatValue.AsString())
				} else {
					if repeat.Get("row").IsTruthy() {
						suffix += "row_" + varName(rowValue.AsString())
					}
					if repeat.Get("column").IsTruthy() {
						suffix += "column_" + varName(columnValue.AsString())
					}
				}
				childName := pre + p.repeaterPrefix + "child__" + suffix
				cp := p.deeper()
				cp.repeater = jsval.Obj(childRepeater)
				cp.repeaterPrefix = childName
				child := n.mapTop(childSpec, cp)
				child.ObjValue().Set("name", jsval.Str(childName))
				concat = append(concat, jsval.Obj(omit(child, "data")))
			}
		}
	}
	var columns Value
	switch {
	case repeat.IsArr():
		columns = spec.Get("columns")
	case repeat.Get("column").IsTruthy():
		columns = jsval.Int(repeat.Get("column").Len())
	default:
		columns = jsval.Int(1)
	}
	out := jsval.NewObject(8)
	if childSpec.Get("data").IsNullish() {
		out.Set("data", data)
	} else {
		out.Set("data", childSpec.Get("data"))
	}
	out.Set("align", jsval.Str("all"))
	spread(out, jsval.Obj(remaining))
	out.Set("columns", columns)
	out.Set("concat", jsval.Arr(concat))
	return jsval.Obj(out)
}

func (n *coreNormalizer) mapFacet(spec Value, p *normParams) Value {
	facet := spec.Get("facet")
	if isFacetMapping(facet) && spec.Get("columns").IsTruthy() {
		spec = jsval.Obj(omit(spec, "columns"))
	}
	return n.baseMapper.mapFacet(spec, p)
}

func (n *coreNormalizer) mapUnitWithParentEncodingOrProjection(spec Value, p *normParams) Value {
	cc := p.cc

	encoding, projection := spec.Get("encoding"), spec.Get("projection")
	mergedProjection := mergeProjection(p.parentProjection, projection)
	mergedEncoding := mergeEncoding(cc, p.parentEncoding, replaceRepeaterInEncoding(cc, encoding, p.repeater), false)
	o := cloneObj(spec.ObjValue())
	if mergedProjection.IsTruthy() {
		o.Set("projection", mergedProjection)
	}
	if mergedEncoding.IsTruthy() {
		o.Set("encoding", mergedEncoding)
	}
	return n.mapUnit(jsval.Obj(o), &normParams{
		cc: p.cc, config: p.config, emptySelections: p.emptySelections, selectionPredicates: p.selectionPredicates,
		sel: p.sel, path: p.path, depth: p.depth,
	})
}

func (n *coreNormalizer) mapFacetedUnit(spec Value, p *normParams) Value {
	cc := p.cc

	enc := spec.Get("encoding")
	row, column, facet := enc.Get("row"), enc.Get("column"), enc.Get("facet")
	encoding := omit(enc, "row", "column", "facet")
	mark, width, projection, height, view, params := spec.Get("mark"), spec.Get("width"), spec.Get("projection"), spec.Get("height"), spec.Get("view"), spec.Get("params")
	outerSpec := omit(spec, "mark", "width", "projection", "height", "view", "params", "encoding")
	facetMapping, layout := n.getFacetMappingAndLayout(row, column, facet, p)
	newEncoding := replaceRepeaterInEncoding(cc, jsval.Obj(encoding), p.repeater)
	inner := jsval.NewObject(8)
	if width.IsTruthy() {
		inner.Set("width", width)
	}
	if height.IsTruthy() {
		inner.Set("height", height)
	}
	if view.IsTruthy() {
		inner.Set("view", view)
	}
	if projection.IsTruthy() {
		inner.Set("projection", projection)
	}
	inner.Set("mark", mark)
	inner.Set("encoding", newEncoding)
	if params.IsTruthy() {
		inner.Set("params", params)
	}
	outer := cloneObj(outerSpec)
	spread(outer, jsval.Obj(layout))
	outer.Set("facet", facetMapping)
	outer.Set("spec", jsval.Obj(inner))
	return n.mapFacet(jsval.Obj(outer), p)
}

func (n *coreNormalizer) getFacetMappingAndLayout(row, column, facet Value, p *normParams) (Value, *Object) {
	cc := p.cc

	if row.IsTruthy() || column.IsTruthy() {
		facetMapping := jsval.NewObject(2)
		layout := jsval.NewObject(3)
		for _, ch := range []string{chRow, chColumn} {
			var def Value
			if ch == chRow {
				def = row
			} else {
				def = column
			}
			if def.IsTruthy() {
				facetMapping.Set(ch, jsval.Obj(omit(def, "align", "center", "spacing", "columns")))
				for _, prop := range []string{"align", "center", "spacing"} {
					if v := def.Get(prop); !v.IsUndefined() {
						l := layout.Lookup(prop)
						if !l.IsObj() {
							l = mkv()
							layout.Set(prop, l)
						}
						l.ObjValue().Set(ch, v)
					}
				}
			}
		}
		return jsval.Obj(facetMapping), layout
	}
	facetMapping := omit(facet, "align", "center", "spacing", "columns")
	layout := jsval.NewObject(4)
	for _, k := range []string{"align", "center", "spacing", "columns"} {
		if v := facet.Get(k); v.IsTruthy() {
			layout.Set(k, v)
		}
	}
	return replaceRepeaterInFacet(cc, jsval.Obj(facetMapping), p.repeater), layout
}

func (n *coreNormalizer) mapLayer(spec Value, p *normParams) Value {
	cc := p.cc

	encoding, projection := spec.Get("encoding"), spec.Get("projection")
	rest := omit(spec, "encoding", "projection")
	cp := p.clone()
	cp.parentEncoding = mergeEncoding(cc, p.parentEncoding, encoding, true)
	cp.parentProjection = mergeProjection(p.parentProjection, projection)
	cp.deeper()
	if spec.Get("name").IsTruthy() {
		rest.Set("name", jsval.Str(joinNonEmpty(cp.repeaterPrefix, spec.Get("name").AsString())))
	}
	return n.baseMapper.mapLayer(jsval.Obj(rest), cp)
}

// mergeEncoding merges a layer's shared encoding into a child's encoding.
func mergeEncoding(cc *compileCtx, parentEncoding, encoding Value, layer bool) Value {
	if !encoding.IsTruthy() && !encoding.IsObj() {
		encoding = mkv()
	}
	var merged *Object
	if parentEncoding.IsTruthy() {
		merged = jsval.NewObject(8)
		seen := map[string]bool{}
		var channels []string
		for _, k := range append(append([]string(nil), keysOf(parentEncoding)...), keysOf(encoding)...) {
			if !seen[k] {
				seen[k] = true
				channels = append(channels, k)
			}
		}
		for _, channel := range channels {
			channelDef := encoding.Get(channel)
			parentChannelDef := parentEncoding.Get(channel)
			switch {
			case isFieldOrDatumDef(cc, channelDef):
				merged.Set(channel, jsval.Obj(merged2(parentChannelDef, channelDef)))
			case hasConditionalFieldOrDatumDef(cc, channelDef):
				o := cloneObj(channelDef.ObjValue())
				o.Set("condition", jsval.Obj(merged2(parentChannelDef, channelDef.Get("condition"))))
				merged.Set(channel, jsval.Obj(o))
			case channelDef.IsTruthy() || channelDef.IsNull():
				merged.Set(channel, channelDef)
			case layer || isValueDef(parentChannelDef) || isSignalRef(parentChannelDef) || isFieldOrDatumDef(cc, parentChannelDef) || parentChannelDef.IsArr():
				merged.Set(channel, parentChannelDef)
			}
		}
	} else if encoding.IsObj() {
		merged = encoding.ObjValue()
	}
	if merged == nil || merged.Len() == 0 {
		return undef
	}
	return jsval.Obj(merged)
}

func merged2(a, b Value) *Object { return spread(spread(jsval.NewObject(4), a), b) }

func mergeProjection(parent, projection Value) Value {
	if projection.IsNullish() {
		return parent
	}
	return projection
}

// ---- path overlay ----

type pathOverlayNormalizer struct{}

func dropLineAndPoint(markDef Value) Value {
	mark := omit(markDef, "point", "line")
	if mark.Len() > 1 {
		return jsval.Obj(mark)
	}
	return mark.Lookup("type")
}

func dropLineAndPointFromConfig(config Value) Value {
	for _, m := range []string{"line", "area", "rule", "trail"} {
		if config.Get(m).IsTruthy() {
			c := cloneObj(config.ObjValue())
			c.Set(m, jsval.Obj(omit(config.Get(m), "point", "line")))
			config = jsval.Obj(c)
		}
	}
	return config
}

// getPointOverlay returns the overlay marks props; ok is false for null/undefined.
func getPointOverlay(markDef, markConfig, encoding Value) (Value, bool) {
	point := markDef.Get("point")
	switch {
	case point.IsStr() && point.StrValue() == "transparent":
		return mkv("opacity", 0), true
	case point.IsTruthy():
		if isObject(point) {
			return point, true
		}
		return mkv(), true
	case !point.IsUndefined():
		return undef, false
	}
	if !markConfig.Get("point").IsTruthy() {
		// `markConfig.point || encoding.shape` reads encoding.shape.
		switch {
		case encoding.IsUndefined():
			throw("Cannot read properties of undefined (reading 'shape')")
		case encoding.IsNull():
			throw("Cannot read properties of null (reading 'shape')")
		}
	}
	if markConfig.Get("point").IsTruthy() || encoding.Get("shape").IsTruthy() {
		if isObject(markConfig.Get("point")) {
			return markConfig.Get("point"), true
		}
		return mkv(), true
	}
	return undef, false
}

func getLineOverlay(markDef, markConfig Value) (Value, bool) {
	line := markDef.Get("line")
	switch {
	case line.IsTruthy():
		if line.IsBool() {
			return mkv(), true
		}
		return line, true
	case !line.IsUndefined():
		return undef, false
	}
	if l := markConfig.Get("line"); l.IsTruthy() {
		if l.IsBool() {
			return mkv(), true
		}
		return l, true
	}
	return undef, false
}

func markDefOf(mark Value) Value {
	if isMarkDef(mark) {
		return mark
	}
	return mkv("type", mark)
}

func isMarkDef(mark Value) bool { return hasProperty(mark, "type") }

func getMarkType(m Value) string {
	if isMarkDef(m) {
		return m.Get("type").AsString()
	}
	return m.AsString()
}

func (pathOverlayNormalizer) hasMatchingType(spec Value, p *normParams) bool {
	config := p.config
	if !isUnitSpec(spec) {
		return false
	}
	markDef := markDefOf(spec.Get("mark"))
	typ := markDef.Get("type").AsString()
	switch typ {
	case "line", "rule", "trail":
		_, ok := getPointOverlay(markDef, config.Get(typ), spec.Get("encoding"))
		return ok && true && overlayTruthy(markDef, config, spec.Get("encoding"), typ, false)
	case "area":
		return overlayTruthy(markDef, config, spec.Get("encoding"), typ, true)
	}
	return false
}

// overlayTruthy: !!getPointOverlay(...) [|| !!getLineOverlay(...)]. An empty
// object is truthy in JavaScript.
func overlayTruthy(markDef, config, encoding Value, typ string, withLine bool) bool {
	if v, ok := getPointOverlay(markDef, config.Get(typ), encoding); ok && v.IsTruthy() {
		return true
	}
	if withLine {
		if v, ok := getLineOverlay(markDef, config.Get(typ)); ok && v.IsTruthy() {
			return true
		}
	}
	return false
}

func (pathOverlayNormalizer) run(spec Value, p *normParams, normalize func(Value, *normParams) Value) Value {
	cc := p.cc

	config := p.config
	params, projection, mark, name, e := spec.Get("params"), spec.Get("projection"), spec.Get("mark"), spec.Get("name"), spec.Get("encoding")
	outerSpec := omit(spec, "params", "projection", "mark", "name", "encoding")
	encoding := normalizeEncoding(cc, e, config)
	markDef := markDefOf(mark)
	pointOverlay, hasPoint := getPointOverlay(markDef, config.Get(markDef.Get("type").AsString()), encoding)
	lineOverlay, hasLine := undef, false
	if markDef.Get("type").AsString() == "area" {
		lineOverlay, hasLine = getLineOverlay(markDef, config.Get("area"))
	}
	first := jsval.NewObject(4)
	first.Set("name", name)
	if params.IsTruthy() {
		first.Set("params", params)
	}
	m := jsval.NewObject(4)
	areaOpacity := getMarkPropOrConfigSimple("opacity", markDef, config).IsNullish() && getMarkPropOrConfigSimple("fillOpacity", markDef, config).IsNullish()
	if cc.v5 {
		// 5.8 looks at the mark definition only, not at the config.
		areaOpacity = markDef.Get("opacity").IsUndefined() && markDef.Get("fillOpacity").IsUndefined()
	}
	if markDef.Get("type").AsString() == "area" && areaOpacity {
		m.Set("opacity", jsval.Num(0.7))
	}
	spread(m, markDef)
	first.Set("mark", dropLineAndPoint(jsval.Obj(m)))
	first.Set("encoding", jsval.Obj(omit(encoding, "shape")))
	layer := []Value{jsval.Obj(first)}

	stackMarkDef := markDef
	if !cc.v5 {
		stackMarkDef = initMarkdef(cc, markDef, encoding, config)
	}
	stackProps := stackOf(cc, stackMarkDef, encoding)
	overlayEncoding := encoding
	if stackProps != nil {
		oe := cloneObj(encoding.ObjValue())
		def := spread(jsval.NewObject(4), encoding.Get(stackProps.fieldChannel))
		if stackProps.offset != "" {
			def.Set("stack", jsval.Str(stackProps.offset))
		}
		oe.Set(stackProps.fieldChannel, jsval.Obj(def))
		overlayEncoding = jsval.Obj(oe)
	}
	overlayEncoding = jsval.Obj(omit(overlayEncoding, "y2", "x2"))
	if hasLine && lineOverlay.IsTruthy() {
		o := jsval.NewObject(3)
		if projection.IsTruthy() {
			o.Set("projection", projection)
		}
		mm := mk("type", "line")
		spread(mm, jsval.Obj(pick(markDef, "clip", "interpolate", "tension", "tooltip")))
		spread(mm, lineOverlay)
		o.Set("mark", jsval.Obj(mm))
		o.Set("encoding", overlayEncoding)
		layer = append(layer, jsval.Obj(o))
	}
	if hasPoint && pointOverlay.IsTruthy() {
		o := jsval.NewObject(3)
		if projection.IsTruthy() {
			o.Set("projection", projection)
		}
		mm := mk("type", "point", "opacity", 1, "filled", true)
		spread(mm, jsval.Obj(pick(markDef, "clip", "tooltip")))
		spread(mm, pointOverlay)
		o.Set("mark", jsval.Obj(mm))
		o.Set("encoding", overlayEncoding)
		layer = append(layer, jsval.Obj(o))
	}
	outer := cloneObj(outerSpec)
	outer.Set("layer", jsval.Arr(layer))
	np := p.clone()
	np.config = dropLineAndPointFromConfig(config)
	return normalize(jsval.Obj(outer), np)
}

// ---- rule for ranged line ----

type ruleForRangedLineNormalizer struct{}

func (ruleForRangedLineNormalizer) hasMatchingType(spec Value, p *normParams) bool {
	cc := p.cc

	if !isUnitSpec(spec) {
		return false
	}
	mark, encoding := spec.Get("mark"), spec.Get("encoding")
	if mark.IsStr() && mark.StrValue() == "line" || (isMarkDef(mark) && mark.Get("type").AsString() == "line") {
		for _, channel := range secondaryRangeChannels {
			mainDef := encoding.Get(getMainRangeChannel(channel))
			if encoding.Get(channel).IsTruthy() {
				if (isFieldDef(cc, mainDef) && !isBinned(mainDef.Get("bin"))) || isDatumDef(mainDef) {
					return true
				}
			}
		}
	}
	return false
}

func (ruleForRangedLineNormalizer) run(spec Value, p *normParams, normalize func(Value, *normParams) Value) Value {
	mark := spec.Get("mark")
	o := cloneObj(spec.ObjValue())
	if isObject(mark) {
		o.Set("mark", jsval.Obj(spread(cloneObj(mark.ObjValue()), mkv("type", "rule"))))
	} else {
		o.Set("mark", jsval.Str("rule"))
	}
	return normalize(jsval.Obj(o), p)
}

// ---- repeater ----

func replaceRepeaterInFacet(cc *compileCtx, facet, repeater Value) Value {
	if !repeater.IsTruthy() {
		return facet
	}
	if isFacetMapping(facet) {
		return replaceRepeaterInMapping(cc, facet, repeater)
	}
	return replaceRepeaterInFieldDef(cc, facet, repeater)
}

func replaceRepeaterInEncoding(cc *compileCtx, encoding, repeater Value) Value {
	if !repeater.IsTruthy() {
		return encoding
	}
	return replaceRepeaterInMapping(cc, encoding, repeater)
}

// replaceRepeatInProp substitutes {repeat: 'x'} in o[prop]. ok=false means the
// referenced repeat value does not exist (upstream returns undefined).
func replaceRepeatInProp(prop string, o, repeater Value) (Value, bool) {
	val := o.Get(prop)
	if isRepeatRef(val) {
		key := val.Get("repeat").AsString()
		if repeater.IsObj() && repeater.ObjValue().Has(key) {
			c := cloneObj(o.ObjValue())
			c.Set(prop, repeater.Get(key))
			return jsval.Obj(c), true
		}
		if repr, ok := inheritedPropertyText(key); ok && prop == "field" && repeater.IsObj() {
			// `field.repeat in repeater` is true for what every object
			// inherits, and the field becomes that function or object, which
			// the field name machinery reads as its text.
			c := cloneObj(o.ObjValue())
			c.Set(prop, jsval.Str(repr))
			return jsval.Obj(c), true
		}
		return undef, false
	}
	return o, true
}

// inheritedPropertyText is the string form of the property of Object.prototype
// called key: a native function prints as `function name() { [native code] }`
// (constructor is Object), __proto__ is the prototype object itself.
func inheritedPropertyText(key string) (string, bool) {
	switch key {
	case "constructor":
		return "function Object() { [native code] }", true
	case "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf", "toLocaleString",
		"__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__":
		return "function " + key + "() { [native code] }", true
	case "__proto__":
		return "[object Object]", true
	}
	return "", false
}

func replaceRepeaterInFieldDef(cc *compileCtx, fd, repeater Value) Value {
	fd2, ok := replaceRepeatInProp("field", fd, repeater)
	if !ok {
		return undef
	}
	fd = fd2
	if isSortableFieldDef(fd) && isSortField(cc, fd.Get("sort")) {
		if sort, ok := replaceRepeatInProp("field", fd.Get("sort"), repeater); ok && sort.IsTruthy() {
			c := cloneObj(fd.ObjValue())
			c.Set("sort", sort)
			fd = jsval.Obj(c)
		}
	}
	return fd
}

func replaceRepeaterInFieldOrDatumDef(cc *compileCtx, def, repeater Value) Value {
	if isFieldDef(cc, def) {
		return replaceRepeaterInFieldDef(cc, def, repeater)
	}
	dd, ok := replaceRepeatInProp("datum", def, repeater)
	if !ok {
		return undef
	}
	if dd.ObjValue() != def.ObjValue() && !dd.Get("type").IsTruthy() {
		dd.ObjValue().Set("type", jsval.Str("nominal"))
	}
	return dd
}

func replaceRepeaterInChannelDef(cc *compileCtx, cd, repeater Value) Value {
	if isFieldOrDatumDef(cc, cd) {
		if fd := replaceRepeaterInFieldOrDatumDef(cc, cd, repeater); fd.IsTruthy() {
			return fd
		} else if isConditionalDef(cd) {
			return mkv("condition", cd.Get("condition"))
		}
		return undef
	}
	if hasConditionalFieldOrDatumDef(cc, cd) {
		if fd := replaceRepeaterInFieldOrDatumDef(cc, cd.Get("condition"), repeater); fd.IsTruthy() {
			c := cloneObj(cd.ObjValue())
			c.Set("condition", fd)
			return jsval.Obj(c)
		}
		return jsval.Obj(omit(cd, "condition"))
	}
	return cd
}

func replaceRepeaterInMapping(cc *compileCtx, mapping, repeater Value) Value {
	out := jsval.NewObject(mapping.Len())
	for _, channel := range keysOf(mapping) {
		if !hasProperty(mapping, channel) {
			continue
		}
		cd := mapping.Get(channel)
		if cd.IsArr() {
			var items []Value
			for _, x := range cd.Items() {
				if r := replaceRepeaterInChannelDef(cc, x, repeater); r.IsTruthy() {
					items = append(items, r)
				}
			}
			out.Set(channel, jsval.Arr(items))
		} else if r := replaceRepeaterInChannelDef(cc, cd, repeater); !r.IsUndefined() {
			out.Set(channel, r)
		}
	}
	return jsval.Obj(out)
}
