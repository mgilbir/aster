package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
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

// isDatumDefOf is isDatumDef as the version has it: 5.8 tests
// channelDef && 'datum' in channelDef.
func isDatumDefOf(cc *compileCtx, cd Value) bool {
	if cc.v5 {
		return cd.IsTruthy() && jsIn(cd, "datum")
	}
	return isDatumDef(cd)
}

func isNumericDataDef(cd Value) bool { return isDatumDef(cd) && cd.Get("datum").IsNum() }

func isFieldOrDatumDef(cc *compileCtx, cd Value) bool {
	if isFieldDef(cc, cd) {
		return true
	}
	// 5.8's isDatumDef holds for a datum key whose value is undefined, and
	// throws on a primitive.
	return isDatumDefOf(cc, cd)
}

func isTypedFieldDef(cd Value) bool {
	return cd.IsTruthy() && (hasProperty(cd, "field") || aggIs(cd, "count")) && hasProperty(cd, "type")
}

// isTypedFieldDefOf is isTypedFieldDef as the compiled version has it: 5.8
// tests `'field' in channelDef` and `'type' in channelDef`, so a def whose
// type an earlier normalization set to undefined (an invalid type) is still
// typed there, and isDiscrete then rejects its type.
func isTypedFieldDefOf(cc *compileCtx, cd Value) bool {
	if !cc.v5 {
		return isTypedFieldDef(cd)
	}
	return cd.IsTruthy() && (jsIn(cd, "field") || aggIs(cd, "count")) && jsIn(cd, "type")
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
	return (isTypedFieldDefOf(cc, cd) && !isDiscreteDef(cc, cd)) || isNumericDataDef(cd)
}

