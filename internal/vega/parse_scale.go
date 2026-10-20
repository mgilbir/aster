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

// initScale is scale.js's initScale: `type = spec.type || 'linear'`, which
// vega-scale's isValidScaleType looks up in a Map, so only a string names a
// scale type.
func initScale(spec jsval.Value, scope *Scope) {
	t := specGet(spec, "type")
	typ := "linear"
	if t.IsTruthy() {
		s, ok := t.Strict()
		if !ok || !scale.IsValidScaleType(s) {
			perr("Unrecognized scale type: %s", stringValue(t))
		}
		typ = s
	} else if !scale.IsValidScaleType(typ) {
		perr("Unrecognized scale type: %s", quote(sv(typ)))
	}
	e := newEntry("scale", nil, plist("type", jsval.Str(typ), "domain", jsval.Undefined))
	scope.addScaleProj(specText(specGet(spec, "name")), e)
}

// parseScale is scale.js's parseScale. The members it does not parse
// itself are each `for (key in spec)` enumerates, a string's or an array's
// indexes too, read as literals.
func parseScale(spec jsval.Value, scope *Scope) {
	params := scope.getScale(specText(specGet(spec, "name"))).params
	params.set("domain", parseScaleDomain(specGet(spec, "domain"), spec, scope))

	if !specGet(spec, "range").IsNullish() {
		params.set("range", parseScaleRange(spec, scope, params))
	}
	if !specGet(spec, "interpolate").IsNullish() {
		parseScaleInterpolate(specGet(spec, "interpolate"), params)
	}
	if !specGet(spec, "nice").IsNullish() {
		params.set("nice", parseScaleNice(specGet(spec, "nice"), scope))
	}
	if !specGet(spec, "bins").IsNullish() {
		params.set("bins", parseScaleBins(specGet(spec, "bins"), scope))
	}
	forInEach(scope.meter(), spec, func(key string, v jsval.Value) {
		if params.has(key) || key == "name" {
			return
		}
		params.set(key, parseLiteral(v, scope))
	})
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

// parseArray is scale.js's parseArray: `v.signal ? signalRef : v.map(...)`,
// a TypeError for any other value than an array.
func parseArray(v jsval.Value, scope *Scope) P {
	if isSignalObj(v) {
		return scope.signalRef(v.Get("signal").AsString())
	}
	items := listOf(v, "v", "map")
	out := make([]P, len(items))
	for i, it := range items {
		scope.work(1)
		out[i] = parseLiteral(it, scope)
	}
	return out
}

// -- domain --------------------------------------------------------------------

func parseScaleDomain(domain jsval.Value, spec jsval.Value, scope *Scope) P {
	if !domain.IsTruthy() {
		if !specGet(spec, "domainMin").IsNullish() || !specGet(spec, "domainMax").IsNullish() {
			perr("No scale domain defined for domainMin/domainMax to override.")
		}
		return nil
	}
	switch {
	case isSignalObj(domain):
		return scope.signalRef(domain.Get("signal").AsString())
	case domain.IsArr():
		return explicitDomain(domain, scope)
	case specGet(domain, "fields").IsTruthy():
		return multipleDomain(domain, spec, scope)
	}
	return singularDomain(domain, spec, scope)
}

func explicitDomain(domain jsval.Value, scope *Scope) P {
	out := make([]P, domain.Len())
	for i, v := range domain.Items() {
		scope.work(1)
		out[i] = parseLiteral(v, scope)
	}
	return out
}

// scaleTypeOf is spec.type as vega-scale's isDiscrete and isQuantile read
// it (a Map lookup): the string, and "" for any other value, which is no
// type; initScale has already refused a type that is not one.
func scaleTypeOf(spec jsval.Value) string {
	if s, ok := spec.Get("type").Strict(); ok {
		return s
	}
	return ""
}

func singularDomain(domain, spec jsval.Value, scope *Scope) P {
	data := scope.getDataNamed(specGet(domain, "data"))
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
	for _, d := range listOf(domain.Get("fields"), "domain.fields", "reduce") {
		scope.work(1)
		switch {
		case d.IsStr():
			fields = append(fields, obj("data", data, "field", d))
		case d.IsArr() || specGet(d, "signal").IsTruthy():
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
		scope.work(1)
		data := scope.getDataNamed(specGet(f, "data"))
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
		scope.work(1)
		data := scope.getDataNamed(specGet(f, "data"))
		values = append(values, data.domainRef(scope, f.Get("field")))
	}
	return scope.add(mk("multivalues", "values", values))
}

func numericMultipleDomain(scope *Scope, fields []jsval.Value) P {
	var extents []P
	for _, f := range fields {
		scope.work(1)
		data := scope.getDataNamed(specGet(f, "data"))
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

// parseScaleNice is scale.js's parseScaleNice. Upstream reads an object's
// interval and step with parseLiteral and no scope, so a signal there is a
// TypeError and any other object an unsupported one; an array is an object
// too, with neither member.
func parseScaleNice(nice jsval.Value, scope *Scope) P {
	if isSignalObj(nice) {
		return scope.signalRef(nice.Get("signal").AsString())
	}
	if nice.IsObj() || nice.IsArr() {
		return obj("interval", parseLiteralNoScope(specGet(nice, "interval")), "step", parseLiteralNoScope(specGet(nice, "step")))
	}
	return parseLiteral(nice, scope)
}

// parseLiteralNoScope is scale.js's parseLiteral(v) called without a scope,
// as parseScaleNice and parseScaleInterpolate call it: a plain value is
// itself, a signal reference reads signalRef of undefined.
func parseLiteralNoScope(v jsval.Value) jsval.Value {
	switch {
	case !v.IsObj() && !v.IsArr():
		return v
	case specGet(v, "signal").IsTruthy():
		perr("Cannot read properties of undefined (reading 'signalRef')")
	}
	perr("Unsupported object: %s", stringValue(v))
	return jsval.Undefined
}

// parseScaleInterpolate is scale.js's parseScaleInterpolate:
// parseLiteral(interpolate.type || interpolate), without a scope.
func parseScaleInterpolate(interpolate jsval.Value, params *paramList) {
	lit := specGet(interpolate, "type")
	if !lit.IsTruthy() {
		lit = interpolate
	}
	params.set("interpolate", parseLiteralNoScope(lit))
	if g := specGet(interpolate, "gamma"); !g.IsNullish() {
		params.set("interpolateGamma", parseLiteralNoScope(g))
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

	if rng.IsNullish() {
		// a config.range entry of null, reached through the alias lookup:
		// `range.signal` reads a member of it
		perr("Cannot read properties of %s (reading 'signal')", nullishName(rng))
	}
	switch {
	case isSignalObj(rng):
		return scope.signalRef(rng.Get("signal").AsString())
	case rng.IsStr():
		name := rng.StrValue()
		if config.IsObj() && config.ObjValue().Has(name) {
			if depth >= maxRangeAliases {
				perrLimit("Scale range aliases nest too deeply: %s", quote(rng))
			}
			return parseScaleRangeAlias(extended(scope.meter(), spec, obj("range", config.Get(name))), scope, params, depth+1)
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
		scope.work(1)
		if v.IsArr() {
			// vega-runtime resolves the references of a parameter's array
			// one level deep: a signal inside an array that is itself an
			// element reaches the scale as its reference object ({$ref:
			// id}), an inert object here (as projectionParameter has it).
			inner := parseArray(v, scope).([]P)
			vals := make([]jsval.Value, len(inner))
			for j, o := range inner {
				vals[j] = projectionInert(o, scope)
			}
			out[i] = jsval.Arr(vals)
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
	// `for (const name in proj)`: an object's members, a string's or an
	// array's indexes (each a parameter), nothing for null.
	forInEach(scope.meter(), proj, func(name string, v jsval.Value) {
		if name == "name" {
			return
		}
		params.set(name, parseProjectionParameter(v, name, scope))
	})
	forInEach(scope.meter(), config, func(name string, v jsval.Value) {
		if cur := params.get(name); cur == nil || isNullP(cur) {
			params.set(name, parseProjectionParameter(v, name, scope))
		}
	})
	// `scope.addProjection(proj.name, params)`, a TypeError for null.
	scope.addScaleProj(specText(specGet(proj, "name")), newEntry("projection", nil, params))
}

func isNullP(p P) bool {
	v, ok := p.(jsval.Value)
	return ok && v.IsNullish()
}

func parseProjectionParameter(v jsval.Value, name string, scope *Scope) P {
	return projectionParameter(v, name, scope, false)
}

// projectionParameter is projection.js's parseParameter. nested is an
// element of an array within an array: vega-runtime's parseParameters
// resolves the references of a parameter and of its array's elements, one
// level deep, so a signal deeper than that reaches the operator as its
// reference object ({$ref: id}, no number and no geometry), an inert object
// here.
func projectionParameter(v jsval.Value, name string, scope *Scope, nested bool) P {
	switch {
	case v.IsArr():
		items := v.Items()
		out := make([]P, len(items))
		for i, it := range items {
			scope.work(1)
			out[i] = projectionParameter(it, name, scope, it.IsArr() || nested)
		}
		if nested {
			vals := make([]jsval.Value, len(out))
			for i, o := range out {
				vals[i] = projectionInert(o, scope)
			}
			return jsval.Arr(vals)
		}
		return out
	case !v.IsObj():
		return v
	case v.Get("signal").IsTruthy():
		ref := scope.signalRef(v.Get("signal").AsString())
		if nested {
			return projectionInert(ref, scope)
		}
		return ref
	case name == "fit":
		return v
	}
	perr("Unsupported parameter object: %s", quote(v))
	return nil
}

// projectionInert is a parameter value as an operator receives it without
// resolving it: a reference is an object of no members.
func projectionInert(p P, scope *Scope) jsval.Value {
	switch x := p.(type) {
	case jsval.Value:
		return x
	case *entry:
		return scope.handle(x)
	}
	return jsval.Undefined
}

func (s *Scope) projectionRef(name string) P { return s.scaleRef(name) }

// -- guides --------------------------------------------------------------------

// guideScope adapts the parser scope to what guides needs.
type guideScope struct{ s *Scope }

func (g guideScope) Config() jsval.Value { return g.s.config }
func (g guideScope) Work(n int)          { g.s.work(n) }
func (g guideScope) ScaleType(name string) string {
	if e := g.s.findScale(name); e != nil {
		return g.s.scaleType(name)
	}
	return ""
}

// opRef is what a guide plan's `from` carries for an operator of the view (its
// data, its ticks or legend entries): a Go value on an object of no members,
// so that no JSON a specification or its data spell stands for one (C92).
// Upstream's is {$ref: id}, an operator id the specification cannot know.
type opRef struct{ e *entry }

// handle returns the reference to an entry that guide plans copy into the
// `from` of the marks they generate.
func (s *Scope) handle(e *entry) jsval.Value {
	o := jsval.NewObject(0)
	o.SetHost(opRef{e})
	return jsval.Obj(o)
}

// opRefOf is the entry a handle refers to.
func opRefOf(v jsval.Value) (*entry, bool) {
	o := v.ObjValue()
	if o == nil {
		return nil, false
	}
	r, ok := o.Host().(opRef)
	return r.e, ok
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
	m, err := plan.Mark(scope.handle(data), scope.handle(ticks))
	if err != nil {
		perr("%s", err.Error())
	}
	parseMark(m, scope)
}

func parseTitle(spec jsval.Value, scope *Scope) {
	plan, err := guides.NewTitle(spec, guideScope{scope})
	if err != nil {
		perr("%s", err.Error())
	}
	data := scope.add(mk("collect"))
	data.literal = []jsval.Value{plan.Datum}
	m, err := plan.Mark(scope.handle(data))
	if err != nil {
		perr("%s", err.Error())
	}
	parseMark(m, scope)
}

func parseLegend(spec jsval.Value, scope *Scope) {
	plan, err := guides.NewLegend(spec, guideScope{scope})
	if err != nil {
		perr("%s", err.Error())
	}
	data := scope.add(mk("collect"))
	data.literal = []jsval.Value{plan.Datum}
	en := plan.Entries
	params := plist(
		"type", en.Type,
		"scale", scope.scaleRef(en.Scale),
		"count", scope.objectProperty(en.Count),
		"limit", scope.property(en.Limit),
		"values", scope.objectProperty(en.Values),
		"minstep", scope.property(en.MinStep),
		"formatType", scope.property(en.FormatType),
		"formatSpecifier", scope.property(en.Format),
	)
	// The entries operator exists before the expressions of its tick count and
	// symbol size do, and so ranks below them until the parameters are
	// connected: which operators have run when one throws depends on it.
	entries := scope.add(newEntry("legendentries", nil, params))
	if en.CountExpr != "" {
		if c := params.get("count"); c == nil || !pTruthy(c) {
			params.set("count", scope.signalRef(en.CountExpr))
		}
	}
	if en.SizeExpr != "" {
		fn := scope.parseExpression(en.SizeExpr)
		params.set("size", pExpr{fn: fn})
	}
	m, err := plan.Mark(scope.handle(data), scope.handle(entries))
	if err != nil {
		perr("%s", err.Error())
	}
	parseMark(m, scope)
}

// pTruthy is JavaScript truthiness of a parameter (references are truthy).
func pTruthy(p P) bool {
	if v, ok := p.(jsval.Value); ok {
		return v.IsTruthy()
	}
	return p != nil
}
