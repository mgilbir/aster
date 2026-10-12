package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Value references for mark encoding — vega-lite/src/compile/mark/encode/valueref.ts.

func datumDefToExpr(cc *compileCtx, dd Value) string {
	datum := dd.Get("datum")
	if isDateTime(datum) {
		return dateTimeToExpr(cc, datum)
	}
	return stringifyText(nil, datum) // `${stringify(datum)}`
}

type midPointParams struct {
	channel                 string
	channelDef, channel2Def Value
	markDef, config         Value
	scaleName               string
	scale                   *scaleComponent
	stack                   *stackProperties
	offset                  Value
	defaultRef              Value
	defaultRefFn            func() Value
	bandPosition            Value
}

func midPointRefWithPositionInvalidTest(cc *compileCtx, p midPointParams) Value {
	if cc.v5 {
		return midPointRefWithPositionInvalidTest58(cc, p)
	}
	scaleChannel := getMainRangeChannel(p.channel)
	mainRef := midPoint(cc, p)
	inc := getConditionalValueRefForIncludingInvalidValue(cc, scaleChannel, p.channelDef, p.scale, p.scaleName, p.markDef, p.config)
	if !inc.IsUndefined() {
		return arr(inc, mainRef)
	}
	return mainRef
}

type refOffsetBand struct {
	offset Value
	band   Value
}

func valueRefForFieldOrDatumDef(cc *compileCtx, fd Value, scaleName string, opt fieldRefOption, encode refOffsetBand) Value {
	ref := jsval.NewObject(4)
	if scaleName != "" {
		ref.Set("scale", jsval.Str(scaleName))
	}
	if isDatumDefOf(cc, fd) {
		datum := fd.Get("datum")
		switch {
		case isDateTime(datum):
			ref.Set("signal", jsval.Str(dateTimeToExpr(cc, datum)))
		case isSignalRef(datum):
			ref.Set("signal", datum.Get("signal"))
		case isExprRef(cc, datum):
			ref.Set("signal", datum.Get("expr"))
		default:
			ref.Set("value", datum)
		}
	} else {
		ref.Set("field", jsval.Str(vgField(cc, fd, opt)))
	}
	if encode.offset.IsTruthy() {
		ref.Set("offset", encode.offset)
	}
	if encode.band.IsTruthy() {
		ref.Set("band", encode.band)
	}
	return jsval.Obj(ref)
}

type interpolatedOpts struct {
	scaleName    string
	fod, fod2    Value
	offset       Value
	startSuffix  string
	endSuffix    string
	bandPosition Value
}

func interpolatedSignalRef(cc *compileCtx, o interpolatedOpts) Value {
	bandPosition := o.bandPosition
	if bandPosition.IsUndefined() {
		bandPosition = jsval.Num(0.5)
	}
	endSuffix := o.endSuffix
	if endSuffix == "" {
		endSuffix = "end"
	}
	// `0 < bandPosition && bandPosition < 1` compares as JavaScript does,
	// the band position read as a number, whatever its type.
	n := jsval.ToNumber(bandPosition)
	expr := ""
	if (cc.v5 || !isSignalRef(bandPosition)) && 0 < n && n < 1 {
		expr = "datum"
	}
	start := vgField(cc, o.fod, fieldRefOption{expr: expr, suffix: o.startSuffix})
	var end string
	if !o.fod2.IsUndefined() {
		end = vgField(cc, o.fod2, fieldRefOption{expr: expr})
	} else {
		end = vgField(cc, o.fod, fieldRefOption{suffix: endSuffix, expr: expr})
	}
	ref := jsval.NewObject(4)
	if bandPosition.IsNum() && (bandPosition.NumValue() == 0 || bandPosition.NumValue() == 1) {
		ref.Set("scale", jsval.Str(o.scaleName))
		if bandPosition.NumValue() == 0 {
			ref.Set("field", jsval.Str(start))
		} else {
			ref.Set("field", jsval.Str(end))
		}
	} else {
		var datum string
		if cc.v5 {
			// Vega-Lite 5.8 weights the start by the band position, 6.x the end.
			if isSignalRef(bandPosition) {
				bp := signalOf(bandPosition)
				datum = bp + " * " + start + " + (1-" + bp + ") * " + end
			} else {
				// `${bandPosition} * … + ${1 - bandPosition} * …`
				datum = bandPosition.AsString() + " * " + start + " + " + jsval.JSNumberString(1-n) + " * " + end
			}
		} else if isSignalRef(bandPosition) {
			bp := signalOf(bandPosition)
			datum = "(1-" + bp + ") * " + start + " + " + bp + " * " + end
		} else {
			datum = jsval.JSNumberString(1-n) + " * " + start + " + " + bandPosition.AsString() + " * " + end
		}
		ref.Set("signal", jsval.Str(`scale("`+jsName(o.scaleName)+`", `+datum+")"))
	}
	if o.offset.IsTruthy() {
		ref.Set("offset", o.offset)
	}
	return jsval.Obj(ref)
}

