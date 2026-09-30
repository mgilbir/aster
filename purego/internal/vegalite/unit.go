package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// unitModel is a single-view chart — vega-lite/src/compile/unit.ts.
type unitModel struct {
	*modelBase
	markDef             Value
	encoding            Value
	specifiedScales     *Object
	stack               *stackProperties
	specifiedAxes       *Object
	specifiedLegends    *Object
	specifiedProjection Value
	selection           []Value
}

func newUnitModel(cc *compileCtx, spec Value, parent Model, parentGivenName string, parentGivenSize *Object, config Value) *unitModel {
	u := &unitModel{}
	var view Value
	if isFrameMixins(spec) {
		view = spec.Get("view")
	}
	u.modelBase = newModelBase(cc, u, spec, "unit", parent, parentGivenName, config, nil, view)
	var markDef *Object
	if isMarkDef(spec.Get("mark")) {
		markDef = cloneObj(spec.Get("mark").ObjValue())
	} else {
		markDef = mk("type", spec.Get("mark"))
	}
	if t := markDef.Lookup("type"); !t.IsStr() || markCompilers[t.StrValue()].encodeEntry == nil {
		throw("Invalid mark %s: expected one of the mark types (arc, area, bar, image, line, point, rect, rule, text, tick, trail, circle, square, geoshape, or a composite mark) or a mark definition with such a type", stringify(spec.Get("mark")))
	}
	mark := markDef.Lookup("type").AsString()
	if markDef.Lookup("filled").IsUndefined() {
		graticule := spec.Get("data").IsTruthy() && isGraticuleGenerator(spec.Get("data"))
		markDef.Set("filled", defaultFilled(jsval.Obj(markDef), config, graticule))
	}
	filled := markDef.Lookup("filled").IsTruthy()
	u.encoding = initEncoding(cc, coalesceObj(spec.Get("encoding")), mark, filled, config)
	u.markDef = initMarkdef(cc, jsval.Obj(markDef), u.encoding, config)
	var size *Object
	if isFrameMixins(spec) {
		size = cloneObj(parentGivenSize)
		// 6.x keeps a falsy size (0); 5.8 ignores it.
		if v := spec.Get("width"); (cc.v5 && v.IsTruthy()) || (!cc.v5 && !v.IsUndefined()) {
			size.Set("width", v)
		}
		if v := spec.Get("height"); (cc.v5 && v.IsTruthy()) || (!cc.v5 && !v.IsUndefined()) {
			size.Set("height", v)
		}
	} else {
		size = parentGivenSize
		if size == nil {
			size = jsval.NewObject(0)
		}
	}
	u.size = initLayoutSize(cc, u.encoding, size)
	u.stack = stackOf(cc, u.markDef, u.encoding)
	u.specifiedScales = u.initScales(u.encoding)
	u.specifiedAxes = u.initAxes(u.encoding)
	u.specifiedLegends = u.initLegends(u.encoding)
	u.specifiedProjection = spec.Get("projection")
	for _, p := range spec.Get("params").Items() {
		if isSelectionParameter(p) {
			u.selection = append(u.selection, p)
		}
	}
	if !cc.v5 {
		u.alignStackOrderWithColorDomain()
	}
	return u
}

func coalesceObj(v Value) Value {
	if v.IsObj() {
		return v
	}
	return mkv()
}

func (u *unitModel) children() []Model { return nil }
func (u *unitModel) mark() string      { return u.markDef.Get("type").AsString() }

func (u *unitModel) hasProjection() bool {
	cc := u.b().ctx

	if u.mark() == "geoshape" {
		return true
	}
	for _, ch := range geoPositionChannels {
		if isFieldOrDatumDef(cc, u.encoding.Get(ch)) {
			return true
		}
	}
	return false
}

func (u *unitModel) scaleDomain(channel string) Value {
	if s := u.specifiedScales.Lookup(channel); s.IsObj() {
		return s.Get("domain")
	}
	return undef
}

func (u *unitModel) axis(channel string) Value   { return u.specifiedAxes.Lookup(channel) }
func (u *unitModel) legend(channel string) Value { return u.specifiedLegends.Lookup(channel) }

func (u *unitModel) initScales(encoding Value) *Object {
	cc := u.b().ctx

	scales := jsval.NewObject(4)
	for _, channel := range scaleChannels {
		fod := getFieldOrDatumDef(cc, encoding.Get(channel))
		if fod.IsTruthy() {
			sc := fod.Get("scale")
			if !sc.IsTruthy() {
				sc = mkv()
			}
			scales.Set(channel, u.initScale(sc))
		}
	}
	return scales
}

