package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
	return &legendComponent{&split{explicit, implicit}}
}

func (l *legendComponent) cloneLegend() *legendComponent {
	return &legendComponent{l.split.clone()}
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
	out := newOmap[*legendComponent]()
	for _, channel := range append([]string{chColor}, legendScaleChannels...) {
		def := getFieldOrDatumDef(u.encoding.Get(channel))
		if !def.IsTruthy() || u.getScaleComponent(channel) == nil {
			continue
		}
		if channel == chShape && isFieldDef(def) && channelDefType(def) == "geojson" {
			continue
		}
		out.set(channel, parseLegendForChannel(u, channel))
	}
	return out
}

func getLegendDefWithScale(m *unitModel, channel string) *Object {
	scale := strOrUndef(m.scaleName(channel, false))
	if m.mark() == "trail" {
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
	scaleType, orient, legendType                        string
	direction                                            Value
}

func parseLegendForChannel(m *unitModel, channel string) *legendComponent {
	legend := m.legend(channel)
	markDef, encoding, config := m.markDef, m.encoding, m.config
	legendConfig := config.Get("legend")
	cmpt := newLegendComponent(jsval.NewObject(2), getLegendDefWithScale(m, channel))
	parseInteractiveLegend(m, channel, cmpt)
	var disable Value
	if !legend.IsUndefined() {
		disable = jsval.Bool(!legend.IsTruthy())
	} else {
		disable = legendConfig.Get("disable")
	}
	cmpt.set("disable", disable, !legend.IsUndefined())
	if disable.IsTruthy() {
		return cmpt
	}
	if !legend.IsTruthy() {
		legend = mkv()
	}
	scaleType := m.getScaleComponent(channel).get("type").AsString()
	fod := getFieldOrDatumDef(encoding.Get(channel))
	timeUnit := ""
	if isFieldDef(fod) {
		if u := normalizeTimeUnit(fod.Get("timeUnit")).Get("unit"); u.IsTruthy() {
			timeUnit = u.AsString()
		}
	}
	orient := or(legend.Get("orient"), config.Get("legend").Get("orient"), jsval.Str("right")).AsString()
	legendType := getLegendType(legend, channel, timeUnit, scaleType)
	direction := getDirection(legendConfig, legendType, orient, legend)
	rp := legendRuleParams{legend: legend, markDef: markDef, encoding: encoding, fod: fod, legendConfig: legendConfig, config: config,
		channel: channel, model: m, scaleType: scaleType, orient: orient, legendType: legendType, direction: direction}
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
			if explicit || config.Get("legend").Get(property).IsUndefined() {
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
			if hasSel && isFieldDef(fod) {
				o.Set("name", jsval.Str(varName(fod.Get("field").AsString())+"_legend_"+part))
			}
			if hasSel {
				o.Set("interactive", jsval.True)
			}
			if hasSel && !v5 {
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
	legends, resolve := m.b().comp.legends, m.b().comp.resolve
	for _, child := range m.children() {
		parseLegend(child)
		cl := child.b().comp.legends
		for _, channel := range cl.keyList() {
			resolve.legend[channel] = parseGuideResolve(m.b().comp.resolve, channel)
			if resolve.legend[channel] == "shared" {
				cur, _ := legends.get(channel)
				childLegend, _ := cl.get(channel)
				merged := mergeLegendComponent(cur, childLegend)
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

func mergeLegendComponent(mergedLegend, childLegend *legendComponent) *legendComponent {
	if mergedLegend == nil {
		return childLegend.cloneLegend()
	}
	mo, co := mergedLegend.getWithExplicit("orient"), childLegend.getWithExplicit("orient")
	if mo.explicit && co.explicit && !strictEq(mo.value, co.value) {
		return nil
	}
	typeMerged := false
	for _, prop := range legendComponentProperties {
		prop := prop
		mv := mergedLegend.getWithExplicit(prop)
		v := mergeValuesWithExplicit(&mv, childLegend.getWithExplicit(prop), prop, "legend", func(v1, v2 withExplicit, _, _ string) withExplicit {
			switch prop {
			case "symbolType":
				if v2.value.IsStr() && v2.value.StrValue() == "circle" {
					return v2
				}
				return v1
			case "title":
				return mergeTitleComponent(v1, v2)
			case "type":
				typeMerged = true
				return makeImplicit(jsval.Str("symbol"))
			}
			return defaultTieBreaker(v1, v2, prop, "legend")
		})
		mergedLegend.setWithExplicit(prop, v)
	}
	if typeMerged {
		if mergedLegend.implicit.Lookup("encode").Get("gradient").IsTruthy() {
			deleteNestedProperty(jsval.Obj(mergedLegend.implicit), []string{"encode", "gradient"})
		}
		if mergedLegend.explicit.Lookup("encode").Get("gradient").IsTruthy() {
			deleteNestedProperty(jsval.Obj(mergedLegend.explicit), []string{"encode", "gradient"})
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

var legendRules = map[string]func(p legendRuleParams) Value{
	"direction": func(p legendRuleParams) Value { return p.direction },
	"format": func(p legendRuleParams) Value {
		return guideFormat(p.fod, channelDefType(p.fod), p.legend.Get("format"), p.legend.Get("formatType"), p.config, false)
	},
	"formatType": func(p legendRuleParams) Value {
		return guideFormatType(p.legend.Get("formatType"), p.fod, p.scaleType)
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
		if contains([]string{"quantile", "threshold", "log", "symlog"}, p.scaleType) {
			return jsval.Str("greedy")
		}
		return undef
	},
	"symbolType": func(p legendRuleParams) Value {
		if v := p.legend.Get("symbolType"); !v.IsNullish() {
			return v
		}
		return defaultSymbolType(p.markDef.Get("type").AsString(), p.channel, p.encoding.Get("shape"), p.markDef.Get("shape"))
	},
	"title": func(p legendRuleParams) Value { return fieldTitle(p.fod, p.config, true, true) },
	"type": func(p legendRuleParams) Value {
		if isColorChannel(p.channel) && isContinuousToContinuous(p.scaleType) {
			if p.legendType == "gradient" {
				return undef
			}
		} else if p.legendType == "symbol" {
			return undef
		}
		return strOrUndef(p.legendType)
	},
	"values": func(p legendRuleParams) Value { return guideValues(p.legend, p.fod) },
}

func defaultSymbolType(mark, channel string, shapeChannelDef, markShape Value) Value {
	if channel != chShape {
		shape := firstDefined(getFirstConditionValue(shapeChannelDef), markShape)
		if shape.IsTruthy() {
			return shape
		}
	}
	switch mark {
	case "bar", "rect", "image", "square":
		return jsval.Str("square")
	case "line", "trail", "rule":
		return jsval.Str("stroke")
	case "arc", "point", "circle", "tick", "geoshape", "area", "text":
		return jsval.Str("circle")
	}
	return undef
}

func getLegendType(legend Value, channel, timeUnit, scaleType string) string {
	if t := legend.Get("type"); !t.IsUndefined() {
		return t.AsString()
	}
	if isColorChannel(channel) {
		if timeUnit == "quarter" || timeUnit == "month" || timeUnit == "day" {
			return "symbol"
		}
		if isContinuousToContinuous(scaleType) {
			return "gradient"
		}
	}
	return "symbol"
}

func getDirection(legendConfig Value, legendType, orient string, legend Value) Value {
	if v := legend.Get("direction"); !v.IsNullish() {
		return v
	}
	key := "symbolDirection"
	if legendType != "" {
		key = "gradientDirection"
	}
	if v := legendConfig.Get(key); !v.IsNullish() {
		return v
	}
	return defaultDirection(orient, legendType)
}

func defaultDirection(orient, legendType string) Value {
	switch orient {
	case "top", "bottom":
		return jsval.Str("horizontal")
	case "left", "right", "none", "":
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

func getConditionValue(channelDef Value, reducer func(v, cond Value) Value) Value {
	if hasConditionalValueDef(channelDef) {
		acc := channelDef.Get("value")
		for _, c := range arrayOf(channelDef.Get("condition")) {
			acc = reducer(acc, c)
		}
		return acc
	} else if isValueDef(channelDef) {
		return channelDef.Get("value")
	}
	return undef
}

func getMaxValue(channelDef Value) Value {
	return getConditionValue(channelDef, func(v, c Value) Value {
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

func getFirstConditionValue(channelDef Value) Value {
	return getConditionValue(channelDef, func(v, c Value) Value { return firstDefined(v, c.Get("value")) })
}

func selectedCondition(m *unitModel, cmpt *legendComponent, fd Value) string {
	selections := cmpt.get("selections")
	if !selections.IsArr() || selections.Len() == 0 {
		return ""
	}
	field := stringValue(fd.Get("field"))
	var parts []string
	for _, n := range selections.Items() {
		name := n.AsString()
		store := stringValue(jsval.Str(varName(name) + storeSuffix))
		parts = append(parts, "(!length(data("+store+")) || ("+name+"["+field+"] && indexof("+name+"["+field+"], datum.value) >= 0))")
	}
	return strings.Join(parts, " || ")
}

func legendSymbolsEncode(symbolsSpec Value, fod Value, m *unitModel, channel string, cmpt *legendComponent, legendType string) Value {
	if legendType != "symbol" {
		return undef
	}
	markDef, encoding, config, mark := m.markDef, m.encoding, m.config, m.mark()
	filled := markDef.Get("filled").IsTruthy() && mark != "trail"
	out := jsval.NewObject(8)
	spreadV(out, applyMarkConfig(mkv(), m, fillStrokeConfig))
	spreadV(out, colorEncode(m, jsval.Bool(filled)))
	symbolOpacity := coalesce(cmpt.get("symbolOpacity"), config.Get("legend").Get("symbolOpacity"))
	symbolFillColor := coalesce(cmpt.get("symbolFillColor"), config.Get("legend").Get("symbolFillColor"))
	symbolStrokeColor := coalesce(cmpt.get("symbolStrokeColor"), config.Get("legend").Get("symbolStrokeColor"))
	opacity := undef
	if symbolOpacity.IsUndefined() {
		opacity = coalesce(getMaxValue(encoding.Get("opacity")), markDef.Get("opacity"))
	}
	if fill := out.Lookup("fill"); fill.IsTruthy() {
		if channel == chFill || (filled && channel == chColor) {
			out.Delete("fill")
		} else if hasProperty(fill, "field") {
			if symbolFillColor.IsTruthy() {
				out.Delete("fill")
			} else {
				out.Set("fill", signalOrValueRef(coalesce(config.Get("legend").Get("symbolBaseFillColor"), jsval.Str("black"))))
				out.Set("fillOpacity", signalOrValueRef(coalesce(opacity, jsval.Int(1))))
			}
		} else if fill.IsArr() {
			src := encoding.Get("fill")
			if !src.IsTruthy() {
				src = encoding.Get("color")
			}
			f := firstDefined(getFirstConditionValue(src), markDef.Get("fill"))
			if f.IsUndefined() || f.IsNull() || (f.IsBool() && !f.BoolValue()) || (f.IsStr() && f.StrValue() == "") {
				if filled {
					f = coalesce(f, markDef.Get("color"))
				}
			}
			f = getFirstNonNullish3(getFirstConditionValue(src), markDef.Get("fill"), func() Value {
				if filled {
					return markDef.Get("color")
				}
				return jsval.False
			}())
			if f.IsTruthy() {
				out.Set("fill", signalOrValueRef(f))
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
			s := firstDefined(getFirstConditionValue(src), markDef.Get("stroke"), alt)
			if s.IsTruthy() {
				out.Set("stroke", mkv("value", s))
			}
		}
	}
	if channel != chOpacity {
		var condition string
		if isFieldDef(fod) {
			condition = selectedCondition(m, cmpt, fod)
		}
		if condition != "" {
			t := mk("test", condition)
			spreadV(t, signalOrValueRef(coalesce(opacity, jsval.Int(1))))
			out.Set("opacity", arr(jsval.Obj(t), signalOrValueRef(config.Get("legend").Get("unselectedOpacity"))))
		} else if opacity.IsTruthy() {
			out.Set("opacity", signalOrValueRef(opacity))
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
	config, markDef, encoding := m.config, m.markDef, m.encoding
	out := jsval.NewObject(2)
	gradientOpacity := coalesce(cmpt.get("gradientOpacity"), config.Get("legend").Get("gradientOpacity"))
	opacity := undef
	if gradientOpacity.IsUndefined() {
		opacity = or(getMaxValue(encoding.Get("opacity")), markDef.Get("opacity"))
	}
	if opacity.IsTruthy() {
		out.Set("opacity", signalOrValueRef(opacity))
	}
	spreadV(out, gradientSpec)
	if out.Len() == 0 {
		return undef
	}
	return jsval.Obj(out)
}

func legendLabelsEncode(specified Value, fod Value, m *unitModel, channel string, cmpt *legendComponent) Value {
	legend := coalesceObj(m.legend(channel))
	if !legend.IsObj() {
		legend = mkv()
	}
	config := m.config
	var condition string
	if isFieldDef(fod) {
		condition = selectedCondition(m, cmpt, fod)
	}
	opacity := undef
	if condition != "" {
		opacity = arr(mkv("test", condition, "value", 1), mkv("value", config.Get("legend").Get("unselectedOpacity")))
	}
	format, formatType := legend.Get("format"), legend.Get("formatType")
	text := undef
	if isCustomFormatType(formatType) {
		text = formatCustomType(fod, format, formatType, "", false, config, "datum.value")
	} else if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() {
		if channelDefType(fod) == "quantitative" && config.Get("numberFormatType").IsTruthy() {
			text = formatCustomType(fod, config.Get("numberFormat"), config.Get("numberFormatType"), "", false, config, "datum.value")
		} else if channelDefType(fod) == "temporal" && config.Get("timeFormatType").IsTruthy() && isFieldDef(fod) && fod.Get("timeUnit").IsUndefined() {
			text = formatCustomType(fod, config.Get("timeFormat"), config.Get("timeFormatType"), "", false, config, "datum.value")
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
	o := e.ObjValue()
	for _, p := range props {
		if v := getMarkConfig(p, m.markDef, m.config, ""); !v.IsUndefined() {
			o.Set(p, signalOrValueRef(v))
		}
	}
	return e
}

// ---- assemble ----

func legendSetEncode(legend *Object, part, vgProp string, vgRef Value) {
	setAxisEncode(legend, part, vgProp, vgRef)
}

func getFieldKeyForChannel(m Model, channel string) string {
	if u := asUnit(m); u != nil {
		if fd := u.fieldDef(channel); fd.Get("field").IsTruthy() {
			return fd.Get("field").AsString()
		}
	}
	var childFields []string
	for _, c := range m.children() {
		if f := getFieldKeyForChannel(c, channel); f != "" {
			childFields = append(childFields, f)
		}
	}
	if len(childFields) > 0 {
		u := uniqueStrings(childFields)
		if len(u) == 1 {
			return u[0]
		}
	}
	return ""
}

func legendsAreMergeCompatible(m Model, a, b string) bool {
	if a == b {
		return true
	}
	ta, tb := m.b().getScaleType(a), m.b().getScaleType(b)
	if ta == "" || tb == "" {
		return false
	}
	return hasDiscreteDomain(ta) == hasDiscreteDomain(tb)
}

func legendGroupKey(fieldKey, channel string) string {
	if fieldKey != "" {
		return "field:" + fieldKey
	}
	return "channel:" + channel
}

func extractDiscreteValuesFromDomain(domain Value) []Value {
	if domain.IsArr() {
		var out []Value
		for _, d := range domain.Items() {
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
			if f.IsArr() {
				for _, d := range f.Items() {
					if d.IsStr() || d.IsNum() || d.IsBool() {
						vals = append(vals, d)
					}
				}
			}
		}
		if len(vals) > 0 {
			return uniqueValues(vals)
		}
	}
	return nil
}

func uniqueValues(in []Value) []Value {
	seen := map[string]bool{}
	var out []Value
	for _, v := range in {
		h := hashOf(v)
		if !seen[h] {
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
	return extractDiscreteValuesFromDomain(assembleDomain(m, channel))
}

func unionDiscreteValuesForChannels(m Model, a, b string) []Value {
	va, vb := getDiscreteValuesForChannel(m, a), getDiscreteValuesForChannel(m, b)
	if va != nil && vb != nil {
		return uniqueValues(append(append([]Value{}, va...), vb...))
	}
	return nil
}

func setImplicitLegendValues(c *legendComponent, values []Value) {
	if len(values) > 0 {
		vp := c.getWithExplicit("values")
		if !vp.explicit {
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
	return hashOf(assembleDomain(m, a)) == hashOf(assembleDomain(m, b))
}

type legendEntry struct {
	channel string
	cmpt    *legendComponent
}

func assembleLegends(m Model) []Value {
	if v5 {
		return assembleLegends58(m)
	}
	index := m.b().comp.legends
	byGroup := newOmap[[]*legendEntry]()
	for _, channel := range index.keyList() {
		fieldKey := getFieldKeyForChannel(m, channel)
		groupKey := legendGroupKey(fieldKey, channel)
		lc, _ := index.get(channel)
		if !byGroup.has(groupKey) {
			byGroup.set(groupKey, []*legendEntry{{channel, lc.cloneLegend()}})
			continue
		}
		merged := false
		for _, existing := range byGroup.m[groupKey] {
			if !legendsAreMergeCompatible(m, existing.channel, channel) {
				continue
			}
			if mm := mergeLegendComponent(existing.cmpt, lc); mm != nil {
				ta, tb := m.b().getScaleType(existing.channel), m.b().getScaleType(channel)
				if ta != "" && tb != "" && hasDiscreteDomain(ta) && hasDiscreteDomain(tb) {
					if domainsExplicitAndEqual(m, existing.channel, channel) {
						setImplicitLegendValues(existing.cmpt, getDiscreteValuesForChannel(m, existing.channel))
					} else {
						setImplicitLegendValues(existing.cmpt, unionDiscreteValuesForChannels(m, existing.channel, channel))
					}
				}
				merged = true
				break
			}
		}
		if !merged {
			byGroup.m[groupKey] = append(byGroup.m[groupKey], &legendEntry{channel, lc.cloneLegend()})
		}
	}
	var out []Value
	for _, k := range byGroup.keys {
		for _, e := range byGroup.m[k] {
			if l := assembleLegend(e.cmpt, m.b().config); l.IsTruthy() {
				out = append(out, l)
			}
		}
	}
	return out
}

func assembleLegend(cmpt *legendComponent, config Value) Value {
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
		expr := labelExpr.AsString()
		lu := legend.Lookup("encode").Get("labels").Get("update")
		if lu.IsObj() && isSignalRef(lu.Get("text")) {
			expr = strings.ReplaceAll(expr, "datum.label", signalOf(lu.Get("text")))
		}
		legendSetEncode(legend, "labels", "text", mkv("signal", expr))
	}
	return jsval.Obj(legend)
}
