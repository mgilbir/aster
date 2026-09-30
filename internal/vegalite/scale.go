package vegalite

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// Scale components: vega-lite/src/compile/scale/{component,parse,type,properties,assemble}.ts.

type scaleComponent struct {
	*split
	merged bool
}

func newScaleComponent(name string, typeWithExplicit withExplicit) *scaleComponent {
	s := &scaleComponent{split: &split{jsval.NewObject(4), mk("name", name)}}
	s.setWithExplicit("type", typeWithExplicit)
	return s
}

// domainHasZero: "definitely", "definitely-not" or "maybe".
func (s *scaleComponent) domainHasZero() string {
	scaleType := s.get("type").AsString()
	if scaleType == "log" || scaleType == "time" || scaleType == "utc" {
		return "definitely-not"
	}
	zero := s.get("zero")
	if (zero.IsBool() && zero.BoolValue()) || (zero.IsUndefined() && (scaleType == "linear" || scaleType == "sqrt" || scaleType == "pow")) {
		return "definitely"
	}
	domains := s.get("domains")
	if domains.Len() > 0 {
		withZero, withoutZero, fieldBased := false, false, false
		for _, d := range domains.Items() {
			if d.IsArr() {
				first, last := d.Index(0), d.Index(d.Len()-1)
				if first.IsNum() && last.IsNum() {
					if first.NumValue() <= 0 && last.NumValue() >= 0 {
						withZero = true
					} else {
						withoutZero = true
					}
					continue
				}
			}
			fieldBased = true
		}
		if withZero {
			return "definitely"
		} else if withoutZero && !fieldBased {
			return "definitely-not"
		}
	}
	return "maybe"
}

func defaultScaleResolve(channel string, m Model) string {
	switch m.(type) {
	case *facetModel:
		if channel == chTheta {
			return "independent"
		}
		return "shared"
	case *layerModel:
		return "shared"
	case *concatModel:
		if isXorY(channel) || channel == chTheta || channel == chRadius {
			return "independent"
		}
		return "shared"
	}
	throw("invalid model type for resolve")
	return ""
}

func parseGuideResolve(resolve *resolveIndex, channel string) string {
	channelScaleResolve := resolve.scale[channel]
	guide := resolve.legend
	if isXorY(channel) {
		guide = resolve.axis
	}
	if channelScaleResolve == "independent" {
		return "independent"
	}
	if g := guide[channel]; g != "" {
		return g
	}
	return "shared"
}

func parseScales(m Model, ignoreRange bool) {
	cc := m.b().ctx

	parseScaleCore(m)
	parseScaleDomain(m)
	for _, prop := range nonTypeDomainRangeVegaScaleProperties {
		if cc.v5 && prop == "domainRaw" {
			continue // 5.8 has no domainRaw scale property
		}
		parseScaleProperty(m, prop)
	}
	if !ignoreRange {
		parseScaleRange(m)
	}
}

func parseScaleCore(m Model) {
	if u := asUnit(m); u != nil {
		u.comp.scales = parseUnitScaleCore(u)
	} else {
		m.b().comp.scales = parseNonUnitScaleCore(m)
	}
}

func parseUnitScaleCore(u *unitModel) *omap[*scaleComponent] {
	cc := u.b().ctx

	scales := newOmap[*scaleComponent]()
	for _, channel := range scaleChannels {
		fod := getFieldOrDatumDef(cc, u.encoding.Get(channel))
		if fod.IsTruthy() && u.mark() == "geoshape" && channel == chShape && channelDefType(fod) == "geojson" {
			continue
		}
		specified := fod.Get("scale")
		if cc.v5 && isXorYOffset(channel) && !channelHasNestedOffsetScale(cc, u.encoding, getMainChannelFromOffsetChannel(channel)) {
			continue // 5.8 ignores an offset scale without a nested main scale
		}
		if fod.IsTruthy() && !specified.IsNull() && !(specified.IsBool() && !specified.BoolValue()) {
			if specified.IsUndefined() {
				specified = mkv()
			}
			hasNested := channelHasNestedOffsetScale(cc, u.encoding, channel)
			sType := computeScaleType(cc, specified, channel, fod, u.markDef, hasNested)
			scales.set(channel, newScaleComponent(u.scaleName(channel, true), withExplicit{
				value:    strOrUndef(sType),
				explicit: specified.Get("type").IsStr() && specified.Get("type").StrValue() == sType,
			}))
		}
	}
	return scales
}