func (u *unitModel) initScale(scale Value) Value {
	domain, rng := scale.Get("domain"), scale.Get("range")
	internal := replaceExprRef(scale, 0)
	io := internal.ObjValue()
	if domain.IsArr() {
		io.Set("domain", mapVals(domain, signalRefOrValue))
	}
	if rng.IsArr() {
		io.Set("range", mapVals(rng, signalRefOrValue))
	}
	return internal
}

func (u *unitModel) initAxes(encoding Value) *Object {
	cc := u.b().ctx

	axes := jsval.NewObject(2)
	for _, channel := range positionScaleChannels {
		cd := encoding.Get(channel)
		if isFieldOrDatumDef(cc, cd) || (channel == chX && isFieldOrDatumDef(cc, encoding.Get("x2"))) || (channel == chY && isFieldOrDatumDef(cc, encoding.Get("y2"))) {
			var axisSpec Value
			if isFieldOrDatumDef(cc, cd) {
				axisSpec = cd.Get("axis")
			}
			if axisSpec.IsTruthy() {
				axes.Set(channel, u.initAxis(jsval.Obj(cloneObj(axisSpec.ObjValue()))))
			} else {
				axes.Set(channel, axisSpec)
			}
		}
	}
	return axes
}

func (u *unitModel) initAxis(axis Value) Value {
	out := jsval.NewObject(axis.Len())
	for _, prop := range keysOf(axis) {
		val := axis.Get(prop)
		if isConditionalAxisValue(val) {
			out.Set(prop, signalOrValueRefWithCondition(val))
		} else {
			out.Set(prop, signalRefOrValue(val))
		}
	}
	return jsval.Obj(out)
}

func (u *unitModel) initLegends(encoding Value) *Object {
	cc := u.b().ctx

	legends := jsval.NewObject(4)
	for _, channel := range nonPositionScaleChannels {
		fod := getFieldOrDatumDef(cc, encoding.Get(channel))
		if fod.IsTruthy() && supportLegend(channel) {
			lg := fod.Get("legend")
			if lg.IsTruthy() {
				legends.Set(channel, replaceExprRef(lg, 0))
			} else {
				legends.Set(channel, lg)
			}
		}
	}
	return legends
}

// alignStackOrderWithColorDomain makes stacked bars follow an explicit
// nominal color domain by adding a sort-index calculate and an order channel.
func (u *unitModel) alignStackOrderWithColorDomain() {
	cc := u.b().ctx

	enc := u.encoding
	color, fill, order, xOffset, yOffset := enc.Get("color"), enc.Get("fill"), enc.Get("order"), enc.Get("xOffset"), enc.Get("yOffset")
	colorField := fill
	if !fill.IsTruthy() {
		colorField = color
	}
	var colorEncoding Value
	if isFieldDef(cc, colorField) {
		colorEncoding = colorField
	}
	field := colorEncoding.Get("field")
	scale := colorEncoding.Get("scale")
	colorType := colorEncoding.Get("type")
	domain := scale.Get("domain")
	offset := xOffset
	if !xOffset.IsTruthy() {
		offset = yOffset
	}
	var offsetEncoding Value
	if isFieldDef(cc, offset) {
		offsetEncoding = offset
	}
	orderFieldName := "_" + field.AsString() + "_sort_index"
	if !order.IsTruthy() && domain.IsArr() && field.IsStr() && colorType.IsStr() && colorType.StrValue() == "nominal" {
		if offsetEncoding.IsTruthy() && !offsetEncoding.Get("sort").IsTruthy() {
			offsetEncoding.ObjValue().Set("sort", domain)
		} else {
			if u.stack == nil {
				return
			}
			orderExpression := "indexof(" + stringValue(domain) + ", datum['" + field.StrValue() + "'])"
			sort := "descending"
			if u.markDef.Get("orient").AsString() == "horizontal" {
				sort = "ascending"
			}
			u.transforms = append(u.transforms, mkv("calculate", orderExpression, "as", orderFieldName))
			enc.ObjValue().Set("order", mkv("field", orderFieldName, "type", "quantitative", "sort", sort))
		}
	}
}

func (u *unitModel) parseData() { u.comp.data = parseDataFor(u) }

func (u *unitModel) parseLayoutSize() { parseUnitLayoutSize(u) }
func (u *unitModel) parseSelections() { u.comp.selection = parseUnitSelection(u, u.selection) }
func (u *unitModel) parseMarkGroup()  { u.comp.mark = parseMarkGroups(u) }
func (u *unitModel) parseAxesAndHeaders() {
	u.comp.axes = parseUnitAxes(u)
}

func (u *unitModel) assembleSelectionTopLevelSignals(signals []Value) []Value {
	return assembleTopLevelSignals(u, signals)
}

func (u *unitModel) assembleSignals() []Value {
	out := assembleAxisSignals(u)
	return append(out, assembleUnitSelectionSignals(u, nil)...)
}

func (u *unitModel) assembleSelectionData(data []Value) []Value {
	return assembleUnitSelectionData(u, data)
}

