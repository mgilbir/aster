package vegalite

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Scale domains — vega-lite/src/compile/scale/domain.ts.

func parseScaleDomain(m Model) {
	if u := asUnit(m); u != nil {
		parseUnitScaleDomain(u)
	} else {
		parseNonUnitScaleDomain(m)
	}
}

func parseUnitScaleDomain(u *unitModel) {
	cc := u.ctx
	local := u.comp.scales
	for _, channel := range local.keyList() {
		domains := parseDomainForChannel(u, channel)
		cmpt, _ := local.get(channel)
		cmpt.setWithExplicit("domains", domains)
		parseSelectionDomain(u, channel)
		if u.comp.data.isFaceted {
			var facetParent Model = u
			for !isFacetModel(facetParent) && facetParent.b().parent != nil {
				facetParent = facetParent.b().parent
			}
			if facetParent.b().comp.resolve.scale[channel] == "shared" {
				for _, d := range domains.value.Items() {
					cc.work(1)
					if isDataRefDomain(cc, d) {
						data := d.Get("data").AsString()
						d.ObjValue().Set("data", jsval.Str(facetScalePrefix+strings.Replace(data, facetScalePrefix, "", 1)))
					}
				}
			}
		}
	}
}

func parseNonUnitScaleDomain(m Model) {
	cc := m.b().ctx
	for _, c := range m.children() {
		cc.work(modelStep)
		parseScaleDomain(c)
	}
	local := m.b().comp.scales
	for _, channel := range local.keyList() {
		var domains *withExplicit
		var selectionExtent Value
		for _, c := range m.children() {
			cc.work(modelStep)
			cs, ok := c.b().comp.scales.get(channel)
			if !ok || cs == nil {
				continue
			}
			cd := cs.getWithExplicit("domains")
			if domains == nil {
				d := cd
				domains = &d
			} else {
				mv := mergeValuesWithExplicit(cc, domains, cd, "domains", "scale", domainsTieBreaker)
				domains = &mv
			}
			se := cs.get("selectionExtent")
			if selectionExtent.IsTruthy() && se.IsTruthy() && !strictEq(selectionExtent.Get("param"), se.Get("param")) {
				cc.warn(msgNeedsSameSelection)
			}
			selectionExtent = se
		}
		lc, _ := local.get(channel)
		if domains != nil {
			lc.setWithExplicit("domains", *domains)
		}
		if selectionExtent.IsTruthy() {
			lc.set("selectionExtent", selectionExtent, true)
		}
	}
}

func domainsTieBreaker(cc *compileCtx, v1, v2 withExplicit, property, propertyOf string) withExplicit {
	if v1.explicit && v2.explicit {
		cc.warn(msgMergeConflictingDomainProperty(cc, property, propertyOf, v1.value, v2.value))
	}
	items := append(append([]Value{}, v1.value.Items()...), v2.value.Items()...)
	return withExplicit{v1.explicit, jsval.Arr(items)}
}

// canUseUnaggregatedDomain is upstream's: whether domain "unaggregated"
// applies, and the warning why not.
func canUseUnaggregatedDomain(cc *compileCtx, fd Value, scaleType string) (bool, string) {
	aggregate := fd.Get("aggregate")
	if !aggregate.IsTruthy() {
		return false, msgUnaggregateDomainHasNoEffectForRawField(cc, fd)
	}
	if aggregate.IsStr() && !sharedDomainOps[aggregate.StrValue()] {
		return false, msgUnaggregateDomainWithNonSharedDomainOp(aggregate)
	}
	if channelDefType(fd) == "quantitative" && scaleType == "log" {
		return false, msgUnaggregatedDomainWithLogScale(cc, fd)
	}
	return true, ""
}

func normalizeUnaggregatedDomain(cc *compileCtx, domain Value, fd Value, scaleType string, scaleConfig Value) Value {
	if domain.IsStr() && domain.StrValue() == "unaggregated" {
		if ok, reason := canUseUnaggregatedDomain(cc, fd, scaleType); !ok {
			cc.warn(reason)
			return undef
		}
	} else if domain.IsUndefined() && jsGet(scaleConfig, "useUnaggregatedDomain").IsTruthy() {
		if ok, _ := canUseUnaggregatedDomain(cc, fd, scaleType); ok {
			return jsval.Str("unaggregated")
		}
	}
	return domain
}

