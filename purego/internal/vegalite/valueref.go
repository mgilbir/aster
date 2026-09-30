package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Value references for mark encoding — vega-lite/src/compile/mark/encode/valueref.ts.

func datumDefToExpr(dd Value) string {
	datum := dd.Get("datum")
	if isDateTime(datum) {
		return dateTimeToExpr(datum)
	}
	return stringify(datum)
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

func midPointRefWithPositionInvalidTest(p midPointParams) Value {
	if v5 {
		return midPointRefWithPositionInvalidTest58(p)
	}
	scaleChannel := getMainRangeChannel(p.channel)
	mainRef := midPoint(p)
	inc := getConditionalValueRefForIncludingInvalidValue(scaleChannel, p.channelDef, p.scale, p.scaleName, p.markDef, p.config)
	if !inc.IsUndefined() {
		return arr(inc, mainRef)
	}
	return mainRef
}

type refOffsetBand struct {
	offset Value
	band   Value
}

func valueRefForFieldOrDatumDef(fd Value, scaleName string, opt fieldRefOption, encode refOffsetBand) Value {
	ref := jsval.NewObject(4)
	if scaleName != "" {
		ref.Set("scale", jsval.Str(scaleName))
	}
	if isDatumDef(fd) {
		datum := fd.Get("datum")
		switch {
		case isDateTime(datum):
			ref.Set("signal", jsval.Str(dateTimeToExpr(datum)))
		case isSignalRef(datum):
			ref.Set("signal", datum.Get("signal"))
		case isExprRef(datum):
			ref.Set("signal", datum.Get("expr"))
		default:
			ref.Set("value", datum)
		}
	} else {
		ref.Set("field", jsval.Str(vgField(fd, opt)))
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

func interpolatedSignalRef(o interpolatedOpts) Value {
	bandPosition := o.bandPosition
	if bandPosition.IsUndefined() {
		bandPosition = jsval.Num(0.5)
	}
	endSuffix := o.endSuffix
	if endSuffix == "" {
		endSuffix = "end"
	}
	expr := ""
	if v5 && bandPosition.IsNum() && 0 < bandPosition.NumValue() && bandPosition.NumValue() < 1 {
		expr = "datum"
	} else if !v5 && !isSignalRef(bandPosition) && bandPosition.IsNum() && 0 < bandPosition.NumValue() && bandPosition.NumValue() < 1 {
		expr = "datum"
	}
	start := vgField(o.fod, fieldRefOption{expr: expr, suffix: o.startSuffix})
	var end string
	if !o.fod2.IsUndefined() {
		end = vgField(o.fod2, fieldRefOption{expr: expr})
	} else {
		end = vgField(o.fod, fieldRefOption{suffix: endSuffix, expr: expr})
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
		if v5 {
			// Vega-Lite 5.8 weights the start by the band position, 6.x the end.
			if isSignalRef(bandPosition) {
				bp := signalOf(bandPosition)
				datum = bp + " * " + start + " + (1-" + bp + ") * " + end
			} else {
				bp := bandPosition.NumValue()
				datum = jsval.JSNumberString(bp) + " * " + start + " + " + jsval.JSNumberString(1-bp) + " * " + end
			}
		} else if isSignalRef(bandPosition) {
			bp := signalOf(bandPosition)
			datum = "(1-" + bp + ") * " + start + " + " + bp + " * " + end
		} else {
			bp := bandPosition.NumValue()
			datum = jsval.JSNumberString(1-bp) + " * " + start + " + " + jsval.JSNumberString(bp) + " * " + end
		}
		ref.Set("signal", jsval.Str(`scale("`+jsName(o.scaleName)+`", `+datum+")"))
	}
	if o.offset.IsTruthy() {
		ref.Set("offset", o.offset)
	}
	return jsval.Obj(ref)
}

func binSizeExpr(scaleName string, fd Value) string {
	start := vgField(fd, fieldRefOption{expr: "datum"})
	end := vgField(fd, fieldRefOption{expr: "datum", suffix: "end"})
	return `abs(scale("` + jsName(scaleName) + `", ` + end + `) - scale("` + jsName(scaleName) + `", ` + start + `))`
}

func midPoint(p midPointParams) Value {
	if p.channelDef.IsTruthy() {
		if isFieldOrDatumDef(p.channelDef) {
			scaleType := ""
			if p.scale != nil {
				scaleType = p.scale.get("type").AsString()
			}
			bandPosition := p.bandPosition
			if isTypedFieldDef(p.channelDef) {
				if bandPosition.IsNullish() {
					bandPosition = getBandPosition(p.channelDef, p.channel2Def, p.markDef, p.config)
				}
				bin, timeUnit := p.channelDef.Get("bin"), p.channelDef.Get("timeUnit")
				typ := channelDefType(p.channelDef)
				if isBinning(bin) || (bandPosition.IsTruthy() && timeUnit.IsTruthy() && typ == "temporal") {
					if p.stack != nil && p.stack.impute {
						return valueRefForFieldOrDatumDef(p.channelDef, p.scaleName, fieldRefOption{binSuffix: "mid"}, refOffsetBand{offset: p.offset})
					}
					if bandPosition.IsTruthy() && !hasDiscreteDomain(scaleType) {
						return interpolatedSignalRef(interpolatedOpts{scaleName: p.scaleName, fod: p.channelDef, bandPosition: bandPosition, offset: p.offset})
					}
					opt := fieldRefOption{}
					if binRequiresRange(p.channelDef, p.channel) {
						opt.binSuffix = "range"
					}
					return valueRefForFieldOrDatumDef(p.channelDef, p.scaleName, opt, refOffsetBand{offset: p.offset})
				} else if isBinned(bin) {
					if isFieldDef(p.channel2Def) {
						return interpolatedSignalRef(interpolatedOpts{scaleName: p.scaleName, fod: p.channelDef, fod2: p.channel2Def, bandPosition: bandPosition, offset: p.offset})
					}
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
			return valueRefForFieldOrDatumDef(p.channelDef, p.scaleName, opt, refOffsetBand{offset: p.offset, band: band})
		} else if isValueDef(p.channelDef) {
			value := p.channelDef.Get("value")
			o := cloneObj(widthHeightValueOrSignalRef(p.channel, value).ObjValue())
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

func widthHeightValueOrSignalRef(channel string, value Value) Value {
	if (channel == "x" || channel == "x2") && value.IsStr() && value.StrValue() == "width" {
		return mkv("field", mkv("group", "width"))
	}
	if (channel == "y" || channel == "y2") && value.IsStr() && value.StrValue() == "height" {
		return mkv("field", mkv("group", "height"))
	}
	r := signalOrValueRef(value)
	if r.IsUndefined() {
		return mkv()
	}
	return r
}

// ---- invalid data value refs ----

func getConditionalValueRefForIncludingInvalidValue(scaleChannel string, channelDef Value, scale *scaleComponent, scaleName string, markDef, config Value) Value {
	scaleType := ""
	if scale != nil {
		scaleType = scale.get("type").AsString()
	}
	fd := getFieldDef(channelDef)
	isCount := fd.IsTruthy() && isCountingAggregateOp(fd.Get("aggregate"))
	mode := getScaleInvalidDataMode(markDef, config, scaleChannel, scaleType, isCount)
	if fd.IsTruthy() && mode == "show" {
		includeAs := coalesce(config.Get("scale").Get("invalid").Get(scaleChannel), jsval.Str("zero-or-min"))
		o := mk("test", fieldValidPredicate(vgField(fd, fieldRefOption{expr: "datum"}), false))
		spread(o, refForInvalidValues(includeAs, scale, scaleName))
		return jsval.Obj(o)
	}
	return undef
}

func refForInvalidValues(includeAs Value, scale *scaleComponent, scaleName string) Value {
	if isObject(includeAs) && includeAs.IsObj() && includeAs.ObjValue().Has("value") {
		v := includeAs.Get("value")
		if isSignalRef(v) {
			return mkv("signal", v.Get("signal"))
		}
		return mkv("value", v)
	}
	return scaledZeroOrMinOrMax(scaleName, scale, "zeroOrMin", "", "")
}

// scaledZeroOrMinOrMax builds the ref for the scaled zero (or domain min/max).
func scaledZeroOrMinOrMax(scaleName string, scale *scaleComponent, mode string, widthSignal, heightSignal string) Value {
	if scale == nil || scaleName == "" {
		return undef
	}
	domain := "domain('" + scaleName + "')"
	min := domain + "[0]"
	max := "peek(" + domain + ")"
	switch scale.domainHasZero() {
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
