package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Stack detection — vega-lite/src/stack.ts.

type stackBy struct {
	channel  string
	fieldDef Value
}

type stackProperties struct {
	groupbyChannels []string
	groupbyFields   *sset
	fieldChannel    string
	impute          bool
	stackBy         []stackBy
	// offset is the offset when it is a string ("" otherwise), for
	// comparisons; offsetV is the value as upstream holds it.
	offset  string
	offsetV Value
}

var stackableMarks = strSet([]string{"arc", "bar", "area", "rule", "point", "circle", "square", "line", "text", "tick"})
var stackByDefaultMarks = strSet([]string{"bar", "area", "arc"})

func isUnbinnedQuantitative(cc *compileCtx, cd Value) bool {
	return isFieldDef(cc, cd) && channelDefType(cd) == "quantitative" && !cd.Get("bin").IsTruthy()
}

// potentialStackedChannel finds which of x/y (or theta/radius) is the stacked measure.
func potentialStackedChannel(cc *compileCtx, encoding Value, x string, markDef Value) string {
	y := chRadius
	if x == chX {
		y = chY
	}
	mark, orient := markDef.Get("type").AsString(), markDef.Get("orient").AsString()
	isCartesianBarOrArea := x == chX && (mark == "bar" || mark == "area")
	if cc.v5 {
		isCartesianBarOrArea = x == chX && mark == "bar"
	}
	xDef, yDef := encoding.Get(x), encoding.Get(y)
	if isFieldDef(cc, xDef) && isFieldDef(cc, yDef) {
		if isUnbinnedQuantitative(cc, xDef) && isUnbinnedQuantitative(cc, yDef) {
			if xDef.Get("stack").IsTruthy() {
				return x
			} else if yDef.Get("stack").IsTruthy() {
				return y
			}
			xAgg, yAgg := xDef.Get("aggregate").IsTruthy(), yDef.Get("aggregate").IsTruthy()
			if xAgg != yAgg {
				if xAgg {
					return x
				}
				return y
			}
			if isCartesianBarOrArea {
				if orient == "vertical" {
					return y
				} else if orient == "horizontal" {
					return x
				}
			}
		} else if isUnbinnedQuantitative(cc, xDef) {
			return x
		} else if isUnbinnedQuantitative(cc, yDef) {
			return y
		}
	} else if isUnbinnedQuantitative(cc, xDef) {
		if !cc.v5 && isCartesianBarOrArea && orient == "vertical" {
			return ""
		}
		return x
	} else if isUnbinnedQuantitative(cc, yDef) {
		if !cc.v5 && isCartesianBarOrArea && orient == "horizontal" {
			return ""
		}
		return y
	}
	return ""
}

func getDimensionChannel(channel string) string {
	switch channel {
	case chX:
		return chY
	case chY:
		return chX
	case chTheta:
		return chRadius
	case chRadius:
		return chTheta
	}
	return ""
}

