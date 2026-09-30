package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Encoding helpers — vega-lite/src/encoding.ts. An encoding is an object mapping
// channel names to channel definitions (or arrays of them for detail, order and
// tooltip).

func channelHasField(cc *compileCtx, enc Value, channel string) bool {
	cd := enc.Get(channel)
	if cd.IsTruthy() {
		if cd.IsArr() {
			for _, fd := range cd.Items() {
				if fd.Get("field").IsTruthy() {
					return true
				}
			}
			return false
		}
		return isFieldDef(cc, cd) || hasConditionalFieldDef(cc, cd)
	}
	return false
}

func channelHasFieldOrDatum(cc *compileCtx, enc Value, channel string) bool {
	cd := enc.Get(channel)
	if cd.IsTruthy() {
		if cd.IsArr() {
			for _, fd := range cd.Items() {
				if fd.Get("field").IsTruthy() {
					return true
				}
			}
			return false
		}
		return isFieldDef(cc, cd) || isDatumDef(cd) || hasConditionalFieldOrDatumDef(cc, cd)
	}
	return false
}

func channelHasNestedOffsetScale(cc *compileCtx, enc Value, channel string) bool {
	if isXorY(channel) {
		fd := enc.Get(channel)
		if (isFieldDef(cc, fd) || isDatumDef(fd)) && (isDiscreteType(channelDefType(fd)) || (!cc.v5 && isFieldDef(cc, fd) && fd.Get("timeUnit").IsTruthy())) {
			return channelHasFieldOrDatum(cc, enc, getOffsetScaleChannel(channel))
		}
	}
	return false
}

// encodingIsAggregate: any channel's field def carries an aggregate.
func encodingIsAggregate(cc *compileCtx, enc Value) bool {
	for _, channel := range allChannels {
		if channelHasField(cc, enc, channel) {
			cd := enc.Get(channel)
			if cd.IsArr() {
				for _, fd := range cd.Items() {
					if fd.Get("aggregate").IsTruthy() {
						return true
					}
				}
			} else if fd := getFieldDef(cc, cd); fd.IsTruthy() && fd.Get("aggregate").IsTruthy() {
				return true
			}
		}
	}
	return false
}

// encodingEach is forEach over a mapping: f is called for every channel def,
// array elements individually.
func encodingEach(mapping Value, f func(cd Value, channel string)) {
	if !mapping.IsObj() {
		return
	}
	o := mapping.ObjValue()
	for _, channel := range append([]string(nil), o.Keys()...) {
		el := o.Lookup(channel)
		if el.IsArr() {
			for _, cd := range el.Items() {
				f(cd, channel)
			}
		} else {
			f(el, channel)
		}
	}
}

// fieldDefsOf returns every field def (or conditional field def) in the encoding.
func fieldDefsOf(cc *compileCtx, enc Value) []Value {
	var out []Value
	for _, channel := range keysOf(enc) {
		if channelHasField(cc, enc, channel) {
			for _, def := range arrayOf(enc.Get(channel)) {
				if isFieldDef(cc, def) {
					out = append(out, def)
				} else if hasConditionalFieldDef(cc, def) {
					out = append(out, def.Get("condition"))
				}
			}
		}
	}
	return out
}

type extractedTransforms struct {
	bins, timeUnits, aggregate []Value
	groupby                    []Value
	encoding                   *Object
}

