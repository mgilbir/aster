package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsmath"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Scale ranges — vega-lite/src/compile/scale/range.ts.

const maxSizeRangeStepRatio = 0.95

func parseUnitScaleRange(u *unitModel) {
	local := u.comp.scales
	for _, channel := range scaleChannels {
		cmpt, ok := local.get(channel)
		if !ok || cmpt == nil {
			continue
		}
		cmpt.setWithExplicit("range", parseRangeForChannel(channel, u))
	}
}

func getBinStepSignal(u *unitModel, channel string) Value {
	fd := u.fieldDef(channel)
	if fd.Get("bin").IsTruthy() {
		bin, field := fd.Get("bin"), fd.Get("field")
		sizeType := getSizeChannel(channel)
		sizeSignal := u.getName(sizeType)
		if isObject(bin) && bin.Get("binned").IsTruthy() && !bin.Get("step").IsUndefined() {
			return lazySignal(u.ctx, func() string {
				scaleName := u.scaleName(channel, false)
				binCount := "(domain(\"" + scaleName + "\")[1] - domain(\"" + scaleName + "\")[0]) / " + bin.Get("step").AsString()
				return u.getSignalName(sizeSignal) + " / (" + binCount + ")"
			})
		} else if isBinning(bin) {
			binSignal := getBinSignalName(u, field.AsString(), bin)
			return lazySignal(u.ctx, func() string {
				n := u.getSignalName(binSignal)
				binCount := "(" + n + ".stop - " + n + ".start) / " + n + ".step"
				return u.getSignalName(sizeSignal) + " / (" + binCount + ")"
			})
		}
	}
	return undef
}

func parseRangeForChannel(channel string, u *unitModel) withExplicit {
	specifiedScale := u.specifiedScales.Lookup(channel)
	size := u.size
	merged := u.getScaleComponent(channel)
	scaleType := merged.get("type").AsString()
	for _, property := range []string{"range", "scheme"} {
		if sv := specifiedScale.Get(property); !sv.IsUndefined() {
			supported := scaleTypeSupportProperty(scaleType, property)
			incompatible := channelScalePropertyIncompatible(channel, property)
			if !supported || incompatible {
				continue
			}
			switch property {
			case "range":
				rng := specifiedScale.Get("range")
				if rng.IsArr() {
					if isXorY(channel) {
						return makeExplicit(mapVals(rng, func(v Value) Value {
							if v.IsStr() && (v.StrValue() == "width" || v.StrValue() == "height") {
								sizeSignal := u.getName(v.StrValue())
								return lazySignal(u.ctx, func() string { return u.getSignalName(sizeSignal) })
							}
							return v
						}))
					}
				} else if isObject(rng) {
					return makeExplicit(mkv("data", u.requestDataName(dsMain), "field", rng.Get("field"),
						"sort", mkv("op", "min", "field", u.vgField(channel, fieldRefOption{}))))
				}
				return makeExplicit(rng)
			case "scheme":
				return makeExplicit(parseScheme(sv))
			}
		}
	}
	sizeChannel := "height"
	if channel == chX || channel == chXOffset {
		sizeChannel = "width"
	}
	sizeValue := size.Lookup(sizeChannel)
	if isStep(sizeValue) {
		if isXorY(channel) {
			if hasDiscreteDomain(scaleType) {
				if step := getPositionStep(sizeValue, u, channel); step.IsTruthy() {
					return makeExplicit(mkv("step", step))
				}
			}
		} else if isXorYOffset(channel) {
			positionChannel := chY
			if channel == chXOffset {
				positionChannel = chX
			}
			pc := u.getScaleComponent(positionChannel)
			if pc.get("type").AsString() == "band" {
				if step := getOffsetStep(sizeValue, scaleType); step.IsTruthy() {
					return makeExplicit(step)
				}
			}
		}
	}
	rangeMin, rangeMax := specifiedScale.Get("rangeMin"), specifiedScale.Get("rangeMax")
	d := defaultRange(channel, u)
	if (!rangeMin.IsUndefined() || !rangeMax.IsUndefined()) && scaleTypeSupportProperty(scaleType, "rangeMin") && d.IsArr() && d.Len() == 2 {
		return makeExplicit(arr(coalesce(rangeMin, d.Index(0)), coalesce(rangeMax, d.Index(1))))
	}
	return makeImplicit(d)
}

