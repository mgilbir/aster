package vegalite

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Position encoding — vega-lite/src/compile/mark/encode/position-*.ts, offset.ts, position-align.ts.

type positionOffsetResult struct {
	offsetType string
	offset     Value
}

func positionOffset(baseChannel string, markDef Value, encoding Value, m *unitModel, bandPosition Value) positionOffsetResult {
	cc := m.b().ctx

	channel := baseChannel + "Offset"
	defaultValue := markDef.Get(channel)
	channelDef := encoding.Get(channel)
	if (channel == "xOffset" || channel == "yOffset") && channelDef.IsTruthy() {
		ref := midPoint(cc, midPointParams{
			channel: channel, channelDef: channelDef, markDef: markDef, config: m.config,
			scaleName: m.scaleName(channel, false), scale: m.getScaleComponent(channel),
			defaultRef: signalOrValueRef(defaultValue), bandPosition: bandPosition,
		})
		return positionOffsetResult{"encoding", ref}
	}
	if v := markDef.Get(channel); v.IsTruthy() {
		return positionOffsetResult{"visual", v}
	}
	return positionOffsetResult{}
}

func vgAlignedPositionChannel(channel string, markDef, config Value, defaultAlign string) string {
	if channel == chRadius || channel == chTheta {
		return getVgPositionChannel(channel)
	}
	alignChannel := "baseline"
	if channel == chX {
		alignChannel = "align"
	}
	align := getMarkPropOrConfigSimple(alignChannel, markDef, config)
	alignExcl := ""
	if !isSignalRef(align) && align.IsTruthy() {
		alignExcl = align.AsString()
	}
	if channel == chX {
		if alignExcl == "" {
			if defaultAlign == "top" {
				alignExcl = "left"
			} else {
				alignExcl = "center"
			}
		}
		return map[string]string{"left": "x", "center": "xc", "right": "x2"}[alignExcl]
	}
	if alignExcl == "" {
		alignExcl = defaultAlign
	}
	return map[string]string{"top": "y", "middle": "yc", "bottom": "y2"}[alignExcl]
}

type pointPositionOpts struct {
	defaultPos string
	vgChannel  string
}

func pointPosition(channel string, m *unitModel, opt pointPositionOpts) Value {
	cc := m.b().ctx

	encoding, markDef, config, stack := m.encoding, m.markDef, m.config, m.stack
	channelDef := encoding.Get(channel)
	channel2Def := encoding.Get(getSecondaryRangeChannel(channel))
	scaleName := m.scaleName(channel, false)
	scale := m.getScaleComponent(channel)
	po := positionOffset(channel, markDef, encoding, m, jsval.Num(0.5))
	defaultRefFn := pointPositionDefaultRef(m, opt.defaultPos, channel, scaleName, scale)
	var valueRef Value
	if !channelDef.IsTruthy() && isXorY(channel) && (encoding.Get("latitude").IsTruthy() || encoding.Get("longitude").IsTruthy()) {
		valueRef = mkv("field", m.getName(channel))
	} else {
		bp := undef
		if po.offsetType == "encoding" {
			bp = jsval.Int(0)
		}
		valueRef = positionRef(cc, midPointParams{
			channel: channel, channelDef: channelDef, channel2Def: channel2Def, markDef: markDef, config: config,
			scaleName: scaleName, scale: scale, stack: stack, offset: po.offset, defaultRefFn: defaultRefFn, bandPosition: bp,
		})
	}
	if valueRef.IsTruthy() {
		vg := opt.vgChannel
		if vg == "" {
			vg = channel
		}
		return mkv(vg, valueRef)
	}
	return undef
}