func parseDomainForChannel(u *unitModel, channel string) withExplicit {
	cc := u.b().ctx

	scaleType := u.scaleTypeOf(channel)
	encoding := u.encoding
	domain := normalizeUnaggregatedDomain(cc, u.scaleDomain(channel), u.typedFieldDef(channel), scaleType, u.config.Get("scale"))
	if !jsval.SameRef(domain, u.scaleDomain(channel)) {
		o := cloneObj(coalesceObj(u.specifiedScales.Lookup(channel)).ObjValue())
		o.Set("domain", domain)
		u.specifiedScales.Set(channel, jsval.Obj(o))
	}
	if channel == chX && getFieldOrDatumDef(cc, encoding.Get("x2")).IsTruthy() {
		if getFieldOrDatumDef(cc, encoding.Get("x")).IsTruthy() {
			a := parseSingleChannelDomain(scaleType, domain, u, "x")
			return mergeValuesWithExplicit(cc, &a, parseSingleChannelDomain(scaleType, domain, u, "x2"), "domain", "scale", domainsTieBreaker)
		}
		return parseSingleChannelDomain(scaleType, domain, u, "x2")
	} else if channel == chY && getFieldOrDatumDef(cc, encoding.Get("y2")).IsTruthy() {
		if getFieldOrDatumDef(cc, encoding.Get("y")).IsTruthy() {
			a := parseSingleChannelDomain(scaleType, domain, u, "y")
			return mergeValuesWithExplicit(cc, &a, parseSingleChannelDomain(scaleType, domain, u, "y2"), "domain", "scale", domainsTieBreaker)
		}
		return parseSingleChannelDomain(scaleType, domain, u, "y2")
	}
	return parseSingleChannelDomain(scaleType, domain, u, channel)
}

func mapDomainToDataSignal(cc *compileCtx, domain Value, typ string, timeUnit Value) Value {
	return mapList(domain, "domain", func(v Value) Value {
		data, _ := valueExpr(cc, v, timeUnit, typ, false, false)
		return mkv("signal", "{data: "+data+"}")
	})
}

func convertDomainIfItIsDateTime(cc *compileCtx, domain Value, typ string, timeUnit Value) []Value {
	unit := normalizeTimeUnit(cc, timeUnit).Get("unit")
	if typ == "temporal" || unit.IsTruthy() {
		return mapDomainToDataSignal(cc, domain, typ, unit).Items()
	}
	return []Value{domain}
}

