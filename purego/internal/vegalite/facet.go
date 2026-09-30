package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

func facetSortFieldName(cc *compileCtx, fieldDef, sort Value, opt fieldRefOption) string {
	opt.suffix = "by_" + vgField(cc, fieldDef, fieldRefOption{})
	return vgField(cc, sort, opt)
}

// facetModel splits one child view into a trellis — vega-lite/src/compile/facet.ts.
type facetModel struct {
	*modelBase
	facet *Object
	child Model
}

func newFacetModel(cc *compileCtx, spec Value, parent Model, parentGivenName string, config Value, depth int) *facetModel {
	f := &facetModel{}
	var resolve *resolveIndex
	if r := spec.Get("resolve"); r.IsObj() {
		resolve = resolveFromSpec(r)
	}
	f.modelBase = newModelBase(cc, f, spec, "facet", parent, parentGivenName, config, resolve, undef)
	f.child = buildModel(cc, spec.Get("spec"), f, f.getName("child"), nil, config, depth+1)
	f.facet = f.initFacet(spec.Get("facet"))
	return f
}

func (f *facetModel) children() []Model { return []Model{f.child} }

func (f *facetModel) initFacet(facet Value) *Object {
	out := jsval.NewObject(2)
	if !isFacetMapping(facet) {
		out.Set("facet", f.initFacetFieldDef(facet, "facet"))
		return out
	}
	for _, channel := range keysOf(facet) {
		if channel != chRow && channel != chColumn {
			break
		}
		fd := facet.Get(channel)
		if fd.Get("field").IsUndefined() {
			break
		}
		out.Set(channel, f.initFacetFieldDef(fd, channel))
	}
	return out
}

func (f *facetModel) initFacetFieldDef(fd Value, channel string) Value {
	cc := f.b().ctx

	ffd := initFieldDef(cc, fd, channel, false)
	o := ffd.ObjValue()
	if h := o.Lookup("header"); h.IsTruthy() {
		o.Set("header", replaceExprRef(h, 0))
	} else if h.IsNull() {
		o.Set("header", jsval.Null)
	}
	return ffd
}

func (f *facetModel) channelHasField(channel string) bool {
	return hasProperty(jsval.Obj(f.facet), channel)
}
func (f *facetModel) fieldDef(channel string) Value { return f.facet.Lookup(channel) }

func (f *facetModel) vgField(channel string, opt fieldRefOption) string {
	cc := f.b().ctx

	fd := f.fieldDef(channel)
	if !fd.IsTruthy() {
		return ""
	}
	return vgField(cc, fd, opt)
}

func (f *facetModel) forEachFieldDef(fn func(fd Value, channel string)) {
	cc := f.b().ctx

	encodingEach(jsval.Obj(f.facet), func(cd Value, c string) {
		if fd := getFieldDef(cc, cd); fd.IsTruthy() {
			fn(fd, c)
		}
	})
}

func (f *facetModel) parseData() {
	f.comp.data = parseDataFor(f)
	f.child.parseData()
}

func (f *facetModel) parseLayoutSize() { parseChildrenLayoutSize(f) }

func (f *facetModel) parseSelections() {
	f.child.parseSelections()
	f.comp.selection = f.child.b().comp.selection
}

func (f *facetModel) parseMarkGroup() { f.child.parseMarkGroup() }

func (f *facetModel) parseAxesAndHeaders() {
	f.child.parseAxesAndHeaders()
	parseFacetHeaders(f)
}

func (f *facetModel) assembleSelectionTopLevelSignals(signals []Value) []Value {
	return f.child.assembleSelectionTopLevelSignals(signals)
}

func (f *facetModel) assembleSignals() []Value {
	f.child.assembleSignals()
	return nil
}

func (f *facetModel) assembleSelectionData(data []Value) []Value {
	return f.child.assembleSelectionData(data)
}

func (f *facetModel) getHeaderLayoutMixins() *Object {
	layout := jsval.NewObject(4)
	for _, channel := range facetChannels {
		for _, headerType := range headerTypes {
			lh := f.comp.layoutHeaders[channel]
			headerComponent := lh.get(headerType)
			if lh.facetFieldDef.IsTruthy() {
				titleOrient := getHeaderProperty("titleOrient", lh.facetFieldDef.Get("header"), f.config, channel)
				if titleOrient.IsStr() && (titleOrient.StrValue() == "right" || titleOrient.StrValue() == "bottom") {
					headerChannel := getHeaderChannel(channel, titleOrient.StrValue())
					ta := layout.Lookup("titleAnchor")
					if ta.IsNullish() {
						ta = mkv()
						layout.Set("titleAnchor", ta)
					}
					ta.ObjValue().Set(headerChannel, jsval.Str("end"))
				}
			}
			if len(headerComponent) > 0 && headerComponent[0] != nil {
				sizeType := "width"
				if channel == chRow {
					sizeType = "height"
				}
				bandType := "footerBand"
				if headerType == "header" {
					bandType = "headerBand"
				}
				if channel != chFacet && f.child.b().comp.layoutSize.get(sizeType).IsNullish() {
					band := layout.Lookup(bandType)
					if band.IsNullish() {
						band = mkv()
						layout.Set(bandType, band)
					}
					band.ObjValue().Set(channel, jsval.Num(0.5))
				}
				if lh.title.IsTruthy() {
					off := layout.Lookup("offset")
					if off.IsNullish() {
						off = mkv()
						layout.Set("offset", off)
					}
					key := "columnTitle"
					if channel == chRow {
						key = "rowTitle"
					}
					off.ObjValue().Set(key, jsval.Int(10))
				}
			}
		}
	}
	return layout
}