func positionRef(cc *compileCtx, p midPointParams) Value {
	if isFieldOrDatumDef(cc, p.channelDef) && p.stack != nil && p.channel == p.stack.fieldChannel {
		if isFieldDef(cc, p.channelDef) {
			bandPosition := p.channelDef.Get("bandPosition")
			if bandPosition.IsUndefined() && p.markDef.Get("type").AsString() == "text" && (p.channel == chRadius || p.channel == chTheta) {
				bandPosition = jsval.Num(0.5)
			}
			if !bandPosition.IsUndefined() {
				return interpolatedSignalRef(cc, interpolatedOpts{scaleName: p.scaleName, fod: p.channelDef, startSuffix: "start", bandPosition: bandPosition, offset: p.offset})
			}
		}
		return valueRefForFieldOrDatumDef(cc, p.channelDef, p.scaleName, fieldRefOption{suffix: "end"}, refOffsetBand{offset: p.offset})
	}
	return midPointRefWithPositionInvalidTest(cc, p)
}

func pointPositionDefaultRef(m *unitModel, defaultPos, channel, scaleName string, scale *scaleComponent) func() Value {
	cc := m.b().ctx

	markDef, config := m.markDef, m.config
	return func() Value {
		if cc.v5 {
			vgChannel := getVgPositionChannel(channel)
			if def := getMarkPropOrConfig(channel, markDef, config, vgChannel, false); !def.IsUndefined() {
				return widthHeightValueOrSignalRef(channel, def)
			}
			if defaultPos == "mid" {
				sizeRef := m.height()
				if getSizeChannel(channel) == "width" {
					sizeRef = m.width()
				}
				o := cloneObj(coalesceObj(sizeRef).ObjValue())
				o.Set("mult", jsval.Num(0.5))
				return jsval.Obj(o)
			}
			return pointPositionDefaultRef58(m, defaultPos, channel, scaleName, scale)
		}
		mainChannel := getMainRangeChannel(channel)
		vgChannel := getVgPositionChannel(channel)
		def := getMarkPropOrConfig(channel, markDef, config, vgChannel, false)
		if !def.IsUndefined() {
			return widthHeightValueOrSignalRef(channel, def)
		}
		switch defaultPos {
		case "zeroOrMin":
			return zeroOrMinOrMaxPosition(mainChannel, "zeroOrMin", scaleName, scale, "", "")
		case "zeroOrMax":
			return zeroOrMinOrMaxPosition(mainChannel, "zeroOrMax", scaleName, scale, signalOf(m.width()), signalOf(m.height()))
		case "mid":
			var sizeRef Value
			if getSizeChannel(channel) == "width" {
				sizeRef = m.width()
			} else {
				sizeRef = m.height()
			}
			o := cloneObj(coalesceObj(sizeRef).ObjValue())
			o.Set("mult", jsval.Num(0.5))
			return jsval.Obj(o)
		}
		return undef
	}
}

func zeroOrMinOrMaxPosition(mainChannel, mode, scaleName string, scale *scaleComponent, widthSignal, heightSignal string) Value {
	if v := scaledZeroOrMinOrMax(scaleName, scale, mode, widthSignal, heightSignal); !v.IsUndefined() {
		return v
	}
	switch mainChannel {
	case chRadius:
		if mode == "zeroOrMin" {
			return mkv("value", 0)
		}
		return sig("min(" + widthSignal + "," + heightSignal + ")/2")
	case chTheta:
		if mode == "zeroOrMin" {
			return mkv("value", 0)
		}
		return sig("2*PI")
	case chX:
		if mode == "zeroOrMin" {
			return mkv("value", 0)
		}
		return mkv("field", mkv("group", "width"))
	case chY:
		if mode == "zeroOrMin" {
			return mkv("field", mkv("group", "height"))
		}
		return mkv("value", 0)
	}
	return undef
}

// ---- range position ----

type rangePosOpts struct {
	defaultPos, defaultPos2 string
	rangeFlag               bool
}

func pointOrRangePosition(channel string, m *unitModel, o rangePosOpts) Value {
	if o.rangeFlag {
		return rangePosition(channel, m, o)
	}
	return pointPosition(channel, m, pointPositionOpts{defaultPos: o.defaultPos})
}

