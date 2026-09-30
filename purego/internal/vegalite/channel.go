package vegalite

// Channel names and the channel classifications of vega-lite/src/channel.ts.
// The order of the lists below is upstream's key order, which decides the
// order in which scales, axes and legends are emitted.

const (
	chRow           = "row"
	chColumn        = "column"
	chFacet         = "facet"
	chX             = "x"
	chY             = "y"
	chX2            = "x2"
	chY2            = "y2"
	chXOffset       = "xOffset"
	chYOffset       = "yOffset"
	chRadius        = "radius"
	chRadius2       = "radius2"
	chTheta         = "theta"
	chTheta2        = "theta2"
	chLatitude      = "latitude"
	chLongitude     = "longitude"
	chLatitude2     = "latitude2"
	chLongitude2    = "longitude2"
	chTime          = "time"
	chColor         = "color"
	chFill          = "fill"
	chStroke        = "stroke"
	chShape         = "shape"
	chSize          = "size"
	chAngle         = "angle"
	chOpacity       = "opacity"
	chFillOpacity   = "fillOpacity"
	chStrokeOpacity = "strokeOpacity"
	chStrokeWidth   = "strokeWidth"
	chStrokeDash    = "strokeDash"
	chText          = "text"
	chOrder         = "order"
	chDetail        = "detail"
	chKey           = "key"
	chTooltip       = "tooltip"
	chHref          = "href"
	chURL           = "url"
	chDescription   = "description"
)

var (
	facetChannels = []string{chRow, chColumn, chFacet}

	unitChannels = []string{
		chX, chY, chX2, chY2,
		chTheta, chTheta2, chRadius, chRadius2,
		chLongitude, chLongitude2, chLatitude, chLatitude2,
		chXOffset, chYOffset,
		chColor, chFill, chStroke,
		chTime,
		chOpacity, chFillOpacity, chStrokeOpacity, chStrokeWidth, chStrokeDash,
		chSize, chAngle, chShape,
		chOrder, chText, chDetail, chKey, chTooltip, chHref, chURL, chDescription,
	}

	allChannels = append(append([]string{}, unitChannels...), facetChannels...)

	// singleDefChannels: every channel except order, detail, tooltip.
	singleDefChannels = filterOut(allChannels, chOrder, chDetail, chTooltip)
	// singleDefUnitChannels additionally drops the facet channels.
	singleDefUnitChannels = filterOut(singleDefChannels, chRow, chColumn, chFacet)

	// nonPositionChannels: unit channels other than positions, offsets and geo/polar positions.
	nonPositionChannels = []string{
		chColor, chFill, chStroke, chTime,
		chOpacity, chFillOpacity, chStrokeOpacity, chStrokeWidth, chStrokeDash,
		chSize, chAngle, chShape,
		chOrder, chText, chDetail, chKey, chTooltip, chHref, chURL, chDescription,
	}
	nonPositionScaleChannels = []string{
		chColor, chFill, chStroke, chTime,
		chOpacity, chFillOpacity, chStrokeOpacity, chStrokeWidth, chStrokeDash,
		chSize, chAngle, chShape,
	}
	scaleChannels = append([]string{chX, chY, chTheta, chRadius, chXOffset, chYOffset}, nonPositionScaleChannels...)

	positionScaleChannels  = []string{chX, chY}
	geoPositionChannels    = []string{chLongitude, chLongitude2, chLatitude, chLatitude2}
	secondaryRangeChannels = []string{chX2, chY2, chLatitude2, chLongitude2, chTheta2, chRadius2}
)

var (
	channelSet            = strSet(allChannels)
	singleDefUnitSet      = strSet(singleDefUnitChannels)
	scaleChannelSet       = strSet(scaleChannels)
	nonPositionChannelSet = strSet(nonPositionChannels)
	geoPositionSet        = strSet(geoPositionChannels)
	polarPositionChannels = strSet([]string{chTheta, chTheta2, chRadius, chRadius2})
)

func strSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func filterOut(in []string, drop ...string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !contains(drop, s) {
			out = append(out, s)
		}
	}
	return out
}

func isChannel(s string) bool               { return channelSet[s] && !(v5 && s == chTime) }
func isSingleDefUnitChannel(s string) bool  { return singleDefUnitSet[s] && !(v5 && s == chTime) }
func isScaleChannel(s string) bool          { return scaleChannelSet[s] && !(v5 && s == chTime) }
func isGeoPositionChannel(s string) bool    { return geoPositionSet[s] }
func isPolarPositionChannel(s string) bool  { return polarPositionChannels[s] }
func isSecondaryRangeChannel(s string) bool { return getMainRangeChannel(s) != s }
func isXorY(s string) bool                  { return s == chX || s == chY }
func isXorYOffset(s string) bool            { return s == chXOffset || s == chYOffset }
func isTimeChannel(s string) bool           { return s == chTime }
func isColorChannel(s string) bool          { return s == chColor || s == chFill || s == chStroke }
func isNonPositionScaleChannel(s string) bool {
	return nonPositionChannelSet[s] && !(v5 && s == chTime)
}

