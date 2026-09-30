package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Channel definitions (field, datum and value defs) — vega-lite/src/channeldef.ts.
// Definitions stay loosely typed objects; the predicates below are upstream's
// type guards.

func isConditionalParameter(c Value) bool { return hasProperty(c, "param") }

func isRepeatRef(v Value) bool { return !v.IsStr() && hasProperty(v, "repeat") }

func isSortableFieldDef(fd Value) bool { return hasProperty(fd, "sort") }

func isFieldDef(cc *compileCtx, cd Value) bool {
	if cc.v5 {
		return cd.IsObj() && (cd.Get("field").IsTruthy() || aggIs(cd, "count"))
	}
	return hasProperty(cd, "field") || (cd.IsObj() && aggIs(cd, "count"))
}

func aggIs(cd Value, op string) bool {
	a := cd.Get("aggregate")
	return a.IsStr() && a.StrValue() == op
}

func channelDefType(cd Value) string {
	t := cd.Get("type")
	if t.IsStr() {
		return t.StrValue()
	}
	return ""
}

func isDatumDef(cd Value) bool { return hasProperty(cd, "datum") }

func isNumericDataDef(cd Value) bool { return isDatumDef(cd) && cd.Get("datum").IsNum() }

func isFieldOrDatumDef(cc *compileCtx, cd Value) bool { return isFieldDef(cc, cd) || isDatumDef(cd) }

func isTypedFieldDef(cd Value) bool {
	return cd.IsTruthy() && (hasProperty(cd, "field") || aggIs(cd, "count")) && hasProperty(cd, "type")
}

func isValueDef(cd Value) bool { return hasProperty(cd, "value") }

func isScaleFieldDef(cd Value) bool { return hasProperty(cd, "scale") || hasProperty(cd, "sort") }

func isPositionFieldOrDatumDef(cd Value) bool {
	return hasProperty(cd, "axis") || hasProperty(cd, "stack") || hasProperty(cd, "impute")
}

func isMarkPropFieldOrDatumDef(cd Value) bool { return hasProperty(cd, "legend") }

func isStringFieldOrDatumDef(cd Value) bool {
	return hasProperty(cd, "format") || hasProperty(cd, "formatType")
}

func isFacetFieldDef(cd Value) bool { return hasProperty(cd, "header") }

func isOrderOnlyDef(cc *compileCtx, cd Value) bool {
	return !cc.v5 && hasProperty(cd, "sort") && !hasProperty(cd, "field")
}

func isConditionalDef(cd Value) bool { return hasProperty(cd, "condition") }

func hasConditionalFieldDef(cc *compileCtx, cd Value) bool {
	c := cd.Get("condition")
	return c.IsTruthy() && !c.IsArr() && isFieldDef(cc, c)
}

func hasConditionalFieldOrDatumDef(cc *compileCtx, cd Value) bool {
	c := cd.Get("condition")
	return c.IsTruthy() && !c.IsArr() && isFieldOrDatumDef(cc, c)
}

func hasConditionalValueDef(cd Value) bool {
	c := cd.Get("condition")
	return c.IsTruthy() && (c.IsArr() || isValueDef(c))
}

func isContinuousFieldOrDatumDef(cc *compileCtx, cd Value) bool {
	return (isTypedFieldDef(cd) && !isDiscreteDef(cc, cd)) || isNumericDataDef(cd)
}

func isUnbinnedQuantitativeFieldOrDatumDef(cd Value) bool {
	return (isTypedFieldDef(cd) && cd.Get("type").IsStr() && cd.Get("type").StrValue() == "quantitative" && !cd.Get("bin").IsTruthy()) || isNumericDataDef(cd)
}

func toStringFieldDef(fd Value) Value {
	return jsval.Obj(omit(fd, "legend", "axis", "header", "scale"))
}

