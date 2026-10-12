package vegalite

// Scale type tables and predicates from vega-lite/src/scale.ts.

var scaleCategory = map[string]string{
	"linear": "numeric", "log": "numeric", "pow": "numeric", "sqrt": "numeric", "symlog": "numeric",
	"identity": "numeric", "sequential": "numeric",
	"time": "time", "utc": "time",
	"ordinal": "ordinal", "bin-ordinal": "bin-ordinal",
	"point": "ordinal-position", "band": "ordinal-position",
	"quantile": "discretizing", "quantize": "discretizing", "threshold": "discretizing",
}

// scaleCompatible compares SCALE_CATEGORY_INDEX[a] and [b]: a type every
// object inherits reads as that member, equal only to itself.
func scaleCompatible(a, b string) bool {
	cat := func(t string) string {
		if c, ok := scaleCategory[t]; ok {
			return c
		}
		if isInheritedKey(t) {
			return "\x00" + t
		}
		return ""
	}
	c1, c2 := cat(a), cat(b)
	return c1 == c2 || (c1 == "ordinal-position" && c2 == "time") || (c2 == "ordinal-position" && c1 == "time")
}

func scaleTypePrecedence(t string) int {
	switch t {
	case "log", "pow", "sqrt", "symlog", "identity", "sequential":
		return 1
	case "point":
		return 10
	case "band":
		return 11
	}
	return 0
}

func isQuantitativeScale(t string) bool {
	switch t {
	case "linear", "log", "pow", "sqrt", "symlog":
		return true
	}
	return false
}

func isContinuousToContinuous(t string) bool {
	return isQuantitativeScale(t) || t == "time" || t == "utc"
}

func isContinuousToDiscrete(t string) bool {
	return t == "quantile" || t == "quantize" || t == "threshold"
}

func hasContinuousDomain(t string) bool {
	return isContinuousToContinuous(t) || isContinuousToDiscrete(t) || t == "sequential" || t == "identity"
}

func hasDiscreteDomain(t string) bool {
	switch t {
	case "ordinal", "bin-ordinal", "point", "band":
		return true
	}
	return false
}

func defaultScaleConfig(cc *compileCtx) *Object {
	if cc.v5 {
		return mk(
			"pointPadding", 0.5,
			"barBandPaddingInner", 0.1,
			"rectBandPaddingInner", 0,
			"bandWithNestedOffsetPaddingInner", 0.2,
			"bandWithNestedOffsetPaddingOuter", 0.2,
			"minBandSize", 2,
			"minFontSize", 8,
			"maxFontSize", 40,
			"minOpacity", 0.3,
			"maxOpacity", 0.8,
			"minSize", 9,
			"minStrokeWidth", 1,
			"maxStrokeWidth", 4,
			"quantileCount", 4,
			"quantizeCount", 4,
			"zero", true,
		)
	}
	return mk(
		"pointPadding", 0.5,
		"barBandPaddingInner", 0.1,
		"rectBandPaddingInner", 0,
		"tickBandPaddingInner", 0.25,
		"bandWithNestedOffsetPaddingInner", 0.2,
		"bandWithNestedOffsetPaddingOuter", 0.2,
		"minBandSize", 2,
		"minFontSize", 8,
		"maxFontSize", 40,
		"minOpacity", 0.3,
		"maxOpacity", 0.8,
		"minSize", 4,
		"minStrokeWidth", 1,
		"maxStrokeWidth", 4,
		"quantileCount", 4,
		"quantizeCount", 4,
		"zero", true,
		"framesPerSecond", 2,
		"animationDuration", 5,
	)
}

// isExtendedScheme is !isString(scheme) && hasProperty(scheme, 'name') in
// 6.4; 5.8 reads !!scheme['name'], a TypeError of null.
func isExtendedScheme(cc *compileCtx, v Value) bool {
	if cc != nil && cc.v5 {
		return !v.IsStr() && jsGet(v, "name").IsTruthy()
	}
	return !v.IsStr() && hasProperty(v, "name")
}

func isParameterDomain(v Value) bool { return hasProperty(v, "param") }

