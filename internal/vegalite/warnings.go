package vegalite

import "strings"

// The warnings upstream's compiler logs (src/log/message.ts), with its text.
// A value interpolated into a template literal is String(value) (tmpl); one
// passed through upstream's stringify is stable JSON (stringifyJS). Each is
// logged with cc.warn where upstream calls log.warn; see warnings_test.go and
// TestVegaLiteWarningsMatchNode.

// tmpl is `${v}`: String(v).
func tmpl(v Value) string { return v.AsString() }

const (
	msgFitNonSingle                    = `Autosize "fit" only works for single views and layered views.`
	msgScaleBindingsContinuous         = "Scale bindings are currently only supported for scales with unbinned, continuous domains."
	msgSequentialScaleDeprecated       = "Sequntial scales are deprecated. The available quantitative scale type values are linear, log, pow, sqrt, symlog, time and utc"
	msgLegendBindingsMustHaveProj      = "Legend bindings are only supported for selections over an individual field or encoding channel."
	msgNeedsSameSelection              = "The same selection must be used to override scale domains in a layered view."
	msgIntervalInitializedWithPos      = `Interval selections should be initialized using "x", "y", "longitude", or "latitude" keys.`
	msgMultipleTimerAnimationSelection = "Multiple timer selections in one unit spec are not supported. Ignoring all but the first."
	msgMultiViewAnimationUnsupported   = "Animation involving facet, layer, or concat is currently unsupported."
	msgConcatCannotShareAxis           = "Axes cannot be shared in concatenated or repeated views yet (https://github.com/vega/vega-lite/issues/2415)."
	msgAddSameChildTwice               = "Attempt to add the same child twice."
	msgNoFieldsNeedsAs                 = `If "from.fields" is not specified, "as" has to be a string that specifies the key to be used for the data from the secondary source.`
	msgReplaceAngleWithTheta           = "Arc marks uses theta channel rather than angle, replacing angle with theta."
	msgLineWithVaryingSize             = "Line marks cannot encode size with a non-groupby field. You may want to use trail marks instead."
	msgMoreThanOneSort                 = "Domains that should be unioned has conflicting sort properties. Sort will be set to true."
	msgFacetedIndependentDiffSources   = "Detected faceted independent scales that union domain of multiple fields from different data sources. We will use the first field. The result view size may be incorrect."
	msgFacetedIndependentSameFields    = "Detected faceted independent scales that union domain of the same fields from different source. We will assume that this is the same field from a different fork of the same data source. However, if this is not the case, the result view size may be incorrect."
	msgFacetedIndependentSameSource    = "Detected faceted independent scales that union domain of multiple fields from the same data source. We will use the first field. The result view size may be incorrect."
)

func msgContainerSizeNonSingle(name string) string {
	return sizeName(name) + ` "container" only works for single views and layered views.`
}

func msgContainerSizeNotCompatibleWithAutosize(name string) string {
	dir := "y"
	if name == "width" {
		dir = "x"
	}
	return sizeName(name) + ` "container" only works well with autosize "fit" or "fit-` + dir + `".`
}

func sizeName(name string) string {
	if name == "width" {
		return "Width"
	}
	return "Height"
}

func msgDroppingFit(channel string) string {
	if channel == "" {
		return `Dropping "fit" because spec has discrete size.`
	}
	size := "height"
	if channel == chX {
		size = "width"
	}
	return `Dropping "fit-` + channel + `" because spec has discrete ` + size + "."
}

func msgUnknownField(channel string) string {
	return "Unknown field for " + channel + ". Cannot calculate view size."
}

func msgCannotProjectOnChannelWithoutField(channel string) string {
	return `Cannot project a selection on encoding channel "` + channel + `", which has no field.`
}

func msgCannotProjectAggregate(channel string, aggregate Value) string {
	return `Cannot project a selection on encoding channel "` + channel + `" as it uses an aggregate function ("` + tmpl(aggregate) + `").`
}

