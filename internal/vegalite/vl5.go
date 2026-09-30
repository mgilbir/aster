package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Vega-Lite 5.8 behaviour that differs from the 6.4 compiler. Each function
// here replaces its 6.4 counterpart when Options.Version is "5.8"; the 6.4
// code paths are untouched.

// wrapCondition58 is vega-lite 5.8's wrapCondition: conditions come first, no
// invalid-value reference, and the array form only when a condition exists.
func wrapCondition58(o wrapConditionOpts) Value {
	main := o.mainRefFn(o.channelDef)
	cond := o.channelDef.Get("condition")
	if isConditionalDef(o.channelDef) && cond.IsTruthy() {
		var refs []Value
		for _, c := range arrayOf(cond) {
			cvr := o.mainRefFn(c)
			var test string
			if isConditionalParameter(c) {
				test = parseSelectionPredicate(o.model, mkv("param", c.Get("param"), "empty", c.Get("empty")), nil, "datum")
			} else {
				test = expression(o.model.b().ctx, o.model, c.Get("test"), nil)
			}
			r := mk("test", test)
			spreadV(r, cvr)
			refs = append(refs, jsval.Obj(r))
		}
		if !main.IsUndefined() {
			refs = append(refs, main)
		}
		return mkv(o.vgChannel, jsval.Arr(refs))
	}
	if !main.IsUndefined() {
		return mkv(o.vgChannel, main)
	}
	return mkv()
}

// allFieldsInvalidPredicate58 joins the invalid (or valid) tests of every
// continuous-scale field of the given channels. withImpute selects the bin
// mid suffix of stacked marks (used by `defined`).
func allFieldsInvalidPredicate58(m *unitModel, invalid bool, channels []string, withImpute bool) string {
	filterIndex := newSset()
	for _, channel := range channels {
		if sc := m.getScaleComponent(channel); sc != nil {
			opt := fieldRefOption{expr: "datum"}
			if withImpute && m.stack != nil && m.stack.impute {
				opt.binSuffix = "mid"
			}
			field := m.vgField(channel, opt)
			if field != "" && hasContinuousDomain(sc.get("type").AsString()) {
				filterIndex.add(field)
			}
		}
	}
	fields := filterIndex.list()
	if len(fields) == 0 {
		return ""
	}
	op := " && "
	if invalid {
		op = " || "
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fieldValidPredicate(f, !invalid)
	}
	return joinStrings(parts, op)
}

// wrapAllFieldsInvalid58 hides marks whose scaled fields are all invalid when
// the mark's `invalid` is "hide".
func wrapAllFieldsInvalid58(m *unitModel, channel string, valueRef Value) Value {
	if inv := getMarkPropOrConfigSimple("invalid", m.markDef, m.config); inv.IsStr() && inv.StrValue() == "hide" && valueRef.IsTruthy() && !isPathMarkName(m.mark()) {
		if test := allFieldsInvalidPredicate58(m, true, scaleChannels, false); test != "" {
			refs := []Value{mkv("test", test, "value", jsval.Null)}
			refs = append(refs, arrayOf(valueRef)...)
			return mkv(channel, jsval.Arr(refs))
		}
	}
	if valueRef.IsTruthy() {
		return mkv(channel, valueRef)
	}
	return mkv()
}

// definedEncode58 breaks paths at invalid positions unless `invalid` is falsy.
func definedEncode58(m *unitModel) Value {
	if getMarkPropOrConfigSimple("invalid", m.markDef, m.config).IsTruthy() {
		if signal := allFieldsInvalidPredicate58(m, false, positionScaleChannels, true); signal != "" {
			return mkv("defined", mkv("signal", signal))
		}
	}
	return mkv()
}

