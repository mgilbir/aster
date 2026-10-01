package vega

import (
	"strconv"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/vega/guides"
)

// This file ports vega-parser's parsers/scale.js and projection.js, and the
// glue that turns guides' planned axes, legends and titles into marks.

// multiDomainSortOps are the aggregate ops allowed to combine the sort of a
// multi-field ordinal domain.
var multiDomainSortOps = map[string]string{"min": "min", "max": "max", "count": "sum"}

func initScale(spec jsval.Value, scope *Scope) {
	typ := "linear"
	if t := spec.Get("type"); t.IsTruthy() {
		typ = t.AsString()
	}
	if !scale.IsValidScaleType(typ) {
		perr("Unrecognized scale type: %s", quote(sv(typ)))
	}
	e := newEntry("scale", nil, plist("type", jsval.Str(typ), "domain", jsval.Undefined))
	scope.addScaleProj(spec.Get("name").AsString(), e)
}

func parseScale(spec jsval.Value, scope *Scope) {
	params := scope.getScale(spec.Get("name").AsString()).params
	params.set("domain", parseScaleDomain(spec.Get("domain"), spec, scope))

	if !spec.Get("range").IsNullish() {
		params.set("range", parseScaleRange(spec, scope, params))
	}
	if !spec.Get("interpolate").IsNullish() {
		parseScaleInterpolate(spec.Get("interpolate"), params)
	}
	if !spec.Get("nice").IsNullish() {
		params.set("nice", parseScaleNice(spec.Get("nice"), scope))
	}
	if !spec.Get("bins").IsNullish() {
		params.set("bins", parseScaleBins(spec.Get("bins"), scope))
	}
	if o := spec.ObjValue(); o != nil {
		for i := 0; i < o.Len(); i++ {
			key := o.KeyAt(i)
			if params.has(key) || key == "name" {
				continue
			}
			params.set(key, parseLiteral(o.ValueAt(i), scope))
		}
	}
}

func parseLiteral(v jsval.Value, scope *Scope) P {
	if !v.IsObj() && !v.IsArr() {
		return v
	}
	if v.IsObj() && v.Get("signal").IsTruthy() {
		return scope.signalRef(v.Get("signal").AsString())
	}
	perr("Unsupported object: %s", quote(v))
	return nil
}

func parseArray(v jsval.Value, scope *Scope) P {
	if v.IsObj() && v.Get("signal").IsTruthy() {
		return scope.signalRef(v.Get("signal").AsString())
	}
	items := v.Items()
	out := make([]P, len(items))
	for i, it := range items {
		out[i] = parseLiteral(it, scope)
	}
	return out
}

func dataLookupError(name jsval.Value) { perr("Can not find data set: %s", quote(name)) }

// -- domain --------------------------------------------------------------------

func parseScaleDomain(domain jsval.Value, spec jsval.Value, scope *Scope) P {
	if !domain.IsTruthy() {
		if !spec.Get("domainMin").IsNullish() || !spec.Get("domainMax").IsNullish() {
			perr("No scale domain defined for domainMin/domainMax to override.")
		}
		return nil
	}
	switch {
	case isSignalObj(domain):
		return scope.signalRef(domain.Get("signal").AsString())
	case domain.IsArr():
		return explicitDomain(domain, scope)
	case domain.Get("fields").IsTruthy():
		return multipleDomain(domain, spec, scope)
	}
	return singularDomain(domain, spec, scope)
}

func explicitDomain(domain jsval.Value, scope *Scope) P {
	out := make([]P, domain.Len())
	for i, v := range domain.Items() {
		out[i] = parseLiteral(v, scope)
	}
	return out
}

func scaleTypeOf(spec jsval.Value) string {
	if t := spec.Get("type"); t.IsTruthy() {
		return t.AsString()
	}
	return "linear"
}

func singularDomain(domain, spec jsval.Value, scope *Scope) P {
	data := scope.findData(domain.Get("data").AsString())
	if data == nil {
		dataLookupError(domain.Get("data"))
	}
	typ := scaleTypeOf(spec)
	switch {
	case scale.IsDiscrete(typ):
		return data.valuesRef(scope, domain.Get("field"), parseSort(domain.Get("sort"), false))
	case scale.IsQuantile(typ):
		return data.domainRef(scope, domain.Get("field"))
	}
	return data.extentRef(scope, domain.Get("field"))
}