func (f *facetModel) assembleDefaultLayout() *Object {
	column, row := f.facet.Lookup("column"), f.facet.Lookup("row")
	var columns Value
	switch {
	case column.IsTruthy():
		columns = f.columnDistinctSignal()
	case row.IsTruthy():
		columns = jsval.Int(1)
	}
	align := "all"
	if !row.IsTruthy() && f.comp.resolve.scale["x"] == "independent" {
		align = "none"
	} else if !column.IsTruthy() && f.comp.resolve.scale["y"] == "independent" {
		align = "none"
	}
	o := f.getHeaderLayoutMixins()
	if columns.IsTruthy() {
		o.Set("columns", columns)
	}
	o.Set("bounds", jsval.Str("full"))
	o.Set("align", jsval.Str(align))
	return o
}

func (f *facetModel) assembleLayoutSignals() []Value { return f.child.assembleLayoutSignals() }

func (f *facetModel) columnDistinctSignal() Value {
	if f.parent != nil && isFacetModel(f.parent) {
		return undef
	}
	return sig("length(data('" + f.getName("column_domain") + "'))")
}

func (f *facetModel) assembleGroupStyle() Value { return undef }

// assembleGroup overrides the shared group assembly for nested facets.
func (f *facetModel) assembleGroupFor(signals []Value) *Object {
	cc := f.b().ctx

	if f.parent != nil && isFacetModel(f.parent) {
		o := jsval.NewObject(6)
		if f.channelHasField("column") {
			o.Set("encode", mkv("update", mkv("columns", mkv("field", vgField(cc, f.facet.Lookup("column"), fieldRefOption{prefix: "distinct"})))))
		}
		spread(o, jsval.Obj(assembleGroup(f, signals)))
		return o
	}
	return assembleGroup(f, signals)
}

type cardinalityAggregate struct {
	fields, ops, as []Value
}

func (f *facetModel) getCardinalityAggregateForChild() cardinalityAggregate {
	cc := f.b().ctx

	var r cardinalityAggregate
	if cf, ok := f.child.(*facetModel); ok {
		if cf.channelHasField("column") {
			field := vgField(cc, cf.facet.Lookup("column"), fieldRefOption{})
			r.fields = append(r.fields, jsval.Str(field))
			r.ops = append(r.ops, jsval.Str("distinct"))
			r.as = append(r.as, jsval.Str("distinct_"+field))
		}
	} else {
		for _, channel := range positionScaleChannels {
			cs, ok := f.child.b().comp.scales.get(channel)
			if ok && cs != nil && !cs.merged {
				typ, rng := cs.get("type").AsString(), cs.get("range")
				if hasDiscreteDomain(typ) && isVgRangeStep(rng) {
					domain := assembleDomain(f.child, channel)
					if field := getFieldFromDomain(domain); field != "" {
						r.fields = append(r.fields, jsval.Str(field))
						r.ops = append(r.ops, jsval.Str("distinct"))
						r.as = append(r.as, jsval.Str("distinct_"+field))
					}
				}
			}
		}
	}
	return r
}