// midPointRefWithPositionInvalidTest58 prepends the zero reference for invalid
// values when `invalid` is null.
func midPointRefWithPositionInvalidTest58(cc *compileCtx, p midPointParams) Value {
	ref := midPoint(cc, p)
	if isFieldDef(cc, p.channelDef) && !isCountingAggregateOp(p.channelDef.Get("aggregate")) && p.scale != nil && isContinuousToContinuous(p.scale.get("type").AsString()) {
		if isPathMarkName(p.markDef.Get("type").AsString()) {
			return ref
		}
		if getMarkPropOrConfigSimple("invalid", p.markDef, p.config).IsNull() {
			test := fieldValidPredicate(vgField(cc, p.channelDef, fieldRefOption{expr: "datum"}), false)
			var zero Value
			if getMainRangeChannel(p.channel) == chY {
				zero = mkv("test", test, "field", mkv("group", "height"))
			} else {
				zero = mkv("test", test, "value", 0)
			}
			return arr(zero, ref)
		}
	}
	return ref
}

// getPathSort58 sorts the marks of a path by the dimension field's own sort
// (6.x sorts by the dimension channel's value alone).
func getPathSort58(m *unitModel, dim string) Value {
	cc := m.b().ctx

	def := m.encoding.Get(dim)
	if !isFieldDef(cc, def) {
		return undef
	}
	s := def.Get("sort")
	switch {
	case s.IsArr():
		return mkv("field", vgField(cc, def, fieldRefOption{prefix: dim, suffix: "sort_index", expr: "datum"}))
	case isSortField(cc, s):
		var agg Value
		if encodingIsAggregate(cc, m.encoding) {
			agg = s.Get("op")
		}
		return mkv("field", vgField(cc, mkv("aggregate", agg, "field", s.Get("field")), fieldRefOption{expr: "datum"}))
	case isSortByEncoding(s):
		return mkv("field", vgField(cc, m.fieldDef(s.Get("encoding").AsString()), fieldRefOption{expr: "datum"}), "order", s.Get("order"))
	case s.IsNull():
		return undef
	}
	opt := fieldRefOption{expr: "datum"}
	if m.stack != nil && m.stack.impute {
		opt.binSuffix = "mid"
	}
	return mkv("field", vgField(cc, def, opt))
}

// domainDefinitelyIncludesZero58 is 5.8's ScaleComponent.domainDefinitelyIncludesZero.
func (s *scaleComponent) domainDefinitelyIncludesZero58() bool {
	if z := s.get("zero"); !(z.IsBool() && !z.BoolValue()) {
		return true
	}
	for _, d := range s.get("domains").Items() {
		if d.IsArr() && d.Len() == 2 {
			lo, hi := d.Index(0), d.Index(1)
			if !lo.IsUndefined() && !hi.IsUndefined() && jsval.ToNumber(lo) <= 0 && jsval.ToNumber(hi) >= 0 {
				return true
			}
		}
	}
	return false
}

// pointPositionDefaultRef58 is 5.8's zeroOrMin / zeroOrMax default reference.
func pointPositionDefaultRef58(m *unitModel, defaultPos, channel, scaleName string, scale *scaleComponent) Value {
	mainChannel := getMainRangeChannel(channel)
	switch defaultPos {
	case "zeroOrMin", "zeroOrMax":
		if scaleName != "" {
			scaleType := scale.get("type").AsString()
			if !(scaleType == "log" || scaleType == "time" || scaleType == "utc") && scale.domainDefinitelyIncludesZero58() {
				return mkv("scale", scaleName, "value", 0)
			}
		}
		if defaultPos == "zeroOrMin" {
			if mainChannel == chY {
				return mkv("field", mkv("group", "height"))
			}
			return mkv("value", 0)
		}
		switch mainChannel {
		case chRadius:
			return sig("min(" + signalOf(m.width()) + "," + signalOf(m.height()) + ")/2")
		case chTheta:
			return sig("2*PI")
		case chX:
			return mkv("field", mkv("group", "width"))
		case chY:
			return mkv("value", 0)
		}
	}
	return undef
}

// ---- rect position (5.8) ----

