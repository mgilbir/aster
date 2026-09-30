package guides

import "github.com/mgilbir/aster/purego/internal/jsval"

// anchorExpr selects among start/end/middle values by the item's anchor.
func anchorExpr(s, e, m string) string {
	return "item.anchor === '" + start + "' ? " + s + " : item.anchor === '" + end + "' ? " + e + " : " + m
}

// alignExpr maps a title anchor to a text alignment.
var alignExpr = anchorExpr(stringValue(jsval.Str(left)), stringValue(jsval.Str(right)), stringValue(jsval.Str("center")))

// tickBandInfo is the resolved band position, extra-tick flag and offset of
// an axis' grid, ticks and labels.
type tickBandInfo struct {
	extra, band, offset Value
}

// tickBand implements guide-util's tickBand: `tickBand: "extent"` puts ticks
// at the band edges and adds an extra closing tick; otherwise the
// bandPosition/tickExtra/tickOffset properties apply. A signal-valued
// tickBand yields expressions that decide at runtime.
func tickBand(l lookup) tickBandInfo {
	v := l.get("tickBand")
	offset := l.get("tickOffset")
	var band, extra Value
	switch {
	case !v.IsTruthy():
		band = l.get("bandPosition")
		extra = l.get("tickExtra")
	case isSignal(v):
		s := signalOf(v)
		band = objv("signal", "("+s+") === 'extent' ? 1 : 0.5")
		extra = objv("signal", "("+s+") === 'extent'")
		if !isObject(offset) {
			offset = objv("signal", "("+s+") === 'extent' ? 0 : "+offset.AsString())
		}
	case v.IsStr() && v.StrValue() == "extent":
		band = jsval.Num(1)
		extra = jsval.True
		offset = jsval.Num(0)
	default:
		band = jsval.Num(0.5)
		extra = jsval.False
	}
	return tickBandInfo{extra: extra, band: band, offset: offset}
}

// extendOffset adds a label offset to a (possibly nested) tick offset.
func extendOffset(v, offset Value) Value {
	switch {
	case !offset.IsTruthy():
		return v
	case !v.IsTruthy():
		return offset
	case !isObject(v):
		return objv("value", v, "offset", offset)
	}
	o := extend(jsval.NewObject(0), v)
	set(o, "offset", extendOffset(v.Get("offset"), offset))
	return jsval.Obj(o)
}