func parseScheme(scheme Value) Value {
	if isExtendedScheme(scheme) {
		o := mk("scheme", scheme.Get("name"))
		spread(o, jsval.Obj(omit(scheme, "name")))
		return jsval.Obj(o)
	}
	return mkv("scheme", scheme)
}

func fullWidthOrHeightRange(channel string, m *unitModel, scaleType string, center bool) Value {
	sizeType := getSizeChannel(channel)
	sizeSignal := m.getName(sizeType)
	sn := func(name string) string { return m.getSignalName(name) }
	lz := func(f func(string) string) Value { return lazySignal(m.ctx, func() string { return f(sizeSignal) }) }
	if channel == chY && hasContinuousDomain(scaleType) {
		if center {
			return arr(lz(func(n string) string { return sn(n) + "/2" }), lz(func(n string) string { return "-" + sn(n) + "/2" }))
		}
		return arr(lz(sn), 0)
	}
	if center {
		return arr(lz(func(n string) string { return "-" + sn(n) + "/2" }), lz(func(n string) string { return sn(n) + "/2" }))
	}
	return arr(0, lz(sn))
}

func defaultRange(channel string, u *unitModel) Value {
	size, config, mark, encoding := u.size, u.config, u.mark(), u.encoding
	typ := channelDefType(getFieldOrDatumDef(encoding.Get(channel)))
	merged := u.getScaleComponent(channel)
	scaleType := merged.get("type").AsString()
	spec := u.specifiedScales.Lookup(channel)
	domain, domainMid := spec.Get("domain"), spec.Get("domainMid")
	switch channel {
	case chX, chY:
		if scaleType == "point" || scaleType == "band" {
			positionSize := getDiscretePositionSize(channel, size, config.Get("view"))
			if isStep(positionSize) {
				return mkv("step", getPositionStep(positionSize, u, channel))
			}
		}
		return fullWidthOrHeightRange(channel, u, scaleType, false)
	case chXOffset, chYOffset:
		return getOffsetRange(channel, u, scaleType)
	case chSize:
		rangeMin := sizeRangeMin(mark, config)
		rangeMax := sizeRangeMax(mark, size, u, config)
		if isContinuousToDiscrete(scaleType) {
			return interpolateRange(u.ctx, rangeMin, rangeMax, defaultContinuousToDiscreteCount(scaleType, config, domain, channel))
		}
		return arr(rangeMin, rangeMax)
	case chTheta:
		return arr(0, math.Pi*2)
	case chAngle:
		return arr(0, 360)
	case chRadius:
		return arr(0, lazySignal(u.ctx, func() string {
			wn, hn := "width", "height"
			if u.parent != nil && isFacetModel(u.parent) {
				wn, hn = "child_width", "child_height"
			}
			return "min(" + u.getSignalName(wn) + "," + u.getSignalName(hn) + ")/2"
		}))
	case chTime:
		return mkv("step", 1000/config.Get("scale").Get("framesPerSecond").AsDouble())
	case chStrokeWidth:
		return arr(config.Get("scale").Get("minStrokeWidth"), config.Get("scale").Get("maxStrokeWidth"))
	case chStrokeDash:
		return arr(arr(1, 0), arr(4, 2), arr(2, 1), arr(1, 1), arr(1, 2, 4, 2))
	case chShape:
		return jsval.Str("symbol")
	case chColor, chFill, chStroke:
		if scaleType == "ordinal" {
			if typ == "nominal" {
				return jsval.Str("category")
			}
			return jsval.Str("ordinal")
		}
		if !domainMid.IsUndefined() {
			return jsval.Str("diverging")
		}
		if mark == "rect" || mark == "geoshape" {
			return jsval.Str("heatmap")
		}
		return jsval.Str("ramp")
	case chOpacity, chFillOpacity, chStrokeOpacity:
		return arr(config.Get("scale").Get("minOpacity"), config.Get("scale").Get("maxOpacity"))
	}
	return undef
}

func getStepFor(step Value, offsetIsDiscrete bool) string {
	if offsetIsDiscrete {
		if f := step.Get("for"); !f.IsNullish() {
			return f.AsString()
		}
		return "offset"
	}
	return "position"
}