func rectPosition58(m *unitModel, channel string) Value {
	cc := m.b().ctx

	config, encoding, markDef := m.config, m.encoding, m.markDef
	mark := markDef.Get("type").AsString()
	channel2 := getSecondaryRangeChannel(channel)
	sizeChannel := getSizeChannel(channel)
	channelDef, channelDef2 := encoding.Get(channel), encoding.Get(channel2)
	scale := m.getScaleComponent(channel)
	scaleType := ""
	if scale != nil {
		scaleType = scale.get("type").AsString()
	}
	orient := markDef.Get("orient").AsString()
	hasSizeDef := coalesce(encoding.Get(sizeChannel), encoding.Get("size"), getMarkPropOrConfig("size", markDef, config, sizeChannel, false))
	isBarBand := mark == "bar" && ((channel == chX && orient == "vertical") || (channel != chX && orient == "horizontal"))
	if isFieldDef(cc, channelDef) &&
		(isBinning(channelDef.Get("bin")) || isBinned(channelDef.Get("bin")) || (channelDef.Get("timeUnit").IsTruthy() && !channelDef2.IsTruthy())) &&
		!(hasSizeDef.IsTruthy() && !isRelativeBandSize(hasSizeDef)) &&
		!hasDiscreteDomain(scaleType) {
		return rectBinPosition58(channelDef, channelDef2, channel, m)
	} else if ((isFieldOrDatumDef(cc, channelDef) && hasDiscreteDomain(scaleType)) || isBarBand) && !channelDef2.IsTruthy() {
		return positionAndSize58(channelDef, channel, m)
	}
	return rangePosition(channel, m, rangePosOpts{defaultPos: "zeroOrMax", defaultPos2: "zeroOrMin"})
}

func defaultSizeRef58(sizeChannel, scaleName string, scale *scaleComponent, config Value, bandSize Value) Value {
	switch {
	case isRelativeBandSize(bandSize):
		if scale != nil {
			scaleType := scale.get("type").AsString()
			if scaleType == "band" {
				bandWidth := "bandwidth('" + jsName(scaleName) + "')"
				if !(bandSize.Get("band").IsNum() && bandSize.Get("band").NumValue() == 1) {
					bandWidth = bandSize.Get("band").AsString() + " * " + bandWidth
				}
				return sig("max(0.25, " + bandWidth + ")")
			}
		} else {
			return mkv("mult", bandSize.Get("band"), "field", mkv("group", sizeChannel))
		}
	case isSignalRef(bandSize):
		return bandSize
	case bandSize.IsTruthy():
		return mkv("value", bandSize)
	}
	if scale != nil {
		if rng := scale.get("range"); isVgRangeStep(rng) && rng.Get("step").IsNum() {
			return mkv("value", rng.Get("step").NumValue()-2)
		}
	}
	return mkv("value", getViewConfigDiscreteStep(config.Get("view"), sizeChannel)-2)
}