func strOrUndef(s string) Value {
	if s == "" {
		return undef
	}
	return jsval.Str(s)
}

var scaleTypeTieBreaker = tieBreakByComparing(func(a, b Value) int {
	return scaleTypePrecedence(a.AsString()) - scaleTypePrecedence(b.AsString())
})

func parseNonUnitScaleCore(m Model) *omap[*scaleComponent] {
	b := m.b()
	scales := newOmap[*scaleComponent]()
	b.comp.scales = scales
	typeIndex := newOmap[withExplicit]()
	resolve := b.comp.resolve
	for _, child := range m.children() {
		parseScaleCore(child)
		cs := child.b().comp.scales
		for _, channel := range cs.keyList() {
			if resolve.scale[channel] == "" {
				resolve.scale[channel] = defaultScaleResolve(channel, m)
			}
			if resolve.scale[channel] == "shared" {
				explicitType, have := typeIndex.get(channel)
				childScale, _ := cs.get(channel)
				childType := childScale.getWithExplicit("type")
				if have {
					if scaleCompatible(explicitType.value.AsString(), childType.value.AsString()) {
						et := explicitType
						typeIndex.set(channel, mergeValuesWithExplicit(&et, childType, "type", "scale", scaleTypeTieBreaker))
					} else {
						resolve.scale[channel] = "independent"
						typeIndex.del(channel)
					}
				} else {
					typeIndex.set(channel, childType)
				}
			}
		}
	}
	for _, channel := range typeIndex.keyList() {
		name := b.scaleName(channel, true)
		te, _ := typeIndex.get(channel)
		scales.set(channel, newScaleComponent(name, te))
		for _, child := range m.children() {
			if cs, ok := child.b().comp.scales.get(channel); ok && cs != nil {
				child.b().renameScale(cs.get("name").AsString(), name)
				cs.merged = true
			}
		}
	}
	return scales
}

// computeScaleType is upstream's scaleType(): the specified type when the
// channel and data type support it, else the default.
func computeScaleType(cc *compileCtx, specified Value, channel string, fieldDef Value, mark Value, hasNested bool) string {
	def := defaultScaleType(cc, channel, fieldDef, mark, hasNested)
	t := specified.Get("type")
	if !isScaleChannel(cc, channel) {
		return ""
	}
	if !t.IsUndefined() {
		ts := t.AsString()
		if !channelSupportScaleType(cc, channel, ts, false) {
			return def
		}
		if isFieldDef(cc, fieldDef) && !scaleTypeSupportDataType(ts, channelDefType(fieldDef)) {
			return def
		}
		return ts
	}
	return def
}