// isDomainUnionWith is hasProperty(domain, 'unionWith') in 6.4; 5.8 reads
// domain?.['unionWith'], so a falsy unionWith is no union.
func isDomainUnionWith(cc *compileCtx, v Value) bool {
	if cc != nil && cc.v5 {
		return !v.IsNullish() && jsProp(v, "unionWith").IsTruthy()
	}
	return hasProperty(v, "unionWith")
}
func isFieldRange(v Value) bool { return isObject(v) && v.IsObj() && v.ObjValue().Has("field") }

// nonTypeDomainRangeVegaScaleProperties drops type, domain, range, rangeMax,
// rangeMin and scheme.
var nonTypeDomainRangeVegaScaleProperties = []string{
	"domainMax", "domainMin", "domainMid", "domainRaw", "align", "bins", "reverse", "round",
	"clamp", "nice", "base", "exponent", "constant", "interpolate", "zero", "padding",
	"paddingInner", "paddingOuter",
}

func scaleTypeSupportProperty(scaleType, prop string) bool {
	switch prop {
	case "type", "domain", "reverse", "range":
		return true
	case "scheme", "interpolate":
		return !contains([]string{"point", "band", "identity"}, scaleType)
	case "bins":
		return !contains([]string{"point", "band", "identity", "ordinal"}, scaleType)
	case "round":
		return isContinuousToContinuous(scaleType) || scaleType == "band" || scaleType == "point"
	case "padding", "rangeMin", "rangeMax":
		return isContinuousToContinuous(scaleType) || scaleType == "point" || scaleType == "band"
	case "paddingOuter", "align":
		return scaleType == "point" || scaleType == "band"
	case "paddingInner":
		return scaleType == "band"
	case "domainMax", "domainMid", "domainMin", "domainRaw", "clamp":
		return isContinuousToContinuous(scaleType)
	case "nice":
		return isContinuousToContinuous(scaleType) || scaleType == "quantize" || scaleType == "threshold"
	case "exponent":
		return scaleType == "pow"
	case "base":
		return scaleType == "log"
	case "constant":
		return scaleType == "symlog"
	case "zero":
		return hasContinuousDomain(scaleType) && !contains([]string{"log", "time", "utc", "threshold", "quantile"}, scaleType)
	}
	return false
}

// channelScalePropertyIncompatible reports whether the scale property cannot be
// used on the channel (only interpolate/scheme/domainMid are color-only).
func channelScalePropertyIncompatible(channel, prop string) bool {
	switch prop {
	case "interpolate", "scheme", "domainMid":
		return !isColorChannel(channel)
	}
	return false
}

func scaleTypeSupportDataType(specified, fieldDefType string) bool {
	switch fieldDefType {
	case "ordinal", "nominal":
		return specified == "" || hasDiscreteDomain(specified)
	case "temporal":
		return specified == "time" || specified == "utc" || specified == ""
	case "quantitative":
		return isQuantitativeScale(specified) || isContinuousToDiscrete(specified) || specified == ""
	}
	return true
}

func channelSupportScaleType(cc *compileCtx, channel, scaleType string, hasNestedOffsetScale bool) bool {
	if !isScaleChannel(cc, channel) {
		return false
	}
	switch channel {
	case chX, chY, chXOffset, chYOffset, chTheta, chRadius:
		if isContinuousToContinuous(scaleType) {
			return true
		} else if scaleType == "band" {
			return true
		} else if scaleType == "point" {
			return !hasNestedOffsetScale
		}
		return false
	case chTime:
		return scaleType == "linear" || scaleType == "band"
	case chSize, chStrokeWidth, chOpacity, chFillOpacity, chStrokeOpacity, chAngle:
		return isContinuousToContinuous(scaleType) || isContinuousToDiscrete(scaleType) ||
			scaleType == "band" || scaleType == "point" || scaleType == "ordinal"
	case chColor, chFill, chStroke:
		return scaleType != "band"
	case chStrokeDash, chShape:
		return scaleType == "ordinal" || isContinuousToDiscrete(scaleType)
	}
	return false
}