func multipleDomain(domain, spec jsval.Value, scope *Scope) P {
	data := domain.Get("data")
	var fields []jsval.Value
	for _, d := range domain.Get("fields").Items() {
		switch {
		case d.IsStr():
			fields = append(fields, obj("data", data, "field", d))
		case d.IsArr() || isSignalObj(d):
			fields = append(fields, domainFieldRef(d, scope))
		default:
			fields = append(fields, d)
		}
	}
	typ := scaleTypeOf(spec)
	switch {
	case scale.IsDiscrete(typ):
		return ordinalMultipleDomain(domain, scope, fields)
	case scale.IsQuantile(typ):
		return quantileMultipleDomain(scope, fields)
	}
	return numericMultipleDomain(scope, fields)
}

// domainFieldRef registers an inline array or signal as an anonymous data set
// and returns a reference to its `data` field.
func domainFieldRef(data jsval.Value, scope *Scope) jsval.Value {
	root := scope
	for root.parentScope != nil {
		root = root.parentScope
	}
	name := "_:vega:_" + strconv.Itoa(root.fieldRefs)
	root.fieldRefs++
	if data.IsArr() {
		coll := mk("collect")
		coll.ingest = &ingestSpec{values: data}
		scope.addDataPipeline(name, []*entry{coll, mk("sieve")})
	} else {
		// Upstream feeds the set with `setdata(name, signal)` whenever the
		// signal changes; a loader that reads the signal's value does the same.
		load := mk("load", "values", scope.signalRef(data.Get("signal").AsString()))
		scope.addDataPipeline(name, []*entry{load, mk("collect"), mk("sieve")})
	}
	return obj("data", sv(name), "field", sv("data"))
}

func ordinalMultipleDomain(domain jsval.Value, scope *Scope, fields []jsval.Value) P {
	sort := parseSort(domain.Get("sort"), true)
	var counts []P
	for _, f := range fields {
		data := scope.findData(f.Get("data").AsString())
		if data == nil {
			dataLookupError(f.Get("data"))
		}
		counts = append(counts, data.countsRef(scope, f.Get("field"), sort))
	}
	p := plist("groupby", keyFieldRef, "pulse", counts)
	if sort.IsObj() {
		a := "count"
		if op := sort.Get("op"); op.IsTruthy() {
			a = op.AsString()
		}
		v := "count"
		if sort.Get("field").IsTruthy() {
			v = aggrField(sv(a), sort.Get("field"))
		}
		p.set("ops", []P{multiDomainSortOps[a]})
		p.set("fields", []P{scope.fieldRef(sv(v), "")})
		p.set("as", []P{v})
	}
	a := scope.add(newEntry("aggregate", nil, p))
	c := scope.add(mk("collect", "pulse", a))
	v := scope.add(mk("values", "field", keyFieldRef, "sort", scope.sortRef(sort), "pulse", c))
	return v
}

func parseSort(sort jsval.Value, multidomain bool) jsval.Value {
	if !sort.IsTruthy() {
		return sort
	}
	field, op := sort.Get("field"), sort.Get("op")
	switch {
	case !field.IsTruthy() && !op.IsTruthy():
		if sort.IsObj() {
			o := sort.ObjValue().Clone()
			o.Set("field", sv("key"))
			return jsval.Obj(o)
		}
		return obj("field", sv("key"))
	case !field.IsTruthy() && !(op.IsStr() && op.StrValue() == "count"):
		perr("No field provided for sort aggregate op: %s", op.AsString())
	case multidomain && field.IsTruthy():
		if op.IsTruthy() {
			if _, ok := multiDomainSortOps[op.AsString()]; !ok {
				perr("Multiple domain scales can not be sorted using %s", op.AsString())
			}
		}
	}
	return sort
}

func quantileMultipleDomain(scope *Scope, fields []jsval.Value) P {
	var values []P
	for _, f := range fields {
		data := scope.findData(f.Get("data").AsString())
		if data == nil {
			dataLookupError(f.Get("data"))
		}
		values = append(values, data.domainRef(scope, f.Get("field")))
	}
	return scope.add(mk("multivalues", "values", values))
}

func numericMultipleDomain(scope *Scope, fields []jsval.Value) P {
	var extents []P
	for _, f := range fields {
		data := scope.findData(f.Get("data").AsString())
		if data == nil {
			dataLookupError(f.Get("data"))
		}
		extents = append(extents, data.extentRef(scope, f.Get("field")))
	}
	return scope.add(mk("multiextent", "extents", extents))
}

// -- bins, nice, interpolate ---------------------------------------------------