func binSizeExpr(cc *compileCtx, scaleName string, fd Value) string {
	start := vgField(cc, fd, fieldRefOption{expr: "datum"})
	end := vgField(cc, fd, fieldRefOption{expr: "datum", suffix: "end"})
	return `abs(scale("` + jsName(scaleName) + `", ` + end + `) - scale("` + jsName(scaleName) + `", ` + start + `))`
}

func midPoint(cc *compileCtx, p midPointParams) Value {
	if p.channelDef.IsTruthy() {
		if isFieldOrDatumDef(cc, p.channelDef) {
			scaleType := ""
			if p.scale != nil {
				scaleType = p.scale.get("type").AsString()
			}
			bandPosition := p.bandPosition
			if isTypedFieldDefOf(cc, p.channelDef) {
				if bandPosition.IsNullish() {
					bandPosition = getBandPosition(cc, p.channelDef, p.channel2Def, p.markDef, p.config)
				}
				bin, timeUnit := p.channelDef.Get("bin"), p.channelDef.Get("timeUnit")
				typ := channelDefType(p.channelDef)
				if isBinning(bin) || (bandPosition.IsTruthy() && timeUnit.IsTruthy() && typ == "temporal") {
					if p.stack != nil && p.stack.impute {
						return valueRefForFieldOrDatumDef(cc, p.channelDef, p.scaleName, fieldRefOption{binSuffix: "mid"}, refOffsetBand{offset: p.offset})
					}
					if bandPosition.IsTruthy() && !hasDiscreteDomain(scaleType) {
						return interpolatedSignalRef(cc, interpolatedOpts{scaleName: p.scaleName, fod: p.channelDef, bandPosition: bandPosition, offset: p.offset})
					}
					opt := fieldRefOption{}
					if binRequiresRange(cc, p.channelDef, p.channel) {
						opt.binSuffix = "range"
					}
					return valueRefForFieldOrDatumDef(cc, p.channelDef, p.scaleName, opt, refOffsetBand{offset: p.offset})
				} else if isBinned(bin) {
					if isFieldDef(cc, p.channel2Def) {
						return interpolatedSignalRef(cc, interpolatedOpts{scaleName: p.scaleName, fod: p.channelDef, fod2: p.channel2Def, bandPosition: bandPosition, offset: p.offset})
					}
					channel2 := chY2
					if p.channel == chX {
						channel2 = chX2
					}
					cc.warn(msgChannelRequiredForBinned(channel2))
				}
			}
			opt := fieldRefOption{}
			if hasDiscreteDomain(scaleType) {
				opt.binSuffix = "range"
			}
			band := undef
			if scaleType == "band" {
				band = coalesce(bandPosition, p.channelDef.Get("bandPosition"), jsval.Num(0.5))
			}
			return valueRefForFieldOrDatumDef(cc, p.channelDef, p.scaleName, opt, refOffsetBand{offset: p.offset, band: band})
		} else if isValueDef(p.channelDef) {
			value := p.channelDef.Get("value")
			o := cloneObj(widthHeightValueOrSignalRef(cc, p.channel, value).ObjValue())
			if p.offset.IsTruthy() {
				o.Set("offset", p.offset)
			}
			return jsval.Obj(o)
		}
	}
	defaultRef := p.defaultRef
	if p.defaultRefFn != nil {
		defaultRef = p.defaultRefFn()
	}
	if defaultRef.IsTruthy() {
		o := cloneObj(defaultRef.ObjValue())
		if p.offset.IsTruthy() {
			o.Set("offset", p.offset)
		}
		return jsval.Obj(o)
	}
	return defaultRef
}