func (f *facetModel) assembleFacet() Value {
	cc := f.b().ctx

	root := f.comp.data.facetRoot
	name, data := root.name, root.data
	row, column := f.facet.Lookup("row"), f.facet.Lookup("column")
	agg := f.getCardinalityAggregateForChild()
	var groupby []Value
	for _, channel := range facetChannels {
		fd := f.facet.Lookup(channel)
		if !fd.IsTruthy() {
			continue
		}
		groupby = append(groupby, jsval.Str(vgField(cc, fd, fieldRefOption{})))
		bin, sort := fd.Get("bin"), fd.Get("sort")
		if isBinning(bin) {
			groupby = append(groupby, jsval.Str(vgField(cc, fd, fieldRefOption{binSuffix: "end"})))
		}
		if isSortField(cc, sort) {
			field := sort.Get("field")
			op := coalesce(sort.Get("op"), jsval.Str(defaultSortOp))
			outputName := facetSortFieldName(cc, fd, sort, fieldRefOption{})
			if row.IsTruthy() && column.IsTruthy() {
				agg.fields = append(agg.fields, jsval.Str(outputName))
				agg.ops = append(agg.ops, jsval.Str("max"))
				agg.as = append(agg.as, jsval.Str(outputName))
			} else {
				agg.fields = append(agg.fields, field)
				agg.ops = append(agg.ops, op)
				agg.as = append(agg.as, jsval.Str(outputName))
			}
		} else if sort.IsArr() {
			outputName := sortArrayIndexField(cc, fd, channel, fieldRefOption{})
			agg.fields = append(agg.fields, jsval.Str(outputName))
			agg.ops = append(agg.ops, jsval.Str("max"))
			agg.as = append(agg.as, jsval.Str(outputName))
		}
	}
	cross := row.IsTruthy() && column.IsTruthy()
	o := mk("name", name, "data", data, "groupby", jsval.Arr(groupby))
	if cross || len(agg.fields) > 0 {
		a := jsval.NewObject(4)
		if cross {
			a.Set("cross", jsval.True)
		}
		if len(agg.fields) > 0 {
			a.Set("fields", jsval.Arr(agg.fields))
			a.Set("ops", jsval.Arr(agg.ops))
			a.Set("as", jsval.Arr(agg.as))
		}
		o.Set("aggregate", jsval.Obj(a))
	}
	return jsval.Obj(o)
}

func (f *facetModel) facetSortFields(channel string) []Value {
	cc := f.b().ctx

	fd := f.facet.Lookup(channel)
	if !fd.IsTruthy() {
		return nil
	}
	sort := fd.Get("sort")
	switch {
	case isSortField(cc, sort):
		return []Value{jsval.Str(facetSortFieldName(cc, fd, sort, fieldRefOption{expr: "datum"}))}
	case sort.IsArr():
		return []Value{jsval.Str(sortArrayIndexField(cc, fd, channel, fieldRefOption{expr: "datum"}))}
	}
	return []Value{jsval.Str(vgField(cc, fd, fieldRefOption{expr: "datum"}))}
}

func (f *facetModel) facetSortOrder(channel string) []Value {
	cc := f.b().ctx

	fd := f.facet.Lookup(channel)
	if !fd.IsTruthy() {
		return nil
	}
	sort := fd.Get("sort")
	var order Value
	if isSortField(cc, sort) {
		order = sort.Get("order")
	} else if !sort.IsArr() {
		order = sort
	}
	return []Value{or(order, jsval.Str("ascending"))}
}

func (f *facetModel) assembleLabelTitle() Value {
	cc := f.b().ctx

	if fd := f.facet.Lookup("facet"); fd.IsTruthy() {
		return assembleLabelTitle(cc, fd, "facet", f.config)
	}
	orth := map[string][]string{"row": {"top", "bottom"}, "column": {"left", "right"}}
	for _, channel := range headerChannels {
		if fd := f.facet.Lookup(channel); fd.IsTruthy() {
			lo := getHeaderProperty("labelOrient", fd.Get("header"), f.config, channel)
			if lo.IsStr() && contains(orth[channel], lo.StrValue()) {
				return assembleLabelTitle(cc, fd, channel, f.config)
			}
		}
	}
	return undef
}

func (f *facetModel) assembleMarks() []Value {
	child := f.child
	facetRoot := f.comp.data.facetRoot
	data := assembleFacetData(facetRoot)
	encodeEntry := child.b().assembleGroupEncodeEntry(false)
	title := f.assembleLabelTitle()
	if !title.IsTruthy() {
		title = child.assembleTitle()
	}
	style := child.assembleGroupStyle()
	mg := mk("name", f.getName("cell"), "type", "group")
	if title.IsTruthy() {
		mg.Set("title", title)
	}
	if style.IsTruthy() {
		mg.Set("style", style)
	}
	mg.Set("from", mkv("facet", f.assembleFacet()))
	var fields, orders []Value
	for _, c := range facetChannels {
		fields = append(fields, f.facetSortFields(c)...)
	}
	for _, c := range facetChannels {
		orders = append(orders, f.facetSortOrder(c)...)
	}
	mg.Set("sort", mkv("field", jsval.Arr(fields), "order", jsval.Arr(orders)))
	if len(data) > 0 {
		mg.Set("data", jsval.Arr(data))
	}
	if encodeEntry.IsTruthy() {
		mg.Set("encode", mkv("update", encodeEntry))
	}
	var facetSignals []Value
	facetSignals = assembleFacetSignals(f, facetSignals)
	if cf, ok := child.(*facetModel); ok {
		spread(mg, jsval.Obj(cf.assembleGroupFor(facetSignals)))
	} else {
		spread(mg, jsval.Obj(assembleGroup(child, facetSignals)))
	}
	return []Value{jsval.Obj(mg)}
}

func (f *facetModel) assembleTitle() Value     { return f.modelBase.assembleTitle() }
func (f *facetModel) assembleLegends() []Value { return f.modelBase.assembleLegends() }
func (f *facetModel) assembleLayout() Value    { return f.modelBase.assembleLayout() }