func rangePosition(channel string, m *unitModel, o rangePosOpts) Value {
	markDef, config := m.markDef, m.config
	channel2 := getSecondaryRangeChannel(channel)
	sizeChannel := getSizeChannel(channel)
	pos2 := pointPosition2OrSize(m, o.defaultPos2, channel2)
	var vgChannel string
	if pos2.Get(sizeChannel).IsTruthy() {
		vgChannel = vgAlignedPositionChannel(channel, markDef, config, "middle")
	} else {
		vgChannel = getVgPositionChannel(channel)
	}
	out := jsval.NewObject(4)
	spreadV(out, pointPosition(channel, m, pointPositionOpts{defaultPos: o.defaultPos, vgChannel: vgChannel}))
	spreadV(out, pos2)
	return jsval.Obj(out)
}

func pointPosition2OrSize(m *unitModel, defaultPos, channel string) Value {
	cc := m.b().ctx

	encoding, markDef, stack, config := m.encoding, m.markDef, m.stack, m.config
	baseChannel := getMainRangeChannel(channel)
	sizeChannel := getSizeChannel(channel)
	vgChannel := getVgPositionChannel(channel)
	channelDef := encoding.Get(baseChannel)
	scaleName := m.scaleName(baseChannel, false)
	scale := m.getScaleComponent(baseChannel)
	var po positionOffsetResult
	if encoding.ObjValue().Has(channel) || markDef.ObjValue().Has(channel) {
		po = positionOffset(channel, markDef, encoding, m, undef)
	} else {
		po = positionOffset(baseChannel, markDef, encoding, m, undef)
	}
	if !channelDef.IsTruthy() && (channel == chX2 || channel == chY2) && (encoding.Get("latitude").IsTruthy() || encoding.Get("longitude").IsTruthy()) {
		vgSizeChannel := getSizeChannel(channel)
		if size := markDef.Get(vgSizeChannel); !size.IsNullish() {
			return mkv(vgSizeChannel, mkv("value", size))
		}
		return mkv(vgChannel, mkv("field", m.getName(channel)))
	}
	valueRef := position2Ref(cc, midPointParams{
		channel: channel, channelDef: channelDef, channel2Def: encoding.Get(channel), markDef: markDef, config: config,
		scaleName: scaleName, scale: scale, stack: stack, offset: po.offset,
	})
	if !valueRef.IsUndefined() {
		return mkv(vgChannel, valueRef)
	}
	if r := position2orSize(channel, markDef); !r.IsUndefined() {
		return r
	}
	styleObj := mkv(channel, getMarkStyleConfig(channel, markDef, config.Get("style")), sizeChannel, getMarkStyleConfig(sizeChannel, markDef, config.Get("style")))
	if r := position2orSize(channel, styleObj); !r.IsUndefined() {
		return r
	}
	if r := position2orSize(channel, config.Get(m.mark())); !r.IsUndefined() {
		return r
	}
	if r := position2orSize(channel, config.Get("mark")); !r.IsUndefined() {
		return r
	}
	return mkv(vgChannel, pointPositionDefaultRef(m, defaultPos, channel, scaleName, scale)())
}

func position2Ref(cc *compileCtx, p midPointParams) Value {
	if isFieldOrDatumDef(cc, p.channelDef) && p.stack != nil && p.channel[:1] == p.stack.fieldChannel[:1] {
		return valueRefForFieldOrDatumDef(cc, p.channelDef, p.scaleName, fieldRefOption{suffix: "start"}, refOffsetBand{offset: p.offset})
	}
	return midPointRefWithPositionInvalidTest(cc, midPointParams{
		channel: p.channel, channelDef: p.channel2Def, scaleName: p.scaleName, scale: p.scale, stack: p.stack,
		markDef: p.markDef, config: p.config, offset: p.offset, defaultRef: p.defaultRef,
	})
}