func defaultScaleType(cc *compileCtx, channel string, fieldDef Value, mark Value, hasNested bool) string {
	markType := mark.Get("type").AsString()
	switch channelDefType(fieldDef) {
	case "nominal", "ordinal":
		if isColorChannel(channel) || rangeType(channel) == "discrete" {
			return "ordinal"
		}
		if isTimeChannel(channel) {
			return "band"
		}
		if isXorY(channel) || isXorYOffset(channel) {
			if contains([]string{"rect", "bar", "image", "rule", "tick"}, markType) && !(cc.v5 && markType == "tick") {
				return "band"
			}
			if hasNested {
				return "band"
			}
		} else if markType == "arc" && (channel == chTheta || channel == chRadius) {
			return "band"
		}
		if sc := getSizeChannel(channel); sc != "" {
			if isRelativeBandSize(mark.Get(sc)) {
				return "band"
			}
		}
		if isPositionFieldOrDatumDef(fieldDef) && fieldDef.Get("axis").Get("tickBand").IsTruthy() {
			return "band"
		}
		return "point"
	case "temporal":
		switch {
		case isColorChannel(channel):
			return "time"
		case rangeType(channel) == "discrete":
			return "ordinal"
		case isFieldDef(cc, fieldDef) && fieldDef.Get("timeUnit").IsTruthy() && normalizeTimeUnit(cc, fieldDef.Get("timeUnit")).Get("utc").IsTruthy():
			return "utc"
		case isTimeChannel(channel):
			return "band"
		}
		return "time"
	case "quantitative":
		switch {
		case isColorChannel(channel):
			if isFieldDef(cc, fieldDef) && isBinning(fieldDef.Get("bin")) {
				return "bin-ordinal"
			}
			return "linear"
		case rangeType(channel) == "discrete":
			return "ordinal"
		case isTimeChannel(channel):
			return "band"
		}
		return "linear"
	case "geojson":
		return ""
	}
	throw("Invalid field type %q.", fieldDef.Get("type").AsString())
	return ""
}

func isRelativeBandSize(v Value) bool { return hasProperty(v, "band") }

// ---- properties ----

func parseScaleProperty(m Model, property string) {
	if u := asUnit(m); u != nil {
		parseUnitScaleProperty(u, property)
	} else {
		parseNonUnitScaleProperty(m, property)
	}
}

func parseUnitScaleProperty(u *unitModel, property string) {
	cc := u.b().ctx

	local := u.comp.scales
	config := u.config
	for _, channel := range local.keyList() {
		specifiedScale := u.specifiedScales.Lookup(channel)
		localCmpt, _ := local.get(channel)
		merged := u.getScaleComponent(channel)
		fod := getFieldOrDatumDef(cc, u.encoding.Get(channel))
		specifiedValue := specifiedScale.Get(property)
		scaleType := merged.get("type").AsString()
		scalePadding := merged.get("padding")
		scalePaddingInner := merged.get("paddingInner")
		supported := scaleTypeSupportProperty(scaleType, property)
		incompatible := channelScalePropertyIncompatible(channel, property)
		if supported && !incompatible {
			if !specifiedValue.IsUndefined() {
				switch property {
				case "domainMax", "domainMin":
					timeUnit, typ := fod.Get("timeUnit"), channelDefType(fod)
					if isDateTime(specifiedValue) || typ == "temporal" || timeUnit.IsTruthy() {
						e, _ := valueExpr(cc, specifiedValue, timeUnit, typ, false, false)
						localCmpt.set(property, sig(e), true)
					} else {
						localCmpt.set(property, specifiedValue, true)
					}
				default:
					localCmpt.copyKeyFromObject(property, specifiedScale)
				}
			} else {
				var value Value
				if rule, ok := scaleRules[property]; ok {
					value = rule(scaleRuleParams{
						model: u, channel: channel, fod: fod, scaleType: scaleType,
						scalePadding: scalePadding, scalePaddingInner: scalePaddingInner,
						domain: specifiedScale.Get("domain"), domainMin: specifiedScale.Get("domainMin"), domainMax: specifiedScale.Get("domainMax"),
						markDef: u.markDef, config: config,
						hasNestedOffsetScale:     channelHasNestedOffsetScale(cc, u.encoding, channel),
						hasSecondaryRangeChannel: u.encoding.Get(getSecondaryRangeChannel(channel)).IsTruthy(),
					})
				} else {
					value = config.Get("scale").Get(property)
				}
				if !value.IsUndefined() {
					localCmpt.set(property, value, false)
				}
			}
		}
	}
}

type scaleRuleParams struct {
	model                           *unitModel
	channel                         string
	fod                             Value
	scaleType                       string
	scalePadding, scalePaddingInner Value
	domain, domainMin, domainMax    Value
	markDef, config                 Value
	hasNestedOffsetScale            bool
	hasSecondaryRangeChannel        bool
}

