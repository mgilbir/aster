package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Mark encoding — vega-lite/src/compile/mark/encode/*.ts. Each function
// returns an encode entry fragment (an object, or undefined) that callers
// spread into the mark's update block.

var vgMarkConfigs = []string{
	"aria", "description", "ariaRole", "ariaRoleDescription", "blend", "opacity", "fill", "fillOpacity",
	"stroke", "strokeCap", "strokeWidth", "strokeOpacity", "strokeDash", "strokeDashOffset", "strokeJoin",
	"strokeOffset", "strokeMiterLimit", "startAngle", "endAngle", "padAngle", "innerRadius", "outerRadius",
	"size", "shape", "interpolate", "tension", "orient", "align", "baseline", "text", "dir", "dx", "dy",
	"ellipsis", "limit", "radius", "theta", "angle", "font", "fontSize", "fontWeight", "fontStyle",
	"lineBreak", "lineHeight", "cursor", "href", "tooltip", "cornerRadius", "cornerRadiusTopLeft",
	"cornerRadiusTopRight", "cornerRadiusBottomLeft", "cornerRadiusBottomRight", "aspect", "width", "height",
	"url", "smooth",
}

var vgMarkIndex = strSet([]string{"arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"})

var vgCornerRadiusChannels = []string{"cornerRadius", "cornerRadiusTopLeft", "cornerRadiusTopRight", "cornerRadiusBottomLeft", "cornerRadiusBottomRight"}

// spreadV spreads src into dst (ignoring undefined).
func spreadV(dst *Object, src Value) {
	if src.IsObj() {
		spread(dst, src)
	}
}

// ---- wrapCondition ----

type wrapConditionOpts struct {
	model           *unitModel
	channelDef      Value
	vgChannel       string
	invalidValueRef Value
	mainRefFn       func(cd Value) Value
}

func wrapCondition(cc *compileCtx, o wrapConditionOpts) Value {
	if cc.v5 {
		return wrapCondition58(o)
	}
	var valueRefs []Value
	if cond := o.channelDef.Get("condition"); isConditionalDef(o.channelDef) && cond.IsTruthy() {
		for _, c := range arrayOf(cond) {
			cvr := o.mainRefFn(c)
			if isConditionalParameter(c) {
				test := parseSelectionPredicate(o.model, mkv("param", c.Get("param"), "empty", c.Get("empty")), nil, "datum")
				r := mk("test", test)
				spreadV(r, cvr)
				valueRefs = append(valueRefs, jsval.Obj(r))
			} else {
				test := expression(o.model.b().ctx, o.model, c.Get("test"), nil)
				r := mk("test", test)
				spreadV(r, cvr)
				valueRefs = append(valueRefs, jsval.Obj(r))
			}
		}
	}
	if !o.invalidValueRef.IsUndefined() {
		valueRefs = append(valueRefs, o.invalidValueRef)
	}
	if main := o.mainRefFn(o.channelDef); !main.IsUndefined() {
		valueRefs = append(valueRefs, main)
	}
	switch {
	case len(valueRefs) > 1 || (len(valueRefs) == 1 && valueRefs[0].Get("test").IsTruthy()):
		return mkv(o.vgChannel, jsval.Arr(valueRefs))
	case len(valueRefs) == 1:
		return mkv(o.vgChannel, valueRefs[0])
	}
	return mkv()
}

// ---- base encode entry ----

type encodeIgnore map[string]string

func baseEncodeEntry(m *unitModel, ignore encodeIgnore) Value {
	cc := m.b().ctx

	var fill, stroke Value
	if ignore["color"] == "include" {
		c := colorEncode(m, undef)
		fill, stroke = c.Get("fill"), c.Get("stroke")
	}
	o := jsval.NewObject(16)
	spreadV(o, markDefProperties(m.markDef, ignore))
	if cc.v5 {
		spreadV(o, wrapAllFieldsInvalid58(m, "fill", fill))
		spreadV(o, wrapAllFieldsInvalid58(m, "stroke", stroke))
	} else {
		if fill.IsTruthy() {
			o.Set("fill", fill)
		}
		if stroke.IsTruthy() {
			o.Set("stroke", stroke)
		}
	}
	for _, ch := range []string{"opacity", "fillOpacity", "strokeOpacity", "strokeWidth", "strokeDash"} {
		spreadV(o, nonPosition(ch, m, nonPositionOpts{}))
	}
	spreadV(o, zindexEncode(m))
	spreadV(o, tooltipEncode(m, false))
	spreadV(o, textEncode(m, "href"))
	spreadV(o, ariaEncode(m))
	return jsval.Obj(o)
}

func markDefProperties(mark Value, ignore encodeIgnore) Value {
	m := jsval.NewObject(8)
	for _, prop := range vgMarkConfigs {
		if prop == "aria" || prop == "width" || prop == "height" {
			continue
		}
		if hasProperty(mark, prop) && ignore[prop] != "ignore" {
			m.Set(prop, signalOrValueRef(mark.Get(prop)))
		}
	}
	return jsval.Obj(m)
}

// ---- color ----

func colorEncode(m *unitModel, filledOpt Value) Value {
	markDef, encoding, config := m.markDef, m.encoding, m.config
	markType := markDef.Get("type").AsString()
	filled := filledOpt
	if filled.IsNullish() {
		filled = getMarkPropOrConfigSimple("filled", markDef, config)
	}
	transparentIfNeeded := undef
	if contains([]string{"bar", "point", "circle", "square", "geoshape"}, markType) {
		transparentIfNeeded = jsval.Str("transparent")
	}
	filledTrue := filled.IsBool() && filled.BoolValue()
	filledFalse := filled.IsBool() && !filled.BoolValue()
	fillChannel := ""
	if filledTrue {
		fillChannel = "color"
	}
	var defaultFill Value
	if fillChannel != "" {
		defaultFill = getMarkPropOrConfig("color", markDef, config, "fill", false)
	} else {
		defaultFill = getMarkPropOrConfig("", markDef, config, "fill", false)
	}
	if defaultFill.IsNullish() {
		var cm Value
		if filledTrue {
			cm = config.Get("mark").Get("color")
		} else {
			cm = config.Get("mark").Get("false")
		}
		defaultFill = coalesce(cm, transparentIfNeeded)
	}
	var defaultStroke Value
	if filledFalse {
		defaultStroke = getMarkPropOrConfig("color", markDef, config, "stroke", false)
	} else {
		defaultStroke = getMarkPropOrConfig("", markDef, config, "stroke", false)
	}
	if defaultStroke.IsNullish() {
		if filledFalse {
			defaultStroke = config.Get("mark").Get("color")
		} else {
			defaultStroke = config.Get("mark").Get("false")
		}
	}
	colorVgChannel := "stroke"
	if filled.IsTruthy() {
		colorVgChannel = "fill"
	}
	out := jsval.NewObject(4)
	if defaultFill.IsTruthy() {
		out.Set("fill", signalOrValueRef(defaultFill))
	}
	if defaultStroke.IsTruthy() {
		out.Set("stroke", signalOrValueRef(defaultStroke))
	}
	dv := defaultStroke
	if filled.IsTruthy() {
		dv = defaultFill
	}
	spreadV(out, nonPosition("color", m, nonPositionOpts{vgChannel: colorVgChannel, defaultValue: dv}))
	fv := undef
	if encoding.Get("fill").IsTruthy() {
		fv = defaultFill
	}
	spreadV(out, nonPosition("fill", m, nonPositionOpts{defaultValue: fv}))
	sv := undef
	if encoding.Get("stroke").IsTruthy() {
		sv = defaultStroke
	}
	spreadV(out, nonPosition("stroke", m, nonPositionOpts{defaultValue: sv}))
	return jsval.Obj(out)
}

// ---- non-position ----

type nonPositionOpts struct {
	vgChannel    string
	defaultRef   Value
	defaultValue Value
}

func nonPosition(channel string, m *unitModel, opt nonPositionOpts) Value {
	cc := m.b().ctx

	markDef, encoding, config := m.markDef, m.encoding, m.config
	channelDef := encoding.Get(channel)
	defaultRef, defaultValue := opt.defaultRef, opt.defaultValue
	if defaultRef.IsUndefined() {
		if defaultValue.IsNullish() {
			defaultValue = getMarkPropOrConfig(channel, markDef, config, opt.vgChannel, cc.v5 || !isConditionalDef(channelDef))
		}
		if !defaultValue.IsUndefined() {
			defaultRef = signalOrValueRef(defaultValue)
		}
	}
	scaleName := m.scaleName(channel, false)
	scale := m.getScaleComponent(channel)
	invalidRef := undef
	if !cc.v5 {
		invalidRef = getConditionalValueRefForIncludingInvalidValue(cc, channel, channelDef, scale, scaleName, markDef, config)
	}
	mainRefFn := func(cd Value) Value {
		return midPoint(cc, midPointParams{
			channel: channel, channelDef: cd, markDef: markDef, config: config,
			scaleName: scaleName, scale: scale, stack: nil, defaultRef: defaultRef,
		})
	}
	vg := opt.vgChannel
	if vg == "" {
		vg = channel
	}
	return wrapCondition(cc, wrapConditionOpts{model: m, channelDef: channelDef, vgChannel: vg, invalidValueRef: invalidRef, mainRefFn: mainRefFn})
}

// ---- text ----

func textEncode(m *unitModel, channel string) Value {
	cc := m.b().ctx

	return wrapCondition(cc, wrapConditionOpts{
		model: m, channelDef: m.encoding.Get(channel), vgChannel: channel,
		mainRefFn: func(cd Value) Value { return textRef(cc, cd, m.config, "datum") },
	})
}

func textRef(cc *compileCtx, channelDef Value, config Value, expr string) Value {
	if channelDef.IsTruthy() {
		if isValueDef(channelDef) {
			return signalOrValueRef(channelDef.Get("value"))
		}
		if isFieldOrDatumDef(cc, channelDef) {
			format, formatType := getFormatMixins(channelDef)
			return formatSignalRef(cc, formatSignalOpts{fieldOrDatumDef: channelDef, format: format, formatType: formatType, expr: expr, config: config})
		}
	}
	return undef
}

// ---- zindex / aria ----

func zindexEncode(m *unitModel) Value {
	cc := m.b().ctx

	order := m.encoding.Get("order")
	if !isPathMarkName(m.mark()) && isValueDef(order) {
		return wrapCondition(cc, wrapConditionOpts{
			model: m, channelDef: order, vgChannel: "zindex",
			mainRefFn: func(cd Value) Value { return signalOrValueRef(cd.Get("value")) },
		})
	}
	return mkv()
}

func ariaEncode(m *unitModel) Value {
	enable := getMarkPropOrConfigSimple("aria", m.markDef, m.config)
	if enable.IsBool() && !enable.BoolValue() {
		return mkv()
	}
	o := jsval.NewObject(3)
	if enable.IsTruthy() {
		o.Set("aria", enable)
	}
	spreadV(o, ariaRoleDescription(m))
	spreadV(o, descriptionEncode(m))
	return jsval.Obj(o)
}

func ariaRoleDescription(m *unitModel) Value {
	if m.config.Get("aria").IsBool() && !m.config.Get("aria").BoolValue() {
		return mkv()
	}
	desc := getMarkPropOrConfigSimple("ariaRoleDescription", m.markDef, m.config)
	if !desc.IsNullish() {
		return mkv("ariaRoleDescription", mkv("value", desc))
	}
	if vgMarkIndex[m.mark()] {
		return mkv()
	}
	return mkv("ariaRoleDescription", mkv("value", m.mark()))
}

func descriptionEncode(m *unitModel) Value {
	cc := m.b().ctx

	channelDef := m.encoding.Get("description")
	if channelDef.IsTruthy() {
		return wrapCondition(cc, wrapConditionOpts{
			model: m, channelDef: channelDef, vgChannel: "description",
			mainRefFn: func(cd Value) Value { return textRef(cc, cd, m.config, "datum") },
		})
	}
	dv := getMarkPropOrConfigSimple("description", m.markDef, m.config)
	if !dv.IsNullish() {
		return mkv("description", signalOrValueRef(dv))
	}
	if m.config.Get("aria").IsBool() && !m.config.Get("aria").BoolValue() {
		return mkv()
	}
	data := tooltipData(cc, m.encoding, m.stack, m.config, false)
	if data.len() == 0 {
		return undef
	}
	var parts []string
	idx := 0
	for _, key := range data.keys {
		value := data.m[key]
		if cc.v5 && value == "" {
			value = "undefined" // 5.8 interpolates a missing expression as is
		} else if !cc.v5 {
			// Vega-Lite 6 hides internal (underscore-prefixed) signals from the
			// description and flattens line breaks; 5.8 prints everything.
			if strings.HasPrefix(key, "_") {
				continue
			}
			value = strings.ReplaceAll(value, `\n`, " ")
		}
		sep := ""
		if idx > 0 {
			sep = "; "
		}
		parts = append(parts, `"`+sep+key+`: " + (`+value+`)`)
		idx++
	}
	return mkv("description", mkv("signal", strings.Join(parts, " + ")))
}

// ---- tooltip ----

func tooltipEncode(m *unitModel, reactiveGeom bool) Value {
	cc := m.b().ctx

	encoding, markDef, config, stack := m.encoding, m.markDef, m.config, m.stack
	channelDef := encoding.Get("tooltip")
	if channelDef.IsArr() {
		r := tooltipRefForEncoding(cc, mkv("tooltip", channelDef), stack, config, reactiveGeom)
		return mkv("tooltip", r)
	}
	datum := "datum"
	if reactiveGeom {
		datum = "datum.datum"
	}
	mainRefFn := func(cd Value) Value {
		if r := tooltipTextRef(cc, cd, config, datum); r.IsTruthy() {
			return r
		}
		if cd.IsNull() {
			return undef
		}
		markTooltip := getMarkPropOrConfigSimple("tooltip", markDef, config)
		if markTooltip.IsBool() && markTooltip.BoolValue() {
			markTooltip = mkv("content", "encoding")
		}
		switch {
		case markTooltip.IsStr():
			return mkv("value", markTooltip)
		case isObject(markTooltip):
			if isSignalRef(markTooltip) {
				return markTooltip
			} else if markTooltip.Get("content").IsStr() && markTooltip.Get("content").StrValue() == "encoding" {
				return tooltipRefForEncoding(cc, encoding, stack, config, reactiveGeom)
			}
			return mkv("signal", datum)
		}
		return undef
	}
	return wrapCondition(cc, wrapConditionOpts{model: m, channelDef: channelDef, vgChannel: "tooltip", mainRefFn: mainRefFn})
}

type tooltipEntry struct {
	channel string
	key     string
	value   string
	has     bool
}

// tooltipData maps each tooltip key to its value expression.
func tooltipData(cc *compileCtx, encoding Value, stack *stackProperties, config Value, reactiveGeom bool) *omap[string] {
	formatConfig := merged(config, config.Get("tooltipFormat"))
	if cc.v5 {
		formatConfig = config.ObjValue()
	}
	toSkip := map[string]bool{}
	expr := "datum"
	if reactiveGeom {
		expr = "datum.datum"
	}
	var tuples []tooltipEntry
	add := func(fDef Value, channel string) {
		mainChannel := getMainRangeChannel(channel)
		fd := fDef
		if !isTypedFieldDef(fDef) {
			o := cloneObj(fDef.ObjValue())
			o.Set("type", encoding.Get(mainChannel).Get("type"))
			fd = jsval.Obj(o)
		}
		var titleV Value
		if t := fd.Get("title"); t.IsTruthy() {
			titleV = t
		} else {
			titleV = defaultTitle(cc, fd, jsval.Obj(formatConfig))
		}
		var titleParts []string
		for _, x := range arrayOf(titleV) {
			titleParts = append(titleParts, x.AsString())
		}
		key := strings.Join(titleParts, ", ")
		if !cc.v5 {
			key = strings.ReplaceAll(key, `"`, `\"`)
		}
		value := ""
		hasValue := false
		if isXorY(channel) {
			channel2 := chY2
			if channel == chX {
				channel2 = chX2
			}
			fd2 := getFieldDef(cc, encoding.Get(channel2))
			if isBinned(fd.Get("bin")) && fd2.IsTruthy() {
				startField := vgField(cc, fd, fieldRefOption{expr: expr})
				endField := vgField(cc, fd2, fieldRefOption{expr: expr})
				format, formatType := getFormatMixins(fd)
				value = binFormatExpression(startField, endField, format, formatType, jsval.Obj(formatConfig))
				hasValue = true
				toSkip[channel2] = true
			}
		}
		if (isXorY(channel) || channel == chTheta || channel == chRadius) && stack != nil && stack.fieldChannel == channel && stack.offset == "normalize" {
			format, formatType := getFormatMixins(fd)
			value = signalOrEmpty(formatSignalRef(cc, formatSignalOpts{fieldOrDatumDef: fd, format: format, formatType: formatType, expr: expr, config: jsval.Obj(formatConfig), normalizeStack: true}))
			hasValue = true
		}
		if !hasValue {
			value = signalOrEmpty(tooltipTextRef(cc, fd, jsval.Obj(formatConfig), expr))
		}
		tuples = append(tuples, tooltipEntry{channel, key, value, true})
	}
	encodingEach(encoding, func(cd Value, channel string) {
		if isFieldDef(cc, cd) {
			add(cd, channel)
		} else if hasConditionalFieldDef(cc, cd) {
			add(cd.Get("condition"), channel)
		}
	})
	out := newOmap[string]()
	for _, t := range tuples {
		// `!out[key]`: an earlier entry whose value was undefined does not block this one.
		if !toSkip[t.channel] && (!out.has(t.key) || out.m[t.key] == "") {
			out.set(t.key, t.value)
		}
	}
	return out
}

func tooltipRefForEncoding(cc *compileCtx, encoding Value, stack *stackProperties, config Value, reactiveGeom bool) Value {
	data := tooltipData(cc, encoding, stack, config, reactiveGeom)
	var kv []string
	for _, k := range data.keys {
		kv = append(kv, `"`+k+`": `+jsName(data.m[k]))
	}
	if len(kv) > 0 {
		return mkv("signal", "{"+strings.Join(kv, ", ")+"}")
	}
	return undef
}

// tooltipTextRef is textRef, which Vega-Lite 6 extends with line breaks for
// discrete fields (5.8 has none).
func tooltipTextRef(cc *compileCtx, channelDef Value, config Value, expr string) Value {
	if cc.v5 {
		return textRef(cc, channelDef, config, expr)
	}
	return addLineBreaksToTooltip(cc, channelDef, config, expr)
}

func addLineBreaksToTooltip(cc *compileCtx, channelDef Value, config Value, expr string) Value {
	if isFieldDef(cc, channelDef) && isDiscreteType(channelDefType(channelDef)) && !channelDef.Get("timeUnit").IsTruthy() {
		format, formatType := getFormatMixins(channelDef)
		if !format.IsTruthy() && !formatType.IsTruthy() {
			fieldString := expr + `["` + channelDef.Get("field").AsString() + `"]`
			return mkv("signal", "isValid("+fieldString+") ? isArray("+fieldString+") ? join("+fieldString+", '\\n') : "+fieldString+` : ""+`+fieldString)
		}
	}
	return textRef(cc, channelDef, config, expr)
}

// signalOrEmpty is ref.signal where an undefined signal is "" (a real signal
// expression is never empty).
func signalOrEmpty(ref Value) string {
	if s := ref.Get("signal"); !s.IsUndefined() {
		return signalOf(ref)
	}
	return ""
}