func toFieldDefBase(fd Value) Value {
	o := jsval.NewObject(4)
	if v := fd.Get("timeUnit"); v.IsTruthy() {
		o.Set("timeUnit", v)
	}
	if v := fd.Get("bin"); v.IsTruthy() {
		o.Set("bin", v)
	}
	if v := fd.Get("aggregate"); v.IsTruthy() {
		o.Set("aggregate", v)
	}
	o.Set("field", fd.Get("field"))
	return jsval.Obj(o)
}

// isDiscreteDef is channeldef.ts's isDiscrete: whether a typed def is discrete.
func isDiscreteDef(cc *compileCtx, def Value) bool {
	switch channelDefType(def) {
	case "nominal", "ordinal", "geojson":
		return true
	case "quantitative":
		return isFieldDef(cc, def) && def.Get("bin").IsTruthy()
	case "temporal":
		return false
	}
	throw("Invalid field type %q.", def.Get("type").AsString())
	return false
}

func isCountDef(fd Value) bool { return aggIs(fd, "count") }

// fieldRefOption is FieldRefOption.
type fieldRefOption struct {
	nofn      bool
	expr      string // "datum", "parent", "datum.datum"
	prefix    string
	binSuffix string // "end", "range", "mid"
	suffix    string
	forAs     bool
}

// vgField is upstream's vgField: the flattened Vega field name of a field def
// (or an aggregate/window op def).
func vgField(cc *compileCtx, fd Value, opt fieldRefOption) string {
	field := ""
	if f := fd.Get("field"); !f.IsNullish() {
		field = f.AsString()
	}
	hasField := field != ""
	suffix := opt.suffix
	argAccessor := ""
	if isCountDef(fd) {
		field = internalField("count")
	} else {
		fn := ""
		if !opt.nofn {
			if hasProperty(fd, "op") {
				fn = fd.Get("op").AsString()
			} else {
				bin, aggregate, timeUnit := fd.Get("bin"), fd.Get("aggregate"), fd.Get("timeUnit")
				switch {
				case isBinning(bin):
					fn = binToString(bin)
					suffix = opt.binSuffix + opt.suffix
				case aggregate.IsTruthy():
					if isArgmaxDef(aggregate) {
						argAccessor = `["` + field + `"]`
						field = "argmax_" + aggregate.Get("argmax").AsString()
						hasField = true
					} else if isArgminDef(aggregate) {
						argAccessor = `["` + field + `"]`
						field = "argmin_" + aggregate.Get("argmin").AsString()
						hasField = true
					} else {
						fn = aggregate.AsString()
					}
				case timeUnit.IsTruthy() && !isBinnedTimeUnit(cc, timeUnit):
					fn = timeUnitToString(cc, timeUnit)
					bs := opt.binSuffix
					if bs == "range" || bs == "mid" {
						bs = ""
					}
					suffix = bs + opt.suffix
				}
			}
		}
		if fn != "" {
			if hasField && field != "" {
				field = fn + "_" + field
			} else {
				field = fn
			}
		}
	}
	if suffix != "" {
		field = field + "_" + suffix
	}
	if opt.prefix != "" {
		field = opt.prefix + "_" + field
	}
	switch {
	case opt.forAs:
		return removePathFromField(field)
	case opt.expr != "":
		return flatAccessWithDatum(field, opt.expr) + argAccessor
	}
	return replacePathInField(field) + argAccessor
}

// ---- titles ----

func verbalTitleFormatter(cc *compileCtx, fd Value, config Value) Value {
	field := fd.Get("field")
	bin, timeUnit, aggregate := fd.Get("bin"), fd.Get("timeUnit"), fd.Get("aggregate")
	switch {
	case aggIs(fd, "count"):
		return config.Get("countTitle")
	case isBinning(bin):
		return jsval.Str(field.AsString() + " (binned)")
	case timeUnit.IsTruthy() && !isBinnedTimeUnit(cc, timeUnit):
		if unit := normalizeTimeUnit(cc, timeUnit).Get("unit"); unit.IsTruthy() {
			return jsval.Str(field.AsString() + " (" + joinStrings(getTimeUnitParts(unit.AsString()), "-") + ")")
		}
	case aggregate.IsTruthy():
		if isArgmaxDef(aggregate) {
			return jsval.Str(field.AsString() + " for max " + aggregate.Get("argmax").AsString())
		} else if isArgminDef(aggregate) {
			return jsval.Str(field.AsString() + " for min " + aggregate.Get("argmin").AsString())
		}
		return jsval.Str(titleCase(aggregate.AsString()) + " of " + field.AsString())
	}
	return field
}