func positionAndSize58(fd Value, channel string, m *unitModel) Value {
	cc := m.b().ctx

	markDef, encoding, config, stack := m.markDef, m.encoding, m.config, m.stack
	orient := markDef.Get("orient").AsString()
	scaleName := m.scaleName(channel, false)
	scale := m.getScaleComponent(channel)
	vgSizeChannel := getSizeChannel(channel)
	channel2 := getSecondaryRangeChannel(channel)
	offsetScaleName := m.scaleName(getOffsetChannel(channel), false)
	useVlSizeChannel := (orient == "horizontal" && channel == chY) || (orient == "vertical" && channel == chX)
	var sizeMixins Value
	if encoding.Get("size").IsTruthy() || markDef.Get("size").IsTruthy() {
		if useVlSizeChannel {
			sizeMixins = nonPosition("size", m, nonPositionOpts{vgChannel: vgSizeChannel, defaultRef: signalOrValueRef(markDef.Get("size"))})
		}
	}
	hasSizeFromMarkOrEncoding := sizeMixins.IsTruthy()
	scType := ""
	if scale != nil {
		scType = scale.get("type").AsString()
	}
	bandSize := getBandSize(cc, channel, fd, undef, markDef, config, scType, useVlSizeChannel)
	if !sizeMixins.IsTruthy() {
		name := offsetScaleName
		if name == "" {
			name = scaleName
		}
		sizeMixins = mkv(vgSizeChannel, defaultSizeRef58(vgSizeChannel, name, scale, config, bandSize))
	}
	defaultBandAlign := "middle"
	if scType == "band" && isRelativeBandSize(bandSize) && !hasSizeFromMarkOrEncoding {
		defaultBandAlign = "top"
	}
	vgChannel := vgAlignedPositionChannel(channel, markDef, config, defaultBandAlign)
	center := vgChannel == "xc" || vgChannel == "yc"
	bp0 := jsval.Int(0)
	if center {
		bp0 = jsval.Num(0.5)
	}
	po := positionOffset(channel, markDef, encoding, m, bp0)
	var bandPosition Value
	switch {
	case center:
		if po.offsetType == "encoding" {
			bandPosition = jsval.Int(0)
		} else {
			bandPosition = jsval.Num(0.5)
		}
	case isSignalRef(bandSize):
		// upstream interpolates the ref itself (not .signal) here.
		bandPosition = sig("(1-[object Object])/2")
	case isRelativeBandSize(bandSize):
		bandPosition = jsval.Num((1 - bandSize.Get("band").AsDouble()) / 2)
	default:
		bandPosition = jsval.Int(0)
	}
	posRef := midPointRefWithPositionInvalidTest(cc, midPointParams{
		channel: channel, channelDef: fd, markDef: markDef, config: config, scaleName: scaleName, scale: scale, stack: stack,
		offset: po.offset, defaultRefFn: pointPositionDefaultRef(m, "mid", channel, scaleName, scale), bandPosition: bandPosition,
	})
	if vgSizeChannel != "" {
		o := mk(vgChannel, posRef)
		spreadV(o, sizeMixins)
		return jsval.Obj(o)
	}
	vgChannel2 := getVgPositionChannel(channel2)
	sizeRef := sizeMixins.Get(vgSizeChannel)
	sizeOffset := sizeRef
	if po.offset.IsTruthy() {
		so := cloneObj(coalesceObj(sizeRef).ObjValue())
		so.Set("offset", po.offset)
		sizeOffset = jsval.Obj(so)
	}
	var second Value
	if posRef.IsArr() {
		p1 := cloneObj(coalesceObj(posRef.Index(1)).ObjValue())
		p1.Set("offset", sizeOffset)
		second = arr(posRef.Index(0), jsval.Obj(p1))
	} else {
		p := cloneObj(coalesceObj(posRef).ObjValue())
		p.Set("offset", sizeOffset)
		second = jsval.Obj(p)
	}
	return mkv(vgChannel, posRef, vgChannel2, second)
}

func getBinSpacing58(channel string, spacing float64, reverse, axisTranslate, offset Value) Value {
	if isPolarPositionChannel(channel) {
		return jsval.Int(0)
	}
	spacingOffset := spacing / 2
	if channel == chX || channel == chY2 {
		spacingOffset = -spacing / 2
	}
	if isSignalRef(reverse) || isSignalRef(offset) || isSignalRef(axisTranslate) {
		reverseExpr := signalOrStringValue(reverse)
		offsetExpr := signalOrStringValue(offset)
		axisTranslateExpr := signalOrStringValue(axisTranslate)
		so := jsval.JSNumberString(spacingOffset)
		t := ""
		if axisTranslateExpr.IsTruthy() {
			t = axisTranslateExpr.AsString() + " + "
		}
		r := ""
		if reverseExpr.IsTruthy() {
			r = "(" + reverseExpr.AsString() + " ? -1 : 1) * "
		}
		o := so
		if offsetExpr.IsTruthy() {
			o = "(" + offsetExpr.AsString() + " + " + so + ")"
		}
		return sig(t + r + o)
	}
	off := 0.0
	if offset.IsTruthy() {
		off = offset.AsDouble()
	}
	var delta float64
	if reverse.IsTruthy() {
		delta = -off - spacingOffset
	} else {
		delta = off + spacingOffset
	}
	return jsval.Num(axisTranslate.AsDouble() + delta)
}