func parseSingleChannelDomain(scaleType string, domain Value, u *unitModel, channel string) withExplicit {
	cc := u.b().ctx

	encoding, markDef, mark, config, stack := u.encoding, u.markDef, u.mark(), u.config, u.stack
	fod := getFieldOrDatumDef(cc, encoding.Get(channel))
	typ := channelDefType(fod)
	timeUnit := fod.Get("timeUnit")
	dsType := dsMain
	if !cc.v5 {
		dsType = getScaleDataSourceForHandlingInvalidValues(getMarkConfig("invalid", markDef, config, ""), isPathMarkName(mark))
	}
	switch {
	case isDomainUnionWith(cc, domain):
		def := parseSingleChannelDomain(scaleType, undef, u, channel)
		union := convertDomainIfItIsDateTime(cc, domain.Get("unionWith"), typ, timeUnit)
		return makeExplicit(jsval.Arr(append(append([]Value{}, union...), def.value.Items()...)))
	case isSignalRef(domain):
		return makeExplicit(arr(domain))
	case domain.IsTruthy() && !(domain.IsStr() && domain.StrValue() == "unaggregated") && !isParameterDomain(domain):
		return makeExplicit(jsval.Arr(convertDomainIfItIsDateTime(cc, domain, typ, timeUnit)))
	}
	if stack != nil && channel == stack.fieldChannel {
		if stack.offset == "normalize" {
			return makeImplicit(arr(arr(0, 1)))
		}
		data := u.requestDataName(dsType)
		return makeImplicit(arr(
			mkv("data", data, "field", u.vgField(channel, fieldRefOption{suffix: "start"})),
			mkv("data", data, "field", u.vgField(channel, fieldRefOption{suffix: "end"})),
		))
	}
	var sort Value
	if isScaleChannel(cc, channel) && isFieldDef(cc, fod) {
		sort = domainSort(u, channel, scaleType)
	}
	if isDatumDefOf(cc, fod) {
		return makeImplicit(jsval.Arr(convertDomainIfItIsDateTime(cc, arr(fod.Get("datum")), typ, timeUnit)))
	}
	fd := fod
	switch {
	case domain.IsStr() && domain.StrValue() == "unaggregated":
		field := fod.Get("field")
		return makeImplicit(arr(
			mkv("data", u.requestDataName(dsType), "field", vgField(cc, mkv("field", field, "aggregate", "min"), fieldRefOption{})),
			mkv("data", u.requestDataName(dsType), "field", vgField(cc, mkv("field", field, "aggregate", "max"), fieldRefOption{})),
		))
	case isBinning(fd.Get("bin")):
		if hasDiscreteDomain(scaleType) {
			if scaleType == "bin-ordinal" {
				return makeImplicit(jsval.Arr(nil))
			}
			var data string
			if sort.IsBool() {
				data = u.requestDataName(dsType)
			} else {
				data = u.requestDataName(dsRaw)
			}
			opt := fieldRefOption{}
			if binRequiresRange(cc, fd, channel) {
				opt.binSuffix = "range"
			}
			var sortV Value
			if (sort.IsBool() && sort.BoolValue()) || !isObject(sort) {
				sortV = mkv("field", u.vgField(channel, fieldRefOption{}), "op", "min")
			} else {
				sortV = sort
			}
			return makeImplicit(arr(mkv("data", data, "field", u.vgField(channel, opt), "sort", sortV)))
		}
		bin := fd.Get("bin")
		if isBinning(bin) {
			binSignal := getBinSignalName(u, fd.Get("field").AsString(), bin)
			return makeImplicit(arr(lazySignal(u.ctx, func() string {
				s := u.getSignalName(binSignal)
				return "[" + s + ".start, " + s + ".stop]"
			})))
		}
		return makeImplicit(arr(mkv("data", u.requestDataName(dsType), "field", u.vgField(channel, fieldRefOption{}))))
	case fd.Get("timeUnit").IsTruthy() && (scaleType == "time" || scaleType == "utc"):
		fd2 := encoding.Get(getSecondaryRangeChannel(channel))
		if hasBandEnd(cc, fd, fd2, markDef, config) {
			data := u.requestDataName(dsType)
			bp := getBandPosition(cc, fd, fd2, markDef, config)
			isRectWithOffset := !cc.v5 && isRectBasedMark(cc, mark) && !(bp.IsNum() && bp.NumValue() == 0.5) && isXorY(channel)
			startOpt := fieldRefOption{}
			endSuffix := "end"
			if isRectWithOffset {
				startOpt.suffix = offsettedRectStartSuffix
				endSuffix = offsettedRectEndSuffix
			}
			return makeImplicit(arr(
				mkv("data", data, "field", u.vgField(channel, startOpt)),
				mkv("data", data, "field", u.vgField(channel, fieldRefOption{suffix: endSuffix})),
			))
		}
	}
	if sort.IsTruthy() {
		var data string
		if sort.IsBool() {
			data = u.requestDataName(dsType)
		} else {
			data = u.requestDataName(dsRaw)
		}
		return makeImplicit(arr(mkv("data", data, "field", u.vgField(channel, fieldRefOption{}), "sort", sort)))
	}
	return makeImplicit(arr(mkv("data", u.requestDataName(dsType), "field", u.vgField(channel, fieldRefOption{}))))
}

func normalizeSortField(sort Value, isStackedMeasure bool) Value {
	op, field, order := sort.Get("op"), sort.Get("field"), sort.Get("order")
	o := jsval.NewObject(3)
	if !op.IsNullish() {
		o.Set("op", op)
	} else if isStackedMeasure {
		o.Set("op", jsval.Str("sum"))
	} else {
		o.Set("op", jsval.Str(defaultSortOp))
	}
	if field.IsTruthy() {
		o.Set("field", jsval.Str(replacePathInFieldV(field))) // the field as given
	}
	if order.IsTruthy() {
		o.Set("order", order)
	}
	return jsval.Obj(o)
}

func parseSelectionDomain(u *unitModel, channel string) {
	scale, _ := u.comp.scales.get(channel)
	spec := u.specifiedScales.Lookup(channel).Get("domain")
	bin := u.fieldDef(channel).Get("bin")
	var domain, extent Value
	if isParameterDomain(spec) {
		domain = spec
	}
	if isObject(bin) && isParameterExtent(bin.Get("extent")) {
		extent = bin.Get("extent")
	}
	if domain.IsTruthy() || extent.IsTruthy() {
		scale.set("selectionExtent", coalesceTruthy(domain, extent), true)
	}
}

