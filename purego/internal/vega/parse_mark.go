package vega

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// This file ports vega-parser's parsers/mark.js and parsers/marks/*: how a mark
// definition becomes the chain of operators that joins data to items, encodes
// them, lays them out and bounds them; and how group marks open sub-scopes,
// with or without faceting.

// markDef is the mark description a Mark operator creates its scenegraph mark
// from (upstream's markdef object).
type markDef struct {
	typ         scene.MarkType
	name        string
	role        string
	zindex      float64
	aria        scene.Tri
	description string
}

func markRole(spec jsval.Value) string {
	role := spec.Get("role").AsString()
	if spec.Get("role").IsNullish() {
		role = ""
	}
	if strings.HasPrefix(role, "axis") || strings.HasPrefix(role, "legend") || strings.HasPrefix(role, "title") {
		return role
	}
	if spec.Get("type").AsString() == "group" {
		return "scope"
	}
	if role == "" {
		return "mark"
	}
	return role
}

func definition(spec jsval.Value) *markDef {
	t, ok := scene.ParseMarkType(spec.Get("type").AsString())
	if !ok {
		perr("Unrecognized mark type: %s", quote(spec.Get("type")))
	}
	d := &markDef{typ: t}
	if n := spec.Get("name"); n.IsTruthy() {
		d.name = n.AsString()
	}
	if r := spec.Get("role"); r.IsTruthy() {
		d.role = r.AsString()
	} else {
		d.role = markRole(spec)
	}
	if z := jsval.ToNumber(spec.Get("zindex")); z != 0 && z == z {
		d.zindex = z
	}
	if a := spec.Get("aria"); !a.IsNullish() {
		d.aria = scene.B(a.AsBoolean())
	}
	if desc := spec.Get("description"); desc.IsTruthy() {
		d.description = desc.AsString()
	}
	return d
}

func parseInteractive(spec jsval.Value, scope *Scope) P {
	if isSignalObj(spec) {
		return scope.signalRef(spec.Get("signal").AsString())
	}
	if spec.IsBool() && !spec.BoolValue() {
		return jsval.False
	}
	return jsval.True
}

func parseClip(clip jsval.Value, scope *Scope) P {
	var code string
	if clip.IsObj() {
		param := func(v jsval.Value) string {
			if isSignalObj(v) {
				return v.Get("signal").AsString()
			}
			return stringValue(v)
		}
		switch {
		case clip.Get("signal").IsTruthy():
			code = clip.Get("signal").AsString()
		case clip.Get("path").IsTruthy():
			code = "pathShape(" + param(clip.Get("path")) + ")"
		case clip.Get("sphere").IsTruthy():
			code = "geoShape(" + param(clip.Get("sphere")) + ", {type: \"Sphere\"})"
		}
	}
	if code != "" {
		return scope.signalRef(code)
	}
	return jsval.Bool(clip.IsTruthy())
}

// facetInput is the result of resolving a mark's `from`.
type facetInput struct {
	key    P
	pulse  P
	parent P
}

// getDataRef resolves the data reference of a `from` or facet specification: an
// opaque operator handle, or the output of a named data set.
func getDataRef(from jsval.Value, scope *Scope) P {
	if h := from.Get("$ref"); h.IsStr() {
		return scope.resolveHandle(h.StrValue())
	}
	if d := from.Get("data"); d.IsObj() && d.Get("$ref").IsStr() {
		return scope.resolveHandle(d.Get("$ref").StrValue())
	}
	return scope.getData(from.Get("data").AsString()).output
}