func functionalTitleFormatter(cc *compileCtx, fd Value) Value {
	aggregate, bin, timeUnit, field := fd.Get("aggregate"), fd.Get("bin"), fd.Get("timeUnit"), fd.Get("field")
	if isArgmaxDef(aggregate) {
		return jsval.Str(field.AsString() + " for argmax(" + aggregate.Get("argmax").AsString() + ")")
	} else if isArgminDef(aggregate) {
		return jsval.Str(field.AsString() + " for argmin(" + aggregate.Get("argmin").AsString() + ")")
	}
	var tup Value
	if timeUnit.IsTruthy() && !isBinnedTimeUnit(cc, timeUnit) {
		tup = normalizeTimeUnit(cc, timeUnit)
	}
	fn := ""
	switch {
	case aggregate.IsTruthy():
		fn = aggregate.AsString()
	case tup.Get("unit").IsTruthy():
		fn = tup.Get("unit").AsString()
	case tup.Get("maxbins").IsTruthy():
		fn = "timeunit"
	case isBinning(bin):
		fn = "bin"
	}
	if fn != "" {
		return jsval.Str(upper(fn) + "(" + field.AsString() + ")")
	}
	return field
}

func defaultTitle(cc *compileCtx, fd Value, config Value) Value {
	switch config.Get("fieldTitle").AsString() {
	case "plain":
		return fd.Get("field")
	case "functional":
		return functionalTitleFormatter(cc, fd)
	}
	return verbalTitleFormatter(cc, fd, config)
}

// getGuide returns the axis, legend or header object of a def, if any.
func getGuide(fd Value) Value {
	switch {
	case isPositionFieldOrDatumDef(fd) && fd.Get("axis").IsTruthy():
		return fd.Get("axis")
	case isMarkPropFieldOrDatumDef(fd) && fd.Get("legend").IsTruthy():
		return fd.Get("legend")
	case isFacetFieldDef(fd) && fd.Get("header").IsTruthy():
		return fd.Get("header")
	}
	return undef
}

// title is upstream's title(): the guide title, else the def's title, else
// (when includeDefault) the default formatted title.
func fieldTitle(cc *compileCtx, fod Value, config Value, allowDisabling, includeDefault bool) Value {
	guideTitle := getGuide(fod).Get("title")
	if !isFieldDef(cc, fod) {
		return coalesce(guideTitle, fod.Get("title"))
	}
	def := undef
	if includeDefault {
		def = defaultTitle(cc, fod, config)
	}
	if allowDisabling {
		return firstDefined(guideTitle, fod.Get("title"), def)
	}
	return coalesce(guideTitle, fod.Get("title"), def)
}

func getFormatMixins(fd Value) (format, formatType Value) {
	if isStringFieldOrDatumDef(fd) {
		return fd.Get("format"), fd.Get("formatType")
	}
	g := getGuide(fd)
	return g.Get("format"), g.Get("formatType")
}

// defaultType infers the type of a field def that lacks one.
func defaultType(fd Value, channel string) string {
	switch channel {
	case chLatitude, chLongitude:
		return "quantitative"
	case chRow, chColumn, chFacet, chShape, chStrokeDash:
		return "nominal"
	case chOrder:
		return "ordinal"
	}
	if isSortableFieldDef(fd) && fd.Get("sort").IsArr() {
		return "ordinal"
	}
	aggregate, bin, timeUnit := fd.Get("aggregate"), fd.Get("bin"), fd.Get("timeUnit")
	if timeUnit.IsTruthy() {
		return "temporal"
	}
	if bin.IsTruthy() || (aggregate.IsTruthy() && !isArgmaxDef(aggregate) && !isArgminDef(aggregate)) {
		return "quantitative"
	}
	if isScaleFieldDef(fd) {
		if st := fd.Get("scale").Get("type"); st.IsStr() {
			switch scaleCategory[st.StrValue()] {
			case "numeric", "discretizing":
				return "quantitative"
			case "time":
				return "temporal"
			}
		}
	}
	return "nominal"
}

