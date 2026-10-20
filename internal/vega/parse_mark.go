package vega

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// This file ports vega-parser's parsers/mark.js and parsers/marks/*: how a mark
// definition becomes the chain of operators that joins data to items, encodes
// them, lays them out and bounds them; and how group marks open sub-scopes,
// with or without faceting.

// markDef is the mark description a Mark operator creates its scenegraph mark
// from (upstream's markdef object).
type markDef struct {
	typ         scene.MarkType
	looseType   bool
	name        string
	named       bool
	described   bool
	role        string
	zindex      float64
	aria        scene.Tri
	description string
}

// markType is how upstream reads a mark's type: vega-scenegraph looks its
// renderer up by the property key String(type), so ["rect"] draws as a
// rect (loose: no `=== 'rect'` holds for it) and a type it has no renderer
// for fails as the mark is bounded and drawn (scene.MarkInvalid), not as
// it is parsed.
func markType(t jsval.Value) (typ scene.MarkType, loose bool) {
	s, ok := t.Strict()
	if !ok {
		s, loose = specText(t), true
	}
	typ, _ = scene.ParseMarkType(s)
	return typ, loose
}

// markRole is marks/role.js: `role = spec.role || ”`, then
// role.startsWith(...), which a truthy role that is no string makes a
// TypeError.
func markRole(spec jsval.Value) string {
	var role string
	if r := specGet(spec, "role"); r.IsTruthy() {
		s, ok := r.Strict()
		if !ok {
			perr("role.startsWith is not a function")
		}
		role = s
	}
	if strings.HasPrefix(role, "axis") || strings.HasPrefix(role, "legend") || strings.HasPrefix(role, "title") {
		return role
	}
	if strictIs(specGet(spec, "type"), "group") {
		return "scope"
	}
	if role == "" {
		return "mark"
	}
	return role
}

// definition is marks/definition.js: `name: spec.name || undefined`,
// `role: spec.role || role(spec)`, `zindex: +spec.zindex || undefined`.
func definition(spec jsval.Value) *markDef {
	d := &markDef{}
	d.typ, d.looseType = markType(specGet(spec, "type"))
	if n := specGet(spec, "name"); n.IsTruthy() {
		d.name, d.named = specText(n), true
	}
	if r := specGet(spec, "role"); r.IsTruthy() {
		d.role = specText(r)
	} else {
		d.role = markRole(spec)
	}
	if z := jsval.ToNumber(specGet(spec, "zindex")); z != 0 && z == z {
		d.zindex = z
	}
	// The mark keeps the value as given; the renderer tests `mark.aria ===
	// false`, so only false hides it.
	d.aria = scene.FalseOf(specGet(spec, "aria"))
	if desc := specGet(spec, "description"); desc.IsTruthy() {
		d.description, d.described = specText(desc), true
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
	have := false // `expr` is a truthy string; the signal's text may be empty ([])
	if clip.IsObj() {
		param := func(v jsval.Value) string {
			if isSignalObj(v) {
				return v.Get("signal").AsString()
			}
			return stringValue(v)
		}
		switch {
		case clip.Get("signal").IsTruthy():
			code, have = clip.Get("signal").AsString(), true
		case clip.Get("path").IsTruthy():
			code, have = "pathShape("+param(clip.Get("path"))+")", true
		case clip.Get("sphere").IsTruthy():
			code, have = "geoShape("+param(clip.Get("sphere"))+", {type: \"Sphere\"})", true
		}
	}
	if have {
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

// getDataRef resolves the data reference of a `from` or facet specification:
// an operator a guide plan refers to (opRef), or the output of a named data
// set. Upstream reads any truthy `$ref` there as an operator id (`from.$ref ?
// from : from.data && from.data.$ref ? from.data : …`), which the runtime
// then fails to find ("Operator not defined"); the ids are upstream's own
// numbering, which a specification cannot know, so one a specification
// spells is that error here.
func getDataRef(from jsval.Value, scope *Scope) P {
	if e, ok := opRefOf(from); ok {
		return e
	}
	if r := from.Get("$ref"); r.IsTruthy() {
		perr("Operator not defined: %s", jsval.StringPrefix(r, jsval.MessageBytes))
	}
	d := from.Get("data")
	if e, ok := opRefOf(d); ok {
		return e
	}
	if r := d.Get("$ref"); d.IsTruthy() && r.IsTruthy() {
		perr("Operator not defined: %s", jsval.StringPrefix(r, jsval.MessageBytes))
	}
	return scope.getData(d.AsString()).output
}

func parseMarkData(from jsval.Value, group bool, scope *Scope) facetInput {
	var in facetInput
	var dataRef P
	// `if (!from)`: a falsy from (false, "", 0) is the singleton too.
	if !from.IsTruthy() {
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
			// `if (!from.data)`: truthiness, so "" aggregates facet.data
			if !from.Get("data").IsTruthy() {
				spec := jsval.NewObject(4)
				spec.Set("type", sv("aggregate"))
				spec.Set("groupby", jsval.Arr(arrayOf(facet.Get("groupby"))))
				extendObj(scope.meter(), spec, facet.Get("aggregate"))
				op := parseTransform(jsval.Obj(spec), scope)
				op.param("key", scope.keyRef(facet.Get("groupby"), false))
				op.param("pulse", getDataRef(facet, scope))
				dataRef = scope.add(op)
				in.parent = dataRef
			} else {
				// ref(scope.getData(from.data).aggregate) reads .id of the
				// data set's last aggregate, which a plain data set lacks.
				ds := scope.getData(from.Get("data").AsString())
				if ds.aggregate == nil {
					nm := "undefined"
					if ds.aggrNull {
						nm = "null"
					}
					perrThrow("Cannot read properties of " + nm + " (reading 'id')")
				}
				in.parent = ds.aggregate
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
	typeV := specGet(spec, "type")
	typ := specText(typeV) // the key config[type] and the encoders read
	group := strictIs(typeV, "group")
	var facet jsval.Value
	if from := specGet(spec, "from"); from.IsTruthy() {
		facet = specGet(from, "facet")
	}
	hasFacet := facet.IsTruthy()
	overlap := specGet(spec, "overlap")

	layout := specGet(spec, "layout").IsTruthy() || role == "scope" || role == "frame"
	nested := role == "mark" || layout || hasFacet

	input := parseMarkData(spec.Get("from"), group, scope)

	key := input.key
	if key == nil && specGet(spec, "key").IsTruthy() {
		key = specFieldRef(specGet(spec, "key"), "")
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
		specGet(spec, "encode"), typ, typeV.IsStr(), role, specGet(spec, "style"), scope,
		plist("mod", jsval.False, "pulse", markOp),
	)))
	enc.params.set("parent", scope.encode())

	op := enc
	var transforms []jsval.Value
	if tx := specGet(spec, "transform"); tx.IsTruthy() {
		transforms = listOf(tx, "spec.transform", "forEach")
	}
	for _, t := range transforms {
		scope.work(1)
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

	// `if (spec.name != null)`: a name of 0 or "" names a data set too.
	if name := spec.Get("name"); !name.IsNullish() {
		scope.addData(name.AsString(), newDataScope(scope, store, render, sieve, nil))
		var triggers []jsval.Value
		if on := specGet(spec, "on"); on.IsTruthy() {
			triggers = listOf(on, "spec.on", "forEach")
		}
		for _, on := range triggers {
			scope.work(1)
			if specGet(on, "insert").IsTruthy() || specGet(on, "remove").IsTruthy() || specGet(on, "toggle").IsTruthy() {
				perr("Marks only support modify triggers.")
			}
			parseTrigger(on, scope, name)
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