var scaleRules = map[string]func(p scaleRuleParams) Value{
	"bins": func(p scaleRuleParams) Value {
		if isFieldDef(p.model.ctx, p.fod) {
			return scaleBins(p.model, p.fod)
		}
		return undef
	},
	"interpolate": func(p scaleRuleParams) Value {
		if isColorChannel(p.channel) && channelDefType(p.fod) != "nominal" {
			return jsval.Str("hcl")
		}
		return undef
	},
	"nice": func(p scaleRuleParams) Value {
		return scaleNice(p.model.ctx, p.scaleType, p.channel, p.domain, p.domainMin, p.domainMax, p.fod)
	},
	"padding": func(p scaleRuleParams) Value {
		return scalePaddingRule(p.model.ctx, p.channel, p.scaleType, p.config.Get("scale"), p.fod, p.markDef, p.config.Get("bar"))
	},
	"paddingInner": func(p scaleRuleParams) Value {
		return scalePaddingInnerRule(p.model.ctx, p.scalePadding, p.channel, p.markDef.Get("type").AsString(), p.scaleType, p.config.Get("scale"), p.hasNestedOffsetScale)
	},
	"paddingOuter": func(p scaleRuleParams) Value {
		return scalePaddingOuterRule(p.scalePadding, p.channel, p.scaleType, p.scalePaddingInner, p.config.Get("scale"), p.hasNestedOffsetScale)
	},
	"reverse": func(p scaleRuleParams) Value {
		sort := undef
		if isFieldDef(p.model.ctx, p.fod) {
			sort = p.fod.Get("sort")
		}
		return scaleReverse(p.scaleType, sort, p.channel, p.config.Get("scale"))
	},
	"zero": func(p scaleRuleParams) Value {
		return scaleZero(p.model.ctx, p.channel, p.fod, p.domain, p.markDef, p.scaleType, p.config.Get("scale"), p.hasSecondaryRangeChannel)
	},
}

func scaleBins(m *unitModel, fd Value) Value {
	bin := fd.Get("bin")
	if isBinning(bin) {
		binSignal := getBinSignalName(m, fd.Get("field").AsString(), bin)
		return lazySignal(m.ctx, func() string { return m.getSignalName(binSignal) })
	} else if isBinned(bin) && isObject(bin) && !bin.Get("step").IsUndefined() {
		return mkv("step", bin.Get("step"))
	}
	return undef
}

func scaleNice(cc *compileCtx, scaleType, channel string, domain, domainMin, domainMax, fod Value) Value {
	if getFieldDef(cc, fod).Get("bin").IsTruthy() || domain.IsArr() || !domainMax.IsNullish() || !domainMin.IsNullish() || scaleType == "time" || scaleType == "utc" {
		return undef
	}
	if isXorY(channel) {
		return jsval.True
	}
	return undef
}

func scalePaddingRule(cc *compileCtx, channel, scaleType string, scaleConfig Value, fod Value, markDef Value, barConfig Value) Value {
	if isXorY(channel) {
		if isContinuousToContinuous(scaleType) {
			if cp := scaleConfig.Get("continuousPadding"); !cp.IsUndefined() {
				return cp
			}
			typ, orient := markDef.Get("type").AsString(), markDef.Get("orient").AsString()
			if typ == "bar" && !(isFieldDef(cc, fod) && (fod.Get("bin").IsTruthy() || fod.Get("timeUnit").IsTruthy())) {
				if (orient == "vertical" && channel == chX) || (orient == "horizontal" && channel == chY) {
					return barConfig.Get("continuousBandSize")
				}
			}
		}
		if scaleType == "point" {
			return scaleConfig.Get("pointPadding")
		}
	}
	return undef
}