func getFieldDef(cc *compileCtx, cd Value) Value {
	if isFieldDef(cc, cd) {
		return cd
	} else if hasConditionalFieldDef(cc, cd) {
		return cd.Get("condition")
	}
	return undef
}

func getFieldOrDatumDef(cc *compileCtx, cd Value) Value {
	if isFieldOrDatumDef(cc, cd) {
		return cd
	} else if hasConditionalFieldOrDatumDef(cc, cd) {
		return cd.Get("condition")
	}
	return undef
}

func isCustomFormatType(ft Value) bool {
	return ft.IsStr() && ft.StrValue() != "" && ft.StrValue() != "time" && ft.StrValue() != "number"
}

// initChannelDef normalizes a channel definition (upstream initChannelDef).
func initChannelDef(cc *compileCtx, cd Value, channel string, config Value, compositeMark bool) Value {
	if cd.IsStr() || cd.IsNum() || cd.IsBool() {
		return mkv("value", cd)
	}
	if isFieldOrDatumDef(cc, cd) {
		return initFieldOrDatumDef(cc, cd, channel, config, compositeMark)
	} else if hasConditionalFieldOrDatumDef(cc, cd) {
		o := cloneObj(cd.ObjValue())
		o.Set("condition", initFieldOrDatumDef(cc, cd.Get("condition"), channel, config, compositeMark))
		return jsval.Obj(o)
	}
	return cd
}

func initFieldOrDatumDef(cc *compileCtx, fd Value, channel string, config Value, compositeMark bool) Value {
	if isStringFieldOrDatumDef(fd) {
		formatType := fd.Get("formatType")
		if isCustomFormatType(formatType) && !config.Get("customFormatTypes").IsTruthy() {
			return initFieldOrDatumDef(cc, jsval.Obj(omit(fd, "format", "formatType")), channel, config, compositeMark)
		}
	} else {
		guideType := ""
		switch {
		case isPositionFieldOrDatumDef(fd):
			guideType = "axis"
		case isMarkPropFieldOrDatumDef(fd):
			guideType = "legend"
		case isFacetFieldDef(fd):
			guideType = "header"
		}
		if guideType != "" && fd.Get(guideType).IsTruthy() {
			g := fd.Get(guideType)
			if isCustomFormatType(g.Get("formatType")) && !config.Get("customFormatTypes").IsTruthy() {
				o := cloneObj(fd.ObjValue())
				o.Set(guideType, jsval.Obj(omit(g, "format", "formatType")))
				return initFieldOrDatumDef(cc, jsval.Obj(o), channel, config, compositeMark)
			}
		}
	}
	if isFieldDef(cc, fd) {
		return initFieldDef(cc, fd, channel, compositeMark)
	}
	return initDatumDef(fd)
}

func initDatumDef(dd Value) Value {
	if dd.Get("type").IsTruthy() {
		return dd
	}
	datum := dd.Get("datum")
	t := undef
	switch {
	case datum.IsNum():
		t = jsval.Str("quantitative")
	case datum.IsStr():
		t = jsval.Str("nominal")
	case isDateTime(datum):
		t = jsval.Str("temporal")
	}
	o := cloneObj(dd.ObjValue())
	o.Set("type", t)
	return jsval.Obj(o)
}

func isSortByChannel(c string) bool { return sortByChannel[c] }