// isNonPositionScaleChannel upstream is hasOwnProperty(NONPOSITION_CHANNEL_INDEX,
// channel), i.e. it is true for text/tooltip/... as well; keep that.

func getPositionChannelFromLatLong(c string) string {
	switch c {
	case chLatitude:
		return chY
	case chLatitude2:
		return chY2
	case chLongitude:
		return chX
	case chLongitude2:
		return chX2
	}
	return ""
}

func getMainRangeChannel(c string) string {
	switch c {
	case chX2:
		return chX
	case chY2:
		return chY
	case chLatitude2:
		return chLatitude
	case chLongitude2:
		return chLongitude
	case chTheta2:
		return chTheta
	case chRadius2:
		return chRadius
	}
	return c
}

func getSecondaryRangeChannel(c string) string {
	switch c {
	case chX:
		return chX2
	case chY:
		return chY2
	case chLatitude:
		return chLatitude2
	case chLongitude:
		return chLongitude2
	case chTheta:
		return chTheta2
	case chRadius:
		return chRadius2
	}
	return ""
}

func getVgPositionChannel(c string) string {
	switch c {
	case chTheta:
		return "startAngle"
	case chTheta2:
		return "endAngle"
	case chRadius:
		return "outerRadius"
	case chRadius2:
		return "innerRadius"
	}
	return c
}

func getSizeChannel(c string) string {
	switch c {
	case chX, chX2:
		return "width"
	case chY, chY2:
		return "height"
	}
	return ""
}

func getOffsetChannel(c string) string {
	switch c {
	case chX:
		return "xOffset"
	case chY:
		return "yOffset"
	case chX2:
		return "x2Offset"
	case chY2:
		return "y2Offset"
	case chTheta:
		return "thetaOffset"
	case chRadius:
		return "radiusOffset"
	case chTheta2:
		return "theta2Offset"
	case chRadius2:
		return "radius2Offset"
	}
	return ""
}

func getOffsetScaleChannel(c string) string {
	switch c {
	case chX:
		return chXOffset
	case chY:
		return chYOffset
	}
	return ""
}

func getMainChannelFromOffsetChannel(c string) string {
	switch c {
	case chXOffset:
		return chX
	case chYOffset:
		return chY
	}
	return ""
}

func getPositionScaleChannel(sizeType string) string {
	if sizeType == "width" {
		return chX
	}
	return chY
}

func supportLegend(c string) bool {
	switch c {
	case chColor, chFill, chStroke, chSize, chShape, chOpacity, chStrokeWidth, chStrokeDash:
		return true
	}
	return false
}

// supportMark reports how channel c supports mark m: "always", "binned" or "".
func supportMark(c, m string) string {
	switch c {
	case chColor, chFill, chStroke, chDescription, chDetail, chKey, chTooltip, chHref, chOrder,
		chOpacity, chFillOpacity, chStrokeOpacity, chStrokeWidth, chFacet, chRow, chColumn:
		return "always"
	case chX, chY, chXOffset, chYOffset, chLatitude, chLongitude, chTime:
		if m == "geoshape" {
			return ""
		}
		return "always"
	case chX2, chY2, chLatitude2, chLongitude2:
		switch m {
		case "area", "bar", "image", "rect", "rule":
			return "always"
		case "circle", "point", "square", "tick", "line", "trail":
			return "binned"
		}
	case chSize:
		switch m {
		case "point", "tick", "rule", "circle", "square", "bar", "text", "line", "trail":
			return "always"
		}
	case chStrokeDash:
		switch m {
		case "line", "point", "tick", "rule", "circle", "square", "bar", "geoshape":
			return "always"
		}
	case chShape:
		if m == "point" || m == "geoshape" {
			return "always"
		}
	case chText:
		if m == "text" {
			return "always"
		}
	case chAngle:
		if m == "point" || m == "square" || m == "text" {
			return "always"
		}
	case chURL:
		if m == "image" {
			return "always"
		}
	case chTheta, chRadius:
		if m == "text" || m == "arc" {
			return "always"
		}
	case chTheta2, chRadius2:
		if m == "arc" {
			return "always"
		}
	}
	return ""
}

// rangeType is upstream's rangeType: "discrete", "flexible" or "" (undefined).
func rangeType(c string) string {
	switch c {
	case chFacet, chRow, chColumn, chShape, chStrokeDash, chText, chTooltip, chHref, chURL, chDescription:
		return "discrete"
	case chColor, chFill, chStroke:
		return "flexible"
	}
	return ""
}