func parseScaleBins(v jsval.Value, scope *Scope) P {
	if isSignalObj(v) || v.IsArr() {
		return parseArray(v, scope)
	}
	return scope.objectProperty(v)
}

func parseScaleNice(nice jsval.Value, scope *Scope) P {
	if isSignalObj(nice) {
		return scope.signalRef(nice.Get("signal").AsString())
	}
	if nice.IsObj() {
		// upstream calls parseLiteral(nice.interval) without a scope, which
		// only works for plain values.
		return obj("interval", nice.Get("interval"), "step", nice.Get("step"))
	}
	return parseLiteral(nice, scope)
}

func parseScaleInterpolate(interpolate jsval.Value, params *paramList) {
	t := interpolate.Get("type")
	var lit jsval.Value
	if t.IsTruthy() {
		lit = t
	} else {
		lit = interpolate
	}
	params.set("interpolate", lit)
	if g := interpolate.Get("gamma"); !g.IsNullish() {
		params.set("interpolateGamma", g)
	}
}

// -- range ---------------------------------------------------------------------

func parseScaleRange(spec jsval.Value, scope *Scope, params *paramList) P {
	return parseScaleRangeAlias(spec, scope, params, 0)
}

// maxRangeAliases bounds a chain of config.range names that name each other
// (config.range.a = "b", config.range.b = "a" never terminates; upstream
// overflows its stack). Real configurations alias one level.
const maxRangeAliases = 32

func parseScaleRangeAlias(spec jsval.Value, scope *Scope, params *paramList, depth int) P {
	config := scope.config.Get("range")
	rng := spec.Get("range")
	typ := scaleTypeOf(spec)

	switch {
	case isSignalObj(rng):
		return scope.signalRef(rng.Get("signal").AsString())
	case rng.IsStr():
		name := rng.StrValue()
		if config.IsObj() && config.ObjValue().Has(name) {
			if depth >= maxRangeAliases {
				perr("Scale range aliases nest too deeply: %s", quote(rng))
			}
			return parseScaleRangeAlias(extended(spec, obj("range", config.Get(name))), scope, params, depth+1)
		}
		switch name {
		case "width":
			rng = jsval.ArrOf(jsval.Num(0), obj("signal", sv("width")))
		case "height":
			if scale.IsDiscrete(typ) {
				rng = jsval.ArrOf(jsval.Num(0), obj("signal", sv("height")))
			} else {
				rng = jsval.ArrOf(obj("signal", sv("height")), jsval.Num(0))
			}
		default:
			perr("Unrecognized scale range value: %s", quote(rng))
		}
	case rng.IsObj() && rng.Get("scheme").IsTruthy():
		sch := rng.Get("scheme")
		if sch.IsArr() {
			params.set("scheme", parseArray(sch, scope))
		} else {
			params.set("scheme", parseLiteral(sch, scope))
		}
		if e := rng.Get("extent"); e.IsTruthy() {
			params.set("schemeExtent", parseArray(e, scope))
		}
		if c := rng.Get("count"); c.IsTruthy() {
			params.set("schemeCount", parseLiteral(c, scope))
		}
		return nil
	case rng.IsObj() && rng.Get("step").IsTruthy():
		params.set("rangeStep", parseLiteral(rng.Get("step"), scope))
		return nil
	case scale.IsDiscrete(typ) && !rng.IsArr():
		return parseScaleDomain(rng, spec, scope)
	case !rng.IsArr():
		perr("Unsupported range type: %s", quote(rng))
	}

	items := rng.Items()
	out := make([]P, len(items))
	for i, v := range items {
		if v.IsArr() {
			out[i] = parseArray(v, scope)
		} else {
			out[i] = parseLiteral(v, scope)
		}
	}
	return out
}

// -- projection ----------------------------------------------------------------

func parseProjection(proj jsval.Value, scope *Scope) {
	config := scope.config.Get("projection")
	params := newParamList()
	if o := proj.ObjValue(); o != nil {
		for i := 0; i < o.Len(); i++ {
			name := o.KeyAt(i)
			if name == "name" {
				continue
			}
			params.set(name, parseProjectionParameter(o.ValueAt(i), name, scope))
		}
	}
	if co := config.ObjValue(); co != nil {
		for i := 0; i < co.Len(); i++ {
			name := co.KeyAt(i)
			if cur := params.get(name); cur == nil || isNullP(cur) {
				params.set(name, parseProjectionParameter(co.ValueAt(i), name, scope))
			}
		}
	}
	scope.addScaleProj(proj.Get("name").AsString(), newEntry("projection", nil, params))
}