func initFieldDef(cc *compileCtx, fd Value, channel string, compositeMark bool) Value {
	aggregate, timeUnit, bin, field := fd.Get("aggregate"), fd.Get("timeUnit"), fd.Get("bin"), fd.Get("field")
	fieldDef := cloneObj(fd.ObjValue())
	if !compositeMark && aggregate.IsTruthy() && !isAggregateOp(cc, aggregate) && !isArgmaxDef(aggregate) && !isArgminDef(aggregate) {
		fieldDef.Delete("aggregate")
	}
	if timeUnit.IsTruthy() {
		fieldDef.Set("timeUnit", normalizeTimeUnit(cc, timeUnit))
	}
	if field.IsTruthy() {
		fieldDef.Set("field", jsval.Str(field.AsString()))
	}
	if isBinning(bin) {
		fieldDef.Set("bin", normalizeBin(bin, channel))
	}
	fdv := jsval.Obj(fieldDef)
	if isTypedFieldDef(fdv) {
		t := channelDefType(fdv)
		if fullType := getFullName(t); t != fullType {
			if fullType == "" {
				fieldDef.Set("type", undef)
			} else {
				fieldDef.Set("type", jsval.Str(fullType))
			}
		}
		if t != "quantitative" && isCountingAggregateOp(aggregate) {
			fieldDef.Set("type", jsval.Str("quantitative"))
		}
	} else if !isSecondaryRangeChannel(channel) {
		fieldDef.Set("type", jsval.Str(defaultType(fdv, channel)))
	}
	if isSortableFieldDef(fdv) && fieldDef.Lookup("sort").IsStr() {
		sort := fieldDef.Lookup("sort").StrValue()
		if isSortByChannel(sort) {
			return jsval.Obj(spread(cloneObj(fieldDef), mkv("sort", mkv("encoding", sort))))
		}
		if len(sort) > 0 && sort[0] == '-' && isSortByChannel(sort[1:]) {
			return jsval.Obj(spread(cloneObj(fieldDef), mkv("sort", mkv("encoding", sort[1:], "order", "descending"))))
		}
	}
	if isFacetFieldDef(fdv) {
		header := fieldDef.Lookup("header")
		if header.IsTruthy() {
			if orient := header.Get("orient"); orient.IsTruthy() {
				rest := omit(header, "orient")
				rest.Set("labelOrient", or(header.Get("labelOrient"), orient))
				rest.Set("titleOrient", or(header.Get("titleOrient"), orient))
				return jsval.Obj(spread(cloneObj(fieldDef), mkv("header", objVal(rest))))
			}
		}
	}
	return jsval.Obj(fieldDef)
}

func isFieldOrDatumDefForTimeFormat(cc *compileCtx, fd Value) bool {
	_, ft := getFormatMixins(fd)
	return (ft.IsStr() && ft.StrValue() == "time") || (!ft.IsTruthy() && isTemporalFieldDef(cc, fd))
}

func isTemporalFieldDef(cc *compileCtx, def Value) bool {
	return def.IsTruthy() && ((def.Get("type").IsStr() && def.Get("type").StrValue() == "temporal") ||
		(isFieldDef(cc, def) && def.Get("timeUnit").IsTruthy()))
}

// valueExpr renders a value (possibly a date or signal) as an expression.
// undefinedIfExprNotRequired makes plain values answer ok=false.
func valueExpr(cc *compileCtx, v Value, timeUnit Value, typ string, wrapTime, undefinedIfExprNotRequired bool) (string, bool) {
	unit := ""
	if timeUnit.IsTruthy() {
		unit = normalizeTimeUnit(cc, timeUnit).Get("unit").AsString()
		if !normalizeTimeUnit(cc, timeUnit).Get("unit").IsTruthy() {
			unit = ""
		}
	}
	isTime := unit != "" || typ == "temporal"
	expr := ""
	have := false
	switch {
	case isExprRef(v):
		expr, have = v.Get("expr").AsString(), true
	case isSignalRef(v):
		expr, have = v.Get("signal").AsString(), true
	case isDateTime(v):
		isTime = true
		expr, have = dateTimeToExpr(v), true
	case v.IsStr() || v.IsNum():
		if isTime {
			expr, have = "datetime("+stringify(v)+")", true
			if isLocalSingleTimeUnit(unit) {
				// For a single timeUnit, dateTimeToExpr matches the number/string to the unit.
				if (v.IsNum() && v.NumValue() < 10000) || (v.IsStr() && dateParseNaN(v.StrValue())) {
					expr = dateTimeToExpr(mkv(unit, v))
				}
			}
		}
	}
	if have && expr != "" {
		if wrapTime && isTime {
			return "time(" + expr + ")", true
		}
		return expr, true
	}
	if undefinedIfExprNotRequired {
		return "", false
	}
	return stringify(v), true
}

