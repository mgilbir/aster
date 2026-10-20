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

// The scale type predicates take the type as the specification gives it:
// upstream's are Set.has, ===, a switch or contains (indexOf), which only the
// string matches, so ["log"] is no log scale there (C40 of the 2026-10-18
// audit). Only scaleCompatible and scaleTypePrecedence read a table with it
// (SCALE_CATEGORY_INDEX[type], SCALE_PRECEDENCE_INDEX[type]) and so take its
// text, as a property key.

// scaleCompatible compares SCALE_CATEGORY_INDEX[a] and [b]: a type every
// object inherits reads as that member, equal only to itself.
func scaleCompatible(a, b Value) bool {
	cat := func(t Value) string {
		k, ok := t.PropertyKey(maxNameKey)
		if !ok {
			return ""
		}
		if c, ok := scaleCategory[k]; ok {
			return c
		}
		if isInheritedKey(k) {
			return "\x00" + k
		}
		return ""
	}
	c1, c2 := cat(a), cat(b)
	return c1 == c2 || (c1 == "ordinal-position" && c2 == "time") || (c2 == "ordinal-position" && c1 == "time")
}

// scaleTypePrecedence is SCALE_PRECEDENCE_INDEX[t], 0 for a type it does not
// hold (whose undefined makes upstream's difference NaN, which its tie-breaker
// reads as 0 does).
func scaleTypePrecedence(t Value) int {
	k, _ := t.PropertyKey(maxNameKey)
	switch k {
	case "log", "pow", "sqrt", "symlog", "identity", "sequential":
		return 1
	case "point":
		return 10
	case "band":
		return 11
	}
	return 0
}

// maxNameKey bounds the text of a value read as the key of one of
// upstream's name tables: no name is longer, so a longer key names nothing.
const maxNameKey = 64

func isQuantitativeScale(t Value) bool {
	switch s, _ := t.Strict(); s {
	case "linear", "log", "pow", "sqrt", "symlog":
		return true
	}
	return false
}

func isContinuousToContinuous(t Value) bool {
	return isQuantitativeScale(t) || strIs(t, "time") || strIs(t, "utc")
}

func isContinuousToDiscrete(t Value) bool {
	return strIs(t, "quantile") || strIs(t, "quantize") || strIs(t, "threshold")
}

func hasContinuousDomain(t Value) bool {
	return isContinuousToContinuous(t) || isContinuousToDiscrete(t) || strIs(t, "sequential") || strIs(t, "identity")
}

func hasDiscreteDomain(t Value) bool {
	switch s, _ := t.Strict(); s {
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

// isParameterDomain is hasProperty(domain, 'param') in 6.4; 5.8 reads
// domain?.['param'], so a falsy param is no parameter domain.
func isParameterDomain(cc *compileCtx, v Value) bool {
	if cc.v5 {
		return !v.IsNullish() && jsProp(v, "param").IsTruthy()
	}
	return hasProperty(v, "param")
}

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

func scaleTypeSupportProperty(scaleType Value, prop string) bool {
	is := func(names ...string) bool {
		for _, n := range names {
			if strIs(scaleType, n) {
				return true
			}
		}
		return false
	}
	switch prop {
	case "type", "domain", "reverse", "range":
		return true
	case "scheme", "interpolate":
		return !is("point", "band", "identity")
	case "bins":
		return !is("point", "band", "identity", "ordinal")
	case "round":
		return isContinuousToContinuous(scaleType) || is("band", "point")
	case "padding", "rangeMin", "rangeMax":
		return isContinuousToContinuous(scaleType) || is("point", "band")
	case "paddingOuter", "align":
		return is("point", "band")
	case "paddingInner":
		return is("band")
	case "domainMax", "domainMid", "domainMin", "domainRaw", "clamp":
		return isContinuousToContinuous(scaleType)
	case "nice":
		return isContinuousToContinuous(scaleType) || is("quantize", "threshold")
	case "exponent":
		return is("pow")
	case "base":
		return is("log")
	case "constant":
		return is("symlog")
	case "zero":
		return hasContinuousDomain(scaleType) && !is("log", "time", "utc", "threshold", "quantile")
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

// scaleTypeSupportDataType: specified is undefined when the
// specification gives no type.
func scaleTypeSupportDataType(specified Value, fieldDefType string) bool {
	switch fieldDefType {
	case "ordinal", "nominal":
		return specified.IsUndefined() || hasDiscreteDomain(specified)
	case "temporal":
		return strIs(specified, "time") || strIs(specified, "utc") || specified.IsUndefined()
	case "quantitative":
		return isQuantitativeScale(specified) || isContinuousToDiscrete(specified) || specified.IsUndefined()
	}
	return true
}

func channelSupportScaleType(cc *compileCtx, channel string, scaleType Value, hasNestedOffsetScale bool) bool {
	if !isScaleChannel(cc, channel) {
		return false
	}
	switch channel {
	case chX, chY, chXOffset, chYOffset, chTheta, chRadius:
		if isContinuousToContinuous(scaleType) {
			return true
		} else if strIs(scaleType, "band") {
			return true
		} else if strIs(scaleType, "point") {
			return !hasNestedOffsetScale
		}
		return false
	case chTime:
		return strIs(scaleType, "linear") || strIs(scaleType, "band")
	case chSize, chStrokeWidth, chOpacity, chFillOpacity, chStrokeOpacity, chAngle:
		return isContinuousToContinuous(scaleType) || isContinuousToDiscrete(scaleType) ||
			strIs(scaleType, "band") || strIs(scaleType, "point") || strIs(scaleType, "ordinal")
	case chColor, chFill, chStroke:
		return !strIs(scaleType, "band")
	case chStrokeDash, chShape:
		return strIs(scaleType, "ordinal") || isContinuousToDiscrete(scaleType)
	}
	return false
}