// extractTransformsFromEncoding moves aggregate/bin/timeUnit out of the field
// defs into explicit transforms (used by the composite marks).
func extractTransformsFromEncoding(cc *compileCtx, oldEncoding Value, config Value) extractedTransforms {
	r := extractedTransforms{encoding: jsval.NewObject(8)}
	encodingEach(oldEncoding, func(cd Value, channel string) {
		if isFieldDef(cc, cd) {
			field, aggOp, bin, timeUnit := cd.Get("field"), cd.Get("aggregate"), cd.Get("bin"), cd.Get("timeUnit")
			remaining := omit(cd, "field", "aggregate", "bin", "timeUnit")
			if aggOp.IsTruthy() || timeUnit.IsTruthy() || bin.IsTruthy() {
				guide := getGuide(cd)
				isTitleDefined := guide.Get("title").IsTruthy()
				newField := vgField(cc, cd, fieldRefOption{forAs: true})
				nf := jsval.NewObject(remaining.Len() + 2)
				if !isTitleDefined {
					nf.Set("title", fieldTitle(cc, cd, config, true, true))
				}
				spread(nf, jsval.Obj(remaining))
				nf.Set("field", jsval.Str(newField))
				if aggOp.IsTruthy() {
					op := ""
					switch {
					case isArgmaxDef(aggOp):
						op = "argmax"
						newField = vgField(cc, mkv("op", "argmax", "field", aggOp.Get("argmax")), fieldRefOption{forAs: true})
						nf.Set("field", jsval.Str(newField+"."+field.AsString()))
					case isArgminDef(aggOp):
						op = "argmin"
						newField = vgField(cc, mkv("op", "argmin", "field", aggOp.Get("argmin")), fieldRefOption{forAs: true})
						nf.Set("field", jsval.Str(newField+"."+field.AsString()))
					case aggOp.IsStr() && aggOp.StrValue() != "boxplot" && aggOp.StrValue() != "errorbar" && aggOp.StrValue() != "errorband":
						op = aggOp.StrValue()
					}
					if op != "" {
						entry := mk("op", op, "as", newField)
						if field.IsTruthy() {
							entry.Set("field", field)
						}
						r.aggregate = append(r.aggregate, jsval.Obj(entry))
					}
				} else {
					r.groupby = append(r.groupby, jsval.Str(newField))
					if isTypedFieldDef(cd) && isBinning(bin) {
						r.bins = append(r.bins, mkv("bin", bin, "field", field, "as", newField))
						r.groupby = append(r.groupby, jsval.Str(vgField(cc, cd, fieldRefOption{binSuffix: "end"})))
						if binRequiresRange(cc, cd, channel) {
							r.groupby = append(r.groupby, jsval.Str(vgField(cc, cd, fieldRefOption{binSuffix: "range"})))
						}
						if isXorY(channel) {
							r.encoding.Set(channel+"2", mkv("field", newField+"_end"))
						}
						nf.Set("bin", jsval.Str("binned"))
						if !isSecondaryRangeChannel(channel) {
							nf.Set("type", jsval.Str("quantitative"))
						}
					} else if timeUnit.IsTruthy() && !isBinnedTimeUnit(cc, timeUnit) {
						r.timeUnits = append(r.timeUnits, mkv("timeUnit", timeUnit, "field", field, "as", newField))
						formatType := ""
						if isTypedFieldDef(cd) && channelDefType(cd) != "temporal" {
							formatType = "time"
						}
						if formatType != "" {
							switch {
							case channel == chText || channel == chTooltip:
								nf.Set("formatType", jsval.Str(formatType))
							case isNonPositionScaleChannel(cc, channel):
								nf.Set("legend", jsval.Obj(spread(mk("formatType", formatType), nf.Lookup("legend"))))
							case isXorY(channel):
								nf.Set("axis", jsval.Obj(spread(mk("formatType", formatType), nf.Lookup("axis"))))
							}
						}
					}
				}
				r.encoding.Set(channel, jsval.Obj(nf))
			} else {
				r.groupby = append(r.groupby, field)
				r.encoding.Set(channel, oldEncoding.Get(channel))
			}
		} else {
			r.encoding.Set(channel, oldEncoding.Get(channel))
		}
	})
	return r
}

func markChannelCompatible(cc *compileCtx, enc Value, channel, mark string) bool {
	switch supportMark(channel, mark) {
	case "":
		return false
	case "binned":
		primaryChannel := chY
		if channel == chX2 {
			primaryChannel = chX
		}
		primary := enc.Get(primaryChannel)
		return isFieldDef(cc, primary) && isFieldDef(cc, enc.Get(channel)) && isBinned(primary.Get("bin"))
	}
	return true
}