func widthHeightValueOrSignalRef(cc *compileCtx, channel string, value Value) Value {
	if (channel == "x" || channel == "x2") && value.IsStr() && value.StrValue() == "width" {
		return mkv("field", mkv("group", "width"))
	}
	if (channel == "y" || channel == "y2") && value.IsStr() && value.StrValue() == "height" {
		return mkv("field", mkv("group", "height"))
	}
	r := signalOrValueRef(cc, value)
	if r.IsUndefined() {
		return mkv()
	}
	return r
}

// ---- invalid data value refs ----

func getConditionalValueRefForIncludingInvalidValue(cc *compileCtx, scaleChannel string, channelDef Value, scale *scaleComponent, scaleName string, markDef, config Value) Value {
	scaleType := ""
	if scale != nil {
		scaleType = scale.get("type").AsString()
	}
	fd := getFieldDef(cc, channelDef)
	isCount := fd.IsTruthy() && isCountingAggregateOp(fd.Get("aggregate"))
	mode := getScaleInvalidDataMode(markDef, config, scaleChannel, scaleType, isCount)
	if fd.IsTruthy() && mode == "show" {
		includeAs := coalesce(config.Get("scale").Get("invalid").Get(scaleChannel), jsval.Str("zero-or-min"))
		o := mk("test", fieldValidPredicate(vgField(cc, fd, fieldRefOption{expr: "datum"}), false))
		spread(o, refForInvalidValues(cc, includeAs, scale, scaleName))
		return jsval.Obj(o)
	}
	return undef
}

func refForInvalidValues(cc *compileCtx, includeAs Value, scale *scaleComponent, scaleName string) Value {
	if isObject(includeAs) && includeAs.IsObj() && includeAs.ObjValue().Has("value") {
		v := includeAs.Get("value")
		if isSignalRef(v) {
			return mkv("signal", v.Get("signal"))
		}
		return mkv("value", v)
	}
	return scaledZeroOrMinOrMax(cc, scaleName, scale, "zeroOrMin", "", "")
}

// scaledZeroOrMinOrMax builds the ref for the scaled zero (or domain min/max).
func scaledZeroOrMinOrMax(cc *compileCtx, scaleName string, scale *scaleComponent, mode string, widthSignal, heightSignal string) Value {
	if scale == nil || scaleName == "" {
		return undef
	}
	domain := "domain('" + scaleName + "')"
	min := domain + "[0]"
	max := "peek(" + domain + ")"
	switch scale.domainHasZero(cc) {
	case "definitely":
		return mkv("scale", scaleName, "value", 0)
	case "maybe":
		nonZero := max
		if mode == "zeroOrMin" {
			nonZero = min
		}
		return sig("scale('" + scaleName + "', inrange(0, " + domain + ") ? 0 : " + nonZero + ")")
	}
	if mode == "zeroOrMin" {
		return sig("scale('" + scaleName + "', " + min + ")")
	}
	return sig("scale('" + scaleName + "', " + max + ")")
}