func rectBinPosition58(fd, fd2 Value, channel string, m *unitModel) Value {
	cc := m.b().ctx

	config, markDef, encoding := m.config, m.markDef, m.encoding
	scale := m.getScaleComponent(channel)
	scaleName := m.scaleName(channel, false)
	scaleType := ""
	reverse := undef
	if scale != nil {
		scaleType = scale.get("type").AsString()
		reverse = scale.get("reverse")
	}
	bandSize := getBandSize(cc, channel, fd, undef, markDef, config, scaleType, false)
	var axis *axisComponent
	if axes, ok := m.comp.axes.get(channel); ok && len(axes) > 0 {
		axis = axes[0]
	}
	axisTranslate := jsval.Num(0.5)
	if axis != nil {
		if t := axis.get("translate"); !t.IsNullish() {
			axisTranslate = t
		}
	}
	spacing := 0.0
	if isXorY(channel) {
		if b := getMarkPropOrConfigSimple("binSpacing", markDef, config); !b.IsNullish() {
			spacing = b.AsDouble()
		}
	}
	channel2 := getSecondaryRangeChannel(channel)
	vgChannel, vgChannel2 := getVgPositionChannel(channel), getVgPositionChannel(channel2)
	offset := positionOffset(channel, markDef, encoding, m, jsval.Int(0)).offset
	var bandPosition Value
	switch {
	case isSignalRef(bandSize):
		bandPosition = sig("(1-" + signalOf(bandSize) + ")/2")
	case isRelativeBandSize(bandSize):
		bandPosition = jsval.Num((1 - bandSize.Get("band").AsDouble()) / 2)
	default:
		bandPosition = jsval.Num(0.5)
	}
	if isBinning(fd.Get("bin")) || fd.Get("timeUnit").IsTruthy() {
		var second Value
		if isSignalRef(bandPosition) {
			second = sig("1-" + signalOf(bandPosition))
		} else {
			second = jsval.Num(1 - bandPosition.NumValue())
		}
		return mkv(
			vgChannel2, rectBinRef58(cc, fd, scaleName, bandPosition, getBinSpacing58(channel2, spacing, reverse, axisTranslate, offset)),
			vgChannel, rectBinRef58(cc, fd, scaleName, second, getBinSpacing58(channel, spacing, reverse, axisTranslate, offset)),
		)
	} else if isBinned(fd.Get("bin")) {
		startRef := valueRefForFieldOrDatumDef(cc, fd, scaleName, fieldRefOption{}, refOffsetBand{offset: getBinSpacing58(channel2, spacing, reverse, axisTranslate, offset)})
		if isFieldDef(cc, fd2) {
			return mkv(vgChannel2, startRef, vgChannel, valueRefForFieldOrDatumDef(cc, fd2, scaleName, fieldRefOption{}, refOffsetBand{offset: getBinSpacing58(channel, spacing, reverse, axisTranslate, offset)}))
		} else if isObject(fd.Get("bin")) && fd.Get("bin").Get("step").IsTruthy() {
			return mkv(vgChannel2, startRef, vgChannel, mkv(
				"signal", `scale("`+scaleName+`", `+vgField(cc, fd, fieldRefOption{expr: "datum"})+" + "+fd.Get("bin").Get("step").AsString()+")",
				"offset", getBinSpacing58(channel, spacing, reverse, axisTranslate, offset),
			))
		}
	}
	return undef
}

func rectBinRef58(cc *compileCtx, fd Value, scaleName string, bandPosition, offset Value) Value {
	return interpolatedSignalRef(cc, interpolatedOpts{scaleName: scaleName, fod: fd, bandPosition: bandPosition, offset: offset})
}

// tick58 is 5.8's tick encode entry: a point position with a size and a
// thickness, not a rect.
func tick58(m *unitModel) Value {
	config, markDef := m.config, m.markDef
	orient := markDef.Get("orient").AsString()
	vgSizeChannel, vgThicknessChannel := "height", "width"
	if orient == "horizontal" {
		vgSizeChannel, vgThicknessChannel = "width", "height"
	}
	thick := jsval.NewObject(1)
	thick.Set(vgThicknessChannel, signalOrValueRef(getMarkPropOrConfigSimple("thickness", markDef, config)))
	return withBase(m, mkIgnore("ignore", "ignore", "include", "ignore", "ignore", "ignore"),
		pointPosition(chX, m, pointPositionOpts{defaultPos: "mid", vgChannel: "xc"}),
		pointPosition(chY, m, pointPositionOpts{defaultPos: "mid", vgChannel: "yc"}),
		nonPosition("size", m, nonPositionOpts{defaultValue: tickDefaultSize58(m), vgChannel: vgSizeChannel}),
		jsval.Obj(thick),
	)
}