// coalesceTruthy is `a ?? b` for the (object or undefined) domain and extent.
func coalesceTruthy(a, b Value) Value {
	if !a.IsNullish() {
		return a
	}
	return b
}

// domainSort returns the sort of a discrete scale's domain: a bool, a sort object, or undefined.
func domainSort(u *unitModel, channel, scaleType string) Value {
	cc := u.b().ctx

	if !hasDiscreteDomain(scaleType) {
		return undef
	}
	fd := u.fieldDef(channel)
	sort := fd.Get("sort")
	if isSortArray(sort) {
		return mkv("op", "min", "field", sortArrayIndexField(cc, fd, channel, fieldRefOption{}), "order", "ascending")
	}
	stack := u.stack
	var stackDimensions *sset
	if stack != nil {
		stackDimensions = stack.groupbyFields.clone()
		for _, s := range stack.stackBy {
			cc.work(1)
			stackDimensions.add(s.fieldDef.Get("field").AsString())
		}
	}
	switch {
	case isSortField(cc, sort):
		isStacked := stack != nil && !stackDimensions.has(sort.Get("field").AsString())
		return normalizeSortField(sort, isStacked)
	case isSortByEncoding(sort):
		encoding, order := sort.Get("encoding"), sort.Get("order")
		fieldDefToSortBy := u.fieldDef(encoding.AsString())
		if fieldDefToSortBy.IsNullish() {
			// const {aggregate, field} = fieldDefToSortBy
			throw("Cannot destructure property 'aggregate' of 'fieldDefToSortBy' as it is %s.", nullishName(fieldDefToSortBy))
		}
		aggregate, field := fieldDefToSortBy.Get("aggregate"), fieldDefToSortBy.Get("field")
		isStacked := stack != nil && !stackDimensions.has(field.AsString())
		if isArgminDef(aggregate) || isArgmaxDef(aggregate) {
			return normalizeSortField(mkv("field", vgField(cc, fieldDefToSortBy, fieldRefOption{}), "order", order), isStacked)
		} else if isAggregateOp(cc, aggregate) || !aggregate.IsTruthy() {
			return normalizeSortField(mkv("op", aggregate, "field", field, "order", order), isStacked)
		}
	case sort.IsStr() && sort.StrValue() == "descending":
		return mkv("op", "min", "field", u.vgField(channel, fieldRefOption{}), "order", "descending")
	case sort.IsUndefined() || (sort.IsStr() && sort.StrValue() == "ascending"):
		return jsval.True
	}
	return undef
}