func msgNearestNotSupportForContinuous(mark string) string {
	return `The "nearest" transform is not supported for ` + mark + " marks."
}

func msgSelectionNotSupported(mark string) string {
	return "Selection not supported for " + mark + " yet."
}

func msgNoSuchRepeatedValue(field Value) string {
	return `Unknown repeated value "` + tmpl(field) + `".`
}

func msgColumnsNotSupportByRowCol(typ string) string {
	return `The "columns" property cannot be used when "` + typ + `" has nested row/column.`
}

func msgSelectionAsScaleDomainWithoutField(cc *compileCtx, field string) string {
	return `A "field" or "encoding" must be specified when using a selection as a scale domain. ` +
		`Using "field": ` + jsonString(cc, field) + "."
}

func msgSelectionAsScaleDomainWrongEncodings(cc *compileCtx, n int, encoding, param Value, field string) string {
	which := "Multiple "
	if n == 0 {
		which = "No "
	}
	return which + "matching " + stringValue(cc, encoding) + " encoding found for selection " + stringValue(cc, param) + ". " +
		`Using "field": ` + jsonString(cc, field) + "."
}

func msgUnrecognizedParse(p string) string { return `Unrecognized parse "` + p + `".` }

func msgDifferentParse(field string, local, ancestor Value) string {
	return `An ancestor parsed field "` + field + `" as ` + tmpl(ancestor) + " but a child wants to parse the field as " + tmpl(local) + "."
}

func msgInvalidTransformIgnored(cc *compileCtx, t Value) string {
	return "Ignoring an invalid transform: " + stringifyJSCC(cc, t) + "."
}

func msgCustomFormatTypeNotAllowed(channel string) string {
	return "Config.customFormatTypes is not true, thus custom format type and format for channel " + channel + " are dropped."
}

func msgProjectionOverridden(cc *compileCtx, parent, projection Value) string {
	return "Layer's shared projection " + stringifyJSCC(cc, parent) + " is overridden by a child projection " + stringifyJSCC(cc, projection) + "."
}

func msgOffsetNestedInsideContinuousPositionScaleDropped(main string) string {
	return main + "Offset dropped because " + main + " is continuous"
}

func msgReplaceOffsetWithMainChannel(main string) string {
	return "There is no " + main + " encoding. Replacing " + main + "Offset encoding as " + main + "."
}

func msgPrimitiveChannelDef(cc *compileCtx, channel, typ string, value Value) string {
	return "Channel " + channel + " is a " + typ + ". Converted to {value: " + stringifyJSCC(cc, value) + "}."
}

func msgInvalidFieldTypeForCountAggregate(typ, aggregate Value) string {
	return `Invalid field type "` + tmpl(typ) + `" for aggregate: "` + tmpl(aggregate) + `", using "quantitative" instead.`
}

func msgInvalidAggregate(aggregate Value) string {
	return `Invalid aggregation operator "` + tmpl(aggregate) + `".`
}

func msgDroppingColor(typ string, fill, stroke bool) string {
	what := "stroke"
	switch {
	case fill && stroke:
		what = "fill and stroke"
	case fill:
		what = "fill"
	}
	return "Dropping color " + typ + " as the plot also has " + what + "."
}

func msgRelativeBandSizeNotSupported(size string) string {
	return "Position range does not support relative band size for " + size + "."
}

func msgEmptyFieldDef(cc *compileCtx, fieldDef Value, channel string) string {
	return "Dropping " + stringifyJSCC(cc, fieldDef) + ` from channel "` + channel + `" since it does not contain any data field, datum, value, or signal.`
}

func msgIncompatibleChannel(channel, markOrFacet, when string) string {
	s := channel + ` dropped as it is incompatible with "` + markOrFacet + `"`
	if when != "" {
		s += " when " + when
	}
	return s + "."
}