func scalePaddingInnerRule(cc *compileCtx, paddingValue Value, channel, mark, scaleType string, scaleConfig Value, hasNested bool) Value {
	if !paddingValue.IsUndefined() {
		return undef
	}
	if isXorY(channel) {
		if hasNested {
			return scaleConfig.Get("bandWithNestedOffsetPaddingInner")
		}
		var fallback Value
		switch mark {
		case "bar":
			fallback = scaleConfig.Get("barBandPaddingInner")
		case "tick":
			fallback = scaleConfig.Get("tickBandPaddingInner")
			if cc.v5 {
				fallback = scaleConfig.Get("rectBandPaddingInner")
			}
		default:
			fallback = scaleConfig.Get("rectBandPaddingInner")
		}
		return firstDefined(scaleConfig.Get("bandPaddingInner"), fallback)
	} else if isXorYOffset(channel) {
		if scaleType == "band" {
			return scaleConfig.Get("offsetBandPaddingInner")
		}
	}
	return undef
}

func scalePaddingOuterRule(paddingValue Value, channel, scaleType string, paddingInnerValue Value, scaleConfig Value, hasNested bool) Value {
	if !paddingValue.IsUndefined() {
		return undef
	}
	if isXorY(channel) {
		if hasNested {
			return scaleConfig.Get("bandWithNestedOffsetPaddingOuter")
		}
		if scaleType == "band" {
			var half Value
			if isSignalRef(paddingInnerValue) {
				half = sig(signalOf(paddingInnerValue) + "/2")
			} else {
				half = jsval.Num(paddingInnerValue.AsDouble() / 2)
				if paddingInnerValue.IsUndefined() {
					half = jsval.Num(math.NaN())
				}
			}
			return firstDefined(scaleConfig.Get("bandPaddingOuter"), half)
		}
	} else if isXorYOffset(channel) {
		if scaleType == "point" {
			return jsval.Num(0.5)
		} else if scaleType == "band" {
			return scaleConfig.Get("offsetBandPaddingOuter")
		}
	}
	return undef
}

func scaleReverse(scaleType string, sort Value, channel string, scaleConfig Value) Value {
	descending := sort.IsStr() && sort.StrValue() == "descending"
	if channel == chX && !scaleConfig.Get("xReverse").IsUndefined() {
		xr := scaleConfig.Get("xReverse")
		if hasContinuousDomain(scaleType) && descending {
			if isSignalRef(xr) {
				return sig("!" + signalOf(xr))
			}
			return jsval.Bool(!xr.IsTruthy())
		}
		return xr
	}
	if hasContinuousDomain(scaleType) && descending {
		return jsval.True
	}
	return undef
}

func scaleZero(cc *compileCtx, channel string, fd Value, specifiedDomain Value, markDef Value, scaleType string, scaleConfig Value, hasSecondary bool) Value {
	hasCustomDomain := specifiedDomain.IsTruthy() && !(specifiedDomain.IsStr() && specifiedDomain.StrValue() == "unaggregated")
	if hasCustomDomain {
		if hasContinuousDomain(scaleType) {
			if specifiedDomain.IsArr() {
				first, last := specifiedDomain.Index(0), specifiedDomain.Index(specifiedDomain.Len()-1)
				if cc.v5 {
					// JavaScript comparison: strings such as "-0.07" count as numbers.
					if !first.IsUndefined() && !last.IsUndefined() && jsval.ToNumber(first) <= 0 && jsval.ToNumber(last) >= 0 {
						return jsval.True
					}
				} else if first.IsNum() && first.NumValue() <= 0 && last.IsNum() && last.NumValue() >= 0 {
					return jsval.True
				}
			}
			return jsval.False
		}
	}
	if channel == chSize && channelDefType(fd) == "quantitative" && !isContinuousToDiscrete(scaleType) {
		return jsval.True
	}
	if !(isFieldDef(cc, fd) && fd.Get("bin").IsTruthy()) && (channel == chX || channel == chY || channel == chTheta || channel == chRadius) {
		orient, typ := markDef.Get("orient").AsString(), markDef.Get("type").AsString()
		if contains([]string{"bar", "area", "line", "trail"}, typ) {
			if (orient == "horizontal" && channel == chY) || (orient == "vertical" && channel == chX) {
				return jsval.False
			}
		}
		if (typ == "bar" || typ == "area") && !hasSecondary {
			return jsval.True
		}
		return scaleConfig.Get("zero")
	}
	return jsval.False
}