func mergeDomains(cc *compileCtx, domains []Value) Value {
	var uniqueDomains []Value
	seen := map[string]bool{}
	for _, d := range domains {
		cc.work(1)
		x := d
		if isDataRefDomain(cc, d) {
			x = jsval.Obj(omit(d, "sort"))
		}
		h := hashOf(cc, x)
		if !jsSeen(cc, seen, h) {
			seen[h] = true
			uniqueDomains = append(uniqueDomains, x)
		}
	}
	var sorts []Value
	seenS := map[string]bool{}
	for _, d := range domains {
		cc.work(1)
		if !isDataRefDomain(cc, d) {
			continue
		}
		s := d.Get("sort")
		if s.IsUndefined() {
			continue
		}
		if !s.IsBool() {
			if s.IsObj() && s.ObjValue().Has("op") && s.Get("op").IsStr() && s.Get("op").StrValue() == "count" {
				s.ObjValue().Delete("field")
			}
			if s.IsObj() && s.Get("order").IsStr() && s.Get("order").StrValue() == "ascending" {
				s.ObjValue().Delete("order")
			}
		}
		h := hashOf(cc, s)
		if !jsSeen(cc, seenS, h) {
			seenS[h] = true
			sorts = append(sorts, s)
		}
	}
	if len(uniqueDomains) == 0 {
		return undef
	} else if len(uniqueDomains) == 1 {
		domain := domains[0]
		if isDataRefDomain(cc, domain) && len(sorts) > 0 {
			sort := sorts[0]
			if len(sorts) > 1 {
				cc.warn(msgMoreThanOneSort)
				var filtered []Value
				allOp := true
				for _, s := range sorts {
					cc.work(1)
					if !(s.IsObj() && s.ObjValue().Has("op")) {
						allOp = false
					}
					if s.IsObj() && s.ObjValue().Has("op") && !(s.Get("op").IsStr() && s.Get("op").StrValue() == "min") {
						filtered = append(filtered, s)
					}
				}
				if allOp && len(filtered) == 1 {
					sort = filtered[0]
				} else {
					sort = jsval.True
				}
			} else if sort.IsObj() && sort.ObjValue().Has("field") {
				if strictEq(domain.Get("field"), sort.Get("field")) {
					if sort.Get("order").IsTruthy() {
						sort = mkv("order", sort.Get("order"))
					} else {
						sort = jsval.True
					}
				}
			}
			o := cloneObj(domain.ObjValue())
			o.Set("sort", sort)
			return jsval.Obj(o)
		}
		return domain
	}
	var unionSorts []Value
	seenU := map[string]bool{}
	for _, s := range sorts {
		cc.work(1)
		var x Value
		if s.IsBool() || !(s.IsObj() && s.ObjValue().Has("op")) || (s.Get("op").IsStr() && (multiDomainSortOps[s.Get("op").StrValue()] || (cc.v5 && isInheritedKey(s.Get("op").StrValue())))) {
			x = s
		} else {
			cc.warn(msgDomainSortDropped(cc, s))
			x = jsval.True
		}
		h := hashOf(cc, x)
		if !jsSeen(cc, seenU, h) {
			seenU[h] = true
			unionSorts = append(unionSorts, x)
		}
	}
	var sort Value
	if len(unionSorts) == 1 {
		sort = unionSorts[0]
	} else if len(unionSorts) > 1 {
		cc.warn(msgMoreThanOneSort)
		sort = jsval.True
	}
	var allData []Value
	seenD := map[string]bool{}
	for _, d := range domains {
		cc.work(1)
		v := jsval.Null
		if isDataRefDomain(cc, d) {
			v = d.Get("data")
		}
		// unique(…, (x) => x): keyed by the value's text, so null and a
		// data set named "null" are one, as two object names are.
		h := v.AsString()
		if !jsSeen(cc, seenD, h) {
			seenD[h] = true
			allData = append(allData, v)
		}
	}
	if len(allData) == 1 && !allData[0].IsNull() {
		var fields []Value
		for _, d := range uniqueDomains {
			cc.work(1)
			fields = append(fields, d.Get("field"))
		}
		o := mk("data", allData[0], "fields", jsval.Arr(fields))
		if sort.IsTruthy() {
			o.Set("sort", sort)
		}
		return jsval.Obj(o)
	}
	o := mk("fields", jsval.Arr(uniqueDomains))
	if sort.IsTruthy() {
		o.Set("sort", sort)
	}
	return jsval.Obj(o)
}

func getFieldFromDomain(cc *compileCtx, domain Value) string {
	switch {
	case isDataRefDomain(cc, domain) && domain.Get("field").IsStr():
		return domain.Get("field").StrValue()
	case isDataRefUnionedDomain(domain):
		field := ""
		for _, nu := range domain.Get("fields").Items() {
			cc.work(1)
			if isDataRefDomain(cc, nu) && nu.Get("field").IsStr() {
				if field == "" {
					field = nu.Get("field").StrValue()
				} else if field != nu.Get("field").StrValue() {
					cc.warn(msgFacetedIndependentDiffSources)
					return field
				}
			}
		}
		cc.warn(msgFacetedIndependentSameFields)
		return field
	case isFieldRefUnionDomain(domain):
		cc.warn(msgFacetedIndependentSameSource)
		if f := domain.Get("fields").Index(0); f.IsStr() {
			return f.StrValue()
		}
	}
	return ""
}

func assembleDomain(m Model, channel string) Value {
	cc := m.b().ctx
	sc, _ := m.b().comp.scales.get(channel)
	var domains []Value
	for _, d := range sc.get("domains").Items() {
		cc.work(1)
		if cc.v5 && !d.IsArr() {
			jsIn(d, "field") // 5.8's isDataRefDomain
		}
		if isDataRefDomain(cc, d) {
			d.ObjValue().Set("data", m.b().lookupDataSource(d.Get("data").AsString()))
		}
		domains = append(domains, d)
	}
	return mergeDomains(m.b().ctx, domains)
}

var _ = math.NaN