func tickDefaultSize58(m *unitModel) Value {
	config, markDef := m.config, m.markDef
	orient := markDef.Get("orient").AsString()
	vgSizeChannel, axis := "height", chY
	if orient == "horizontal" {
		vgSizeChannel, axis = "width", chX
	}
	scale := m.getScaleComponent(axis)
	v := coalesce(getMarkPropOrConfig("size", markDef, config, vgSizeChannel, false), config.Get("tick").Get("bandSize"))
	if !v.IsUndefined() && !v.IsNull() {
		return v
	}
	if scale != nil {
		if rng := scale.get("range"); rng.IsTruthy() && isVgRangeStep(rng) && rng.Get("step").IsNum() {
			return jsval.Num(float64(rng.Get("step").NumValue()*3) / 4)
		}
	}
	return jsval.Num(float64(getViewConfigDiscreteStep(config.Get("view"), vgSizeChannel)*3) / 4)
}

// mouseMoveEvent is the unit-tracking event: 6.x listens to pointer events,
// 5.8 to mouse events.
func mouseMoveEvent(cc *compileCtx) string {
	if cc.v5 {
		return "mousemove"
	}
	return "pointermove"
}

// sizeRangeMin58 is 5.8's size range minimum, which starts at 0 for a scale
// that includes zero.
func sizeRangeMin58(mark string, zero Value, config Value) Value {
	if zero.IsTruthy() {
		if isSignalRef(zero) {
			return sig(signalOf(zero) + " ? 0 : " + signalOrStringValue(sizeRangeMin(mark, config)).AsString())
		}
		return jsval.Int(0)
	}
	return sizeRangeMin(mark, config)
}

// cmAccess reads a field of the current datum in a composite mark's generated
// expressions: 6.x escapes it into a quoted path, 5.8 interpolates it as is.
func cmAccess(cc *compileCtx, p string) string {
	if cc.v5 {
		return `datum["` + p + `"]`
	}
	return accessWithDatumToUnescapedPath(p)
}

// cmAlias is the field name composite marks use in the names of their derived
// fields: 6.x strips the path syntax, 5.8 does not.
func cmAlias(cc *compileCtx, field string) string {
	if cc.v5 {
		return field
	}
	return removePathFromField(field)
}

// legendSelectionUpdate is the expression reading the clicked legend entry.
func legendSelectionUpdate(cc *compileCtx) string {
	if cc.v5 {
		return "datum.value || item().items[0].items[0].datum.value"
	}
	return "isDefined(datum.value) ? datum.value : item().items[0].items[0].datum.value"
}

// assembleLegends58 groups legends by the domains of their scales (6.x groups
// them by field); legends of one domain that cannot merge stay separate.
func assembleLegends58(m Model) []Value {
	index := m.b().comp.legends
	byDomain := newOmap[[]*legendComponent]()
	for _, channel := range index.keyList() {
		lc, _ := index.get(channel)
		sc := m.b().getScaleComponent(channel)
		if sc == nil {
			throw("No scale found for the %s legend; check the `resolve` property.", channel)
		}
		domainHash := stringify(sc.get("domains"))
		if !byDomain.has(domainHash) {
			byDomain.set(domainHash, []*legendComponent{lc.cloneLegend()})
			continue
		}
		// Upstream appends while iterating, so the appended component is visited
		// (and merged with itself) too.
		list := byDomain.m[domainHash]
		for i := 0; i < len(list); i++ {
			if mergeLegendComponent(list[i], lc) == nil {
				list = append(list, lc)
			}
		}
		byDomain.m[domainHash] = list
	}
	var out []Value
	for _, k := range byDomain.keys {
		for _, l := range byDomain.m[k] {
			if v := assembleLegend(l, m.b().config); v.IsTruthy() {
				out = append(out, v)
			}
		}
	}
	return out
}