func getPositionStep(step Value, u *unitModel, channel string) Value {
	encoding := u.encoding
	merged := u.getScaleComponent(channel)
	offsetChannel := getOffsetScaleChannel(channel)
	offsetDef := encoding.Get(offsetChannel)
	stepFor := getStepFor(step, isFieldOrDatumDef(offsetDef) && isDiscreteType(channelDefType(offsetDef)))
	if stepFor == "offset" && channelHasFieldOrDatum(encoding, offsetChannel) {
		offsetCmpt := u.getScaleComponent(offsetChannel)
		offsetScaleName := u.scaleName(offsetChannel, false)
		stepCount := "domain('" + offsetScaleName + "').length"
		if offsetCmpt.get("type").AsString() == "band" {
			inner := coalesce(offsetCmpt.get("paddingInner"), offsetCmpt.get("padding"), jsval.Int(0))
			outer := coalesce(offsetCmpt.get("paddingOuter"), offsetCmpt.get("padding"), jsval.Int(0))
			stepCount = "bandspace(" + stepCount + ", " + inner.AsString() + ", " + outer.AsString() + ")"
		}
		paddingInner := coalesce(merged.get("paddingInner"), merged.get("padding"))
		return sig(step.Get("step").AsString() + " * " + stepCount + " / (1-" + exprFromSignalRefOrValue(paddingInner) + ")")
	}
	return step.Get("step")
}

func getOffsetStep(step Value, offsetScaleType string) Value {
	if getStepFor(step, hasDiscreteDomain(offsetScaleType)) == "offset" {
		return mkv("step", step.Get("step"))
	}
	return undef
}

func getOffsetRange(channel string, u *unitModel, offsetScaleType string) Value {
	positionChannel := chY
	if channel == chXOffset {
		positionChannel = chX
	}
	pc := u.getScaleComponent(positionChannel)
	if pc == nil {
		return fullWidthOrHeightRange(positionChannel, u, offsetScaleType, true)
	}
	positionScaleType := pc.get("type").AsString()
	positionScaleName := u.scaleName(positionChannel, false)
	markDef, config := u.markDef, u.config
	if positionScaleType == "band" {
		size := getDiscretePositionSize(positionChannel, u.size, u.config.Get("view"))
		if isStep(size) {
			if step := getOffsetStep(size, offsetScaleType); step.IsTruthy() {
				return step
			}
		}
		return arr(0, sig("bandwidth('"+positionScaleName+"')"))
	}
	positionDef := u.encoding.Get(positionChannel)
	if isFieldDef(positionDef) && positionDef.Get("timeUnit").IsTruthy() {
		duration := durationExpr(positionDef.Get("timeUnit"), func(expr string) string {
			return "scale('" + positionScaleName + "', " + expr + ")"
		})
		padding := u.config.Get("scale").Get("bandWithNestedOffsetPaddingInner")
		bp := getBandPosition(positionDef, undef, markDef, config)
		bandPositionOffset := bp.AsDouble() - 0.5
		if bp.IsUndefined() {
			bandPositionOffset = math.NaN()
		}
		bandPositionOffsetExpr := ""
		if bandPositionOffset != 0 {
			bandPositionOffsetExpr = " + " + jsval.JSNumberString(bandPositionOffset)
		}
		if padding.IsTruthy() {
			var startRatio, endRatio string
			if isSignalRef(padding) {
				startRatio = signalOf(padding) + "/2" + bandPositionOffsetExpr
				endRatio = "(1 - " + signalOf(padding) + "/2)" + bandPositionOffsetExpr
			} else {
				p := padding.AsDouble()
				startRatio = jsval.JSNumberString(p/2 + bandPositionOffset)
				endRatio = jsval.JSNumberString(1 - p/2 + bandPositionOffset)
			}
			return arr(sig(startRatio+" * ("+duration+")"), sig(endRatio+" * ("+duration+")"))
		}
		return arr(0, sig(duration))
	}
	throw("Cannot use %s scale if %s scale is not discrete.", channel, positionChannel)
	return undef
}

