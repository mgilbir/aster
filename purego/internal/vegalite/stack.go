package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
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
	offset          string
}

var stackableMarks = strSet([]string{"arc", "bar", "area", "rule", "point", "circle", "square", "line", "text", "tick"})
var stackByDefaultMarks = strSet([]string{"bar", "area", "arc"})

func isUnbinnedQuantitative(cd Value) bool {
	return isFieldDef(cd) && channelDefType(cd) == "quantitative" && !cd.Get("bin").IsTruthy()
}

// potentialStackedChannel finds which of x/y (or theta/radius) is the stacked measure.
func potentialStackedChannel(encoding Value, x string, markDef Value) string {
	y := chRadius
	if x == chX {
		y = chY
	}
	mark, orient := markDef.Get("type").AsString(), markDef.Get("orient").AsString()
	isCartesianBarOrArea := x == chX && (mark == "bar" || mark == "area")
	if v5 {
		isCartesianBarOrArea = x == chX && mark == "bar"
	}
	xDef, yDef := encoding.Get(x), encoding.Get(y)
	if isFieldDef(xDef) && isFieldDef(yDef) {
		if isUnbinnedQuantitative(xDef) && isUnbinnedQuantitative(yDef) {
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
		} else if isUnbinnedQuantitative(xDef) {
			return x
		} else if isUnbinnedQuantitative(yDef) {
			return y
		}
	} else if isUnbinnedQuantitative(xDef) {
		if !v5 && isCartesianBarOrArea && orient == "vertical" {
			return ""
		}
		return x
	} else if isUnbinnedQuantitative(yDef) {
		if !v5 && isCartesianBarOrArea && orient == "horizontal" {
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
func stackOf(m Value, encoding Value) *stackProperties {
	markDef := m
	if !isMarkDef(m) {
		markDef = mkv("type", m)
	}
	mark := markDef.Get("type").AsString()
	if !stackableMarks[mark] {
		return nil
	}
	fieldChannel := potentialStackedChannel(encoding, chX, markDef)
	if fieldChannel == "" {
		fieldChannel = potentialStackedChannel(encoding, chTheta, markDef)
	}
	if fieldChannel == "" {
		return nil
	}
	stackedFieldDef := encoding.Get(fieldChannel)
	stackedField := ""
	if isFieldDef(stackedFieldDef) {
		stackedField = vgField(stackedFieldDef, fieldRefOption{})
	}
	dimensionChannel := getDimensionChannel(fieldChannel)
	var groupbyChannels []string
	groupbyFields := newSset()
	if encoding.Get(dimensionChannel).IsTruthy() {
		dimensionDef := encoding.Get(dimensionChannel)
		dimensionField := ""
		if isFieldDef(dimensionDef) {
			dimensionField = vgField(dimensionDef, fieldRefOption{})
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
	if isFieldDef(dimensionOffsetDef) {
		dimensionOffsetField = vgField(dimensionOffsetDef, fieldRefOption{})
	}
	if dimensionOffsetField != "" && dimensionOffsetField != stackedField {
		groupbyChannels = append(groupbyChannels, dimensionOffsetChannel)
		groupbyFields.add(dimensionOffsetField)
	}
	var stackByList []stackBy
	for _, channel := range nonPositionChannels {
		if channel != chTooltip && channelHasField(encoding, channel) {
			for _, cDef := range arrayOf(encoding.Get(channel)) {
				fd := getFieldDef(cDef)
				if fd.Get("aggregate").IsTruthy() {
					continue
				}
				f := vgField(fd, fieldRefOption{})
				if f == "" || !groupbyFields.has(f) {
					stackByList = append(stackByList, stackBy{channel, fd})
				}
			}
		}
	}
	offset := ""
	haveOffset := false
	if s := stackedFieldDef.Get("stack"); !s.IsUndefined() {
		if s.IsBool() {
			if s.BoolValue() {
				offset, haveOffset = "zero", true
			}
		} else if s.IsStr() {
			offset, haveOffset = s.StrValue(), true
		}
	} else if stackByDefaultMarks[mark] {
		offset, haveOffset = "zero", true
	}
	if !haveOffset || (offset != "zero" && offset != "center" && offset != "normalize") {
		return nil
	}
	if encodingIsAggregate(encoding) && len(stackByList) == 0 {
		return nil
	}
	if st := stackedFieldDef.Get("scale").Get("type"); v5 && st.IsTruthy() && !(st.IsStr() && st.StrValue() == "linear") {
		return nil // 5.8 refuses to stack a non-linear scale
	}
	if sec := getSecondaryRangeChannel(fieldChannel); sec != "" && isFieldOrDatumDef(encoding.Get(sec)) {
		return nil
	}
	impute := isPathMarkName(mark)
	if stackedFieldDef.Get("impute").IsNull() {
		impute = false
	}
	return &stackProperties{
		groupbyChannels: groupbyChannels, groupbyFields: groupbyFields, fieldChannel: fieldChannel,
		impute: impute, stackBy: stackByList, offset: offset,
	}
}

func isPathMarkName(m string) bool { return m == "line" || m == "area" || m == "trail" }

func isRectBasedMark(m string) bool {
	switch m {
	case "rect", "bar", "image", "arc":
		return true
	case "tick":
		return !v5
	}
	return false
}

var _ = jsval.Null
