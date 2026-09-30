package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Mark compilation — vega-lite/src/compile/mark/*.ts.

type markCompiler struct {
	vgMark                string
	encodeEntry           func(m *unitModel) Value
	postEncodingTransform func(m *unitModel) Value
}

func mkIgnore(align, baseline, color, orient, size, theta string) encodeIgnore {
	return encodeIgnore{"align": align, "baseline": baseline, "color": color, "orient": orient, "size": size, "theta": theta}
}

func withBase(m *unitModel, ig encodeIgnore, parts ...Value) Value {
	o := jsval.NewObject(16)
	spreadV(o, baseEncodeEntry(m, ig))
	for _, p := range parts {
		spreadV(o, p)
	}
	return jsval.Obj(o)
}

func symbolEncode(m *unitModel, fixedShape string) Value {
	var shape Value
	if fixedShape != "" {
		shape = mkv("shape", mkv("value", fixedShape))
	} else {
		shape = nonPosition("shape", m, nonPositionOpts{})
	}
	return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "include", "ignore"),
		pointPosition(chX, m, pointPositionOpts{defaultPos: "mid"}),
		pointPosition(chY, m, pointPositionOpts{defaultPos: "mid"}),
		nonPosition("size", m, nonPositionOpts{}),
		nonPosition("angle", m, nonPositionOpts{}),
		shape,
	)
}

var markCompilers = map[string]markCompiler{
	"arc": {vgMark: "arc", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			pointPosition(chX, m, pointPositionOpts{defaultPos: "mid"}),
			pointPosition(chY, m, pointPositionOpts{defaultPos: "mid"}),
			rectPosition(m, chRadius),
			rectPosition(m, chTheta),
		)
	}},
	"area": {vgMark: "area", encodeEntry: func(m *unitModel) Value {
		horizontal := m.markDef.Get("orient").AsString() == "horizontal"
		vertical := m.markDef.Get("orient").AsString() == "vertical"
		return withBase(m, mkIgnore("ignore", "ignore", "include", "include", "ignore", "ignore"),
			pointOrRangePosition(chX, m, rangePosOpts{defaultPos: "zeroOrMin", defaultPos2: "zeroOrMin", rangeFlag: horizontal}),
			pointOrRangePosition(chY, m, rangePosOpts{defaultPos: "zeroOrMin", defaultPos2: "zeroOrMin", rangeFlag: vertical}),
			definedEncode(m),
		)
	}},
	"bar": {vgMark: "rect", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			rectPosition(m, chX), rectPosition(m, chY))
	}},
	"circle": {vgMark: "symbol", encodeEntry: func(m *unitModel) Value { return symbolEncode(m, "circle") }},
	"square": {vgMark: "symbol", encodeEntry: func(m *unitModel) Value { return symbolEncode(m, "square") }},
	"point":  {vgMark: "symbol", encodeEntry: func(m *unitModel) Value { return symbolEncode(m, "") }},
	"geoshape": {vgMark: "shape", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"))
	}, postEncodingTransform: func(m *unitModel) Value {
		shapeDef := m.encoding.Get("shape")
		t := mk("type", "geoshape", "projection", strOrUndef(m.projectionName(false)))
		if shapeDef.IsTruthy() && isFieldDef(shapeDef) && channelDefType(shapeDef) == "geojson" {
			t.Set("field", jsval.Str(vgField(shapeDef, fieldRefOption{expr: "datum"})))
		}
		return arr(jsval.Obj(t))
	}},
	"image": {vgMark: "image", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "ignore", "ignore", "ignore", "ignore"),
			rectPosition(m, chX), rectPosition(m, chY), textEncode(m, "url"))
	}},
	"line": {vgMark: "line", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			pointPosition(chX, m, pointPositionOpts{defaultPos: "mid"}),
			pointPosition(chY, m, pointPositionOpts{defaultPos: "mid"}),
			nonPosition("size", m, nonPositionOpts{vgChannel: "strokeWidth"}),
			definedEncode(m),
		)
	}},
	"trail": {vgMark: "trail", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "include", "ignore"),
			pointPosition(chX, m, pointPositionOpts{defaultPos: "mid"}),
			pointPosition(chY, m, pointPositionOpts{defaultPos: "mid"}),
			nonPosition("size", m, nonPositionOpts{}),
			definedEncode(m),
		)
	}},
	"rect": {vgMark: "rect", encodeEntry: func(m *unitModel) Value {
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			rectPosition(m, chX), rectPosition(m, chY))
	}},
	"rule": {vgMark: "rule", encodeEntry: func(m *unitModel) Value {
		orient := m.markDef.Get("orient").AsString()
		enc := m.encoding
		if !enc.Get("x").IsTruthy() && !enc.Get("y").IsTruthy() && !enc.Get("latitude").IsTruthy() && !enc.Get("longitude").IsTruthy() {
			return mkv()
		}
		defX, defY := "mid", "mid"
		if orient == "horizontal" {
			defX = "zeroOrMax"
		}
		if orient == "vertical" {
			defY = "zeroOrMax"
		}
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			pointOrRangePosition(chX, m, rangePosOpts{defaultPos: defX, defaultPos2: "zeroOrMin", rangeFlag: orient != "vertical"}),
			pointOrRangePosition(chY, m, rangePosOpts{defaultPos: defY, defaultPos2: "zeroOrMin", rangeFlag: orient != "horizontal"}),
			nonPosition("size", m, nonPositionOpts{vgChannel: "strokeWidth"}),
		)
	}},
	"text": {vgMark: "text", encodeEntry: func(m *unitModel) Value {
		align, baseline := undef, undef
		if getMarkPropOrConfigSimple("align", m.markDef, m.config).IsUndefined() {
			align = jsval.Str("center")
		}
		if getMarkPropOrConfigSimple("baseline", m.markDef, m.config).IsUndefined() {
			baseline = jsval.Str("middle")
		}
		return withBase(m, mkIgnore("include", "include", "include", "ignore", "ignore", "include"),
			pointPosition(chX, m, pointPositionOpts{defaultPos: "mid"}),
			pointPosition(chY, m, pointPositionOpts{defaultPos: "mid"}),
			textEncode(m, "text"),
			nonPosition("size", m, nonPositionOpts{vgChannel: "fontSize"}),
			nonPosition("angle", m, nonPositionOpts{}),
			valueIfDefined("align", align),
			valueIfDefined("baseline", baseline),
			pointPosition(chRadius, m, pointPositionOpts{defaultPos: ""}),
			pointPosition(chTheta, m, pointPositionOpts{defaultPos: ""}),
		)
	}},
	"tick": {vgMark: "rect", encodeEntry: func(m *unitModel) Value {
		orient := m.markDef.Get("orient").AsString()
		vgSizeAxis, vgThicknessAxis, vgThicknessChannel := chY, chX, "width"
		if orient == "horizontal" {
			vgSizeAxis, vgThicknessAxis, vgThicknessChannel = chX, chY, "height"
		}
		vgc := "xc"
		if vgThicknessAxis == chY {
			vgc = "yc"
		}
		thick := jsval.NewObject(1)
		thick.Set(vgThicknessChannel, signalOrValueRef(getMarkPropOrConfigSimple("thickness", m.markDef, m.config)))
		return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
			rectPosition(m, vgSizeAxis),
			pointPosition(vgThicknessAxis, m, pointPositionOpts{defaultPos: "mid", vgChannel: vgc}),
			jsval.Obj(thick),
		)
	}},
}

func valueIfDefined(prop string, value Value) Value {
	if !value.IsUndefined() {
		return mkv(prop, signalOrValueRef(value))
	}
	return undef
}

func definedEncode(m *unitModel) Value {
	fields := newSset()
	m.forEachFieldDef(func(fd Value, channel string) {
		if !isScaleChannel(channel) {
			return
		}
		scaleType := m.getScaleType(channel)
		if scaleType == "" {
			return
		}
		mode := getScaleInvalidDataMode(m.markDef, m.config, channel, scaleType, isCountingAggregateOp(fd.Get("aggregate")))
		if shouldBreakPath(mode) {
			opt := fieldRefOption{expr: "datum"}
			if m.stack != nil && m.stack.impute {
				opt.binSuffix = "mid"
			}
			if f := m.vgField(channel, opt); f != "" {
				fields.add(f)
			}
		}
	})
	if fields.size() > 0 {
		var parts []string
		for _, f := range fields.list() {
			parts = append(parts, fieldValidPredicate(f, true))
		}
		return mkv("defined", mkv("signal", joinStrings(parts, " && ")))
	}
	return undef
}

// ---- mark groups ----

const facetedPathPrefix = "faceted_path_"
const stackGroupPrefix = "stack_group_"

func parseMarkGroups(m *unitModel) []Value {
	mark := m.mark()
	if mark == "line" || mark == "area" || mark == "trail" {
		if details := pathGroupingFields(mark, m.encoding); len(details) > 0 {
			return getPathGroups(m, details)
		}
	} else if mark == "bar" {
		hasCornerRadius := false
		for _, p := range vgCornerRadiusChannels {
			if getMarkPropOrConfigSimple(p, m.markDef, m.config).IsTruthy() {
				hasCornerRadius = true
			}
		}
		if m.stack != nil && !m.fieldDef("size").IsTruthy() && hasCornerRadius {
			return getGroupsForStackedBarWithCornerRadius(m)
		}
	}
	return getMarkGroup(m, "")
}

func getPathGroups(m *unitModel, details []string) []Value {
	return []Value{mkv(
		"name", m.getName("pathgroup"),
		"type", "group",
		"from", mkv("facet", mkv(
			"name", facetedPathPrefix+m.requestDataName(dsMain),
			"data", m.requestDataName(dsMain),
			"groupby", strsVal(details),
		)),
		"encode", mkv("update", mkv("width", mkv("field", mkv("group", "width")), "height", mkv("field", mkv("group", "height")))),
		"marks", jsval.Arr(getMarkGroup(m, facetedPathPrefix)),
	)}
}

func getGroupsForStackedBarWithCornerRadius(m *unitModel) []Value {
	markV := getMarkGroup(m, stackGroupPrefix)[0]
	update := markV.Get("encode").Get("update")
	fieldScale := m.scaleName(m.stack.fieldChannel, false)
	stackField := func(opt fieldRefOption) string { return m.vgField(m.stack.fieldChannel, opt) }
	stackFieldGroup := func(fn, expr string) string {
		fields := []string{
			stackField(fieldRefOption{prefix: "min", suffix: "start", expr: expr}),
			stackField(fieldRefOption{prefix: "max", suffix: "start", expr: expr}),
			stackField(fieldRefOption{prefix: "min", suffix: "end", expr: expr}),
			stackField(fieldRefOption{prefix: "max", suffix: "end", expr: expr}),
		}
		parts := make([]string, len(fields))
		for i, f := range fields {
			parts[i] = "scale('" + fieldScale + "'," + f + ")"
		}
		return fn + "(" + joinStrings(parts, ",") + ")"
	}
	var groupUpdate, innerGroupUpdate *Object
	if m.stack.fieldChannel == "x" {
		groupUpdate = spread(jsval.NewObject(8), jsval.Obj(pick(update, append([]string{"y", "yc", "y2", "height"}, vgCornerRadiusChannels...)...)))
		groupUpdate.Set("x", mkv("signal", stackFieldGroup("min", "datum")))
		groupUpdate.Set("x2", mkv("signal", stackFieldGroup("max", "datum")))
		groupUpdate.Set("clip", mkv("value", true))
		innerGroupUpdate = mk("x", mkv("field", mkv("group", "x"), "mult", -1), "height", mkv("field", mkv("group", "height")))
		nu := cloneObj(omit(update, "y", "yc", "y2"))
		nu.Set("height", mkv("field", mkv("group", "height")))
		update = jsval.Obj(nu)
	} else {
		groupUpdate = spread(jsval.NewObject(8), jsval.Obj(pick(update, "x", "xc", "x2", "width")))
		groupUpdate.Set("y", mkv("signal", stackFieldGroup("min", "datum")))
		groupUpdate.Set("y2", mkv("signal", stackFieldGroup("max", "datum")))
		groupUpdate.Set("clip", mkv("value", true))
		innerGroupUpdate = mk("y", mkv("field", mkv("group", "y"), "mult", -1), "width", mkv("field", mkv("group", "width")))
		nu := cloneObj(omit(update, "x", "xc", "x2"))
		nu.Set("width", mkv("field", mkv("group", "width")))
		update = jsval.Obj(nu)
	}
	uo := update.ObjValue()
	for _, key := range vgCornerRadiusChannels {
		configValue := getMarkConfig(key, m.markDef, m.config, "")
		if uo.Lookup(key).IsTruthy() {
			groupUpdate.Set(key, uo.Lookup(key))
			uo.Delete(key)
		} else if configValue.IsTruthy() {
			groupUpdate.Set(key, signalOrValueRef(configValue))
		}
		if configValue.IsTruthy() {
			uo.Set(key, mkv("value", 0))
		}
	}
	var groupby []string
	for _, gc := range m.stack.groupbyChannels {
		gd := m.fieldDef(gc)
		if f := vgField(gd, fieldRefOption{}); f != "" {
			groupby = append(groupby, f)
		}
		if gd.Get("bin").IsTruthy() || gd.Get("timeUnit").IsTruthy() {
			groupby = append(groupby, vgField(gd, fieldRefOption{binSuffix: "end"}))
		}
	}
	for _, prop := range []string{"stroke", "strokeWidth", "strokeJoin", "strokeCap", "strokeDash", "strokeDashOffset", "strokeMiterLimit", "strokeOpacity"} {
		if v := uo.Lookup(prop); v.IsTruthy() {
			groupUpdate.Set(prop, v)
		} else if cv := getMarkConfig(prop, m.markDef, m.config, ""); !cv.IsUndefined() {
			groupUpdate.Set(prop, signalOrValueRef(cv))
		}
	}
	if groupUpdate.Lookup("stroke").IsTruthy() {
		groupUpdate.Set("strokeForeground", mkv("value", true))
		groupUpdate.Set("strokeOffset", mkv("value", 0))
	}
	// the mark's encode.update was replaced with the reduced update
	mo := cloneObj(markV.ObjValue())
	mo.Set("encode", mkv("update", update))
	return []Value{mkv(
		"type", "group",
		"from", mkv("facet", mkv(
			"data", m.requestDataName(dsMain),
			"name", stackGroupPrefix+m.requestDataName(dsMain),
			"groupby", strsVal(groupby),
			"aggregate", mkv(
				"fields", arr(stackField(fieldRefOption{suffix: "start"}), stackField(fieldRefOption{suffix: "start"}), stackField(fieldRefOption{suffix: "end"}), stackField(fieldRefOption{suffix: "end"})),
				"ops", arr("min", "max", "min", "max"),
			),
		)),
		"encode", mkv("update", jsval.Obj(groupUpdate)),
		"marks", arr(mkv("type", "group", "encode", mkv("update", jsval.Obj(innerGroupUpdate)), "marks", arr(jsval.Obj(mo)))),
	)}
}

func getSort(m *unitModel) Value {
	encoding, stack, mark, markDef, config := m.encoding, m.stack, m.mark(), m.markDef, m.config
	order := encoding.Get("order")
	isNullOrFalse := func(v Value) bool { return v.IsNull() || (v.IsBool() && !v.BoolValue()) }
	if (!order.IsArr() && isValueDef(order) && isNullOrFalse(order.Get("value"))) ||
		(!order.IsTruthy() && isNullOrFalse(getMarkPropOrConfigSimple("order", markDef, config))) {
		return undef
	} else if (order.IsArr() || isFieldDef(order)) && stack == nil {
		f, o := sortParams(order, fieldRefOption{expr: "datum"})
		return mkv("field", jsval.Arr(f), "order", jsval.Arr(o))
	} else if isPathMarkName(mark) {
		dim := chX
		if markDef.Get("orient").AsString() == "horizontal" {
			dim = chY
		}
		if isFieldDef(encoding.Get(dim)) {
			return mkv("field", dim)
		}
	}
	return undef
}

func getMarkGroup(m *unitModel, fromPrefix string) []Value {
	mark, markDef, encoding, config := m.mark(), m.markDef, m.encoding, m.config
	clip := firstDefined(markDef.Get("clip"), scaleClip(m), projectionClip(m))
	style := getStyles(markDef)
	key := encoding.Get("key")
	sort := getSort(m)
	interactive := interactiveFlag(m)
	if interactive.IsTruthy() {
		for _, name := range m.comp.selection.keyList() {
			s := m.comp.selection.lookup(name)
			if s.typ == "point" && !s.props.Lookup("bind").IsTruthy() && s.props.Lookup("on").AsString() != "pointerover" {
				if m.markDef.Get("cursor").IsNullish() {
					m.markDef.ObjValue().Set("cursor", jsval.Str("pointer"))
				}
				break
			}
		}
	}
	aria := getMarkPropOrConfigSimple("aria", markDef, config)
	mc := markCompilers[mark]
	var post Value
	if mc.postEncodingTransform != nil {
		post = mc.postEncodingTransform(m)
	}
	o := mk("name", m.getName("marks"), "type", mc.vgMark)
	if clip.IsTruthy() {
		o.Set("clip", clip)
	}
	if len(style) > 0 {
		o.Set("style", strsVal(style))
	}
	if key.IsTruthy() {
		o.Set("key", key.Get("field"))
	}
	if sort.IsTruthy() {
		o.Set("sort", sort)
	}
	if interactive.IsTruthy() {
		spread(o, interactive)
	}
	if aria.IsBool() && !aria.BoolValue() {
		o.Set("aria", aria)
	}
	o.Set("from", mkv("data", fromPrefix+m.requestDataName(dsMain)))
	o.Set("encode", mkv("update", mc.encodeEntry(m)))
	if post.IsTruthy() {
		o.Set("transform", post)
	}
	return []Value{jsval.Obj(o)}
}

func scaleClip(m *unitModel) Value {
	xs, ys := m.getScaleComponent(chX), m.getScaleComponent(chY)
	if (xs != nil && xs.get("selectionExtent").IsTruthy()) || (ys != nil && ys.get("selectionExtent").IsTruthy()) {
		return jsval.True
	}
	return undef
}

func projectionClip(m *unitModel) Value {
	p := m.comp.projection
	if p != nil && !p.isFit() {
		return jsval.True
	}
	return undef
}

func interactiveFlag(m *unitModel) Value {
	if m.comp.selection == nil {
		return jsval.Null
	}
	unitCount := m.comp.selection.len()
	parentCount := unitCount
	parent := m.parent
	for parent != nil && parentCount == 0 {
		if s := parent.b().comp.selection; s != nil {
			parentCount = s.len()
		}
		parent = parent.b().parent
	}
	if parentCount > 0 {
		return mkv("interactive", unitCount > 0 || m.mark() == "geoshape" || m.encoding.Get("tooltip").IsTruthy() || m.markDef.Get("tooltip").IsTruthy())
	}
	return jsval.Null
}