func parseScaleRange(m Model) {
	if u := asUnit(m); u != nil {
		parseUnitScaleRange(u)
	} else {
		parseNonUnitScaleProperty(m, "range")
	}
}

func parseNonUnitScaleProperty(m Model, property string) {
	local := m.b().comp.scales
	for _, child := range m.children() {
		if property == "range" {
			parseScaleRange(child)
		} else {
			parseScaleProperty(child, property)
		}
	}
	tb := tieBreakByComparing(func(v1, v2 Value) int {
		if property == "range" {
			s1, s2 := v1.Get("step"), v2.Get("step")
			if s1.IsTruthy() && s2.IsTruthy() {
				d := s1.AsDouble() - s2.AsDouble()
				switch {
				case d > 0:
					return 1
				case d < 0:
					return -1
				}
				return 0
			}
		}
		return 0
	})
	for _, channel := range local.keyList() {
		var valueWithExplicit *withExplicit
		for _, child := range m.children() {
			cc, ok := child.b().comp.scales.get(channel)
			if ok && cc != nil {
				cv := cc.getWithExplicit(property)
				merged := mergeValuesWithExplicit(valueWithExplicit, cv, property, "scale", tb)
				valueWithExplicit = &merged
			}
		}
		lc, _ := local.get(channel)
		if valueWithExplicit != nil {
			lc.setWithExplicit(property, *valueWithExplicit)
		}
	}
}

// ---- assemble ----

func assembleScales(m Model) []Value {
	if isLayerModel(m) || isConcatModel(m) {
		scales := assembleScalesForModel(m)
		for _, child := range m.children() {
			scales = append(scales, assembleScales(child)...)
		}
		return scales
	}
	return assembleScalesForModel(m)
}

func assembleScalesForModel(m Model) []Value {
	var scales []Value
	sc := m.b().comp.scales
	for _, channel := range sc.keyList() {
		cmpt, _ := sc.get(channel)
		if cmpt.merged {
			continue
		}
		scale := cmpt.combine()
		name, typ, selectionExtent, reverse := scale.Lookup("name"), scale.Lookup("type"), scale.Lookup("selectionExtent"), scale.Lookup("reverse")
		other := omit(jsval.Obj(scale), "name", "type", "selectionExtent", "domains", "range", "reverse")
		rng := assembleScaleRange(scale.Lookup("range"), name.AsString(), channel, m)
		domain := assembleDomain(m, channel)
		domainRaw := jsval.Null
		if selectionExtent.IsTruthy() {
			domainRaw = assembleSelectionScaleDomain(m, selectionExtent, cmpt, domain)
		}
		o := jsval.NewObject(8)
		o.Set("name", name)
		o.Set("type", typ)
		if domain.IsTruthy() {
			o.Set("domain", domain)
		}
		if domainRaw.IsTruthy() {
			o.Set("domainRaw", domainRaw)
		}
		o.Set("range", rng)
		if !reverse.IsUndefined() {
			o.Set("reverse", reverse)
		}
		spread(o, jsval.Obj(other))
		scales = append(scales, jsval.Obj(o))
	}
	return scales
}

func assembleScaleRange(scaleRange Value, scaleName, channel string, m Model) Value {
	if isXorY(channel) {
		if isVgRangeStep(scaleRange) {
			return mkv("step", sig(scaleName+"_step"))
		}
	} else if isObject(scaleRange) && isDataRefDomain(scaleRange) {
		o := cloneObj(scaleRange.ObjValue())
		o.Set("data", jsval.Str(m.b().lookupDataSource(scaleRange.Get("data").AsString())))
		return jsval.Obj(o)
	}
	return scaleRange
}

func isDataRefUnionedDomain(d Value) bool {
	return !d.IsArr() && hasProperty(d, "fields") && !hasProperty(d, "data")
}
func isFieldRefUnionDomain(d Value) bool {
	return !d.IsArr() && hasProperty(d, "fields") && hasProperty(d, "data")
}
func isDataRefDomain(d Value) bool {
	return !d.IsArr() && hasProperty(d, "field") && hasProperty(d, "data")
}