func position2orSize(channel string, markDef Value) Value {
	sizeChannel := getSizeChannel(channel)
	vgChannel := getVgPositionChannel(channel)
	switch {
	case !markDef.Get(vgChannel).IsUndefined():
		return mkv(vgChannel, widthHeightValueOrSignalRef(channel, markDef.Get(vgChannel)))
	case !markDef.Get(channel).IsUndefined():
		return mkv(vgChannel, widthHeightValueOrSignalRef(channel, markDef.Get(channel)))
	case markDef.Get(sizeChannel).IsTruthy():
		dim := markDef.Get(sizeChannel)
		if !isRelativeBandSize(dim) {
			return mkv(sizeChannel, widthHeightValueOrSignalRef(channel, dim))
		}
	}
	return undef
}

// ---- rect position ----

func rectPosition(m *unitModel, channel string) Value {
	cc := m.b().ctx

	if cc.v5 {
		return rectPosition58(m, channel)
	}
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
	offsetScaleChannel := getOffsetChannel(channel)
	isBarOrTickBand := (mark == "bar" && ((channel == chX && orient == "vertical") || (channel != chX && orient == "horizontal"))) ||
		(mark == "tick" && ((channel == chY && orient == "vertical") || (channel != chY && orient == "horizontal")))
	if isFieldDef(cc, channelDef) &&
		(isBinning(channelDef.Get("bin")) || isBinned(channelDef.Get("bin")) || (channelDef.Get("timeUnit").IsTruthy() && !channelDef2.IsTruthy())) &&
		!(hasSizeDef.IsTruthy() && !isRelativeBandSize(hasSizeDef)) &&
		!encoding.Get(offsetScaleChannel).IsTruthy() &&
		!hasDiscreteDomain(scaleType) {
		return rectBinPosition(channelDef, channelDef2, channel, m)
	} else if ((isFieldOrDatumDef(cc, channelDef) && hasDiscreteDomain(scaleType)) || isBarOrTickBand) && !channelDef2.IsTruthy() {
		return positionAndSize(channelDef, channel, m)
	}
	return rangePosition(channel, m, rangePosOpts{defaultPos: "zeroOrMax", defaultPos2: "zeroOrMin"})
}