// valueArray maps values to signals when they need an expression.
func valueArray(cc *compileCtx, fod Value, values []Value) []Value {
	typ := channelDefType(fod)
	out := make([]Value, len(values))
	for i, v := range values {
		tu := undef
		if isFieldDef(cc, fod) && !isBinnedTimeUnit(cc, fod.Get("timeUnit")) {
			tu = fod.Get("timeUnit")
		}
		if e, ok := valueExpr(cc, v, tu, typ, false, true); ok {
			out[i] = sig(e)
		} else {
			out[i] = v
		}
	}
	return out
}

func binRequiresRange(cc *compileCtx, fd Value, channel string) bool {
	if !isBinning(fd.Get("bin")) {
		return false
	}
	t := channelDefType(fd)
	return isScaleChannel(cc, channel) && (t == "ordinal" || t == "nominal")
}

// getBandPosition is upstream's getBandPosition; undefined when there is none.
func getBandPosition(cc *compileCtx, fd, fd2 Value, markDef, config Value) Value {
	if isFieldOrDatumDef(cc, fd) && !fd.Get("bandPosition").IsUndefined() {
		return fd.Get("bandPosition")
	}
	if isFieldDef(cc, fd) {
		timeUnit, bin := fd.Get("timeUnit"), fd.Get("bin")
		if timeUnit.IsTruthy() && !fd2.IsTruthy() {
			if cc.v5 && isRectBasedMark(cc, markDef.Get("type").AsString()) {
				return jsval.Int(0)
			}
			return getMarkConfig("timeUnitBandPosition", markDef, config, "")
		} else if isBinning(bin) {
			return jsval.Num(0.5)
		}
	}
	return undef
}

// getBandSize is upstream's getBandSize: a number, {band}, a signal or undefined.
func getBandSize(cc *compileCtx, channel string, fd, fd2 Value, markDef, config Value, scaleType string, useVlSizeChannel bool) Value {
	sizeChannel := getSizeChannel(channel)
	sizeProp := sizeChannel
	if useVlSizeChannel {
		sizeProp = "size"
	}
	if size := getMarkPropOrConfig(sizeProp, markDef, config, sizeChannel, false); !size.IsUndefined() {
		return size
	}
	if isFieldDef(cc, fd) {
		timeUnit, bin := fd.Get("timeUnit"), fd.Get("bin")
		if timeUnit.IsTruthy() && !fd2.IsTruthy() {
			return mkv("band", getMarkConfig("timeUnitBandSize", markDef, config, ""))
		} else if isBinning(bin) && !hasDiscreteDomain(scaleType) {
			return mkv("band", 1)
		}
	}
	markType := markDef.Get("type").AsString()
	if isRectBasedMark(cc, markType) {
		mc := config.Get(markType)
		if scaleType != "" {
			if hasDiscreteDomain(scaleType) {
				if d := mc.Get("discreteBandSize"); d.IsTruthy() {
					return d
				}
				return mkv("band", 1)
			}
			return mc.Get("continuousBandSize")
		}
		return mc.Get("discreteBandSize")
	}
	return undef
}

func hasBandEnd(cc *compileCtx, fd, fd2 Value, markDef, config Value) bool {
	if isBinning(fd.Get("bin")) || (fd.Get("timeUnit").IsTruthy() && isTypedFieldDef(fd) && channelDefType(fd) == "temporal") {
		return !getBandPosition(cc, fd, fd2, markDef, config).IsUndefined()
	}
	return false
}