// stackOf computes the stack properties of a unit spec, or nil when it does not stack.
func stackOf(cc *compileCtx, m Value, encoding Value) *stackProperties {
	markDef := m
	if !isMarkDef(m) {
		markDef = mkv("type", m)
	}
	mark := markDef.Get("type").AsString()
	if !stackableMarks[mark] {
		return nil
	}
	fieldChannel := potentialStackedChannel(cc, encoding, chX, markDef)
	if fieldChannel == "" {
		fieldChannel = potentialStackedChannel(cc, encoding, chTheta, markDef)
	}
	if fieldChannel == "" {
		return nil
	}
	stackedFieldDef := encoding.Get(fieldChannel)
	stackedField := ""
	if isFieldDef(cc, stackedFieldDef) {
		stackedField = vgField(cc, stackedFieldDef, fieldRefOption{})
	}
	dimensionChannel := getDimensionChannel(fieldChannel)
	var groupbyChannels []string
	groupbyFields := newSset()
	if encoding.Get(dimensionChannel).IsTruthy() {
		dimensionDef := encoding.Get(dimensionChannel)
		dimensionField := ""
		if isFieldDef(cc, dimensionDef) {
			dimensionField = vgField(cc, dimensionDef, fieldRefOption{})
		}
		if dimensionField != "" && dimensionField != stackedField {
			groupbyChannels = append(groupbyChannels, dimensionChannel)
			groupbyFields.add(dimensionField)
		}
	}
	dimensionOffsetChannel := chYOffset
	if dimensionChannel == chX {
		dimensionOffsetChannel = chXOffset
	}
	dimensionOffsetDef := encoding.Get(dimensionOffsetChannel)
	dimensionOffsetField := ""
	if isFieldDef(cc, dimensionOffsetDef) {
		dimensionOffsetField = vgField(cc, dimensionOffsetDef, fieldRefOption{})
	}
	if dimensionOffsetField != "" && dimensionOffsetField != stackedField {
		groupbyChannels = append(groupbyChannels, dimensionOffsetChannel)
		groupbyFields.add(dimensionOffsetField)
	}
	var stackByList []stackBy
	for _, channel := range nonPositionChannels {
		if channel != chTooltip && channelHasField(cc, encoding, channel) {
			for _, cDef := range arrayOf(encoding.Get(channel)) {
				cc.work(1)
				fd := getFieldDef(cc, cDef)
				if fd.Get("aggregate").IsTruthy() {
					continue
				}
				f := vgField(cc, fd, fieldRefOption{})
				if f == "" || !groupbyFields.has(f) {
					stackByList = append(stackByList, stackBy{channel, fd})
				}
			}
		}
	}
	offsetV := undef
	if s := stackedFieldDef.Get("stack"); !s.IsUndefined() {
		if s.IsBool() {
			if s.BoolValue() {
				offsetV = jsval.Str("zero")
			}
		} else {
			offsetV = s
		}
	} else if stackByDefaultMarks[mark] {
		offsetV = jsval.Str("zero")
	}
	// isStackOffset: `s in STACK_OFFSET_INDEX` in 5.8, which a name every
	// object inherits passes too; hasOwnProperty in 6.4. Both look the value
	// up by its text.
	if !offsetV.IsTruthy() {
		return nil
	}
	switch key := offsetV.AsString(); {
	case key == "zero" || key == "center" || key == "normalize":
	case cc.v5 && isInheritedKey(key):
	default:
		return nil
	}
	offset := ""
	if offsetV.IsStr() {
		offset = offsetV.StrValue()
	}
	if encodingIsAggregate(cc, encoding) && len(stackByList) == 0 {
		return nil
	}
	if st := stackedFieldDef.Get("scale").Get("type"); st.IsTruthy() && !(st.IsStr() && st.StrValue() == "linear") {
		if stackedFieldDef.Get("stack").IsTruthy() {
			cc.warn(msgStackNonLinearScale(cc.v5, st))
		}
		if cc.v5 {
			return nil // 5.8 refuses to stack a non-linear scale
		}
	}
	if sec := getSecondaryRangeChannel(fieldChannel); sec != "" && isFieldOrDatumDef(cc, encoding.Get(sec)) {
		if !stackedFieldDef.Get("stack").IsUndefined() {
			cc.warn(msgCannotStackRangedMark(fieldChannel))
		}
		return nil
	}
	if agg := stackedFieldDef.Get("aggregate"); isFieldDef(cc, stackedFieldDef) && agg.IsTruthy() && !(agg.IsStr() && sumOps[agg.StrValue()]) {
		cc.warn(msgStackNonSummativeAggregate(agg))
	}
	impute := isPathMarkName(mark)
	if stackedFieldDef.Get("impute").IsNull() {
		impute = false
	}
	return &stackProperties{
		groupbyChannels: groupbyChannels, groupbyFields: groupbyFields, fieldChannel: fieldChannel,
		impute: impute, stackBy: stackByList, offset: offset, offsetV: offsetV,
	}
}

// sumOps is upstream's SUM_OPS: the aggregates a stack sums meaningfully.
var sumOps = strSet([]string{"count", "sum", "distinct", "valid", "missing"})

func isPathMarkName(m string) bool { return m == "line" || m == "area" || m == "trail" }

func isRectBasedMark(cc *compileCtx, m string) bool {
	switch m {
	case "rect", "bar", "image", "arc":
		return true
	case "tick":
		return !cc.v5
	}
	return false
}

var _ = jsval.Null