func getDiscretePositionSize(channel string, size *Object, view Value) Value {
	sizeChannel := "height"
	if channel == chX {
		sizeChannel = "width"
	}
	if v := size.Lookup(sizeChannel); !v.IsUndefined() {
		return v
	}
	return getViewConfigDiscreteSize(view, sizeChannel)
}

func defaultContinuousToDiscreteCount(scaleType string, config Value, domain Value, channel string) Value {
	switch scaleType {
	case "quantile":
		return config.Get("scale").Get("quantileCount")
	case "quantize":
		return config.Get("scale").Get("quantizeCount")
	case "threshold":
		if !domain.IsUndefined() && domain.IsArr() {
			return jsval.Int(domain.Len() + 1)
		}
		return jsval.Int(3)
	}
	return undef
}

func interpolateRange(ctx *compileCtx, rangeMin, rangeMax, cardinality Value) Value {
	f := func() string {
		rMax := signalOrStringValue(rangeMax).AsString()
		rMin := signalOrStringValue(rangeMin).AsString()
		step := "(" + rMax + " - " + rMin + ") / (" + cardinality.AsString() + " - 1)"
		return "sequence(" + rMin + ", " + rMax + " + " + step + ", " + step + ")"
	}
	if isSignalRef(rangeMax) {
		return lazySignal(ctx, f)
	}
	return sig(f())
}

func sizeRangeMin(mark string, config Value) Value {
	sc := config.Get("scale")
	switch mark {
	case "bar", "tick":
		return sc.Get("minBandSize")
	case "line", "trail", "rule":
		return sc.Get("minStrokeWidth")
	case "text":
		return sc.Get("minFontSize")
	case "point", "square", "circle":
		return sc.Get("minSize")
	}
	throw("Invalid channel size for mark %s.", mark)
	return undef
}

func sizeRangeMax(mark string, size *Object, u *unitModel, config Value) Value {
	sc := config.Get("scale")
	xy := [2]Value{getBinStepSignal(u, chX), getBinStepSignal(u, chY)}
	switch mark {
	case "bar", "tick":
		if !sc.Get("maxBandSize").IsUndefined() {
			return sc.Get("maxBandSize")
		}
		min := minXYStep(u.ctx, size, xy, config.Get("view"))
		if min.IsNum() {
			return jsval.Num(min.NumValue() - 1)
		}
		return lazySignal(u.ctx, func() string { return signalOf(min) + " - 1" })
	case "line", "trail", "rule":
		return sc.Get("maxStrokeWidth")
	case "text":
		return sc.Get("maxFontSize")
	case "point", "square", "circle":
		if sc.Get("maxSize").IsTruthy() {
			return sc.Get("maxSize")
		}
		pointStep := minXYStep(u.ctx, size, xy, config.Get("view"))
		if pointStep.IsNum() {
			return jsval.Num(jsmath.Pow(maxSizeRangeStepRatio*pointStep.NumValue(), 2))
		}
		return lazySignal(u.ctx, func() string {
			return "pow(" + jsval.JSNumberString(maxSizeRangeStepRatio) + " * " + signalOf(pointStep) + ", 2)"
		})
	}
	throw("Invalid channel size for mark %s.", mark)
	return undef
}

func minXYStep(ctx *compileCtx, size *Object, xy [2]Value, view Value) Value {
	var widthStep, heightStep float64
	if w := size.Lookup("width"); isStep(w) {
		widthStep = w.Get("step").AsDouble()
	} else {
		widthStep = getViewConfigDiscreteStep(view, "width")
	}
	if h := size.Lookup("height"); isStep(h) {
		heightStep = h.Get("step").AsDouble()
	} else {
		heightStep = getViewConfigDiscreteStep(view, "height")
	}
	if xy[0].IsTruthy() || xy[1].IsTruthy() {
		return lazySignal(ctx, func() string {
			var exprs [2]string
			if xy[0].IsTruthy() {
				exprs[0] = signalOf(xy[0])
			} else {
				exprs[0] = jsval.JSNumberString(widthStep)
			}
			if xy[1].IsTruthy() {
				exprs[1] = signalOf(xy[1])
			} else {
				exprs[1] = jsval.JSNumberString(heightStep)
			}
			return "min(" + exprs[0] + ", " + exprs[1] + ")"
		})
	}
	return jsval.Num(math.Min(widthStep, heightStep))
}
