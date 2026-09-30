package vegalite

import (
	"fmt"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Composite marks — vega-lite/src/compositemark/*. Boxplot, errorbar and
// errorband expand into layers of primitive marks plus aggregate transforms.

type compositeNormalizer struct {
	name string
	fn   func(spec Value, config Value) Value
}

func (c compositeNormalizer) hasMatchingType(spec Value, _ Value) bool {
	return isUnitSpec(spec) && getMarkType(spec.Get("mark")) == c.name
}

func (c compositeNormalizer) run(spec Value, p *normParams, _ func(Value, *normParams) Value) Value {
	return c.fn(spec, p.config)
}

// jsEscape is the legacy global escape(): everything but A-Z a-z 0-9 @*_+-./
// becomes %XX (or %uXXXX above U+00FF).
func jsEscape(s string) string {
	var b strings.Builder
	for _, u := range utf16Len(s) {
		switch {
		case u < 128 && (u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || strings.ContainsRune("@*_+-./", rune(u))):
			b.WriteByte(byte(u))
		case u < 256:
			fmt.Fprintf(&b, "%%%02X", u)
		default:
			fmt.Fprintf(&b, "%%u%04X", u)
		}
	}
	return b.String()
}

type tooltipSummaryItem struct {
	fieldPrefix string
	titlePrefix Value
}

func compositeTitle(def Value) Value { return firstDefined(def.Get("title"), def.Get("field")) }

func filterTooltipWithAggregatedField(oldEncoding Value) (customWithout Value, filtered *Object) {
	filtered = omit(oldEncoding, "tooltip")
	tooltip := oldEncoding.Get("tooltip")
	if !tooltip.IsTruthy() {
		return undef, filtered
	}
	var with, without []Value
	haveWith, haveWithout := false, false
	if tooltip.IsArr() {
		for _, t := range tooltip.Items() {
			if t.Get("aggregate").IsTruthy() {
				haveWith = true
				with = append(with, t)
			} else {
				haveWithout = true
				without = append(without, t)
			}
		}
		if haveWith {
			filtered.Set("tooltip", jsval.Arr(with))
		}
		if haveWithout {
			customWithout = jsval.Arr(without)
		}
	} else {
		if tooltip.Get("aggregate").IsTruthy() {
			filtered.Set("tooltip", tooltip)
		} else {
			customWithout = tooltip
		}
	}
	if customWithout.IsArr() && customWithout.Len() == 1 {
		customWithout = customWithout.Index(0)
	}
	return customWithout, filtered
}

func getCompositeMarkTooltip(summary []tooltipSummaryItem, continuousAxisChannelDef Value, encodingWithoutContinuousAxis Value, withFieldName bool) Value {
	if encodingWithoutContinuousAxis.IsObj() && encodingWithoutContinuousAxis.ObjValue().Has("tooltip") {
		return mkv("tooltip", encodingWithoutContinuousAxis.Get("tooltip"))
	}
	var items []Value
	for _, s := range summary {
		mainTitle := ""
		if withFieldName {
			mainTitle = " of " + compositeTitle(continuousAxisChannelDef).AsString()
		}
		var title Value
		if isSignalRef(s.titlePrefix) {
			title = mkv("signal", signalOf(s.titlePrefix)+`"`+jsEscape(mainTitle)+`"`)
		} else {
			title = jsval.Str(s.titlePrefix.AsString() + mainTitle)
		}
		items = append(items, mkv(
			"field", s.fieldPrefix+continuousAxisChannelDef.Get("field").AsString(),
			"type", continuousAxisChannelDef.Get("type"),
			"title", title,
		))
	}
	seen := map[string]bool{}
	for _, fd := range fieldDefsOf(encodingWithoutContinuousAxis) {
		sfd := toStringFieldDef(fd)
		h := hashOf(sfd)
		if !seen[h] {
			seen[h] = true
			items = append(items, sfd)
		}
	}
	return mkv("tooltip", jsval.Arr(items))
}

type partFactory func(partName string, mark Value, positionPrefix, endPositionPrefix string, extraEncoding Value) []Value

func makeCompositeAggregatePartFactory(compositeMarkDef Value, continuousAxis string, continuousAxisChannelDef Value, sharedEncoding Value, compositeMarkConfig Value) partFactory {
	scale, axis := continuousAxisChannelDef.Get("scale"), continuousAxisChannelDef.Get("axis")
	return func(partName string, mark Value, positionPrefix, endPositionPrefix string, extraEncoding Value) []Value {
		title := compositeTitle(continuousAxisChannelDef)
		axisDef := mk(
			"field", positionPrefix+"_"+continuousAxisChannelDef.Get("field").AsString(),
			"type", continuousAxisChannelDef.Get("type"),
		)
		if !title.IsUndefined() {
			axisDef.Set("title", title)
		}
		if !scale.IsUndefined() {
			axisDef.Set("scale", scale)
		}
		if !axis.IsUndefined() {
			axisDef.Set("axis", axis)
		}
		enc := jsval.NewObject(8)
		enc.Set(continuousAxis, jsval.Obj(axisDef))
		if endPositionPrefix != "" {
			enc.Set(continuousAxis+"2", mkv("field", endPositionPrefix+"_"+continuousAxisChannelDef.Get("field").AsString()))
		}
		spread(enc, sharedEncoding)
		spread(enc, extraEncoding)
		return partLayerMixins(compositeMarkDef, partName, compositeMarkConfig, mkv("mark", mark, "encoding", jsval.Obj(enc)))
	}
}

func partLayerMixins(markDef Value, part string, compositeMarkConfig Value, partBaseSpec Value) []Value {
	clip, color, opacity := markDef.Get("clip"), markDef.Get("color"), markDef.Get("opacity")
	mark := markDef.Get("type").AsString()
	if markDef.Get(part).IsTruthy() || (markDef.Get(part).IsUndefined() && compositeMarkConfig.Get(part).IsTruthy()) {
		m := jsval.NewObject(8)
		spread(m, compositeMarkConfig.Get(part))
		if clip.IsTruthy() {
			m.Set("clip", clip)
		}
		if color.IsTruthy() {
			m.Set("color", color)
		}
		if opacity.IsTruthy() {
			m.Set("opacity", opacity)
		}
		spread(m, markDefOf(partBaseSpec.Get("mark")))
		m.Set("style", jsval.Str(mark+"-"+part))
		if !markDef.Get(part).IsBool() {
			spread(m, markDef.Get(part))
		}
		s := cloneObj(partBaseSpec.ObjValue())
		s.Set("mark", jsval.Obj(m))
		return []Value{jsval.Obj(s)}
	}
	return nil
}

func filterAggregateFromChannelDef(def Value, compositeMark string) Value {
	if def.Get("aggregate").IsTruthy() {
		return jsval.Obj(omit(def, "aggregate"))
	}
	return def
}

type continuousAxisInfo struct {
	def, def2, defError, defError2 Value
	axis                           string
}

func compositeMarkContinuousAxis(spec Value, orient, compositeMark string) continuousAxisInfo {
	encoding := spec.Get("encoding")
	axis := chY
	if orient != "vertical" {
		axis = chX
	}
	return continuousAxisInfo{
		def:       filterAggregateFromChannelDef(encoding.Get(axis), compositeMark),
		def2:      filterAggregateFromChannelDef(encoding.Get(axis+"2"), compositeMark),
		defError:  filterAggregateFromChannelDef(encoding.Get(axis+"Error"), compositeMark),
		defError2: filterAggregateFromChannelDef(encoding.Get(axis+"Error2"), compositeMark),
		axis:      axis,
	}
}

func compositeMarkOrient(spec Value, compositeMark string) string {
	mark, encoding := spec.Get("mark"), spec.Get("encoding")
	x, y := encoding.Get("x"), encoding.Get("y")
	if isMarkDef(mark) && mark.Get("orient").IsTruthy() {
		return mark.Get("orient").AsString()
	}
	if isContinuousFieldOrDatumDef(x) {
		if isContinuousFieldOrDatumDef(y) {
			xAgg, yAgg := "", ""
			if isFieldDef(x) {
				xAgg = x.Get("aggregate").AsString()
			}
			if isFieldDef(y) {
				yAgg = y.Get("aggregate").AsString()
			}
			if x.Get("aggregate").IsUndefined() {
				xAgg = ""
			}
			if y.Get("aggregate").IsUndefined() {
				yAgg = ""
			}
			switch {
			case xAgg == "" && yAgg == compositeMark:
				return "vertical"
			case yAgg == "" && xAgg == compositeMark:
				return "horizontal"
			case xAgg == compositeMark && yAgg == compositeMark:
				throw("Both x and y cannot have aggregate")
			}
			if isFieldOrDatumDefForTimeFormat(y) && !isFieldOrDatumDefForTimeFormat(x) {
				return "horizontal"
			}
			return "vertical"
		}
		return "horizontal"
	} else if isContinuousFieldOrDatumDef(y) {
		return "vertical"
	}
	throw("Need a valid continuous axis for %ss", compositeMark)
	return ""
}

// ---- boxplot ----

func boxParamsQuartiles(field string) []Value {
	aliased := removePathFromField(field)
	return []Value{
		mkv("op", "q1", "field", field, "as", "lower_box_"+aliased),
		mkv("op", "q3", "field", field, "as", "upper_box_"+aliased),
	}
}

func normalizeBoxPlot(spec Value, config Value) Value {
	so := cloneObj(spec.ObjValue())
	so.Set("encoding", normalizeEncoding(spec.Get("encoding"), config))
	spec = jsval.Obj(so)
	mark, params := spec.Get("mark"), spec.Get("params")
	_ = params
	outerSpec := omit(spec, "mark", "encoding", "params", "projection")
	markDef := markDefOf(mark)
	extent := coalesce(markDef.Get("extent"), config.Get("boxplot").Get("extent"))
	sizeValue := getMarkPropOrConfigSimple("size", markDef, config)
	invalid := markDef.Get("invalid")
	boxPlotType := extent
	if extent.IsNum() {
		boxPlotType = jsval.Str("tukey")
	}
	isMinMax := boxPlotType.IsStr() && boxPlotType.StrValue() == "min-max"
	isTukey := boxPlotType.IsStr() && boxPlotType.StrValue() == "tukey"
	bp := boxParams(spec, extent, config)
	contField := bp.continuousAxisChannelDef.Get("field").AsString()
	aliasedFieldName := removePathFromField(contField)
	color, size := bp.encodingWithoutContinuousAxis.Get("color"), bp.encodingWithoutContinuousAxis.Get("size")
	encodingWithoutSizeColorAndContinuousAxis := omit(bp.encodingWithoutContinuousAxis, "color", "size")
	makePart := func(shared Value) partFactory {
		return makeCompositeAggregatePartFactory(markDef, bp.continuousAxis, bp.continuousAxisChannelDef, shared, config.Get("boxplot"))
	}
	makeExtent := makePart(jsval.Obj(encodingWithoutSizeColorAndContinuousAxis))
	makeBox := makePart(bp.encodingWithoutContinuousAxis)
	var defaultBoxColor Value
	if box := config.Get("boxplot").Get("box"); isObject(box) {
		defaultBoxColor = box.Get("color")
	} else {
		defaultBoxColor = config.Get("mark").Get("color")
	}
	defaultBoxColor = or(defaultBoxColor, jsval.Str("#4c78a8"))
	midEnc := cloneObj(encodingWithoutSizeColorAndContinuousAxis)
	if size.IsTruthy() {
		midEnc.Set("size", size)
	}
	cond := jsval.NewObject(4)
	cond.Set("test", jsval.Str(accessWithDatumToUnescapedPath("lower_box_"+contField)+" >= "+accessWithDatumToUnescapedPath("upper_box_"+contField)))
	if color.IsTruthy() {
		spread(cond, color)
	} else {
		spread(cond, mkv("value", defaultBoxColor))
	}
	midEnc.Set("color", mkv("condition", jsval.Obj(cond)))
	makeMidTick := makePart(jsval.Obj(midEnc))

	maxPrefix, minPrefix := "max_", "min_"
	if isMinMax {
		maxPrefix, minPrefix = "upper_whisker_", "lower_whisker_"
	}
	fiveSummary := getCompositeMarkTooltip([]tooltipSummaryItem{
		{maxPrefix, jsval.Str("Max")},
		{"upper_box_", jsval.Str("Q3")},
		{"mid_box_", jsval.Str("Median")},
		{"lower_box_", jsval.Str("Q1")},
		{minPrefix, jsval.Str("Min")},
	}, bp.continuousAxisChannelDef, bp.encodingWithoutContinuousAxis, true)
	endTick := mkv("type", "tick", "color", "black", "opacity", 1, "orient", bp.ticksOrient, "invalid", invalid, "aria", false)
	whiskerTooltip := fiveSummary
	if !isMinMax {
		whiskerTooltip = getCompositeMarkTooltip([]tooltipSummaryItem{
			{"upper_whisker_", jsval.Str("Upper Whisker")},
			{"lower_whisker_", jsval.Str("Lower Whisker")},
		}, bp.continuousAxisChannelDef, bp.encodingWithoutContinuousAxis, true)
	}
	ruleMark := func() Value { return mkv("type", "rule", "invalid", invalid, "aria", false) }
	var whiskerLayers []Value
	whiskerLayers = append(whiskerLayers, makeExtent("rule", ruleMark(), "lower_whisker", "lower_box", whiskerTooltip)...)
	whiskerLayers = append(whiskerLayers, makeExtent("rule", ruleMark(), "upper_box", "upper_whisker", whiskerTooltip)...)
	whiskerLayers = append(whiskerLayers, makeExtent("ticks", endTick, "lower_whisker", "", whiskerTooltip)...)
	whiskerLayers = append(whiskerLayers, makeExtent("ticks", endTick, "upper_whisker", "", whiskerTooltip)...)

	var boxLayers []Value
	if !isTukey {
		boxLayers = append(boxLayers, whiskerLayers...)
	}
	boxMark := mk("type", "bar")
	if sizeValue.IsTruthy() {
		boxMark.Set("size", sizeValue)
	}
	boxMark.Set("orient", jsval.Str(bp.boxOrient))
	boxMark.Set("invalid", invalid)
	boxMark.Set("ariaRoleDescription", jsval.Str("box"))
	boxLayers = append(boxLayers, makeBox("box", jsval.Obj(boxMark), "lower_box", "upper_box", fiveSummary)...)
	medianMark := mk("type", "tick", "invalid", invalid)
	if med := config.Get("boxplot").Get("median"); isObject(med) && med.Get("color").IsTruthy() {
		medianMark.Set("color", med.Get("color"))
	}
	if sizeValue.IsTruthy() {
		medianMark.Set("size", sizeValue)
	}
	medianMark.Set("orient", jsval.Str(bp.ticksOrient))
	medianMark.Set("aria", jsval.False)
	boxLayers = append(boxLayers, makeMidTick("median", jsval.Obj(medianMark), "mid_box", "", fiveSummary)...)

	if isMinMax {
		out := cloneObj(outerSpec)
		out.Set("transform", jsval.Arr(appendVals(arrayOf(outerSpec.Lookup("transform")), bp.transform...)))
		out.Set("layer", jsval.Arr(boxLayers))
		return jsval.Obj(out)
	}
	lowerBoxExpr := accessWithDatumToUnescapedPath("lower_box_" + contField)
	upperBoxExpr := accessWithDatumToUnescapedPath("upper_box_" + contField)
	iqrExpr := "(" + upperBoxExpr + " - " + lowerBoxExpr + ")"
	extentStr := extent.AsString()
	lowerWhiskerExpr := lowerBoxExpr + " - " + extentStr + " * " + iqrExpr
	upperWhiskerExpr := upperBoxExpr + " + " + extentStr + " * " + iqrExpr
	fieldExpr := accessWithDatumToUnescapedPath(contField)
	joinaggregate := mkv("joinaggregate", jsval.Arr(boxParamsQuartiles(contField)), "groupby", jsval.Arr(bp.groupby))
	aggs := []Value{
		mkv("op", "min", "field", contField, "as", "lower_whisker_"+aliasedFieldName),
		mkv("op", "max", "field", contField, "as", "upper_whisker_"+aliasedFieldName),
		mkv("op", "min", "field", "lower_box_"+contField, "as", "lower_box_"+aliasedFieldName),
		mkv("op", "max", "field", "upper_box_"+contField, "as", "upper_box_"+aliasedFieldName),
	}
	aggs = append(aggs, bp.aggregate...)
	filteredWhiskerSpec := mk(
		"transform", arr(
			mkv("filter", "("+lowerWhiskerExpr+" <= "+fieldExpr+") && ("+fieldExpr+" <= "+upperWhiskerExpr+")"),
			mkv("aggregate", jsval.Arr(aggs), "groupby", jsval.Arr(bp.groupby)),
		),
		"layer", jsval.Arr(whiskerLayers),
	)
	encNoTooltip := omit(jsval.Obj(encodingWithoutSizeColorAndContinuousAxis), "tooltip")
	scale, axis := bp.continuousAxisChannelDef.Get("scale"), bp.continuousAxisChannelDef.Get("axis")
	title := compositeTitle(bp.continuousAxisChannelDef)
	outEnc := jsval.NewObject(8)
	cd := mk("field", contField, "type", bp.continuousAxisChannelDef.Get("type"))
	if !title.IsUndefined() {
		cd.Set("title", title)
	}
	if !scale.IsUndefined() {
		cd.Set("scale", scale)
	}
	if !axis.IsUndefined() {
		cd.Set("axis", axis)
	}
	outEnc.Set(bp.continuousAxis, jsval.Obj(cd))
	spread(outEnc, jsval.Obj(encNoTooltip))
	if color.IsTruthy() {
		outEnc.Set("color", color)
	}
	if bp.customTooltipWithoutAggregatedField.IsTruthy() {
		outEnc.Set("tooltip", bp.customTooltipWithoutAggregatedField)
	}
	outlierList := partLayerMixins(markDef, "outliers", config.Get("boxplot"), mkv(
		"transform", arr(mkv("filter", "("+fieldExpr+" < "+lowerWhiskerExpr+") || ("+fieldExpr+" > "+upperWhiskerExpr+")")),
		"mark", "point",
		"encoding", jsval.Obj(outEnc),
	))
	filteredTransforms := append(append(append([]Value{}, bp.bins...), bp.timeUnits...), joinaggregate)
	var filteredLayers Value
	if len(outlierList) > 0 {
		filteredLayers = mkv("transform", jsval.Arr(filteredTransforms), "layer", arr(outlierList[0], jsval.Obj(filteredWhiskerSpec)))
	} else {
		tr := append(append([]Value{}, filteredTransforms...), filteredWhiskerSpec.Lookup("transform").Items()...)
		filteredWhiskerSpec.Set("transform", jsval.Arr(tr))
		filteredLayers = jsval.Obj(filteredWhiskerSpec)
	}
	out := cloneObj(outerSpec)
	out.Set("layer", arr(filteredLayers, mkv("transform", jsval.Arr(bp.transform), "layer", jsval.Arr(boxLayers))))
	return jsval.Obj(out)
}

type boxParamsResult struct {
	bins, timeUnits, transform, groupby, aggregate []Value
	continuousAxisChannelDef                       Value
	continuousAxis                                 string
	encodingWithoutContinuousAxis                  Value
	ticksOrient, boxOrient                         string
	customTooltipWithoutAggregatedField            Value
}

func boxParams(spec Value, extent Value, config Value) boxParamsResult {
	orient := compositeMarkOrient(spec, "boxplot")
	cai := compositeMarkContinuousAxis(spec, orient, "boxplot")
	contField := cai.def.Get("field").AsString()
	aliased := removePathFromField(contField)
	boxPlotType := extent
	if extent.IsNum() {
		boxPlotType = jsval.Str("tukey")
	}
	isMinMax := boxPlotType.IsStr() && boxPlotType.StrValue() == "min-max"
	isTukey := boxPlotType.IsStr() && boxPlotType.StrValue() == "tukey"
	minAs, maxAs := "min_", "max_"
	if isMinMax {
		minAs, maxAs = "lower_whisker_", "upper_whisker_"
	}
	specific := append(boxParamsQuartiles(contField),
		mkv("op", "median", "field", contField, "as", "mid_box_"+aliased),
		mkv("op", "min", "field", contField, "as", minAs+aliased),
		mkv("op", "max", "field", contField, "as", maxAs+aliased),
	)
	var post []Value
	if !isMinMax && !isTukey {
		ext := extent.AsString()
		up, lo := accessWithDatumToUnescapedPath("upper_box_"+aliased), accessWithDatumToUnescapedPath("lower_box_"+aliased)
		iqr := accessWithDatumToUnescapedPath("iqr_" + aliased)
		post = []Value{
			mkv("calculate", up+" - "+lo, "as", "iqr_"+aliased),
			mkv("calculate", "min("+up+" + "+iqr+" * "+ext+", "+accessWithDatumToUnescapedPath("max_"+aliased)+")", "as", "upper_whisker_"+aliased),
			mkv("calculate", "max("+lo+" - "+iqr+" * "+ext+", "+accessWithDatumToUnescapedPath("min_"+aliased)+")", "as", "lower_whisker_"+aliased),
		}
	}
	oldEncodingWithout := omit(spec.Get("encoding"), cai.axis)
	customTooltip, filteredEncoding := filterTooltipWithAggregatedField(jsval.Obj(oldEncodingWithout))
	ex := extractTransformsFromEncoding(jsval.Obj(filteredEncoding), config)
	ticksOrient := "vertical"
	if orient == "vertical" {
		ticksOrient = "horizontal"
	}
	transform := append(append([]Value{}, ex.bins...), ex.timeUnits...)
	transform = append(transform, mkv("aggregate", jsval.Arr(append(append([]Value{}, ex.aggregate...), specific...)), "groupby", jsval.Arr(ex.groupby)))
	transform = append(transform, post...)
	return boxParamsResult{
		bins: ex.bins, timeUnits: ex.timeUnits, transform: transform, groupby: ex.groupby, aggregate: ex.aggregate,
		continuousAxisChannelDef: cai.def, continuousAxis: cai.axis,
		encodingWithoutContinuousAxis: jsval.Obj(ex.encoding),
		ticksOrient:                   ticksOrient, boxOrient: orient, customTooltipWithoutAggregatedField: customTooltip,
	}
}

// ---- errorbar ----

func isFOD(v Value) bool { return isFieldOrDatumDef(v) }

func errorBarOrientAndInputType(spec Value, compositeMark string) (orient, inputType string) {
	encoding := spec.Get("encoding")
	if (isFOD(encoding.Get("x")) || isFOD(encoding.Get("y"))) && !isFOD(encoding.Get("x2")) && !isFOD(encoding.Get("y2")) &&
		!isFOD(encoding.Get("xError")) && !isFOD(encoding.Get("xError2")) && !isFOD(encoding.Get("yError")) && !isFOD(encoding.Get("yError2")) {
		return compositeMarkOrient(spec, compositeMark), "raw"
	}
	isUpperLower := isFOD(encoding.Get("x2")) || isFOD(encoding.Get("y2"))
	isError := isFOD(encoding.Get("xError")) || isFOD(encoding.Get("xError2")) || isFOD(encoding.Get("yError")) || isFOD(encoding.Get("yError2"))
	x, y := encoding.Get("x"), encoding.Get("y")
	if isUpperLower {
		if isError {
			throw("%s cannot be both type aggregated-upper-lower and aggregated-error", compositeMark)
		}
		x2, y2 := encoding.Get("x2"), encoding.Get("y2")
		switch {
		case isFOD(x2) && isFOD(y2):
			throw("%s cannot have both x2 and y2", compositeMark)
		case isFOD(x2):
			if isContinuousFieldOrDatumDef(x) {
				return "horizontal", "aggregated-upper-lower"
			}
			throw("Both x and x2 have to be quantitative in %s", compositeMark)
		case isFOD(y2):
			if isContinuousFieldOrDatumDef(y) {
				return "vertical", "aggregated-upper-lower"
			}
			throw("Both y and y2 have to be quantitative in %s", compositeMark)
		}
		throw("No ranged axis")
	}
	xError, xError2, yError, yError2 := encoding.Get("xError"), encoding.Get("xError2"), encoding.Get("yError"), encoding.Get("yError2")
	if isFOD(xError2) && !isFOD(xError) {
		throw("%s cannot have xError2 without xError", compositeMark)
	}
	if isFOD(yError2) && !isFOD(yError) {
		throw("%s cannot have yError2 without yError", compositeMark)
	}
	switch {
	case isFOD(xError) && isFOD(yError):
		throw("%s cannot have both xError and yError with both are quantiative", compositeMark)
	case isFOD(xError):
		if isContinuousFieldOrDatumDef(x) {
			return "horizontal", "aggregated-error"
		}
		throw("All x, xError, and xError2 (if exist) have to be quantitative")
	case isFOD(yError):
		if isContinuousFieldOrDatumDef(y) {
			return "vertical", "aggregated-error"
		}
		throw("All y, yError, and yError2 (if exist) have to be quantitative")
	}
	throw("No ranged axis")
	return "", ""
}

type errorBarParamsResult struct {
	transform                     []Value
	continuousAxisChannelDef      Value
	continuousAxis                string
	encodingWithoutContinuousAxis *Object
	ticksOrient                   string
	markDef                       Value
	outerSpec                     *Object
	tooltipEncoding               Value
}

func errorBarParams(spec Value, compositeMark string, config Value) errorBarParamsResult {
	mark, encoding := spec.Get("mark"), spec.Get("encoding")
	outerSpec := omit(spec, "mark", "encoding", "params", "projection")
	markDef := markDefOf(mark)
	orient, inputType := errorBarOrientAndInputType(spec, compositeMark)
	cai := compositeMarkContinuousAxis(spec, orient, compositeMark)
	agg, post, tooltipSummary, tooltipTitleWithFieldName := errorBarAggregationAndCalculation(markDef, cai.def, cai.def2, cai.defError, cai.defError2, inputType, compositeMark, config)
	var axis2, err, err2 string
	if cai.axis == "x" {
		axis2, err, err2 = "x2", "xError", "xError2"
	} else {
		axis2, err, err2 = "y2", "yError", "yError2"
	}
	oldWithout := omit(encoding, cai.axis, axis2, err, err2)
	ex := extractTransformsFromEncoding(jsval.Obj(oldWithout), config)
	aggregate := append(append([]Value{}, ex.aggregate...), agg...)
	groupby := ex.groupby
	if inputType != "raw" {
		groupby = nil
	}
	tooltipEncoding := getCompositeMarkTooltip(tooltipSummary, cai.def, jsval.Obj(ex.encoding), tooltipTitleWithFieldName)
	transform := append([]Value{}, arrayOf(outerSpec.Lookup("transform"))...)
	transform = append(transform, ex.bins...)
	transform = append(transform, ex.timeUnits...)
	if len(aggregate) > 0 {
		transform = append(transform, mkv("aggregate", jsval.Arr(aggregate), "groupby", jsval.Arr(groupby)))
	}
	transform = append(transform, post...)
	ticksOrient := "vertical"
	if orient == "vertical" {
		ticksOrient = "horizontal"
	}
	return errorBarParamsResult{
		transform: transform, continuousAxisChannelDef: cai.def, continuousAxis: cai.axis,
		encodingWithoutContinuousAxis: ex.encoding, ticksOrient: ticksOrient, markDef: markDef,
		outerSpec: outerSpec, tooltipEncoding: tooltipEncoding,
	}
}

func getTitlePrefix(center, extent, op string) string {
	return titleCase(center) + " " + op + " " + extent
}

func errorBarAggregationAndCalculation(markDef, def, def2, defError, defError2 Value, inputType, compositeMark string, config Value) (agg, post []Value, tooltipSummary []tooltipSummaryItem, tooltipTitleWithFieldName bool) {
	contField := def.Get("field").AsString()
	contFieldV := def.Get("field")
	acc := accessWithDatumToUnescapedPath
	if inputType == "raw" {
		var center string
		switch {
		case markDef.Get("center").IsTruthy():
			center = markDef.Get("center").AsString()
		case markDef.Get("extent").IsTruthy():
			if markDef.Get("extent").AsString() == "iqr" {
				center = "median"
			} else {
				center = "mean"
			}
		default:
			center = config.Get("errorbar").Get("center").AsString()
		}
		var extent string
		switch {
		case markDef.Get("extent").IsTruthy():
			extent = markDef.Get("extent").AsString()
		case center == "mean":
			extent = "stderr"
		default:
			extent = "iqr"
		}
		if extent == "stderr" || extent == "stdev" {
			agg = []Value{
				mkv("op", extent, "field", contFieldV, "as", "extent_"+contField),
				mkv("op", center, "field", contFieldV, "as", "center_"+contField),
			}
			post = []Value{
				mkv("calculate", acc("center_"+contField)+" + "+acc("extent_"+contField), "as", "upper_"+contField),
				mkv("calculate", acc("center_"+contField)+" - "+acc("extent_"+contField), "as", "lower_"+contField),
			}
			tooltipSummary = []tooltipSummaryItem{
				{"center_", jsval.Str(titleCase(center))},
				{"upper_", jsval.Str(getTitlePrefix(center, extent, "+"))},
				{"lower_", jsval.Str(getTitlePrefix(center, extent, "-"))},
			}
			tooltipTitleWithFieldName = true
		} else {
			var centerOp, lowerOp, upperOp string
			if extent == "ci" {
				centerOp, lowerOp, upperOp = "mean", "ci0", "ci1"
			} else {
				centerOp, lowerOp, upperOp = "median", "q1", "q3"
			}
			agg = []Value{
				mkv("op", lowerOp, "field", contFieldV, "as", "lower_"+contField),
				mkv("op", upperOp, "field", contFieldV, "as", "upper_"+contField),
				mkv("op", centerOp, "field", contFieldV, "as", "center_"+contField),
			}
			mkTitle := func(op string) Value {
				return fieldTitle(mkv("field", contFieldV, "aggregate", op, "type", "quantitative"), config, false, true)
			}
			tooltipSummary = []tooltipSummaryItem{
				{"upper_", mkTitle(upperOp)},
				{"lower_", mkTitle(lowerOp)},
				{"center_", mkTitle(centerOp)},
			}
		}
	} else {
		switch inputType {
		case "aggregated-upper-lower":
			tooltipSummary = []tooltipSummaryItem{}
			post = []Value{
				mkv("calculate", acc(def2.Get("field").AsString()), "as", "upper_"+contField),
				mkv("calculate", acc(contField), "as", "lower_"+contField),
			}
		case "aggregated-error":
			tooltipSummary = []tooltipSummaryItem{{"", jsval.Str(contField)}}
			post = []Value{mkv("calculate", acc(contField)+" + "+acc(defError.Get("field").AsString()), "as", "upper_"+contField)}
			if defError2.IsTruthy() {
				post = append(post, mkv("calculate", acc(contField)+" + "+acc(defError2.Get("field").AsString()), "as", "lower_"+contField))
			} else {
				post = append(post, mkv("calculate", acc(contField)+" - "+acc(defError.Get("field").AsString()), "as", "lower_"+contField))
			}
		}
		for _, c := range post {
			as := c.Get("as").AsString()
			calc := strings.ReplaceAll(strings.ReplaceAll(c.Get("calculate").AsString(), "datum['", ""), "']", "")
			tooltipSummary = append(tooltipSummary, tooltipSummaryItem{as[:min(6, len(as))], jsval.Str(calc)})
		}
	}
	return
}

func normalizeErrorBar(spec Value, config Value) Value {
	so := cloneObj(spec.ObjValue())
	so.Set("encoding", normalizeEncoding(spec.Get("encoding"), config))
	spec = jsval.Obj(so)
	r := errorBarParams(spec, "errorbar", config)
	r.encodingWithoutContinuousAxis.Delete("size")
	makePart := makeCompositeAggregatePartFactory(r.markDef, r.continuousAxis, r.continuousAxisChannelDef, jsval.Obj(r.encodingWithoutContinuousAxis), config.Get("errorbar"))
	thickness, size := r.markDef.Get("thickness"), r.markDef.Get("size")
	tick := mk("type", "tick", "orient", r.ticksOrient, "aria", false)
	if !thickness.IsUndefined() {
		tick.Set("thickness", thickness)
	}
	if !size.IsUndefined() {
		tick.Set("size", size)
	}
	tv := jsval.Obj(tick)
	var layer []Value
	layer = append(layer, makePart("ticks", tv, "lower", "", r.tooltipEncoding)...)
	layer = append(layer, makePart("ticks", tv, "upper", "", r.tooltipEncoding)...)
	rule := mk("type", "rule", "ariaRoleDescription", "errorbar")
	if !thickness.IsUndefined() {
		rule.Set("size", thickness)
	}
	layer = append(layer, makePart("rule", jsval.Obj(rule), "lower", "upper", r.tooltipEncoding)...)
	out := cloneObj(r.outerSpec)
	out.Set("transform", jsval.Arr(r.transform))
	if len(layer) > 1 {
		out.Set("layer", jsval.Arr(layer))
	} else if len(layer) == 1 {
		spread(out, layer[0])
	}
	return jsval.Obj(out)
}

func normalizeErrorBand(spec Value, config Value) Value {
	so := cloneObj(spec.ObjValue())
	so.Set("encoding", normalizeEncoding(spec.Get("encoding"), config))
	spec = jsval.Obj(so)
	r := errorBarParams(spec, "errorband", config)
	def := r.markDef
	makePart := makeCompositeAggregatePartFactory(def, r.continuousAxis, r.continuousAxisChannelDef, jsval.Obj(r.encodingWithoutContinuousAxis), config.Get("errorband"))
	is2D := spec.Get("encoding").Get("x").IsUndefined() == false && spec.Get("encoding").Get("y").IsUndefined() == false
	bandMark := mk("type", "rect")
	bordersMark := mk("type", "rule")
	if is2D {
		bandMark = mk("type", "area")
		bordersMark = mk("type", "line")
	}
	interpolate := jsval.NewObject(2)
	if def.Get("interpolate").IsTruthy() {
		interpolate.Set("interpolate", def.Get("interpolate"))
	}
	if def.Get("tension").IsTruthy() && def.Get("interpolate").IsTruthy() {
		interpolate.Set("tension", def.Get("tension"))
	}
	if is2D {
		spread(bandMark, jsval.Obj(interpolate))
		bandMark.Set("ariaRoleDescription", jsval.Str("errorband"))
		spread(bordersMark, jsval.Obj(interpolate))
		bordersMark.Set("aria", jsval.False)
	}
	var layer []Value
	layer = append(layer, makePart("band", jsval.Obj(bandMark), "lower", "upper", r.tooltipEncoding)...)
	layer = append(layer, makePart("borders", jsval.Obj(bordersMark), "lower", "", r.tooltipEncoding)...)
	layer = append(layer, makePart("borders", jsval.Obj(bordersMark), "upper", "", r.tooltipEncoding)...)
	out := cloneObj(r.outerSpec)
	out.Set("transform", jsval.Arr(r.transform))
	out.Set("layer", jsval.Arr(layer))
	return jsval.Obj(out)
}