func isUnbinnedQuantitativeFieldOrDatumDef(cc *compileCtx, cd Value) bool {
	return (isTypedFieldDefOf(cc, cd) && cd.Get("type").IsStr() && cd.Get("type").StrValue() == "quantitative" && !cd.Get("bin").IsTruthy()) || isNumericDataDef(cd)
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
	// missing is "undefined" or "null" while `field` is that JavaScript
	// value: it reads as "" in the checks, as that word in a template string,
	// and a path function given it throws.
	missing := ""
	// pristine is true while `field` is still the value the spec gave. A
	// number or a boolean there has no length, which splitAccessPath loops
	// over, so the path functions read it as the empty path.
	pristine := false
	rawField := fd.Get("field")
	if f := rawField; !f.IsNullish() {
		field = f.AsString()
		pristine = !f.IsStr()
	} else if f.IsNull() {
		missing = "null"
	} else {
		missing = "undefined"
	}
	jsField := func() string {
		if missing != "" {
			return missing
		}
		return field
	}
	hasField := rawField.IsTruthy()
	suffix := opt.suffix
	argAccessor := ""
	if isCountDef(fd) {
		field, missing, pristine = internalField("count"), "", false
	} else {
		fn := ""
		// opSet: `if (fn)` of fn = fieldDef.op, the op as given (an empty
		// array is truthy and prints as nothing).
		opSet := false
		if !opt.nofn {
			// isOpFieldDef: hasProperty(fieldDef, 'op') in 6.4, 'op' in
			// fieldDef in 5.8 (a TypeError on a primitive).
			if (cc.v5 && jsIn(fd, "op")) || (!cc.v5 && hasProperty(fd, "op")) {
				if op := fd.Get("op"); op.IsTruthy() {
					fn, opSet = op.AsString(), true
				}
			} else {
				bin, aggregate, timeUnit := fd.Get("bin"), fd.Get("aggregate"), fd.Get("timeUnit")
				switch {
				case isBinning(bin):
					fn = binToString(cc, bin)
					suffix = opt.binSuffix + opt.suffix
				case aggregate.IsTruthy():
					if isArgmaxDef(cc, aggregate) {
						argAccessor = `["` + jsField() + `"]`
						field, missing, pristine = "argmax_"+argDefOf(cc, aggregate, "argmax").AsString(), "", false
						hasField = true
					} else if isArgminDef(cc, aggregate) {
						argAccessor = `["` + jsField() + `"]`
						field, missing, pristine = "argmin_"+argDefOf(cc, aggregate, "argmin").AsString(), "", false
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
		if fn != "" || opSet {
			if hasField {
				field = fn + "_" + field
			} else {
				field = fn
			}
			missing, pristine = "", false
		}
	}
	if suffix != "" {
		field, missing, pristine = jsField()+"_"+suffix, "", false
	}
	if opt.prefix != "" {
		field, missing, pristine = opt.prefix+"_"+jsField(), "", false
	}
	if missing != "" {
		// replacePathInField, removePathFromField and flatAccessWithDatum all
		// start with splitAccessPath(field).
		throw("Cannot read properties of %s (reading 'length')", missing)
	}
	if pristine {
		// The spec's own non-string value reaches the path functions.
		switch {
		case opt.forAs:
			return removePathFromFieldV(rawField)
		case opt.expr != "":
			return flatAccessWithDatumV(cc, rawField, opt.expr) + argAccessor
		}
		return replacePathInFieldV(rawField) + argAccessor
	}
	switch {
	case opt.forAs:
		return removePathFromField(field)
	case opt.expr != "":
		return flatAccessWithDatum(cc, field, opt.expr) + argAccessor
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
		if isArgmaxDef(cc, aggregate) {
			return jsval.Str(field.AsString() + " for max " + argDefOf(cc, aggregate, "argmax").AsString())
		} else if isArgminDef(cc, aggregate) {
			return jsval.Str(field.AsString() + " for min " + argDefOf(cc, aggregate, "argmin").AsString())
		}
		return jsval.Str(titleCaseV(aggregate) + " of " + field.AsString())
	}
	return field
}

func functionalTitleFormatter(cc *compileCtx, fd Value) Value {
	aggregate, bin, timeUnit, field := fd.Get("aggregate"), fd.Get("bin"), fd.Get("timeUnit"), fd.Get("field")
	if isArgmaxDef(cc, aggregate) {
		return jsval.Str(field.AsString() + " for argmax(" + argDefOf(cc, aggregate, "argmax").AsString() + ")")
	} else if isArgminDef(cc, aggregate) {
		return jsval.Str(field.AsString() + " for argmin(" + argDefOf(cc, aggregate, "argmin").AsString() + ")")
	}
	var tup Value
	if timeUnit.IsTruthy() && !isBinnedTimeUnit(cc, timeUnit) {
		tup = normalizeTimeUnit(cc, timeUnit)
	}
	// aggregate || unit || (maxbins && 'timeunit') || (isBinning(bin) &&
	// 'bin'), then fn.toUpperCase(): the aggregate as given, which only a
	// string can upper-case.
	var fn Value
	switch {
	case aggregate.IsTruthy():
		fn = aggregate
	case tup.Get("unit").IsTruthy():
		fn = tup.Get("unit")
	case tup.Get("maxbins").IsTruthy():
		fn = jsval.Str("timeunit")
	case isBinning(bin):
		fn = jsval.Str("bin")
	}
	if fn.IsTruthy() {
		if !fn.IsStr() {
			throw("fn.toUpperCase is not a function")
		}
		return jsval.Str(upper(fn.StrValue()) + "(" + field.AsString() + ")")
	}
	return field
}

func defaultTitle(cc *compileCtx, fd Value, config Value) Value {
	// switch (config.fieldTitle): strict, so ["plain"] is the default (C91
	// of the 2026-10-18 audit).
	switch s, _ := config.Get("fieldTitle").Strict(); s {
	case "plain":
		return jsGet(fd, "field")
	case "functional":
		if fd.IsNullish() {
			throw("Cannot destructure property 'aggregate' of 'fieldDef' as it is %s.", nullishName(fd))
		}
		return functionalTitleFormatter(cc, fd)
	}
	if fd.IsNullish() {
		// const {field, bin, timeUnit, aggregate} = fieldDef: a title list
		// with a null in it reaches here (C86's class: an array title
		// read element by element).
		throw("Cannot destructure property 'field' of 'fieldDef' as it is %s.", nullishName(fd))
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
func defaultType(cc *compileCtx, fd Value, channel string) string {
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
	if bin.IsTruthy() || (aggregate.IsTruthy() && !isArgmaxDef(cc, aggregate) && !isArgminDef(cc, aggregate)) {
		return "quantitative"
	}
	if isScaleFieldDef(fd) {
		// SCALE_CATEGORY_INDEX[fieldDef.scale.type]: the type as a key, so
		// ["log"] is numeric here (and no log scale after).
		if st := fd.Get("scale").Get("type"); st.IsTruthy() {
			switch scaleCategory[propKey(st)] {
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

// isCustomFormatType is `formatType && formatType !== 'number' &&
// formatType !== 'time'`: any truthy value but those two names.
func isCustomFormatType(ft Value) bool {
	return ft.IsTruthy() && !(ft.IsStr() && (ft.StrValue() == "time" || ft.StrValue() == "number"))
}

// initChannelDef normalizes a channel definition (upstream initChannelDef).
func initChannelDef(cc *compileCtx, cd Value, channel string, config Value, compositeMark bool) Value {
	if cd.IsStr() || cd.IsNum() || cd.IsBool() {
		typ := "boolean"
		switch {
		case cd.IsStr():
			typ = "string"
		case cd.IsNum():
			typ = "number"
		}
		cc.warn(msgPrimitiveChannelDef(cc, channel, typ, cd))
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
			cc.warn(msgCustomFormatTypeNotAllowed(channel))
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
				cc.warn(msgCustomFormatTypeNotAllowed(channel))
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

// isSortByChannel is `c in SORT_BY_CHANNEL_INDEX` in 5.8 (so an inherited
// name passes), hasOwnProperty in 6.4.
func isSortByChannel(cc *compileCtx, c string) bool {
	return sortByChannel[c] || (cc.v5 && isInheritedKey(c))
}

func initFieldDef(cc *compileCtx, fd Value, channel string, compositeMark bool) Value {
	aggregate, timeUnit, bin, field := fd.Get("aggregate"), fd.Get("timeUnit"), fd.Get("bin"), fd.Get("field")
	fieldDef := cloneObj(fd.ObjValue())
	if !compositeMark && aggregate.IsTruthy() && !isAggregateOp(cc, aggregate) && !isArgmaxDef(cc, aggregate) && !isArgminDef(cc, aggregate) {
		cc.warn(msgInvalidAggregate(aggregate))
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
	if isBinned(bin) && !isXorY(channel) {
		cc.warn(msgChannelShouldNotBeUsedForBinned(channel))
	}
	fdv := jsval.Obj(fieldDef)
	if isTypedFieldDefOf(cc, fdv) {
		t := channelDefType(fdv)
		// getFullName(type): a truthy type is lower-cased (a TypeError when
		// it is not a string), a falsy one is undefined; the type is
		// replaced unless it is already the full name.
		tv := fieldDef.Lookup("type")
		fullType := ""
		if tv.IsTruthy() {
			if !tv.IsStr() {
				throw("type.toLowerCase is not a function")
			}
			fullType = getFullName(t)
		}
		if !tv.IsStr() || t != fullType || fullType == "" {
			if fullType == "" {
				fieldDef.Set("type", undef)
			} else {
				fieldDef.Set("type", jsval.Str(fullType))
			}
		}
		if !(tv.IsStr() && t == "quantitative") && isCountingAggregateOp(aggregate) {
			cc.warn(msgInvalidFieldTypeForCountAggregate(tv, aggregate))
			fieldDef.Set("type", jsval.Str("quantitative"))
		}
	} else if !isSecondaryRangeChannel(channel) {
		fieldDef.Set("type", jsval.Str(defaultType(cc, fdv, channel)))
	}
	if isTypedFieldDefOf(cc, fdv) {
		if warning := channelCompatibilityWarning(cc, fdv, channel); warning != "" {
			cc.warn(warning)
		}
	}
	if isSortableFieldDef(fdv) && fieldDef.Lookup("sort").IsStr() {
		sort := fieldDef.Lookup("sort").StrValue()
		if isSortByChannel(cc, sort) {
			return jsval.Obj(spread(cloneObj(fieldDef), mkv("sort", mkv("encoding", sort))))
		}
		if len(sort) > 0 && sort[0] == '-' && isSortByChannel(cc, sort[1:]) {
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

// valueExpr renders a value (possibly a date or signal) as an expression:
// an expression or signal reference's own expr or signal as given (upstream
// tests it for truth and returns it, so {signal: ["a"]} gives ["a"] and
// {expr: 0} the value's stringify), a date's expression, or stringify(v);
// undefined when undefinedIfExprNotRequired and v needs no expression, or
// when v has no JSON text.
func valueExpr(cc *compileCtx, v Value, timeUnit Value, typ string, wrapTime, undefinedIfExprNotRequired bool) Value {
	unit := ""
	if timeUnit.IsTruthy() {
		unit = normalizeTimeUnit(cc, timeUnit).Get("unit").StrValue() // a string, or normalizeTimeUnit throws
	}
	isTime := unit != "" || typ == "temporal"
	expr := undef
	switch {
	case isExprRef(cc, v):
		expr = v.Get("expr")
	case isSignalRef(cc, v):
		expr = v.Get("signal")
	case isDateTime(v):
		isTime = true
		expr = jsval.Str(dateTimeToExpr(cc, v))
	case v.IsStr() || v.IsNum():
		if isTime {
			expr = jsval.Str("datetime(" + stringify(cc, v) + ")")
			if isLocalSingleTimeUnit(cc, unit) {
				// For a single timeUnit, dateTimeToExpr matches the number/string to the unit.
				if (v.IsNum() && v.NumValue() < 10000) || (v.IsStr() && dateParseNaN(v.StrValue())) {
					expr = jsval.Str(dateTimeToExpr(cc, mkv(unit, v)))
				}
			}
		}
	}
	if expr.IsTruthy() {
		if wrapTime && isTime {
			return jsval.Str("time(" + tmpl(expr) + ")")
		}
		return expr
	}
	if undefinedIfExprNotRequired {
		return undef
	}
	if v.IsUndefined() {
		return undef // stringify(undefined)
	}
	return jsval.Str(stringify(cc, v))
}

// valueArray maps values to signals when they need an expression.
func valueArray(cc *compileCtx, fod Value, values []Value) []Value {
	typ := channelDefType(fod)
	out := make([]Value, len(values))
	for i, v := range values {
		cc.work(1)
		tu := undef
		if isFieldDef(cc, fod) && !isBinnedTimeUnit(cc, fod.Get("timeUnit")) {
			tu = fod.Get("timeUnit")
		}
		if e := valueExpr(cc, v, tu, typ, false, true); !e.IsUndefined() {
			out[i] = mkv("signal", e)
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
			if cc.v5 && isRectBasedMark(cc, markDef.Get("type")) {
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
func getBandSize(cc *compileCtx, channel string, fd, fd2 Value, markDef, config Value, scaleType Value, useVlSizeChannel bool) Value {
	sizeChannel := getSizeChannel(channel)
	// getMarkPropOrConfig(useVlSizeChannel ? 'size' : sizeChannel, mark,
	// config, {vgChannel: sizeChannel}): the property is read with the key
	// "undefined" for a polar channel, whose undefined vgChannel is none.
	sizeProp := jsKey(sizeChannel)
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
	markType := markDef.Get("type")
	if isRectBasedMark(cc, markType) {
		mc := config.Get(markType.StrValue()) // a string: isRectBasedMark is strict
		if scaleType.IsTruthy() {
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
	if isBinning(fd.Get("bin")) || (fd.Get("timeUnit").IsTruthy() && isTypedFieldDefOf(cc, fd) && channelDefType(fd) == "temporal") {
		return !getBandPosition(cc, fd, fd2, markDef, config).IsUndefined()
	}
	return false
}

// channelCompatibilityWarning is upstream's channelCompatibility: the warning
// for a typed field def a channel should not be used with, or "".
func channelCompatibilityWarning(cc *compileCtx, fd Value, channel string) string {
	typ := fd.Get("type")
	isType := func(t string) bool { return typ.IsStr() && typ.StrValue() == t }
	if isType("geojson") && channel != chShape {
		return "Channel " + channel + " should not be used with a geojson data."
	}
	switch channel {
	case chRow, chColumn, chFacet:
		if !isDiscreteDef(cc, fd) {
			return msgChannelShouldBeDiscrete(channel)
		}
	case chLongitude, chLongitude2, chLatitude, chLatitude2:
		if !isType("quantitative") {
			return "Channel " + channel + " should be used with a quantitative field only, not " + tmpl(typ) + " field."
		}
	case chOpacity, chFillOpacity, chStrokeOpacity, chStrokeWidth, chSize, chTheta2, chRadius2, chX2, chY2, chTime:
		if channel == chTime && cc.v5 {
			return ""
		}
		if isType("nominal") && !fd.Get("sort").IsTruthy() {
			return "Channel " + channel + " should not be used with an unsorted discrete field."
		}
	case chShape, chStrokeDash:
		if !isDiscreteDef(cc, fd) && !(isScaleFieldDef(fd) && isContinuousToDiscrete(fd.Get("scale").Get("type"))) {
			return msgChannelShouldBeDiscreteOrDiscretizing(channel)
		}
	case chOrder:
		if isType("nominal") && !fd.ObjValue().Has("sort") {
			return "Channel order is inappropriate for nominal field, which has no inherent order."
		}
	}
	return ""
}
