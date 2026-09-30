package vegalite

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Axes — vega-lite/src/compile/axis/*.ts.

var axisParts = []string{"domain", "grid", "labels", "ticks", "title"}

var axisPropertyType = map[string]string{
	"grid": "grid", "gridCap": "grid", "gridColor": "grid", "gridDash": "grid", "gridDashOffset": "grid",
	"gridOpacity": "grid", "gridScale": "grid", "gridWidth": "grid",
	"orient": "main", "bandPosition": "both", "aria": "main", "description": "main", "domain": "main",
	"domainCap": "main", "domainColor": "main", "domainDash": "main", "domainDashOffset": "main",
	"domainOpacity": "main", "domainWidth": "main", "format": "main", "formatType": "main",
	"labelAlign": "main", "labelAngle": "main", "labelBaseline": "main", "labelBound": "main",
	"labelColor": "main", "labelFlush": "main", "labelFlushOffset": "main", "labelFont": "main",
	"labelFontSize": "main", "labelFontStyle": "main", "labelFontWeight": "main", "labelLimit": "main",
	"labelLineHeight": "main", "labelOffset": "main", "labelOpacity": "main", "labelOverlap": "main",
	"labelPadding": "main", "labels": "main", "labelSeparation": "main", "maxExtent": "main",
	"minExtent": "main", "offset": "both", "position": "main", "tickCap": "main", "tickColor": "main",
	"tickDash": "main", "tickDashOffset": "main", "tickMinStep": "both", "tickOffset": "both",
	"tickOpacity": "main", "tickRound": "both", "ticks": "main", "tickSize": "main", "tickWidth": "both",
	"title": "main", "titleAlign": "main", "titleAnchor": "main", "titleAngle": "main",
	"titleBaseline": "main", "titleColor": "main", "titleFont": "main", "titleFontSize": "main",
	"titleFontStyle": "main", "titleFontWeight": "main", "titleLimit": "main", "titleLineHeight": "main",
	"titleOpacity": "main", "titlePadding": "main", "titleX": "main", "titleY": "main", "encode": "both",
	"scale": "both", "tickBand": "both", "tickCount": "both", "tickExtra": "both", "translate": "both",
	"values": "both", "zindex": "both",
}

// commonAxisProperties is COMMON_AXIS_PROPERTIES_INDEX's key order.
var commonAxisProperties = []string{
	"orient", "aria", "bandPosition", "description", "domain", "domainCap", "domainColor", "domainDash",
	"domainDashOffset", "domainOpacity", "domainWidth", "format", "formatType", "grid", "gridCap",
	"gridColor", "gridDash", "gridDashOffset", "gridOpacity", "gridWidth", "labelAlign", "labelAngle",
	"labelBaseline", "labelBound", "labelColor", "labelFlush", "labelFlushOffset", "labelFont",
	"labelFontSize", "labelFontStyle", "labelFontWeight", "labelLimit", "labelLineHeight", "labelOffset",
	"labelOpacity", "labelOverlap", "labelPadding", "labels", "labelSeparation", "maxExtent", "minExtent",
	"offset", "position", "tickBand", "tickCap", "tickColor", "tickCount", "tickDash", "tickDashOffset",
	"tickExtra", "tickMinStep", "tickOffset", "tickOpacity", "tickRound", "ticks", "tickSize", "tickWidth",
	"title", "titleAlign", "titleAnchor", "titleAngle", "titleBaseline", "titleColor", "titleFont",
	"titleFontSize", "titleFontStyle", "titleFontWeight", "titleLimit", "titleLineHeight", "titleOpacity",
	"titlePadding", "titleX", "titleY", "translate", "values", "zindex",
}

var axisProperties = strSet(append(append([]string{}, commonAxisProperties...), "style", "labelExpr", "encoding"))

// axisComponentProperties: disable, gridScale, scale, the common properties, labelExpr, encode.
var axisComponentProperties = append(append([]string{"disable", "gridScale", "scale"}, commonAxisProperties...), "labelExpr", "encode")

func isAxisProperty(p string) bool { return axisProperties[p] }

var conditionalAxisProp = map[string]struct {
	part, vgProp string
	isNull       bool
}{
	"labelAlign": {"labels", "align", false}, "labelBaseline": {"labels", "baseline", false},
	"labelColor": {"labels", "fill", false}, "labelFont": {"labels", "font", false},
	"labelFontSize": {"labels", "fontSize", false}, "labelFontStyle": {"labels", "fontStyle", false},
	"labelFontWeight": {"labels", "fontWeight", false}, "labelOpacity": {"labels", "opacity", false},
	"labelOffset": {"", "", true}, "labelPadding": {"", "", true},
	"gridColor": {"grid", "stroke", false}, "gridDash": {"grid", "strokeDash", false},
	"gridDashOffset": {"grid", "strokeDashOffset", false}, "gridOpacity": {"grid", "opacity", false},
	"gridWidth": {"grid", "strokeWidth", false},
	"tickColor": {"ticks", "stroke", false}, "tickDash": {"ticks", "strokeDash", false},
	"tickDashOffset": {"ticks", "strokeDashOffset", false}, "tickOpacity": {"ticks", "opacity", false},
	"tickSize": {"", "", true}, "tickWidth": {"ticks", "strokeWidth", false},
}

type axisComponent struct {
	*split
	mainExtracted bool
}

func newAxisComponent() *axisComponent { return &axisComponent{split: newSplit()} }

func (a *axisComponent) cloneAxis() *axisComponent {
	return &axisComponent{split: a.split.clone(), mainExtracted: a.mainExtracted}
}

func isFalseOrNull(v Value) bool { return v.IsNull() || (v.IsBool() && !v.BoolValue()) }

func (a *axisComponent) hasAxisPart(part string) bool {
	switch part {
	case "axis":
		return true
	case "grid", "title":
		return a.get(part).IsTruthy()
	}
	return !isFalseOrNull(a.get(part))
}

func (a *axisComponent) hasOrientSignalRef() bool { return isSignalRef(a.explicit.Lookup("orient")) }

func parseUnitAxes(u *unitModel) *omap[[]*axisComponent] {
	axes := newOmap[[]*axisComponent]()
	for _, channel := range positionScaleChannels {
		if sc, ok := u.comp.scales.get(channel); ok && sc != nil {
			axes.set(channel, []*axisComponent{parseAxis(channel, u)})
		}
	}
	return axes
}

var oppositeOrient = map[string]string{"bottom": "top", "top": "bottom", "left": "right", "right": "left"}

func parseLayerAxes(m *layerModel) {
	axes, resolve := m.comp.axes, m.comp.resolve
	axisCount := map[string]int{"top": 0, "bottom": 0, "right": 0, "left": 0}
	for _, child := range m.kids {
		child.parseAxesAndHeaders()
		for _, channel := range child.b().comp.axes.keyList() {
			resolve.axis[channel] = parseGuideResolve(m.comp.resolve, channel)
			if resolve.axis[channel] == "shared" {
				cur, _ := axes.get(channel)
				childAxes, _ := child.b().comp.axes.get(channel)
				mergedAxes, ok := mergeAxisComponents(cur, childAxes)
				if !ok {
					resolve.axis[channel] = "independent"
					axes.del(channel)
				} else {
					axes.set(channel, mergedAxes)
				}
			}
		}
	}
	for _, channel := range positionScaleChannels {
		for _, child := range m.kids {
			childAxes, ok := child.b().comp.axes.get(channel)
			if !ok || childAxes == nil {
				continue
			}
			if resolve.axis[channel] == "independent" {
				cur, _ := axes.get(channel)
				axes.set(channel, append(append([]*axisComponent{}, cur...), childAxes...))
				for _, ac := range childAxes {
					we := ac.getWithExplicit("orient")
					if isSignalRef(we.value) {
						continue
					}
					orient := we.value.AsString()
					if axisCount[orient] > 0 && !we.explicit {
						opposite := oppositeOrient[orient]
						if axisCount[orient] > axisCount[opposite] {
							ac.set("orient", jsval.Str(opposite), false)
						}
					}
					axisCount[orient]++
				}
			}
			child.b().comp.axes.del(channel)
		}
		if resolve.axis[channel] == "independent" {
			if list, ok := axes.get(channel); ok && len(list) > 1 {
				for i, ac := range list {
					if i > 0 && ac.get("grid").IsTruthy() && !ac.explicit.Lookup("grid").IsTruthy() {
						ac.implicit.Set("grid", jsval.False)
					}
				}
			}
		}
	}
}

func mergeAxisComponents(merged, child []*axisComponent) ([]*axisComponent, bool) {
	if merged != nil {
		if len(merged) != len(child) {
			return nil, false
		}
		for i := range merged {
			mg, ch := merged[i], child[i]
			if (mg != nil) != (ch != nil) {
				return nil, false
			} else if mg != nil && ch != nil {
				mo, co := mg.getWithExplicit("orient"), ch.getWithExplicit("orient")
				if mo.explicit && co.explicit && !strictEq(mo.value, co.value) {
					return nil, false
				}
				merged[i] = mergeAxisComponent(mg, ch)
			}
		}
		return merged, true
	}
	out := make([]*axisComponent, len(child))
	for i, c := range child {
		out[i] = c.cloneAxis()
	}
	return out, true
}

func mergeAxisComponent(merged, child *axisComponent) *axisComponent {
	for _, prop := range axisComponentProperties {
		prop := prop
		mv := merged.getWithExplicit(prop)
		v := mergeValuesWithExplicit(&mv, child.getWithExplicit(prop), prop, "axis", func(v1, v2 withExplicit, _, _ string) withExplicit {
			switch prop {
			case "title":
				return mergeTitleComponent(v1, v2)
			case "gridScale":
				return withExplicit{v1.explicit, firstDefined(v1.value, v2.value)}
			}
			return defaultTieBreaker(v1, v2, prop, "axis")
		})
		merged.setWithExplicit(prop, v)
	}
	return merged
}

func axisIsExplicit(value Value, property string, axis Value, m *unitModel, channel string) bool {
	if property == "disable" {
		return !axis.IsUndefined()
	}
	if !axis.IsObj() {
		axis = mkv()
	}
	switch property {
	case "titleAngle", "labelAngle":
		la := axis.Get("labelAngle")
		var want Value
		if isSignalRef(la) {
			want = la
		} else {
			want = normalizeAngle(la)
		}
		return strictEq(value, want)
	case "values":
		return axis.Get("values").IsTruthy()
	case "encode":
		return axis.Get("encoding").IsTruthy() || axis.Get("labelAngle").IsTruthy()
	case "title":
		if strictEq(value, getFieldDefTitle(m, channel)) {
			return true
		}
	}
	return strictEq(value, axis.Get(property))
}

func normalizeAngle(a Value) Value {
	if a.IsUndefined() {
		return undef
	}
	f := a.AsDouble()
	if !a.IsNum() {
		f = jsval.ToNumber(a)
	}
	return jsval.Num(math.Mod(math.Mod(f, 360)+360, 360))
}

var propsToAlwaysIncludeConfig = strSet([]string{"grid", "translate", "format", "formatType", "orient", "labelExpr", "tickCount", "position", "tickMinStep"})

type axisRuleParams struct {
	fod        Value
	axis       Value
	channel    string
	model      *unitModel
	scaleType  string
	orient     Value
	labelAngle Value
	format     Value
	formatType Value
	mark       string
	config     Value
}

func parseAxis(channel string, m *unitModel) *axisComponent {
	axis := m.axis(channel)
	axisComponent := newAxisComponent()
	fod := getFieldOrDatumDef(m.encoding.Get(channel))
	mark, config := m.mark(), m.config
	axisChannel := "axisY"
	if channel == chX {
		axisChannel = "axisX"
	}
	orient := or(axis.Get("orient"), config.Get(axisChannel).Get("orient"), config.Get("axis").Get("orient"), jsval.Str(defaultAxisOrient(channel)))
	scaleType := m.getScaleComponent(channel).get("type").AsString()
	axisConfigs := getAxisConfigs(channel, scaleType, orient, m.config)
	var disable Value
	if !axis.IsUndefined() {
		disable = jsval.Bool(!axis.IsTruthy())
	} else {
		disable, _ = getAxisConfig("disable", config.Get("style"), axis.Get("style"), axisConfigs)
	}
	axisComponent.set("disable", disable, !axis.IsUndefined())
	if disable.IsTruthy() {
		return axisComponent
	}
	if axis.IsUndefined() || !axis.IsTruthy() {
		axis = mkv()
	}
	labelAngle := getLabelAngle(fod, axis, channel, config.Get("style"), axisConfigs)
	formatType := guideFormatType(axis.Get("formatType"), fod, scaleType)
	format := guideFormat(fod, channelDefType(fod), axis.Get("format"), axis.Get("formatType"), config, true)
	rp := axisRuleParams{fod: fod, axis: axis, channel: channel, model: m, scaleType: scaleType, orient: orient, labelAngle: labelAngle,
		format: format, formatType: formatType, mark: mark, config: config}
	for _, property := range axisComponentProperties {
		var value Value
		if rule, ok := axisRules[property]; ok {
			value = rule(rp)
		} else if isAxisProperty(property) {
			value = axis.Get(property)
		}
		hasValue := !value.IsUndefined()
		explicit := axisIsExplicit(value, property, axis, m, channel)
		if hasValue && explicit {
			axisComponent.set(property, value, explicit)
		} else {
			var configValue Value
			var configFrom string
			if isAxisProperty(property) && property != "values" {
				configValue, configFrom = getAxisConfig(property, config.Get("style"), axis.Get("style"), axisConfigs)
			}
			hasConfigValue := !configValue.IsUndefined()
			if hasValue && !hasConfigValue {
				axisComponent.set(property, value, explicit)
			} else if configFrom != "vgAxisConfig" || (propsToAlwaysIncludeConfig[property] && hasConfigValue) || isConditionalAxisValue(configValue) || isSignalRef(configValue) {
				axisComponent.set(property, configValue, false)
			}
		}
	}
	axisEncoding := coalesce(axis.Get("encoding"), mkv())
	axisEncode := jsval.NewObject(4)
	for _, part := range axisParts {
		if !axisComponent.hasAxisPart(part) {
			continue
		}
		axisEncodingPart := guideEncodeEntry(coalesce(axisEncoding.Get(part), mkv()), m)
		value := axisEncodingPart
		if part == "labels" {
			value = axisLabelsEncode(m, channel, axisEncodingPart)
		}
		if !value.IsUndefined() && !isEmptyObj(value) {
			axisEncode.Set(part, mkv("update", value))
		}
	}
	if axisEncode.Len() > 0 {
		axisComponent.set("encode", jsval.Obj(axisEncode), axis.Get("encoding").IsTruthy() || !axis.Get("labelAngle").IsUndefined())
	}
	return axisComponent
}

func defaultAxisOrient(channel string) string {
	if channel == chX {
		return "bottom"
	}
	return "left"
}

func guideEncodeEntry(encoding Value, m *unitModel) Value {
	out := jsval.NewObject(4)
	for _, channel := range keysOf(encoding) {
		spreadV(out, wrapCondition(wrapConditionOpts{
			model: m, channelDef: encoding.Get(channel), vgChannel: channel,
			mainRefFn: func(def Value) Value { return signalOrValueRef(def.Get("value")) },
		}))
	}
	return jsval.Obj(out)
}

var axisRules = map[string]func(p axisRuleParams) Value{
	"scale":      func(p axisRuleParams) Value { return strOrUndef(p.model.scaleName(p.channel, false)) },
	"format":     func(p axisRuleParams) Value { return p.format },
	"formatType": func(p axisRuleParams) Value { return p.formatType },
	"grid": func(p axisRuleParams) Value {
		if g := p.axis.Get("grid"); !g.IsNullish() {
			return g
		}
		return jsval.Bool(defaultGrid(p.scaleType, p.fod))
	},
	"gridScale": func(p axisRuleParams) Value { return gridScaleFor(p.model, p.channel) },
	"labelAlign": func(p axisRuleParams) Value {
		if v := p.axis.Get("labelAlign"); v.IsTruthy() {
			return v
		}
		return defaultLabelAlign(p.labelAngle, p.orient, p.channel)
	},
	"labelAngle": func(p axisRuleParams) Value { return p.labelAngle },
	"labelBaseline": func(p axisRuleParams) Value {
		if v := p.axis.Get("labelBaseline"); v.IsTruthy() {
			return v
		}
		return defaultLabelBaseline(p.labelAngle, p.orient, p.channel, false)
	},
	"labelFlush": func(p axisRuleParams) Value {
		if v := p.axis.Get("labelFlush"); !v.IsNullish() {
			return v
		}
		return defaultLabelFlush(channelDefType(p.fod), p.channel)
	},
	"labelOverlap": func(p axisRuleParams) Value {
		if v := p.axis.Get("labelOverlap"); !v.IsNullish() {
			return v
		}
		sort := undef
		if isFieldDef(p.fod) {
			sort = p.fod.Get("sort")
		}
		return defaultLabelOverlapAxis(channelDefType(p.fod), p.scaleType, isFieldDef(p.fod) && p.fod.Get("timeUnit").IsTruthy(), sort)
	},
	"orient": func(p axisRuleParams) Value { return p.orient },
	"tickCount": func(p axisRuleParams) Value {
		if v := p.axis.Get("tickCount"); !v.IsNullish() {
			return v
		}
		var size Value
		switch p.channel {
		case chX:
			size = p.model.getSizeSignalRef("width")
		case chY:
			size = p.model.getSizeSignalRef("height")
		}
		return defaultTickCount(p.fod, p.scaleType, size, p.axis.Get("values"))
	},
	"tickMinStep": func(p axisRuleParams) Value {
		if v := p.axis.Get("tickMinStep"); !v.IsNullish() {
			return v
		}
		return defaultTickMinStep(p.format, p.fod)
	},
	"title": func(p axisRuleParams) Value {
		if t := p.axis.Get("title"); !t.IsUndefined() {
			return t
		}
		if t := getFieldDefTitle(p.model, p.channel); !t.IsUndefined() {
			return t
		}
		fd := p.model.typedFieldDef(p.channel)
		channel2 := chY2
		if p.channel == chX {
			channel2 = chX2
		}
		fd2 := p.model.fieldDef(channel2)
		var f1, f2 []Value
		if fd.IsTruthy() {
			f1 = []Value{toFieldDefBase(fd)}
		}
		if isFieldDef(fd2) {
			f2 = []Value{toFieldDefBase(fd2)}
		}
		return jsval.Arr(mergeTitleFieldDefs(f1, f2))
	},
	"values": func(p axisRuleParams) Value { return guideValues(p.axis, p.fod) },
	"zindex": func(p axisRuleParams) Value {
		if z := p.axis.Get("zindex"); !z.IsNullish() {
			return z
		}
		return jsval.Int(defaultZindex(p.mark, p.fod))
	},
}

func guideValues(guide Value, fod Value) Value {
	vals := guide.Get("values")
	if vals.IsArr() {
		return jsval.Arr(valueArray(fod, vals.Items()))
	} else if isSignalRef(vals) {
		return vals
	}
	return undef
}

func defaultGrid(scaleType string, fd Value) bool {
	return !hasDiscreteDomain(scaleType) && isFieldDef(fd) && !isBinning(fd.Get("bin")) && !isBinned(fd.Get("bin"))
}

func gridScaleFor(m *unitModel, channel string) Value {
	gridChannel := chX
	if channel == chX {
		gridChannel = chY
	}
	if m.getScaleComponent(gridChannel) != nil {
		return strOrUndef(m.scaleName(gridChannel, false))
	}
	return undef
}

func getLabelAngle(fod Value, axis Value, channel string, styleConfig Value, axisConfigs axisConfigSet) Value {
	labelAngle := axis.Get("labelAngle")
	if !labelAngle.IsUndefined() {
		if isSignalRef(labelAngle) {
			return labelAngle
		}
		return normalizeAngle(labelAngle)
	}
	angle, _ := getAxisConfig("labelAngle", styleConfig, axis.Get("style"), axisConfigs)
	if !angle.IsUndefined() {
		return normalizeAngle(angle)
	}
	t := channelDefType(fod)
	if channel == chX && (t == "nominal" || t == "ordinal") && !(isFieldDef(fod) && fod.Get("timeUnit").IsTruthy()) {
		return jsval.Int(270)
	}
	return undef
}

func normalizeAngleExpr(angle Value) string {
	return "(((" + signalOf(angle) + " % 360) + 360) % 360)"
}

func defaultLabelBaseline(angle, orient Value, channel string, alwaysIncludeMiddle bool) Value {
	if angle.IsUndefined() {
		return undef
	}
	if channel == chX {
		if isSignalRef(angle) {
			a := normalizeAngleExpr(angle)
			var orientIsTop string
			if isSignalRef(orient) {
				orientIsTop = "(" + signalOf(orient) + ` === "top")`
			} else {
				orientIsTop = boolStr(orient.IsStr() && orient.StrValue() == "top")
			}
			return sig("(45 < " + a + " && " + a + " < 135) || (225 < " + a + " && " + a + " < 315) ? \"middle\" :" +
				"(" + a + " <= 45 || 315 <= " + a + ") === " + orientIsTop + " ? \"bottom\" : \"top\"")
		}
		ang := angle.AsDouble()
		if (45 < ang && ang < 135) || (225 < ang && ang < 315) {
			return jsval.Str("middle")
		}
		if isSignalRef(orient) {
			op := "!=="
			if ang <= 45 || 315 <= ang {
				op = "==="
			}
			return sig(signalOf(orient) + " " + op + ` "top" ? "bottom" : "top"`)
		}
		isTop := orient.IsStr() && orient.StrValue() == "top"
		if (ang <= 45 || 315 <= ang) == isTop {
			return jsval.Str("bottom")
		}
		return jsval.Str("top")
	}
	if isSignalRef(angle) {
		a := normalizeAngleExpr(angle)
		var orientIsLeft string
		if isSignalRef(orient) {
			orientIsLeft = "(" + signalOf(orient) + ` === "left")`
		} else {
			orientIsLeft = boolStr(orient.IsStr() && orient.StrValue() == "left")
		}
		middle := "null"
		if alwaysIncludeMiddle {
			middle = `"middle"`
		}
		return sig(a + " <= 45 || 315 <= " + a + " || (135 <= " + a + " && " + a + " <= 225) ? " + middle + " : (45 <= " + a + " && " + a + " <= 135) === " + orientIsLeft + ` ? "top" : "bottom"`)
	}
	ang := angle.AsDouble()
	if ang <= 45 || 315 <= ang || (135 <= ang && ang <= 225) {
		if alwaysIncludeMiddle {
			return jsval.Str("middle")
		}
		return jsval.Null
	}
	if isSignalRef(orient) {
		op := "!=="
		if 45 <= ang && ang <= 135 {
			op = "==="
		}
		return sig(signalOf(orient) + " " + op + ` "left" ? "top" : "bottom"`)
	}
	isLeft := orient.IsStr() && orient.StrValue() == "left"
	if (45 <= ang && ang <= 135) == isLeft {
		return jsval.Str("top")
	}
	return jsval.Str("bottom")
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func defaultLabelAlign(angle, orient Value, channel string) Value {
	if angle.IsUndefined() {
		return undef
	}
	isX := channel == chX
	startAngle := 0.0
	mainOrient := "bottom"
	if !isX {
		startAngle = 90
		mainOrient = "left"
	}
	if isSignalRef(angle) {
		a := normalizeAngleExpr(angle)
		var orientIsMain string
		if isSignalRef(orient) {
			orientIsMain = "(" + signalOf(orient) + ` === "` + mainOrient + `")`
		} else {
			orientIsMain = boolStr(orient.IsStr() && orient.StrValue() == mainOrient)
		}
		var lhs string
		if startAngle != 0 {
			lhs = "(" + a + " + 90)"
		} else {
			lhs = a
		}
		res := "null"
		if !isX {
			res = `"center"`
		}
		sa := jsval.JSNumberString(startAngle)
		return sig("(" + lhs + " % 180 === 0) ? " + res + " :" +
			"(" + sa + " < " + a + " && " + a + " < " + jsval.JSNumberString(180+startAngle) + ") === " + orientIsMain + ` ? "left" : "right"`)
	}
	ang := angle.AsDouble()
	if math.Mod(ang+startAngle, 180) == 0 {
		if isX {
			return jsval.Null
		}
		return jsval.Str("center")
	}
	if isSignalRef(orient) {
		op := "!=="
		if startAngle < ang && ang < 180+startAngle {
			op = "==="
		}
		return sig(signalOf(orient) + " " + op + ` "` + mainOrient + `" ? "left" : "right"`)
	}
	if (startAngle < ang && ang < 180+startAngle) == (orient.IsStr() && orient.StrValue() == mainOrient) {
		return jsval.Str("left")
	}
	return jsval.Str("right")
}

func defaultLabelFlush(typ, channel string) Value {
	if channel == chX && (typ == "quantitative" || typ == "temporal") {
		return jsval.True
	}
	return undef
}

func defaultLabelOverlapAxis(typ, scaleType string, hasTimeUnit bool, sort Value) Value {
	if (hasTimeUnit && !isObject(sort)) || (typ != "nominal" && typ != "ordinal") {
		if scaleType == "log" || scaleType == "symlog" {
			return jsval.Str("greedy")
		}
		return jsval.True
	}
	return undef
}

func defaultTickCount(fod Value, scaleType string, size Value, values Value) Value {
	if !values.IsTruthy() && !hasDiscreteDomain(scaleType) && scaleType != "log" {
		if isFieldDef(fod) {
			if isBinning(fod.Get("bin")) {
				return sig("ceil(" + signalOf(size) + "/10)")
			}
			if fod.Get("timeUnit").IsTruthy() {
				if u := normalizeTimeUnit(fod.Get("timeUnit")).Get("unit"); u.IsStr() && contains([]string{"month", "hours", "day", "quarter"}, u.StrValue()) {
					return undef
				}
			}
		}
		return sig("ceil(" + signalOf(size) + "/40)")
	}
	return undef
}

func defaultTickMinStep(format Value, fod Value) Value {
	if format.IsStr() && format.StrValue() == "d" {
		return jsval.Int(1)
	}
	if isFieldDef(fod) {
		if tu := fod.Get("timeUnit"); tu.IsTruthy() {
			if s := durationExpr(tu, nil); s != "" {
				return sig(s)
			}
		}
	}
	return undef
}

func getFieldDefTitle(m *unitModel, channel string) Value {
	channel2 := chY2
	if channel == chX {
		channel2 = chX2
	}
	fd, fd2 := m.fieldDef(channel), m.fieldDef(channel2)
	var t1, t2 Value
	if fd.IsTruthy() {
		t1 = fd.Get("title")
	}
	if fd2.IsTruthy() {
		t2 = fd2.Get("title")
	}
	switch {
	case t1.IsTruthy() && t2.IsTruthy():
		return mergeTitle(t1, t2)
	case t1.IsTruthy():
		return t1
	case t2.IsTruthy():
		return t2
	case !t1.IsUndefined():
		return t1
	case !t2.IsUndefined():
		return t2
	}
	return undef
}

func defaultZindex(mark string, fd Value) int {
	if mark == "rect" && isDiscreteDef(fd) {
		return 1
	}
	return 0
}

// ---- axis config ----

type axisConfigSet struct {
	vlOnlyAxisConfig, vgAxisConfig, axisConfigStyle Value
}

func getAxisConfigFromConfigTypes(configTypes []string, config Value, channel string, orient Value) Value {
	out := jsval.NewObject(8)
	for _, ct := range configTypes {
		if ct == "axisOrient" {
			orient1, key1, key2 := "left", "axisLeft", "axisRight"
			if channel == chX {
				orient1, key1, key2 = "bottom", "axisBottom", "axisTop"
			}
			oc1, oc2 := coalesceObj(config.Get(key1)), coalesceObj(config.Get(key2))
			props := uniqueStrings(append(append([]string{}, keysOf(oc1)...), keysOf(oc2)...))
			for _, prop := range props {
				out.Set(prop, mkv("signal", signalOf(orient)+` === "`+orient1+`" ? `+signalOrStringValue(oc1.Get(prop)).AsString()+" : "+signalOrStringValue(oc2.Get(prop)).AsString()))
			}
			continue
		}
		spreadV(out, config.Get(ct))
	}
	return jsval.Obj(out)
}

func getAxisConfigs(channel, scaleType string, orient Value, config Value) axisConfigSet {
	var typeBased []string
	switch {
	case scaleType == "band":
		typeBased = []string{"axisDiscrete", "axisBand"}
	case scaleType == "point":
		typeBased = []string{"axisDiscrete", "axisPoint"}
	case isQuantitativeScale(scaleType):
		typeBased = []string{"axisQuantitative"}
	case scaleType == "time" || scaleType == "utc":
		typeBased = []string{"axisTemporal"}
	}
	axisChannel := "axisY"
	if channel == chX {
		axisChannel = "axisX"
	}
	axisOrient := "axisOrient"
	if !isSignalRef(orient) {
		axisOrient = "axis" + titleCase(orient.AsString())
	}
	vlOnly := append([]string{}, typeBased...)
	for _, c := range typeBased {
		vlOnly = append(vlOnly, axisChannel+c[4:])
	}
	vgTypes := []string{"axis", axisOrient, axisChannel}
	return axisConfigSet{
		vlOnlyAxisConfig: getAxisConfigFromConfigTypes(vlOnly, config, channel, orient),
		vgAxisConfig:     getAxisConfigFromConfigTypes(vgTypes, config, channel, orient),
		axisConfigStyle:  getAxisConfigStyle(append(append([]string{}, vgTypes...), vlOnly...), config),
	}
}

func getAxisConfigStyle(types []string, config Value) Value {
	out := jsval.NewObject(4)
	for _, ct := range types {
		if style := config.Get(ct).Get("style"); style.IsTruthy() {
			for _, s := range arrayOf(style) {
				spreadV(out, config.Get("style").Get(s.AsString()))
			}
		}
	}
	return jsval.Obj(out)
}

// getAxisConfig finds a property in the style config, then vlOnlyAxisConfig,
// vgAxisConfig, axisConfigStyle; it returns the value and where it came from.
func getAxisConfig(property string, styleConfigIndex Value, style Value, cfgs axisConfigSet) (Value, string) {
	if sc := getStyleConfig(property, arrayStrings(style), styleConfigIndex); !sc.IsUndefined() {
		return sc, "style"
	}
	for _, from := range []struct {
		name string
		v    Value
	}{{"vlOnlyAxisConfig", cfgs.vlOnlyAxisConfig}, {"vgAxisConfig", cfgs.vgAxisConfig}, {"axisConfigStyle", cfgs.axisConfigStyle}} {
		if v := from.v.Get(property); !v.IsUndefined() {
			return v, from.name
		}
	}
	return undef, ""
}

func arrayStrings(v Value) []string {
	var out []string
	for _, x := range arrayOf(v) {
		out = append(out, x.AsString())
	}
	return out
}

// ---- labels encode ----

func axisLabelsEncode(m *unitModel, channel string, specified Value) Value {
	encoding, config := m.encoding, m.config
	fod := getFieldOrDatumDef(encoding.Get(channel))
	if !fod.IsTruthy() {
		fod = getFieldOrDatumDef(encoding.Get(getSecondaryRangeChannel(channel)))
	}
	axis := coalesceObj(m.axis(channel))
	format, formatType := axis.Get("format"), axis.Get("formatType")
	withText := func(text Value) Value {
		o := mk("text", text)
		spreadV(o, specified)
		return jsval.Obj(o)
	}
	if isCustomFormatType(formatType) {
		return withText(formatCustomType(fod, format, formatType, "", false, config, "datum.value"))
	} else if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() {
		if channelDefType(fod) == "quantitative" {
			if isPositionFieldOrDatumDef(fod) && fod.Get("stack").IsStr() && fod.Get("stack").StrValue() == "normalize" && config.Get("normalizedNumberFormatType").IsTruthy() {
				return withText(formatCustomType(fod, config.Get("normalizedNumberFormat"), config.Get("normalizedNumberFormatType"), "", false, config, "datum.value"))
			} else if config.Get("numberFormatType").IsTruthy() {
				return withText(formatCustomType(fod, config.Get("numberFormat"), config.Get("numberFormatType"), "", false, config, "datum.value"))
			}
		}
		if channelDefType(fod) == "temporal" && config.Get("timeFormatType").IsTruthy() && isFieldDef(fod) && !fod.Get("timeUnit").IsTruthy() {
			return withText(formatCustomType(fod, config.Get("timeFormat"), config.Get("timeFormatType"), "", false, config, "datum.value"))
		}
	}
	return specified
}

// ---- assemble ----

func assembleAxisTitle(title Value, config Value) Value {
	if !title.IsTruthy() {
		return undef
	}
	if title.IsArr() && !isText(title) {
		var parts []string
		for _, fd := range title.Items() {
			parts = append(parts, joinItem(defaultTitle(fd, config)))
		}
		return jsval.Str(strings.Join(parts, ", "))
	}
	return title
}

func setAxisEncode(axis *Object, part, vgProp string, vgRef Value) {
	enc := axis.Lookup("encode")
	if !enc.IsObj() {
		enc = mkv()
		axis.Set("encode", enc)
	}
	p := enc.Get(part)
	if !p.IsObj() {
		p = mkv()
		enc.ObjValue().Set(part, p)
	}
	u := p.Get("update")
	if !u.IsObj() {
		u = mkv()
		p.ObjValue().Set("update", u)
	}
	u.ObjValue().Set(vgProp, vgRef)
}

func assembleAxis(a *axisComponent, kind string, config Value, header bool) Value {
	comb := a.combine()
	disable, orient, scale, labelExpr, title, zindex := comb.Lookup("disable"), comb.Lookup("orient"), comb.Lookup("scale"), comb.Lookup("labelExpr"), comb.Lookup("title"), comb.Lookup("zindex")
	axis := omap2(omit(jsval.Obj(comb), "disable", "orient", "scale", "labelExpr", "title", "zindex"))
	if disable.IsTruthy() {
		return undef
	}
	// Properties are dropped after the loop: deleting from a large object one key
	// at a time is quadratic.
	drop := map[string]bool{}
	for _, prop := range append([]string(nil), axis.Keys()...) {
		propType := axisPropertyType[prop]
		propValue := axis.Lookup(prop)
		if propType != "" && propType != kind && propType != "both" {
			drop[prop] = true
		} else if isConditionalAxisValue(propValue) {
			condition := propValue.Get("condition")
			valueOrSignalRef := jsval.Obj(omit(propValue, "condition"))
			conditions := arrayOf(condition)
			if pi, ok := conditionalAxisProp[prop]; ok && !pi.isNull {
				var vgRef []Value
				for _, c := range conditions {
					test := c.Get("test")
					o := mk("test", expression(nil, test, nil))
					spreadV(o, jsval.Obj(omit(c, "test")))
					vgRef = append(vgRef, jsval.Obj(o))
				}
				vgRef = append(vgRef, valueOrSignalRef)
				setAxisEncode(axis, pi.part, pi.vgProp, jsval.Arr(vgRef))
				drop[prop] = true
			} else if ok && pi.isNull {
				var sb strings.Builder
				for _, c := range conditions {
					sb.WriteString(expression(nil, c.Get("test"), nil) + " ? " + exprFromValueRefOrSignalRef(jsval.Obj(omit(c, "test"))) + " : ")
				}
				sb.WriteString(exprFromValueRefOrSignalRef(valueOrSignalRef))
				axis.Set(prop, mkv("signal", sb.String()))
			}
		} else if isSignalRef(propValue) {
			if pi, ok := conditionalAxisProp[prop]; ok && !pi.isNull {
				setAxisEncode(axis, pi.part, pi.vgProp, propValue)
				drop[prop] = true
			}
		}
		if (prop == "labelAlign" || prop == "labelBaseline") && axis.Lookup(prop).IsNull() {
			drop[prop] = true
		}
	}
	if len(drop) > 0 {
		axis = omitSet(axis, drop)
	}
	if kind == "grid" {
		if !axis.Lookup("grid").IsTruthy() {
			return undef
		}
		if enc := axis.Lookup("encode"); enc.IsTruthy() {
			ne := jsval.NewObject(1)
			if g := enc.Get("grid"); g.IsTruthy() {
				ne.Set("grid", g)
			}
			axis.Set("encode", jsval.Obj(ne))
			if ne.Len() == 0 {
				axis.Delete("encode")
			}
		}
		o := mk("scale", scale, "orient", orient)
		spreadV(o, jsval.Obj(axis))
		o.Set("domain", jsval.False)
		o.Set("labels", jsval.False)
		o.Set("aria", jsval.False)
		o.Set("maxExtent", jsval.Int(0))
		o.Set("minExtent", jsval.Int(0))
		o.Set("ticks", jsval.False)
		o.Set("zindex", firstDefined(zindex, jsval.Int(0)))
		return jsval.Obj(o)
	}
	if !header && a.mainExtracted {
		return undef
	}
	if !labelExpr.IsUndefined() {
		expr := labelExpr.AsString()
		text := axis.Lookup("encode").Get("labels").Get("update").Get("text")
		if axis.Lookup("encode").Get("labels").Get("update").IsObj() && isSignalRef(text) {
			expr = strings.ReplaceAll(expr, "datum.label", signalOf(text))
		}
		setAxisEncode(axis, "labels", "text", mkv("signal", expr))
	}
	if axis.Lookup("labelAlign").IsNull() {
		axis.Delete("labelAlign")
	}
	if enc := axis.Lookup("encode"); enc.IsObj() {
		eo := enc.ObjValue()
		for _, part := range axisParts {
			if !a.hasAxisPart(part) {
				eo.Delete(part)
			}
		}
		if eo.Len() == 0 {
			axis.Delete("encode")
		}
	}
	titleString := assembleAxisTitle(title, config)
	o := mk("scale", scale, "orient", orient, "grid", false)
	if titleString.IsTruthy() {
		o.Set("title", titleString)
	}
	spreadV(o, jsval.Obj(axis))
	if config.Get("aria").IsBool() && !config.Get("aria").BoolValue() {
		o.Set("aria", jsval.False)
	}
	o.Set("zindex", firstDefined(zindex, jsval.Int(0)))
	return jsval.Obj(o)
}

func omap2(o *Object) *Object { return o }

func assembleAxisSignals(m Model) []Value {
	var signals []Value
	axes := m.b().comp.axes
	for _, channel := range positionScaleChannels {
		list, ok := axes.get(channel)
		if !ok {
			continue
		}
		for _, axis := range list {
			if !axis.get("disable").IsTruthy() && !axis.get("gridScale").IsTruthy() {
				sizeType := "width"
				if channel == chX {
					sizeType = "height"
				}
				update := signalOf(m.b().getSizeSignalRef(sizeType))
				if sizeType != update {
					signals = append(signals, mkv("name", sizeType, "update", update))
				}
			}
		}
	}
	return signals
}

func assembleAxes(axisComponents *omap[[]*axisComponent], config Value) []Value {
	x, _ := axisComponents.get(chX)
	y, _ := axisComponents.get(chY)
	var out []Value
	add := func(list []*axisComponent, kind string) {
		for _, a := range list {
			if v := assembleAxis(a, kind, config, false); v.IsTruthy() {
				out = append(out, v)
			}
		}
	}
	add(x, "grid")
	add(y, "grid")
	add(x, "main")
	add(y, "main")
	return out
}

// joinItem is how Array.prototype.join renders one element: nullish is "".
func joinItem(v Value) string {
	if v.IsNullish() {
		return ""
	}
	return v.AsString()
}

// omitSet copies o without the keys in drop, preserving order.
func omitSet(o *Object, drop map[string]bool) *Object {
	out := jsval.NewObject(o.Len())
	for i := 0; i < o.Len(); i++ {
		if !drop[o.KeyAt(i)] {
			out.Set(o.KeyAt(i), o.ValueAt(i))
		}
	}
	return out
}