func defaultSizeRef(sizeChannel, scaleName string, scale *scaleComponent, config Value, bandSize Value, hasFieldDef bool, mark string) Value {
	switch {
	case isRelativeBandSize(bandSize):
		if scale != nil {
			scaleType := scale.get("type").AsString()
			if scaleType == "band" {
				bandWidth := "bandwidth('" + jsName(scaleName) + "')"
				if !(bandSize.Get("band").IsNum() && bandSize.Get("band").NumValue() == 1) {
					bandWidth = bandSize.Get("band").AsString() + " * " + bandWidth
				}
				minBandSize := getMarkConfig("minBandSize", mkv("type", mark), config, "")
				if minBandSize.IsTruthy() {
					return sig("max(" + signalOrStringValue(minBandSize).AsString() + ", " + bandWidth + ")")
				}
				return sig(bandWidth)
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
	if !hasFieldDef {
		sc := config.Get("scale")
		var fallback Value
		switch mark {
		case "tick":
			fallback = sc.Get("tickBandPaddingInner")
		case "bar":
			fallback = sc.Get("barBandPaddingInner")
		default:
			fallback = sc.Get("rectBandPaddingInner")
		}
		padding := firstDefined(sc.Get("bandPaddingInner"), fallback)
		if isSignalRef(padding) {
			return sig("(1 - (" + signalOf(padding) + ")) * " + sizeChannel)
		} else if padding.IsNum() {
			return sig(jsval.JSNumberString(1-padding.NumValue()) + " * " + sizeChannel)
		}
	}
	defaultStep := getViewConfigDiscreteStep(config.Get("view"), sizeChannel)
	return mkv("value", defaultStep-2)
}

func positionAndSize(fd Value, channel string, m *unitModel) Value {
	cc := m.b().ctx

	markDef, encoding, config, stack := m.markDef, m.encoding, m.config, m.stack
	orient := markDef.Get("orient").AsString()
	scaleName := m.scaleName(channel, false)
	scale := m.getScaleComponent(channel)
	vgSizeChannel := getSizeChannel(channel)
	channel2 := getSecondaryRangeChannel(channel)
	offsetScaleChannel := getOffsetChannel(channel)
	offsetScaleName := m.scaleName(offsetScaleChannel, false)
	offsetScale := m.getScaleComponent(getOffsetScaleChannel(channel))
	useVlSizeChannel := markDef.Get("type").AsString() == "tick" || (orient == "horizontal" && channel == chY) || (orient == "vertical" && channel == chX)
	var sizeMixins Value
	if encoding.Get("size").IsTruthy() || markDef.Get("size").IsTruthy() {
		if useVlSizeChannel {
			sizeMixins = nonPosition("size", m, nonPositionOpts{vgChannel: vgSizeChannel, defaultRef: signalOrValueRef(markDef.Get("size"))})
		}
	}
	hasSizeFromMarkOrEncoding := sizeMixins.IsTruthy()
	var sc *scaleComponent = scale
	if sc == nil {
		sc = offsetScale
	}
	scType := ""
	if sc != nil {
		scType = sc.get("type").AsString()
	}
	bandSize := getBandSize(cc, channel, fd, undef, markDef, config, scType, useVlSizeChannel)
	if !sizeMixins.IsTruthy() {
		useScale := offsetScale
		if useScale == nil {
			useScale = scale
		}
		name := offsetScaleName
		if name == "" {
			name = scaleName
		}
		sizeMixins = mkv(vgSizeChannel, defaultSizeRef(vgSizeChannel, name, useScale, config, bandSize, fd.IsTruthy(), markDef.Get("type").AsString()))
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
	var timeUnitBandPosition Value
	if center && po.offsetType != "encoding" && isFieldDef(cc, fd) && fd.Get("timeUnit").IsTruthy() && !encoding.Get(channel2).IsTruthy() {
		timeUnitBandPosition = getBandPosition(cc, fd, undef, markDef, config)
	}
	var bandPosition Value
	switch {
	case !timeUnitBandPosition.IsNullish():
		bandPosition = timeUnitBandPosition
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

func getBinSpacing(channel string, spacing float64, reverse, axisTranslate, offset, minBandSize Value, bandSizeExpr string) Value {
	if isPolarPositionChannel(channel) {
		return jsval.Int(0)
	}
	isEnd := channel == chX || channel == chY2
	spacingOffset := spacing / 2
	if isEnd {
		spacingOffset = -spacing / 2
	}
	if isSignalRef(reverse) || isSignalRef(offset) || isSignalRef(axisTranslate) || minBandSize.IsTruthy() {
		reverseExpr := signalOrStringValue(reverse)
		offsetExpr := signalOrStringValue(offset)
		axisTranslateExpr := signalOrStringValue(axisTranslate)
		minBandSizeExpr := signalOrStringValue(minBandSize)
		sign := "-"
		if isEnd {
			sign = ""
		}
		so := jsval.JSNumberString(spacingOffset)
		spacingAndSizeOffset := so
		if minBandSize.IsTruthy() {
			mbs := minBandSizeExpr.AsString()
			spacingAndSizeOffset = "(" + bandSizeExpr + " < " + mbs + " ? " + sign + "0.5 * (" + mbs + " - (" + bandSizeExpr + ")) : " + so + ")"
		}
		t := ""
		if axisTranslateExpr.IsTruthy() {
			t = axisTranslateExpr.AsString() + " + "
		}
		r := ""
		if reverseExpr.IsTruthy() {
			r = "(" + reverseExpr.AsString() + " ? -1 : 1) * "
		}
		o := spacingAndSizeOffset
		if offsetExpr.IsTruthy() {
			o = "(" + offsetExpr.AsString() + " + " + spacingAndSizeOffset + ")"
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

func rectBinPosition(fd, fd2 Value, channel string, m *unitModel) Value {
	cc := m.b().ctx

	config, markDef, encoding := m.config, m.markDef, m.encoding
	scale := m.getScaleComponent(channel)
	scaleName := m.scaleName(channel, false)
	scaleType := ""
	if scale != nil {
		scaleType = scale.get("type").AsString()
	}
	reverse := undef
	if scale != nil {
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
	minBandSize := getMarkConfig("minBandSize", markDef, config, "")
	po := positionOffset(channel, markDef, encoding, m, jsval.Int(0))
	po2 := positionOffset(channel2, markDef, encoding, m, jsval.Int(0))
	bandSizeExprS := binSizeExpr(cc, scaleName, fd)
	binSpacingOffset := getBinSpacing(channel, spacing, reverse, axisTranslate, po.offset, minBandSize, bandSizeExprS)
	offset2 := po2.offset
	if offset2.IsNullish() {
		offset2 = po.offset
	}
	binSpacingOffset2 := getBinSpacing(channel2, spacing, reverse, axisTranslate, offset2, minBandSize, bandSizeExprS)
	var bandPositionForBandSize Value
	switch {
	case isSignalRef(bandSize):
		bandPositionForBandSize = sig("(1-" + signalOf(bandSize) + ")/2")
	case isRelativeBandSize(bandSize):
		bandPositionForBandSize = jsval.Num((1 - bandSize.Get("band").AsDouble()) / 2)
	default:
		bandPositionForBandSize = jsval.Num(0.5)
	}
	bandPosition := getBandPosition(cc, fd, fd2, markDef, config)
	if isBinning(fd.Get("bin")) || fd.Get("timeUnit").IsTruthy() {
		useRectOffsetField := fd.Get("timeUnit").IsTruthy() && !(bandPosition.IsNum() && bandPosition.NumValue() == 0.5)
		var second Value
		if isSignalRef(bandPositionForBandSize) {
			second = sig("1-" + signalOf(bandPositionForBandSize))
		} else {
			second = jsval.Num(1 - bandPositionForBandSize.NumValue())
		}
		return mkv(
			vgChannel2, rectBinRef(cc, fd, scaleName, bandPositionForBandSize, binSpacingOffset2, useRectOffsetField),
			vgChannel, rectBinRef(cc, fd, scaleName, second, binSpacingOffset, useRectOffsetField),
		)
	} else if isBinned(fd.Get("bin")) {
		startRef := valueRefForFieldOrDatumDef(cc, fd, scaleName, fieldRefOption{}, refOffsetBand{offset: binSpacingOffset2})
		if isFieldDef(cc, fd2) {
			return mkv(vgChannel2, startRef, vgChannel, valueRefForFieldOrDatumDef(cc, fd2, scaleName, fieldRefOption{}, refOffsetBand{offset: binSpacingOffset}))
		} else if isObject(fd.Get("bin")) && fd.Get("bin").Get("step").IsTruthy() {
			return mkv(vgChannel2, startRef, vgChannel, mkv(
				"signal", `scale("`+scaleName+`", `+vgField(cc, fd, fieldRefOption{expr: "datum"})+" + "+fd.Get("bin").Get("step").AsString()+")",
				"offset", binSpacingOffset,
			))
		}
	}
	return undef
}

func rectBinRef(cc *compileCtx, fd Value, scaleName string, bandPosition, offset Value, useRectOffsetField bool) Value {
	o := interpolatedOpts{scaleName: scaleName, fod: fd, bandPosition: bandPosition, offset: offset}
	if useRectOffsetField {
		o.startSuffix, o.endSuffix = offsettedRectStartSuffix, offsettedRectEndSuffix
	}
	return interpolatedSignalRef(cc, o)
}

var _ = math.Pi