func msgOffsetEncodingScaleIgnored(channel string) string {
	return channel + " encoding has no scale, so specified scale is ignored."
}

func msgInvalidEncodingChannel(channel string) string {
	return channel + "-encoding is dropped as " + channel + " is not a valid encoding channel."
}

func msgChannelShouldBeDiscrete(channel string) string {
	return channel + " encoding should be discrete (ordinal / nominal / binned)."
}

func msgChannelShouldBeDiscreteOrDiscretizing(channel string) string {
	return channel + " encoding should be discrete (ordinal / nominal / binned) or use a discretizing scale (e.g. threshold)."
}

func msgFacetChannelDropped(channels []string) string {
	verb := "is"
	if len(channels) > 1 {
		verb = "are"
	}
	return "Facet encoding dropped as " + strings.Join(channels, " and ") + " " + verb + " also specified."
}

func msgDiscreteChannelCannotEncode(channel, typ string) string {
	what := "magnitude"
	if typ == "ordinal" {
		what = "order"
	}
	return `Using discrete channel "` + channel + `" to encode "` + typ + `" field can be misleading as it does not encode ` + what + "."
}

func msgRangeMarkAlignmentCannotBeExpression(align string) string {
	return "The " + align + " for range marks cannot be an expression"
}

func msgLineWithRange(hasX2, hasY2 bool) string {
	channels := "y2"
	switch {
	case hasX2 && hasY2:
		channels = "x2 and y2"
	case hasX2:
		channels = "x2"
	}
	return "Line mark is for continuous lines and thus cannot be used with " + channels + ". We will use the rule mark (line segments) instead."
}

func msgOrientOverridden(original, actual Value) string {
	return `Specified orient "` + tmpl(original) + `" overridden with "` + tmpl(actual) + `".`
}

func msgCannotUseScalePropertyWithNonColor(prop string) string {
	return `Cannot use the scale property "` + prop + `" with non-color channel.`
}

func msgCannotUseRelativeBandSizeWithNonBandScale(scaleType Value) string {
	return "Cannot use the relative band size with " + tmpl(scaleType) + " scale."
}

func msgUnaggregateDomainHasNoEffectForRawField(cc *compileCtx, fd Value) string {
	return "Using unaggregated domain with raw field has no effect (" + stringifyJSCC(cc, fd) + ")."
}

func msgUnaggregateDomainWithNonSharedDomainOp(aggregate Value) string {
	return `Unaggregated domain not applicable for "` + tmpl(aggregate) + `" since it produces values outside the origin domain of the source data.`
}

func msgUnaggregatedDomainWithLogScale(cc *compileCtx, fd Value) string {
	return "Unaggregated domain is currently unsupported for log scale (" + stringifyJSCC(cc, fd) + ")."
}

func msgCannotApplySizeToNonOrientedMark(mark Value) string {
	return `Cannot apply size to non-oriented mark "` + tmpl(mark) + `".`
}

func msgScaleTypeNotWorkWithChannel(channel string, scaleType Value, def Value) string {
	return `Channel "` + channel + `" does not work with "` + tmpl(scaleType) + `" scale. We are using "` + tmpl(def) + `" scale instead.`
}

func msgScaleTypeNotWorkWithFieldDef(scaleType Value, def Value) string {
	return `FieldDef does not work with "` + tmpl(scaleType) + `" scale. We are using "` + tmpl(def) + `" scale instead.`
}

func msgScalePropertyNotWorkWithScaleType(scaleType Value, prop, channel string) string {
	return channel + `-scale's "` + prop + `" is dropped as it does not work with ` + tmpl(scaleType) + " scale."
}

func msgStepDropped(channel string) string {
	axis := "y"
	if channel == "width" {
		axis = "x"
	}
	return `The step for "` + channel + `" is dropped because the ` + axis + " is continuous."
}

