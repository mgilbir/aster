package guides

// Orientations and anchors used by the guide encoders.
const (
	top    = "top"
	left   = "left"
	right  = "right"
	bottom = "bottom"

	start  = "start"
	middle = "middle"
	end    = "end"

	vertical = "vertical"
)

// Data field names of the guide datums.
const (
	fIndex  = "index"
	fLabel  = "label"
	fOffset = "offset"
	fPerc   = "perc"
	fPerc2  = "perc2"
	fValue  = "value"
	fSize   = "size"
)

// Style names of the guide text marks and of group titles.
const (
	guideLabelStyle    = "guide-label"
	guideTitleStyle    = "guide-title"
	groupTitleStyle    = "group-title"
	groupSubtitleStyle = "group-subtitle"
)

// Legend types.
const (
	symbolLegend   = "symbol"
	gradientLegend = "gradient"
	discreteLegend = "discrete"
)

// legendScales lists the encoding channels a legend can be built from, in the
// priority order that selects the canonical scale.
var legendScales = []string{"size", "shape", "fill", "stroke", "strokeWidth", "strokeDash", "opacity"}

// skip names the encode-block keys that describe the mark rather than its
// encoders (guideMark and extendEncode do not merge them).
func skipKey(name string) bool {
	return name == "name" || name == "style" || name == "interactive"
}