func (u *unitModel) assembleLayout() Value { return jsval.Null }

func (u *unitModel) assembleLayoutSignals() []Value { return assembleLayoutSignals(u) }

func (u *unitModel) correctDataNames(mark Value) Value {
	cc := u.b().ctx

	from := mark.Get("from")
	if from.Get("data").IsTruthy() {
		d := u.lookupDataSource(from.Get("data").AsString())
		if !cc.v5 && u.encoding.ObjValue().Has("time") {
			d += curr
		}
		from.ObjValue().Set("data", jsval.Str(d))
	}
	if from.Get("facet").Get("data").IsTruthy() {
		from.Get("facet").ObjValue().Set("data", jsval.Str(u.lookupDataSource(from.Get("facet").Get("data").AsString())))
	}
	return mark
}

func (u *unitModel) assembleMarks() []Value {
	marks := u.comp.mark
	if u.parent == nil || !isLayerModel(u.parent) {
		marks = assembleUnitSelectionMarks(u, marks)
	}
	out := make([]Value, len(marks))
	for i, m := range marks {
		out[i] = u.correctDataNames(m)
	}
	return out
}

func (u *unitModel) assembleGroupStyle() Value {
	if s := u.view.Get("style"); !s.IsUndefined() {
		return s
	}
	if u.encoding.Get("x").IsTruthy() || u.encoding.Get("y").IsTruthy() {
		return jsval.Str("cell")
	}
	return jsval.Str("view")
}

func (u *unitModel) assembleTitle() Value     { return u.modelBase.assembleTitle() }
func (u *unitModel) assembleLegends() []Value { return u.modelBase.assembleLegends() }

func (u *unitModel) channelHasField(channel string) bool {
	cc := u.b().ctx
	return channelHasField(cc, u.encoding, channel)
}

func (u *unitModel) fieldDef(channel string) Value {
	cc := u.b().ctx
	return getFieldDef(cc, u.encoding.Get(channel))
}

func (u *unitModel) typedFieldDef(channel string) Value {
	fd := u.fieldDef(channel)
	if isTypedFieldDef(fd) {
		return fd
	}
	return undef
}

func (u *unitModel) vgField(channel string, opt fieldRefOption) string {
	cc := u.b().ctx

	fd := u.fieldDef(channel)
	if !fd.IsTruthy() {
		return ""
	}
	return vgField(cc, fd, opt)
}

// forEachFieldDef calls f for each field def (or conditional field def) in the encoding.
func (u *unitModel) forEachFieldDef(f func(fd Value, channel string)) {
	cc := u.b().ctx

	encodingEach(u.encoding, func(cd Value, c string) {
		if fd := getFieldDef(cc, cd); fd.IsTruthy() {
			f(fd, c)
		}
	})
}

// ---- mark definition init (compile/mark/init.ts) ----

func defaultFilled(markDef Value, config Value, graticule bool) Value {
	if graticule {
		return jsval.False
	}
	filledConfig := getMarkConfig("filled", markDef, config, "")
	mark := markDef.Get("type").AsString()
	return firstDefined(filledConfig, jsval.Bool(mark != "point" && mark != "line" && mark != "rule"))
}

func initMarkdef(cc *compileCtx, original Value, encoding Value, config Value) Value {
	mdv := replaceExprRef(original, 0)
	md := mdv.ObjValue()
	specifiedOrient := getMarkPropOrConfigSimple("orient", mdv, config)
	orient := markOrient(cc, md.Lookup("type").AsString(), encoding, specifiedOrient)
	md.Set("orient", strOrUndef(orient))
	if md.Lookup("type").AsString() == "bar" && orient != "" {
		if cre := getMarkPropOrConfigSimple("cornerRadiusEnd", mdv, config); !cre.IsUndefined() {
			var newProps []string
			if (orient == "horizontal" && encoding.Get("x2").IsTruthy()) || (orient == "vertical" && encoding.Get("y2").IsTruthy()) {
				newProps = []string{"cornerRadius"}
			} else if orient == "horizontal" {
				newProps = []string{"cornerRadiusTopRight", "cornerRadiusBottomRight"}
			} else {
				newProps = []string{"cornerRadiusTopLeft", "cornerRadiusTopRight"}
			}
			for _, p := range newProps {
				md.Set(p, cre)
			}
			if !md.Lookup("cornerRadiusEnd").IsUndefined() {
				md.Delete("cornerRadiusEnd")
			}
		}
	}
	so := getMarkPropOrConfigSimple("opacity", mdv, config)
	sfo := getMarkPropOrConfigSimple("fillOpacity", mdv, config)
	if so.IsUndefined() && (cc.v5 || sfo.IsUndefined()) {
		md.Set("opacity", markOpacity(cc, md.Lookup("type").AsString(), encoding))
	}
	if getMarkPropOrConfigSimple("cursor", mdv, config).IsUndefined() {
		md.Set("cursor", markCursor(mdv, encoding, config))
	}
	return mdv
}