func isNullP(p P) bool {
	v, ok := p.(jsval.Value)
	return ok && v.IsNullish()
}

func parseProjectionParameter(v jsval.Value, name string, scope *Scope) P {
	switch {
	case v.IsArr():
		items := v.Items()
		out := make([]P, len(items))
		for i, it := range items {
			out[i] = parseProjectionParameter(it, name, scope)
		}
		return out
	case !v.IsObj():
		return v
	case v.Get("signal").IsTruthy():
		return scope.signalRef(v.Get("signal").AsString())
	case name == "fit":
		return v
	}
	perr("Unsupported parameter object: %s", quote(v))
	return nil
}

func (s *Scope) projectionRef(name string) P { return s.scaleRef(name) }

// -- guides --------------------------------------------------------------------

// guideScope adapts the parser scope to what guides needs.
type guideScope struct{ s *Scope }

func (g guideScope) Config() jsval.Value { return g.s.config }
func (g guideScope) ScaleType(name string) string {
	if e := g.s.findScale(name); e != nil {
		return g.s.scaleType(name)
	}
	return ""
}

// handle returns an opaque reference to an entry that guide plans copy into the
// `from` of the marks they generate.
func (s *Scope) handle(e *entry) jsval.Value {
	root := s
	for root.parentScope != nil {
		root = root.parentScope
	}
	if root.handles == nil {
		root.handles = map[string]*entry{}
	}
	id := "$h" + strconv.Itoa(len(root.handles))
	root.handles[id] = e
	return obj("$ref", sv(id))
}

func (s *Scope) resolveHandle(id string) *entry {
	root := s
	for root.parentScope != nil {
		root = root.parentScope
	}
	e := root.handles[id]
	if e == nil {
		perr("Unknown operator reference: %s", id)
	}
	return e
}

func parseAxis(spec jsval.Value, scope *Scope) {
	plan, err := guides.NewAxis(spec, guideScope{scope})
	if err != nil {
		perr("%s", err.Error())
	}
	data := scope.add(mk("collect"))
	data.literal = []jsval.Value{plan.Datum}
	t := plan.Ticks
	ticks := scope.add(mk("axisticks",
		"scale", scope.scaleRef(t.Scale),
		"extra", scope.property(t.Extra),
		"count", scope.objectProperty(t.Count),
		"values", scope.objectProperty(t.Values),
		"minstep", scope.property(t.MinStep),
		"formatType", scope.property(t.FormatType),
		"formatSpecifier", scope.property(t.Format),
	))
	parseMark(plan.Mark(scope.handle(data), scope.handle(ticks)), scope)
}

func parseTitle(spec jsval.Value, scope *Scope) {
	plan, err := guides.NewTitle(spec, guideScope{scope})
	if err != nil {
		perr("%s", err.Error())
	}
	data := scope.add(mk("collect"))
	data.literal = []jsval.Value{plan.Datum}
	parseMark(plan.Mark(scope.handle(data)), scope)
}

func parseLegend(spec jsval.Value, scope *Scope) {
	plan, err := guides.NewLegend(spec, guideScope{scope})
	if err != nil {
		perr("%s", err.Error())
	}
	data := scope.add(mk("collect"))
	data.literal = []jsval.Value{plan.Datum}
	en := plan.Entries
	cfg := scope.config.Get("legend")
	_ = cfg
	params := plist(
		"type", jsval.Str(en.Type),
		"scale", scope.scaleRef(en.Scale),
		"count", scope.objectProperty(en.Count),
		"limit", scope.property(en.Limit),
		"values", scope.objectProperty(en.Values),
		"minstep", scope.property(en.MinStep),
		"formatType", scope.property(en.FormatType),
		"formatSpecifier", scope.property(en.Format),
	)
	if en.CountExpr != "" {
		if c := params.get("count"); c == nil || !pTruthy(c) {
			params.set("count", scope.signalRef(en.CountExpr))
		}
	}
	if en.SizeExpr != "" {
		fn := scope.parseExpression(en.SizeExpr)
		params.set("size", pExpr{fn: fn})
	}
	entries := scope.add(newEntry("legendentries", nil, params))
	parseMark(plan.Mark(scope.handle(data), scope.handle(entries)), scope)
}

// pTruthy is JavaScript truthiness of a parameter (references are truthy).
func pTruthy(p P) bool {
	if v, ok := p.(jsval.Value); ok {
		return v.IsTruthy()
	}
	return p != nil
}
