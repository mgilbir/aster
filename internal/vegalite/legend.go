package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Legends — vega-lite/src/compile/legend/*.ts.

var legendScaleChannels = []string{"size", "shape", "fill", "stroke", "strokeDash", "strokeWidth", "opacity"}

var commonLegendProperties = []string{
	"aria", "clipHeight", "columnPadding", "columns", "cornerRadius", "description", "direction", "fillColor",
	"format", "formatType", "gradientLength", "gradientOpacity", "gradientStrokeColor", "gradientStrokeWidth",
	"gradientThickness", "gridAlign", "labelAlign", "labelBaseline", "labelColor", "labelFont", "labelFontSize",
	"labelFontStyle", "labelFontWeight", "labelLimit", "labelOffset", "labelOpacity", "labelOverlap",
	"labelPadding", "labelSeparation", "legendX", "legendY", "offset", "orient", "padding", "rowPadding",
	"strokeColor", "symbolDash", "symbolDashOffset", "symbolFillColor", "symbolLimit", "symbolOffset",
	"symbolOpacity", "symbolSize", "symbolStrokeColor", "symbolStrokeWidth", "symbolType", "tickCount",
	"tickMinStep", "title", "titleAlign", "titleAnchor", "titleBaseline", "titleColor", "titleFont",
	"titleFontSize", "titleFontStyle", "titleFontWeight", "titleLimit", "titleLineHeight", "titleOpacity",
	"titleOrient", "titlePadding", "type", "values", "zindex",
}

var legendComponentProperties = append(append([]string{}, commonLegendProperties...),
	"disable", "labelExpr", "selections", "opacity", "shape", "stroke", "fill", "size", "strokeWidth", "strokeDash", "encode")

type legendComponent struct{ *split }

func newLegendComponent(explicit, implicit *Object) *legendComponent {
	return &legendComponent{newSplitOf(explicit, implicit)}
}

func (l *legendComponent) cloneLegend(cc *compileCtx) *legendComponent {
	return &legendComponent{l.split.clone(cc)}
}

func parseLegend(m Model) {
	var lc *omap[*legendComponent]
	if u := asUnit(m); u != nil {
		lc = parseUnitLegend(u)
	} else {
		lc = parseNonUnitLegend(m)
	}
	m.b().comp.legends = lc
}

func parseUnitLegend(u *unitModel) *omap[*legendComponent] {
	cc := u.b().ctx

	out := newOmap[*legendComponent]()
	for _, channel := range append([]string{chColor}, legendScaleChannels...) {
		def := getFieldOrDatumDef(cc, u.encoding.Get(channel))
		if !def.IsTruthy() || u.getScaleComponent(channel) == nil {
			continue
		}
		if channel == chShape && isFieldDef(cc, def) && channelDefType(def) == "geojson" {
			continue
		}
		out.set(channel, parseLegendForChannel(u, channel))
	}
	return out
}

func getLegendDefWithScale(m *unitModel, channel string) *Object {
	scale := strOrUndef(m.scaleName(channel, false))
	if strIs(m.mark(), "trail") {
		if channel == chColor {
			return mk("stroke", scale)
		} else if channel == chSize {
			return mk("strokeWidth", scale)
		}
	}
	if channel == chColor {
		if m.markDef.Get("filled").IsTruthy() {
			return mk("fill", scale)
		}
		return mk("stroke", scale)
	}
	return mk(channel, scale)
}

func legendIsExplicit(value Value, property string, legend Value, fd Value) bool {
	switch property {
	case "disable":
		return !legend.IsUndefined()
	case "values":
		return legend.Get("values").IsTruthy()
	case "title":
		if strictEq(value, fd.Get("title")) && !fd.IsUndefined() {
			return true
		}
	}
	if !legend.IsTruthy() {
		legend = mkv()
	}
	return strictEq(value, legend.Get(property))
}

type legendRuleParams struct {
	legend, markDef, encoding, fod, legendConfig, config Value
	channel                                              string
	model                                                *unitModel
	orient, legendType                                   string
	scaleType                                            Value
	// legendTypeV is the legend type as upstream holds it (a legend's
	// `type` of any type); legendType is it when it is a string.
	legendTypeV Value
	direction   Value
}

func parseLegendForChannel(m *unitModel, channel string) *legendComponent {
	cc := m.b().ctx

	legend := m.legend(channel)
	markDef, encoding, config := m.markDef, m.encoding, m.config
	legendConfig := config.Get("legend")
	cmpt := newLegendComponent(jsval.NewObject(2), getLegendDefWithScale(m, channel))
	parseInteractiveLegend(m, channel, cmpt)
	var disable Value
	if !legend.IsUndefined() {
		disable = jsval.Bool(!legend.IsTruthy())
	} else {
		disable = jsGet(legendConfig, "disable") // config.legend is read unguarded
	}
	cmpt.set("disable", disable, !legend.IsUndefined())
	if disable.IsTruthy() {
		return cmpt
	}
	if !legend.IsTruthy() {
		legend = mkv()
	}
	scaleType := m.scaleTypeOf(channel)
	fod := getFieldOrDatumDef(cc, encoding.Get(channel))
	timeUnit := undef
	if isFieldDef(cc, fod) {
		timeUnit = normalizeTimeUnit(cc, fod.Get("timeUnit")).Get("unit")
	}
	// legend.orient || config.legend.orient || 'right', compared with ===:
	// an orient that is not a string (a truthy [] or {}) is no named one.
	orient := ""
	if o := or(legend.Get("orient"), jsGet(config.Get("legend"), "orient"), jsval.Str("right")); o.IsStr() {
		orient = o.StrValue()
	}
	legendTypeV := getLegendType(legend, channel, timeUnit, scaleType)
	legendType := ""
	if legendTypeV.IsStr() {
		legendType = legendTypeV.StrValue()
	}
	direction := getDirection(legendConfig, legendTypeV, orient, legend)
	rp := legendRuleParams{legend: legend, markDef: markDef, encoding: encoding, fod: fod, legendConfig: legendConfig, config: config,
		channel: channel, model: m, scaleType: scaleType, orient: orient, legendType: legendType, legendTypeV: legendTypeV, direction: direction}
	for _, property := range legendComponentProperties {
		if (legendType == "gradient" && strings.HasPrefix(property, "symbol")) || (legendType == "symbol" && strings.HasPrefix(property, "gradient")) {
			continue
		}
		var value Value
		if rule, ok := legendRules[property]; ok {
			value = rule(rp)
		} else {
			value = legend.Get(property)
		}
		if !value.IsUndefined() {
			explicit := legendIsExplicit(value, property, legend, m.fieldDef(channel))
			if explicit || jsGet(config.Get("legend"), property).IsUndefined() {
				cmpt.set(property, value, explicit)
			}
		}
	}
	legendEncoding := coalesce(legend.Get("encoding"), mkv())
	selections := cmpt.get("selections")
	legendEncode := jsval.NewObject(4)
	hasSel := selections.IsArr() && selections.Len() > 0
	for _, part := range []string{"labels", "legend", "title", "symbols", "gradient", "entries"} {
		partSpec := guideEncodeEntry(coalesce(legendEncoding.Get(part), mkv()), m)
		value := partSpec
		switch part {
		case "symbols":
			value = legendSymbolsEncode(partSpec, fod, m, channel, cmpt, legendType)
		case "gradient":
			value = legendGradientEncode(partSpec, m, legendType, cmpt)
		case "labels":
			value = legendLabelsEncode(partSpec, fod, m, channel, cmpt)
		case "entries":
			value = legendEntriesEncode(partSpec, cmpt)
		}
		if !value.IsUndefined() && !isEmptyObj(value) {
			o := jsval.NewObject(4)
			if hasSel && isFieldDef(cc, fod) {
				o.Set("name", jsval.Str(varNameOf(fod.Get("field"))+"_legend_"+part))
			}
			if hasSel {
				o.Set("interactive", jsval.True)
			}
			if hasSel && !cc.v5 {
				u := cloneObj(coalesceObj(value).ObjValue())
				u.Set("cursor", mkv("value", "pointer"))
				o.Set("update", jsval.Obj(u))
			} else {
				o.Set("update", value)
			}
			legendEncode.Set(part, jsval.Obj(o))
		}
	}
	if legendEncode.Len() > 0 {
		cmpt.set("encode", jsval.Obj(legendEncode), legend.Get("encoding").IsTruthy())
	}
	return cmpt
}

func parseNonUnitLegend(m Model) *omap[*legendComponent] {
	cc := m.b().ctx
	legends, resolve := m.b().comp.legends, m.b().comp.resolve
	for _, child := range m.children() {
		cc.work(modelStep)
		parseLegend(child)
		cl := child.b().comp.legends
		for _, channel := range cl.keyList() {
			v := parseGuideResolve(cc, m.b().comp.resolve, channel)
			resolve.write(resolveLegend, channel)
			resolve.legend[channel] = v
			if resolve.legend[channel] == "shared" {
				cur, _ := legends.get(channel)
				childLegend, _ := cl.get(channel)
				merged := mergeLegendComponent(cc, cur, childLegend)
				if merged == nil {
					resolve.legend[channel] = "independent"
					legends.del(channel)
				} else {
					legends.set(channel, merged)
				}
			}
		}
	}
	for _, channel := range legends.keyList() {
		for _, child := range m.children() {
			cc.work(modelStep)
			if _, ok := child.b().comp.legends.get(channel); !ok {
				continue
			}
			if resolve.legend[channel] == "shared" {
				child.b().comp.legends.del(channel)
			}
		}
	}
	return legends
}

func mergeLegendComponent(cc *compileCtx, mergedLegend, childLegend *legendComponent) *legendComponent {
	if mergedLegend == nil {
		return childLegend.cloneLegend(cc)
	}
	mo, co := mergedLegend.getWithExplicit("orient"), childLegend.getWithExplicit("orient")
	if mo.explicit && co.explicit && !strictEq(mo.value, co.value) {
		return nil
	}
	typeMerged := false
	for _, prop := range legendComponentProperties {
		prop := prop
		mv := mergedLegend.getWithExplicit(prop)
		v := mergeValuesWithExplicit(cc, &mv, childLegend.getWithExplicit(prop), prop, "legend", func(cc *compileCtx, v1, v2 withExplicit, _, _ string) withExplicit {
			switch prop {
			case "symbolType":
				if v2.value.IsStr() && v2.value.StrValue() == "circle" {
					return v2
				}
				return v1
			case "title":
				return mergeTitleComponent(cc, v1, v2)
			case "type":
				typeMerged = true
				return makeImplicit(jsval.Str("symbol"))
			}
			return defaultTieBreaker(cc, v1, v2, prop, "legend")
		})
		mergedLegend.setWithExplicit(prop, v)
	}
	if typeMerged {
		if mergedLegend.implicit.Lookup("encode").Get("gradient").IsTruthy() {
			deleteNestedSplitProperty(&mergedLegend.implicit, "encode", "gradient")
		}
		if mergedLegend.explicit.Lookup("encode").Get("gradient").IsTruthy() {
			deleteNestedSplitProperty(&mergedLegend.explicit, "encode", "gradient")
		}
	}
	return mergedLegend
}

func deleteNestedProperty(obj Value, props []string) bool {
	if len(props) == 0 {
		return true
	}
	prop := props[0]
	if obj.IsObj() && obj.ObjValue().Has(prop) && deleteNestedProperty(obj.Get(prop), props[1:]) {
		obj.ObjValue().Delete(prop)
	}
	return isEmptyObj(obj)
}

// deleteNestedSplitProperty is deleteNestedProperty(side, [prop, ...rest]) on
// one side of a split.
func deleteNestedSplitProperty(side *jsObj, prop string, rest ...string) {
	if side.Has(prop) && deleteNestedProperty(side.Lookup(prop), rest) {
		side.Delete(prop)
	}
}

var legendRules = map[string]func(p legendRuleParams) Value{
	"direction": func(p legendRuleParams) Value { return p.direction },
	"format": func(p legendRuleParams) Value {
		return guideFormat(p.model.ctx, p.fod, channelDefType(p.fod), p.legend.Get("format"), p.legend.Get("formatType"), p.config, false)
	},
	"formatType": func(p legendRuleParams) Value {
		return guideFormatType(p.model.ctx, p.legend.Get("formatType"), p.fod, p.scaleType)
	},
	"gradientLength": func(p legendRuleParams) Value {
		if v := p.legend.Get("gradientLength"); !v.IsNullish() {
			return v
		}
		if v := p.legendConfig.Get("gradientLength"); !v.IsNullish() {
			return v
		}
		return defaultGradientLength(p)
	},
	"labelOverlap": func(p legendRuleParams) Value {
		if v := p.legend.Get("labelOverlap"); !v.IsNullish() {
			return v
		}
		if v := p.legendConfig.Get("labelOverlap"); !v.IsNullish() {
			return v
		}
		if strIn(p.scaleType, "quantile", "threshold", "log", "symlog") {
			return jsval.Str("greedy")
		}
		return undef
	},
	"symbolType": func(p legendRuleParams) Value {
		if v := p.legend.Get("symbolType"); !v.IsNullish() {
			return v
		}
		return defaultSymbolType(p.model.ctx, p.markDef.Get("type"), p.channel, p.encoding.Get("shape"), p.markDef.Get("shape"))
	},
	"title": func(p legendRuleParams) Value { return fieldTitle(p.model.ctx, p.fod, p.config, true, true) },
	"type": func(p legendRuleParams) Value {
		if isColorChannel(p.channel) && isContinuousToContinuous(p.scaleType) {
			if p.legendType == "gradient" {
				return undef
			}
		} else if p.legendType == "symbol" {
			return undef
		}
		return p.legendTypeV
	},
	"values": func(p legendRuleParams) Value { return guideValues(p.model.ctx, p.legend, p.fod) },
}

func defaultSymbolType(cc *compileCtx, mark Value, channel string, shapeChannelDef, markShape Value) Value {
	if channel != chShape {
		shape := firstDefined(getFirstConditionValue(cc, shapeChannelDef), markShape)
		if shape.IsTruthy() {
			return shape
		}
	}
	switch s, _ := mark.Strict(); s {
	case "bar", "rect", "image", "square":
		return jsval.Str("square")
	case "line", "trail", "rule":
		return jsval.Str("stroke")
	case "arc", "point", "circle", "tick", "geoshape", "area", "text":
		return jsval.Str("circle")
	}
	return undef
}

// getLegendType is getFirstDefined(legend.type, defaultType(...)): the
// legend's type as given, of whatever type.
func getLegendType(legend Value, channel string, timeUnit, scaleType Value) Value {
	if t := legend.Get("type"); !t.IsUndefined() {
		return t
	}
	if isColorChannel(channel) {
		if strIn(timeUnit, "quarter", "month", "day") {
			return jsval.Str("symbol")
		}
		if isContinuousToContinuous(scaleType) {
			return jsval.Str("gradient")
		}
	}
	return jsval.Str("symbol")
}

func getDirection(legendConfig Value, legendTypeV Value, orient string, legend Value) Value {
	if v := legend.Get("direction"); !v.IsNullish() {
		return v
	}
	// legendConfig[legendType ? 'gradientDirection' : 'symbolDirection']
	key := "symbolDirection"
	if legendTypeV.IsTruthy() {
		key = "gradientDirection"
	}
	if v := legendConfig.Get(key); !v.IsNullish() {
		return v
	}
	legendType := ""
	if legendTypeV.IsStr() {
		legendType = legendTypeV.StrValue()
	}
	return defaultDirection(orient, legendType)
}

func defaultDirection(orient, legendType string) Value {
	switch orient {
	case "top", "bottom":
		return jsval.Str("horizontal")
	case "left", "right", "none":
		return undef
	}
	if legendType == "gradient" {
		return jsval.Str("horizontal")
	}
	return undef
}

func defaultGradientLength(p legendRuleParams) Value {
	lc := p.legendConfig
	if isContinuousToContinuous(p.scaleType) {
		if p.direction.IsStr() && p.direction.StrValue() == "horizontal" {
			if p.orient == "top" || p.orient == "bottom" {
				return gradientLengthSignal(p.model, "width", lc.Get("gradientHorizontalMinLength"), lc.Get("gradientHorizontalMaxLength"))
			}
			return lc.Get("gradientHorizontalMinLength")
		}
		return gradientLengthSignal(p.model, "height", lc.Get("gradientVerticalMinLength"), lc.Get("gradientVerticalMaxLength"))
	}
	return undef
}

func gradientLengthSignal(m *unitModel, sizeType string, min, max Value) Value {
	sizeSignal := signalOf(m.getSizeSignalRef(sizeType))
	return sig("clamp(" + sizeSignal + ", " + min.AsString() + ", " + max.AsString() + ")")
}

// ---- legend encode ----

func getConditionValue(cc *compileCtx, channelDef Value, reducer func(v, cond Value) Value) Value {
	if hasConditionalValueDef(channelDef) {
		acc := channelDef.Get("value")
		for _, c := range arrayOf(channelDef.Get("condition")) {
			cc.work(1)
			acc = reducer(acc, c)
		}
		return acc
	} else if isValueDef(channelDef) {
		return channelDef.Get("value")
	}
	return undef
}

func getMaxValue(cc *compileCtx, channelDef Value) Value {
	return getConditionValue(cc, channelDef, func(v, c Value) Value {
		a, b := jsval.ToNumber(v), jsval.ToNumber(c.Get("value"))
		if a != a || b != b {
			return jsval.Num(a + b)
		}
		if b > a {
			return jsval.Num(b)
		}
		return jsval.Num(a)
	})
}

func getFirstConditionValue(cc *compileCtx, channelDef Value) Value {
	return getConditionValue(cc, channelDef, func(v, c Value) Value { return firstDefined(v, c.Get("value")) })
}

func selectedCondition(m *unitModel, cmpt *legendComponent, fd Value) string {
	cc := m.ctx
	selections := cmpt.get("selections")
	if !selections.IsArr() || selections.Len() == 0 {
		return ""
	}
	field := stringValue(cc, fd.Get("field"))
	var parts []string
	for _, n := range selections.Items() {
		cc.work(1)
		name := n.AsString()
		store := stringValue(cc, jsval.Str(varName(name)+storeSuffix))
		parts = append(parts, cc.cat("(!length(data(", store, ")) || (", name, "[", field, "] && indexof(", name, "[", field, "], datum.value) >= 0))"))
	}
	return joinCC(cc, parts, " || ")
}

func legendSymbolsEncode(symbolsSpec Value, fod Value, m *unitModel, channel string, cmpt *legendComponent, legendType string) Value {
	cc := m.b().ctx

	if legendType != "symbol" {
		return undef
	}
	markDef, encoding, config, mark := m.markDef, m.encoding, m.config, m.mark()
	filled := markDef.Get("filled").IsTruthy() && !strIs(mark, "trail")
	out := jsval.NewObject(8)
	spreadV(out, applyMarkConfig(mkv(), m, fillStrokeConfig))
	spreadV(out, colorEncode(m, jsval.Bool(filled)))
	symbolOpacity := coalesce(cmpt.get("symbolOpacity"), jsGet(config.Get("legend"), "symbolOpacity"))
	symbolFillColor := coalesce(cmpt.get("symbolFillColor"), jsGet(config.Get("legend"), "symbolFillColor"))
	symbolStrokeColor := coalesce(cmpt.get("symbolStrokeColor"), jsGet(config.Get("legend"), "symbolStrokeColor"))
	opacity := undef
	if symbolOpacity.IsUndefined() {
		opacity = coalesce(getMaxValue(cc, encoding.Get("opacity")), markDef.Get("opacity"))
	}
	if fill := out.Lookup("fill"); fill.IsTruthy() {
		if channel == chFill || (filled && channel == chColor) {
			out.Delete("fill")
		} else if hasProperty(fill, "field") {
			if symbolFillColor.IsTruthy() {
				out.Delete("fill")
			} else {
				out.Set("fill", signalOrValueRef(cc, coalesce(jsGet(config.Get("legend"), "symbolBaseFillColor"), jsval.Str("black"))))
				out.Set("fillOpacity", signalOrValueRef(cc, coalesce(opacity, jsval.Int(1))))
			}
		} else if fill.IsArr() {
			src := encoding.Get("fill")
			if !src.IsTruthy() {
				src = encoding.Get("color")
			}
			f := getFirstNonNullish3(getFirstConditionValue(cc, src), markDef.Get("fill"), func() Value {
				if filled {
					return markDef.Get("color")
				}
				return jsval.False
			}())
			if f.IsTruthy() {
				out.Set("fill", signalOrValueRef(cc, f))
			}
		}
	}
	if stroke := out.Lookup("stroke"); stroke.IsTruthy() {
		if channel == chStroke || (!filled && channel == chColor) {
			out.Delete("stroke")
		} else if hasProperty(stroke, "field") || symbolStrokeColor.IsTruthy() {
			out.Delete("stroke")
		} else if stroke.IsArr() {
			src := encoding.Get("stroke")
			if !src.IsTruthy() {
				src = encoding.Get("color")
			}
			var alt Value
			if filled {
				alt = markDef.Get("color")
			}
			s := firstDefined(getFirstConditionValue(cc, src), markDef.Get("stroke"), alt)
			if s.IsTruthy() {
				out.Set("stroke", mkv("value", s))
			}
		}
	}
	if channel != chOpacity {
		var condition string
		if isFieldDef(cc, fod) {
			condition = selectedCondition(m, cmpt, fod)
		}
		if condition != "" {
			t := mk("test", condition)
			spreadV(t, signalOrValueRef(cc, coalesce(opacity, jsval.Int(1))))
			out.Set("opacity", arr(jsval.Obj(t), signalOrValueRef(cc, jsGet(config.Get("legend"), "unselectedOpacity"))))
		} else if opacity.IsTruthy() {
			out.Set("opacity", signalOrValueRef(cc, opacity))
		}
	}
	spreadV(out, symbolsSpec)
	if out.Len() == 0 {
		return undef
	}
	return jsval.Obj(out)
}

func getFirstNonNullish3(a, b, c Value) Value {
	// `a ?? b ?? c`
	return coalesce(a, b, c)
}

func legendGradientEncode(gradientSpec Value, m *unitModel, legendType string, cmpt *legendComponent) Value {
	if legendType != "gradient" {
		return undef
	}
	cc := m.ctx
	config, markDef, encoding := m.config, m.markDef, m.encoding
	out := jsval.NewObject(2)
	gradientOpacity := coalesce(cmpt.get("gradientOpacity"), jsGet(config.Get("legend"), "gradientOpacity"))
	opacity := undef
	if gradientOpacity.IsUndefined() {
		opacity = or(getMaxValue(m.ctx, encoding.Get("opacity")), markDef.Get("opacity"))
	}
	if opacity.IsTruthy() {
		out.Set("opacity", signalOrValueRef(cc, opacity))
	}
	spreadV(out, gradientSpec)
	if out.Len() == 0 {
		return undef
	}
	return jsval.Obj(out)
}

func legendLabelsEncode(specified Value, fod Value, m *unitModel, channel string, cmpt *legendComponent) Value {
	cc := m.b().ctx

	legend := coalesceObj(m.legend(channel))
	if !legend.IsObj() {
		legend = mkv()
	}
	config := m.config
	var condition string
	if isFieldDef(cc, fod) {
		condition = selectedCondition(m, cmpt, fod)
	}
	opacity := undef
	if condition != "" {
		opacity = arr(mkv("test", condition, "value", 1), mkv("value", jsGet(config.Get("legend"), "unselectedOpacity")))
	}
	format, formatType := legend.Get("format"), legend.Get("formatType")
	text := undef
	if isCustomFormatType(formatType) {
		text = formatCustomType(cc, fod, format, formatType, "", false, config, "datum.value")
	} else if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() {
		if channelDefType(fod) == "quantitative" && config.Get("numberFormatType").IsTruthy() {
			text = formatCustomType(cc, fod, config.Get("numberFormat"), config.Get("numberFormatType"), "", false, config, "datum.value")
		} else if channelDefType(fod) == "temporal" && config.Get("timeFormatType").IsTruthy() && isFieldDef(cc, fod) && fod.Get("timeUnit").IsUndefined() {
			text = formatCustomType(cc, fod, config.Get("timeFormat"), config.Get("timeFormatType"), "", false, config, "datum.value")
		}
	}
	o := jsval.NewObject(3)
	if opacity.IsTruthy() {
		o.Set("opacity", opacity)
	}
	if text.IsTruthy() {
		o.Set("text", text)
	}
	spreadV(o, specified)
	if o.Len() == 0 {
		return undef
	}
	return jsval.Obj(o)
}

func legendEntriesEncode(spec Value, cmpt *legendComponent) Value {
	selections := cmpt.get("selections")
	if selections.IsArr() && selections.Len() > 0 {
		o := cloneObj(coalesceObj(spec).ObjValue())
		o.Set("fill", mkv("value", "transparent"))
		return jsval.Obj(o)
	}
	return spec
}

var fillStrokeConfig = []string{
	"stroke", "strokeWidth", "strokeDash", "strokeDashOffset", "strokeOpacity", "strokeJoin", "strokeMiterLimit",
	"fill", "fillOpacity",
}

// applyMarkConfig adds the mark config values of the listed properties to e.
func applyMarkConfig(e Value, m *unitModel, props []string) Value {
	cc := m.ctx
	o := e.ObjValue()
	for _, p := range props {
		if v := getMarkConfig(p, m.markDef, m.config, ""); !v.IsUndefined() {
			o.Set(p, signalOrValueRef(cc, v))
		}
	}
	return e
}

// ---- assemble ----

func legendSetEncode(legend *Object, part, vgProp string, vgRef Value) {
	setAxisEncode(legend, part, vgProp, vgRef)
}

// getFieldKeyForChannel is the field a channel's legends share: the
// unit's field as given, else the one field all children agree on, by
// util.unique(childFields, hash), whose plain-object seen set drops a field
// named "__proto__" (see jsSeen); undefined when there is none.
func getFieldKeyForChannel(m Model, channel string) Value {
	cc := m.b().ctx
	if u := asUnit(m); u != nil {
		if f := u.fieldDef(channel).Get("field"); f.IsTruthy() {
			return f
		}
	}
	var childFields []Value
	for _, c := range m.children() {
		cc.work(modelStep)
		if f := getFieldKeyForChannel(c, channel); f.IsTruthy() {
			childFields = append(childFields, f)
		}
	}
	if len(childFields) > 0 {
		if u := uniqueValues(cc, childFields); len(u) == 1 {
			return u[0]
		}
	}
	return undef
}

func legendsAreMergeCompatible(m Model, a, b string) bool {
	if a == b {
		return true
	}
	ta, tb := m.b().getScaleType(a), m.b().getScaleType(b)
	if !ta.IsTruthy() || !tb.IsTruthy() {
		return false
	}
	return hasDiscreteDomain(ta) == hasDiscreteDomain(tb)
}

func legendGroupKey(fieldKey Value, channel string) string {
	if fieldKey.IsTruthy() {
		return "field:" + fieldKey.AsString()
	}
	return "channel:" + channel
}

func extractDiscreteValuesFromDomain(cc *compileCtx, domain Value) []Value {
	if domain.IsArr() {
		var out []Value
		for _, d := range domain.Items() {
			cc.work(1)
			if d.IsStr() || d.IsNum() || d.IsBool() {
				out = append(out, d)
			}
		}
		if len(out) > 0 {
			return out
		}
		return nil
	}
	if isDataRefUnionedDomain(domain) {
		var vals []Value
		for _, f := range domain.Get("fields").Items() {
			cc.work(1)
			if f.IsArr() {
				for _, d := range f.Items() {
					cc.work(1)
					if d.IsStr() || d.IsNum() || d.IsBool() {
						vals = append(vals, d)
					}
				}
			}
		}
		if len(vals) > 0 {
			return uniqueValues(cc, vals)
		}
	}
	return nil
}

func uniqueValues(cc *compileCtx, in []Value) []Value {
	seen := map[string]bool{}
	var out []Value
	for _, v := range in {
		cc.work(1)
		h := hashOf(cc, v)
		if !jsSeen(cc, seen, h) {
			seen[h] = true
			out = append(out, v)
		}
	}
	return out
}

func getDiscreteValuesForChannel(m Model, channel string) (out []Value) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(compileError); ok {
				out = nil
				return
			}
			panic(r)
		}
	}()
	return extractDiscreteValuesFromDomain(m.b().ctx, assembleDomain(m, channel))
}

func unionDiscreteValuesForChannels(m Model, a, b string) []Value {
	va, vb := getDiscreteValuesForChannel(m, a), getDiscreteValuesForChannel(m, b)
	if va != nil && vb != nil {
		return uniqueValues(m.b().ctx, append(append([]Value{}, va...), vb...))
	}
	return nil
}

func setImplicitLegendValues(cc *compileCtx, c *legendComponent, values []Value, warning string) {
	if len(values) > 0 {
		vp := c.getWithExplicit("values")
		if !vp.explicit {
			if warning != "" {
				cc.warn(warning)
			}
			c.set("values", jsval.Arr(values), false)
		}
	}
}

func domainsExplicitAndEqual(m Model, a, b string) bool {
	sa, sb := m.b().getScaleComponent(a), m.b().getScaleComponent(b)
	if sa == nil || sb == nil {
		return false
	}
	da, db := sa.getWithExplicit("domains"), sb.getWithExplicit("domains")
	if !(da.explicit && db.explicit) {
		return false
	}
	return hashOf(m.b().ctx, assembleDomain(m, a)) == hashOf(m.b().ctx, assembleDomain(m, b))
}

type legendEntry struct {
	channel string
	cmpt    *legendComponent
}

func assembleLegends(m Model) []Value {
	cc := m.b().ctx

	if cc.v5 {
		return assembleLegends58(m)
	}
	index := m.b().comp.legends
	byGroup := newOmap[[]*legendEntry]()
	for _, channel := range index.keyList() {
		fieldKey := getFieldKeyForChannel(m, channel)
		groupKey := legendGroupKey(fieldKey, channel)
		lc, _ := index.get(channel)
		if !byGroup.has(groupKey) {
			byGroup.set(groupKey, []*legendEntry{{channel, lc.cloneLegend(cc)}})
			continue
		}
		merged := false
		for _, existing := range byGroup.m[groupKey] {
			if !legendsAreMergeCompatible(m, existing.channel, channel) {
				continue
			}
			if mm := mergeLegendComponent(cc, existing.cmpt, lc); mm != nil {
				ta, tb := m.b().getScaleType(existing.channel), m.b().getScaleType(channel)
				if ta.IsTruthy() && tb.IsTruthy() && hasDiscreteDomain(ta) && hasDiscreteDomain(tb) {
					if domainsExplicitAndEqual(m, existing.channel, channel) {
						setImplicitLegendValues(cc, existing.cmpt, getDiscreteValuesForChannel(m, existing.channel), "")
					} else {
						setImplicitLegendValues(cc, existing.cmpt, unionDiscreteValuesForChannels(m, existing.channel, channel), msgLegendValuesUnioned(existing.channel, channel))
					}
				}
				merged = true
				break
			}
		}
		if !merged {
			byGroup.m[groupKey] = append(byGroup.m[groupKey], &legendEntry{channel, lc.cloneLegend(cc)})
		}
	}
	var out []Value
	for _, k := range byGroup.keysView() {
		for _, e := range byGroup.m[k] {
			if l := assembleLegend(cc, e.cmpt, m.b().config); l.IsTruthy() {
				out = append(out, l)
			}
		}
	}
	return out
}

func assembleLegend(cc *compileCtx, cmpt *legendComponent, config Value) Value {
	comb := cmpt.combine()
	disable, labelExpr := comb.Lookup("disable"), comb.Lookup("labelExpr")
	legend := omit(jsval.Obj(comb), "disable", "labelExpr", "selections")
	if disable.IsTruthy() {
		return undef
	}
	if config.Get("aria").IsBool() && !config.Get("aria").BoolValue() && legend.Lookup("aria").IsNullish() {
		legend.Set("aria", jsval.False)
	}
	if sym := legend.Lookup("encode").Get("symbols"); sym.IsTruthy() {
		out := sym.Get("update")
		if fill := out.Get("fill"); fill.IsTruthy() && !(fill.Get("value").IsStr() && fill.Get("value").StrValue() == "transparent") && !out.Get("stroke").IsTruthy() && !legend.Lookup("stroke").IsTruthy() {
			out.ObjValue().Set("stroke", mkv("value", "transparent"))
		}
		for _, p := range legendScaleChannels {
			if legend.Lookup(p).IsTruthy() {
				out.ObjValue().Delete(p)
			}
		}
	}
	if !legend.Lookup("title").IsTruthy() {
		legend.Delete("title")
	}
	if !labelExpr.IsUndefined() {
		// The expression as given; util.replaceAll calls string.replace on it.
		expr := labelExpr
		lu := legend.Lookup("encode").Get("labels").Get("update")
		if lu.IsObj() && isSignalRef(cc, lu.Get("text")) {
			if !labelExpr.IsStr() {
				throw("string.replace is not a function")
			}
			expr = jsval.Str(jsReplaceAll(cc, labelExpr.StrValue(), "datum.label", signalOf(lu.Get("text"))))
		}
		legendSetEncode(legend, "labels", "text", mkv("signal", expr))
	}
	return jsval.Obj(legend)
}