func msgMergeConflictingProperty(cc *compileCtx, property, propertyOf string, v1, v2 Value) string {
	a := stringifyJSCC(cc, v1)
	return "Conflicting " + propertyOf + ` property "` + property + `" (` + a + " and " + stringifyJSCC(cc, v2) + "). Using " + a + "."
}

func msgMergeConflictingDomainProperty(cc *compileCtx, property, propertyOf string, v1, v2 Value) string {
	return "Conflicting " + propertyOf + ` property "` + property + `" (` + stringifyJSCC(cc, v1) + " and " + stringifyJSCC(cc, v2) + "). Using the union of the two domains."
}

func msgIndependentScaleMeansIndependentGuide(channel string) string {
	return `Setting the scale to be independent for "` + channel + `" means we also have to set the guide (axis or legend) to be independent.`
}

func msgDomainSortDropped(cc *compileCtx, sort Value) string {
	return "Dropping sort property " + stringifyJSCC(cc, sort) + ` as unioned domains only support boolean or op "count", "min", and "max".`
}

func msgLegendValuesUnioned(a, b string) string {
	return "Unioning discrete legend values from " + a + " and " + b + "."
}

func msgCannotStackRangedMark(channel string) string {
	return `Cannot stack "` + channel + `" if there is already "` + channel + `2".`
}

func msgStackNonLinearScale(v5 bool, scaleType Value) string {
	if v5 {
		return "Cannot stack non-linear scale (" + tmpl(scaleType) + ")."
	}
	return "Stack is applied to a non-linear scale (" + tmpl(scaleType) + ")."
}

func msgStackNonSummativeAggregate(aggregate Value) string {
	return `Stacking is applied even though the aggregate function is non-summative ("` + tmpl(aggregate) + `").`
}

func msgInvalidTimeUnit(cc *compileCtx, unitName string, value Value) string {
	return "Invalid " + unitName + ": " + stringifyJSCC(cc, value) + "."
}

func msgDroppedDay(cc *compileCtx, d Value) string {
	return "Dropping day from datetime " + stringifyJSCC(cc, d) + " as day cannot be combined with other units."
}

func msgErrorBarCenterAndExtentAreNotNeeded(center, extent Value) string {
	var b strings.Builder
	e, c := extent.IsTruthy(), center.IsTruthy()
	if e {
		b.WriteString("extent ")
	}
	if e && c {
		b.WriteString("and ")
	}
	if c {
		b.WriteString("center ")
	}
	if e && c {
		b.WriteString("are ")
	} else {
		b.WriteString("is ")
	}
	b.WriteString("not needed when data are aggregated.")
	return b.String()
}

func msgErrorBarCenterIsUsedWithWrongExtent(center, extent Value, mark string) string {
	return tmpl(center) + " is not usually used with " + tmpl(extent) + " for " + mark + "."
}

func msgErrorBarContinuousAxisHasCustomizedAggregate(aggregate Value, compositeMark string) string {
	return "Continuous axis should not have customized aggregation function " + tmpl(aggregate) + "; " + compositeMark + " already agregates the axis."
}

func msgErrorBand1DNotSupport(property string) string {
	return "1D error band does not support " + property + "."
}

func msgChannelRequiredForBinned(channel string) string {
	return "Channel " + channel + ` is required for "binned" bin.`
}

func msgChannelShouldNotBeUsedForBinned(channel string) string {
	return "Channel " + channel + ` should not be used with "binned" bin.`
}

func msgDomainRequiredForThresholdScale(channel string) string {
	return "Domain for " + channel + " is required for threshold scale."
}

// stringifyJSCC is upstream's stringify as a template literal renders it
// (undefined prints as "undefined").
func stringifyJSCC(cc *compileCtx, v Value) string { return stringifyText(cc, v) }

func msgInvalidSpec(cc *compileCtx, spec Value) string {
	return "Invalid specification " + stringifyText(cc, spec) + `. Make sure the specification includes at least one of the following properties: "mark", "layer", "facet", "hconcat", "vconcat", "concat", or "repeat".`
}