func parseMarkData(from jsval.Value, group bool, scope *Scope) facetInput {
	var in facetInput
	var dataRef P
	if from.IsNullish() {
		e := scope.add(mk("collect"))
		e.literal = []jsval.Value{jsval.Obj(jsval.NewObject(0))}
		dataRef = e
	} else if facet := from.Get("facet"); facet.IsTruthy() {
		if !group {
			perr("Only group marks can be faceted.")
		}
		if !facet.Get("field").IsNullish() {
			dataRef = getDataRef(facet, scope)
			in.parent = dataRef
		} else {
			if from.Get("data").IsNullish() || from.Get("data").IsUndefined() {
				spec := jsval.NewObject(4)
				spec.Set("type", sv("aggregate"))
				spec.Set("groupby", jsval.Arr(arrayOf(facet.Get("groupby"))))
				extendObj(spec, facet.Get("aggregate"))
				op := parseTransform(jsval.Obj(spec), scope)
				op.param("key", scope.keyRef(facet.Get("groupby"), false))
				op.param("pulse", getDataRef(facet, scope))
				dataRef = scope.add(op)
				in.parent = dataRef
			} else {
				in.parent = scope.getData(from.Get("data").AsString()).aggregate
			}
			in.key = scope.keyRef(facet.Get("groupby"), true)
		}
	}
	if dataRef == nil {
		dataRef = getDataRef(from, scope)
	}
	in.pulse = dataRef
	return in
}

func parseMark(spec jsval.Value, scope *Scope) {
	role := markRole(spec)
	typ := spec.Get("type").AsString()
	group := typ == "group"
	facet := spec.Get("from").Get("facet")
	hasFacet := facet.IsTruthy()
	overlap := spec.Get("overlap")

	layout := spec.Get("layout").IsTruthy() || role == "scope" || role == "frame"
	nested := role == "mark" || layout || hasFacet

	input := parseMarkData(spec.Get("from"), group, scope)

	var key P = input.key
	if key == nil && spec.Get("key").IsTruthy() {
		key = pField{path: spec.Get("key").AsString()}
	}
	join := scope.add(mk("datajoin", "key", key, "pulse", input.pulse, "clean", jsval.Bool(!group)))
	store := scope.add(mk("itemstore", "pulse", join))

	markOp := scope.add(mk("mark",
		"markdef", definition(spec),
		"interactive", parseInteractive(spec.Get("interactive"), scope),
		"clip", parseClip(spec.Get("clip"), scope),
		"context", pContext{},
		"groups", scope.lookup(),
		"parent", func() P {
			if scope.findSignal("parent") != nil {
				return scope.signalRef("parent")
			}
			return nil
		}(),
		"index", jsval.Int(scope.nextMarkpath()),
		"pulse", store,
	))

	enc := scope.add(newEntry("encode", nil, parseEncode(
		spec.Get("encode"), typ, role, spec.Get("style"), scope,
		plist("mod", jsval.False, "pulse", markOp),
	)))
	enc.params.set("parent", scope.encode())

	op := enc
	for _, t := range arrayOf(spec.Get("transform")) {
		tx := parseTransform(t, scope)
		if tx.meta.generates || tx.meta.changes {
			perr("Mark transforms should not generate new data.")
		}
		if !tx.meta.nomod {
			enc.params.set("mod", jsval.True)
		}
		tx.param("pulse", op)
		op = scope.add(tx)
	}

	if s := spec.Get("sort"); s.IsTruthy() {
		op = scope.add(mk("sortitems", "sort", scope.compareRef(s), "pulse", op))
	}

	encodeRef := op

	var layoutEntry *entry
	if hasFacet || layout {
		layoutEntry = scope.add(mk("viewlayout",
			"layout", scope.objectProperty(spec.Get("layout")),
			"legends", scope.legends,
			"mark", markOp,
			"pulse", encodeRef,
		))
	}

	var boundIn P = encodeRef
	if layoutEntry != nil {
		boundIn = layoutEntry
	}
	bound := scope.add(mk("bound", "mark", markOp, "pulse", boundIn))
	var boundRef P = bound

	if group {
		var ops []*entry
		if nested {
			scope.operators = scope.operators[:len(scope.operators)-1]
			if layoutEntry != nil {
				scope.operators = scope.operators[:len(scope.operators)-1]
			}
		}
		var lookupP P = join
		var parentP P = bound
		if layoutEntry != nil {
			parentP = layoutEntry
		}
		scope.pushState(encodeRef, parentP, lookupP)
		switch {
		case hasFacet:
			parseFacet(spec, scope, input)
		case nested:
			parseSubflow(spec, scope, input)
		default:
			// A guide group: its content lives in the enclosing scope.
			scope.depth++
			parseScopeSpec(spec, scope, nil)
			scope.depth--
		}
		scope.popState()
		_ = ops
		if nested {
			if layoutEntry != nil {
				scope.operators = append(scope.operators, layoutEntry)
			}
			scope.operators = append(scope.operators, bound)
		}
	}

	if overlap.IsTruthy() {
		boundRef = parseOverlap(overlap, boundRef, scope)
	}

	render := scope.add(mk("render", "pulse", boundRef))
	sieve := scope.add(mk("sieve", "pulse", render))
	sieve.parent = entryOf(scope.parent())

	if name := spec.Get("name"); !name.IsNullish() && name.IsTruthy() {
		scope.addData(name.AsString(), newDataScope(scope, store, render, sieve, nil))
		for _, on := range arrayOf(spec.Get("on")) {
			if on.Get("insert").IsTruthy() || on.Get("remove").IsTruthy() || on.Get("toggle").IsTruthy() {
				perr("Marks only support modify triggers.")
			}
			parseTrigger(on, scope, name.AsString())
		}
	}
}