func markCursor(markDef, encoding, config Value) Value {
	if encoding.Get("href").IsTruthy() || markDef.Get("href").IsTruthy() || getMarkPropOrConfigSimple("href", markDef, config).IsTruthy() {
		return jsval.Str("pointer")
	}
	return markDef.Get("cursor")
}

const defaultReducedOpacity = 0.7

func markOpacity(cc *compileCtx, mark string, encoding Value) Value {
	if (mark == "point" || mark == "tick" || mark == "circle" || mark == "square") && !encodingIsAggregate(cc, encoding) {
		return jsval.Num(defaultReducedOpacity)
	}
	return undef
}

func markOrient(cc *compileCtx, mark string, encoding Value, specifiedOrient Value) string {
	switch mark {
	case "point", "circle", "square", "rect", "image":
		return ""
	case "text":
		if cc.v5 {
			return ""
		}
	}
	x, y, x2, y2 := encoding.Get("x"), encoding.Get("y"), encoding.Get("x2"), encoding.Get("y2")
	so := ""
	if specifiedOrient.IsTruthy() {
		so = specifiedOrient.AsString()
	}
	// The cases below fall through in upstream: text/bar -> rule -> area -> line/tick.
	stage := 0
	switch mark {
	case "text", "bar":
		stage = 0
	case "rule":
		stage = 1
	case "area":
		stage = 2
	case "line", "tick":
		stage = 3
	default:
		return "vertical"
	}
	if stage == 0 {
		if isFieldDef(cc, x) && (isBinned(x.Get("bin")) || (isFieldDef(cc, y) && y.Get("aggregate").IsTruthy() && !x.Get("aggregate").IsTruthy())) {
			return "vertical"
		}
		if isFieldDef(cc, y) && (isBinned(y.Get("bin")) || (isFieldDef(cc, x) && x.Get("aggregate").IsTruthy() && !y.Get("aggregate").IsTruthy())) {
			return "horizontal"
		}
		if y2.IsTruthy() || x2.IsTruthy() {
			if so != "" {
				return so
			}
			if !x2.IsTruthy() {
				if (isFieldDef(cc, x) && channelDefType(x) == "quantitative" && !isBinning(x.Get("bin"))) || isNumericDataDef(x) {
					if isFieldDef(cc, y) && isBinned(y.Get("bin")) {
						return "horizontal"
					}
				}
				return "vertical"
			}
			if !y2.IsTruthy() {
				if (isFieldDef(cc, y) && channelDefType(y) == "quantitative" && !isBinning(y.Get("bin"))) || isNumericDataDef(y) {
					if isFieldDef(cc, x) && isBinned(x.Get("bin")) {
						return "vertical"
					}
				}
				return "horizontal"
			}
		}
		stage = 1
	}
	if stage == 1 {
		if x2.IsTruthy() && !(isFieldDef(cc, x) && isBinned(x.Get("bin"))) && y2.IsTruthy() && !(isFieldDef(cc, y) && isBinned(y.Get("bin"))) {
			return ""
		}
		stage = 2
	}
	if stage == 2 {
		switch {
		case y2.IsTruthy():
			if isFieldDef(cc, y) && isBinned(y.Get("bin")) {
				return "horizontal"
			}
			return "vertical"
		case x2.IsTruthy():
			if isFieldDef(cc, x) && isBinned(x.Get("bin")) {
				return "vertical"
			}
			return "horizontal"
		case mark == "rule":
			if x.IsTruthy() && !y.IsTruthy() {
				return "vertical"
			} else if y.IsTruthy() && !x.IsTruthy() {
				return "horizontal"
			}
		}
	}
	// line and tick (and fall-through from above)
	xIsMeasure := isUnbinnedQuantitativeFieldOrDatumDef(x)
	yIsMeasure := isUnbinnedQuantitativeFieldOrDatumDef(y)
	switch {
	case so != "":
		return so
	case xIsMeasure && !yIsMeasure:
		if mark != "tick" {
			return "horizontal"
		}
		return "vertical"
	case !xIsMeasure && yIsMeasure:
		if mark != "tick" {
			return "vertical"
		}
		return "horizontal"
	case xIsMeasure && yIsMeasure:
		return "vertical"
	}
	xIsTemporal := isTypedFieldDef(x) && channelDefType(x) == "temporal"
	yIsTemporal := isTypedFieldDef(y) && channelDefType(y) == "temporal"
	if xIsTemporal && !yIsTemporal {
		return "vertical"
	} else if !xIsTemporal && yIsTemporal {
		return "horizontal"
	}
	return ""
}