// initEncoding normalizes a unit spec's encoding: drops channels the mark does
// not support or that conflict, and initializes each channel def.
func initEncoding(cc *compileCtx, encoding Value, mark string, filled bool, config Value) Value {
	normalized := jsval.NewObject(8)
	for _, ch := range unitChannels {
		channel := ch
		if !encoding.Get(channel).IsTruthy() {
			continue
		}
		channelDef := encoding.Get(channel)
		if cc.v5 && channel == chTime {
			continue // the time channel is a 6.x feature
		}
		if isXorYOffset(channel) {
			mainChannel := getMainChannelFromOffsetChannel(channel)
			positionDef := normalized.Lookup(mainChannel)
			if isFieldDef(cc, positionDef) && isContinuousType(channelDefType(positionDef)) {
				if isFieldDef(cc, channelDef) && (cc.v5 || !positionDef.Get("timeUnit").IsTruthy()) {
					continue
				}
			} else if cc.v5 && !isFieldDef(cc, positionDef) {
				// 5.8 turns an offset without a position into the position itself.
				channel = mainChannel
			}
		}
		if channel == chAngle && mark == "arc" && !encoding.Get("theta").IsTruthy() {
			channel = chTheta
		}
		if !markChannelCompatible(cc, encoding, channel, mark) {
			continue
		}
		if channel == chSize && mark == "line" {
			if fd := getFieldDef(cc, encoding.Get(channel)); fd.Get("aggregate").IsTruthy() {
				continue
			}
		}
		if channel == chColor {
			has := false
			if filled {
				has = encoding.ObjValue().Has("fill")
			} else {
				has = encoding.ObjValue().Has("stroke")
			}
			if has {
				continue
			}
		}
		if channel == chDetail || (channel == chOrder && !channelDef.IsArr() && !isValueDef(channelDef)) || (channel == chTooltip && channelDef.IsArr()) {
			if channelDef.IsTruthy() {
				if channel == chOrder {
					def := encoding.Get(channel)
					if isOrderOnlyDef(cc, def) {
						normalized.Set(channel, def)
						continue
					}
				}
				var defs []Value
				for _, fd := range arrayOf(channelDef) {
					if isFieldDef(cc, fd) {
						defs = append(defs, initFieldDef(cc, fd, channel, false))
					}
				}
				normalized.Set(channel, jsval.Arr(defs))
			}
		} else {
			if channel == chTooltip && channelDef.IsNull() {
				normalized.Set(channel, jsval.Null)
			} else if !isFieldDef(cc, channelDef) && !isDatumDef(channelDef) && !isValueDef(channelDef) && !isConditionalDef(channelDef) && !isSignalRef(channelDef) {
				continue
			} else {
				normalized.Set(channel, initChannelDef(cc, channelDef, channel, config, false))
			}
		}
	}
	return jsval.Obj(normalized)
}

func normalizeEncoding(cc *compileCtx, encoding Value, config Value) Value {
	out := jsval.NewObject(encoding.Len())
	for _, channel := range keysOf(encoding) {
		out.Set(channel, initChannelDef(cc, encoding.Get(channel), channel, config, true))
	}
	return jsval.Obj(out)
}

// pathGroupingFields lists the fields that split a line/area/trail into
// separate paths.
func pathGroupingFields(cc *compileCtx, mark string, encoding Value) []string {
	var details []string
	for _, channel := range keysOf(encoding) {
		switch channel {
		case chX, chY, chHref, chDescription, chURL, chX2, chY2:
		case chXOffset, chYOffset:
			if !cc.v5 && (mark == "line" || mark == "area" || mark == "trail") {
				offsetDef := encoding.Get(channel)
				if isFieldDef(cc, offsetDef) {
					mainChannel := chX
					if channel == chYOffset {
						mainChannel = chY
					}
					mainDef := encoding.Get(mainChannel)
					if isFieldDef(cc, mainDef) && !mainDef.Get("aggregate").IsTruthy() && !offsetDef.Get("aggregate").IsTruthy() {
						mainField := vgField(cc, mainDef, fieldRefOption{})
						offsetField := vgField(cc, offsetDef, fieldRefOption{})
						if mainField != "" && offsetField != "" && mainField != offsetField {
							details = append(details, mainField)
						}
					}
				}
			}
		case chTheta, chTheta2, chRadius, chRadius2, chTime, chLatitude, chLongitude, chLatitude2, chLongitude2,
			chText, chShape, chAngle, chTooltip:
		case chOrder, chDetail, chKey:
			if channel == chOrder && (mark == "line" || mark == "trail") {
				continue
			}
			cd := encoding.Get(channel)
			if cd.IsArr() || isFieldDef(cc, cd) {
				for _, fd := range arrayOf(cd) {
					if !fd.Get("aggregate").IsTruthy() {
						details = append(details, vgField(cc, fd, fieldRefOption{}))
					}
				}
			}
		case chSize, chColor, chFill, chStroke, chOpacity, chFillOpacity, chStrokeOpacity, chStrokeDash, chStrokeWidth:
			if channel == chSize && mark == "trail" {
				continue
			}
			fd := getFieldDef(cc, encoding.Get(channel))
			if fd.IsTruthy() && !fd.Get("aggregate").IsTruthy() {
				details = append(details, vgField(cc, fd, fieldRefOption{}))
			}
		}
	}
	return details
}