func entryOf(p P) *entry {
	e, _ := p.(*entry)
	return e
}

// plist builds a parameter list from alternating names and values.
func plist(kv ...any) *paramList {
	l := newParamList()
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == nil {
			continue
		}
		l.set(kv[i].(string), kv[i+1])
	}
	return l
}

func parseOverlap(overlap jsval.Value, source P, scope *Scope) P {
	method := overlap.Get("method")
	bound := overlap.Get("bound")
	sep := overlap.Get("separation")

	sigOr := func(v jsval.Value) P {
		if isSignalObj(v) {
			return scope.signalRef(v.Get("signal").AsString())
		}
		return v
	}
	params := plist("separation", sigOr(sep), "method", sigOr(method), "pulse", source)
	if o := overlap.Get("order"); !o.IsNullish() && o.IsTruthy() {
		params.set("sort", scope.compareRef(obj("field", o)))
	}
	if bound.IsTruthy() {
		tol := bound.Get("tolerance")
		if isSignalObj(tol) {
			params.set("boundTolerance", scope.signalRef(tol.Get("signal").AsString()))
		} else {
			params.set("boundTolerance", jsval.Num(jsval.ToNumber(tol)))
		}
		params.set("boundScale", scope.scaleRef(bound.Get("scale").AsString()))
		params.set("boundOrient", bound.Get("orient"))
	}
	return scope.add(newEntry("overlap", nil, params))
}

func parseSubflow(spec jsval.Value, scope *Scope, input facetInput) {
	op := scope.add(mk("prefacet", "pulse", input.pulse))
	sub := scope.fork()
	sub.add(mk("sieve"))
	sub.addSignal("parent", jsval.Null)
	parseScopeSpec(spec, sub, nil)
	op.param("subflow", pSubflow{spec: sub.toRuntime()})
}

func parseFacet(spec jsval.Value, scope *Scope, group facetInput) {
	facet := spec.Get("from").Get("facet")
	name := facet.Get("name")
	data := getDataRef(facet, scope)
	if !name.IsTruthy() {
		perr("Facet must have a name: %s", quote(facet))
	}
	if facet.Get("data").IsNullish() || !facet.Get("data").IsTruthy() {
		perr("Facet must reference a data set: %s", quote(facet))
	}
	var op *entry
	switch {
	case facet.Get("field").IsTruthy():
		op = scope.add(mk("prefacet", "field", scope.fieldRef(facet.Get("field"), ""), "pulse", data))
	case facet.Get("groupby").IsTruthy():
		var gp P
		if group.parent != nil {
			gp = scope.proxy(group.parent)
		}
		op = scope.add(mk("facet", "key", scope.keyRef(facet.Get("groupby"), false), "group", gp, "pulse", data))
	default:
		perr("Facet must specify groupby or field: %s", quote(facet))
	}
	sub := scope.fork()
	source := sub.add(mk("collect"))
	values := sub.add(mk("sieve", "pulse", source))
	sub.addData(name.AsString(), newDataScope(sub, source, source, values, nil))
	sub.addSignal("parent", jsval.Null)
	parseScopeSpec(spec, sub, nil)
	op.param("subflow", pSubflow{spec: sub.toRuntime()})
}
