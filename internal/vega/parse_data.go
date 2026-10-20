package vega

import (
	_ "embed"
	"sync"

	"github.com/mgilbir/aster/internal/jsval"
)

// definitions.json is the table of transform definitions vega-parser drives its
// parameter parsing from (vega-dataflow's `definition` registry), recorded from
// upstream by gen_definitions.mjs.
//
//go:embed definitions.json
var definitionsJSON []byte

// paramDef is one parameter (or, for type "param", a group of sub-parameters
// selected by key) of a transform definition.
type paramDef struct {
	name     string
	typ      string
	array    bool
	required bool
	expr     bool
	params   []*paramDef
	key      *jsval.Object
}

type transformDef struct {
	typ    string
	meta   tmeta
	params []*paramDef
}

var (
	defsOnce sync.Once
	defsMap  map[string]*transformDef
)

func decodeParam(v jsval.Value) *paramDef {
	d := &paramDef{
		name:     v.Get("name").StrValue(),
		typ:      v.Get("type").StrValue(),
		array:    v.Get("array").IsTruthy(),
		required: v.Get("required").IsTruthy(),
		expr:     v.Get("expr").IsTruthy(),
	}
	if k := v.Get("key"); k.IsObj() {
		d.key = k.ObjValue()
	}
	for _, p := range v.Get("params").Items() {
		d.params = append(d.params, decodeParam(p))
	}
	return d
}

func definitions() map[string]*transformDef {
	defsOnce.Do(func() {
		v, err := jsval.ParseJSON(definitionsJSON)
		if err != nil {
			panic("vega: bad embedded transform definitions: " + err.Error())
		}
		defsMap = map[string]*transformDef{}
		o := v.ObjValue()
		for i := 0; i < o.Len(); i++ {
			dv := o.ValueAt(i)
			d := &transformDef{typ: dv.Get("type").StrValue()}
			m := dv.Get("metadata")
			d.meta = tmeta{
				source:    m.Get("source").IsTruthy(),
				generates: m.Get("generates").IsTruthy(),
				changes:   m.Get("changes").IsTruthy(),
				modifies:  m.Get("modifies").IsTruthy(),
				nomod:     m.Get("nomod").IsTruthy(),
			}
			for _, p := range dv.Get("params").Items() {
				d.params = append(d.params, decodeParam(p))
			}
			defsMap[o.KeyAt(i)] = d
		}
		// Transforms that vega-dataflow registers without a definition entry
		// in the recorded table but that specifications may use.
		if defsMap["collect"] != nil {
			defsMap["collect"].meta.source = true
		}
	})
	return defsMap
}

// parseTransform parses one data transform specification into an entry
// (transform.js). Its definition is vega-dataflow's `definition(type)`:
// `type && type.toLowerCase()`, a TypeError for a truthy type that is no
// string, and no definition for a falsy one.
func parseTransform(spec jsval.Value, scope *Scope) *entry {
	t := specGet(spec, "type")
	var def *transformDef
	if t.IsTruthy() {
		s, ok := t.Strict()
		if !ok {
			perr("type.toLowerCase is not a function")
		}
		def = definitions()[jsval.JSLower(s)]
	}
	if def == nil {
		perr("Unrecognized transform type: %s", stringValue(t))
	}
	typ := jsval.JSLower(def.typ)
	e := newEntry(typ, nil, parseParameters(def.params, spec, scope))
	if sig := specGet(spec, "signal"); sig.IsTruthy() {
		scope.addSignal(specText(sig), scope.proxy(e))
	}
	e.meta = def.meta
	return e
}

func parseParameters(defs []*paramDef, spec jsval.Value, scope *Scope) *paramList {
	params := newParamList()
	for _, pdef := range defs {
		if v := parseParameter(pdef, spec, scope); v != nil {
			params.set(pdef.name, v)
		}
	}
	return params
}

func parseParameter(def *paramDef, spec jsval.Value, scope *Scope) P {
	value := spec.Get(def.name)
	switch {
	case def.typ == "index":
		return parseIndexParameter(spec, scope)
	case value.IsUndefined():
		if def.required {
			perr("Missing required %s parameter: %s", quote(spec.Get("type")), quote(sv(def.name)))
		}
		return nil
	case def.typ == "param":
		return parseSubParameters(def, spec, scope)
	case def.typ == "projection":
		return scope.projectionRefOf(value)
	}
	if def.array && !isSignalObj(value) {
		items := listOf(value, "value", "map")
		out := make([]P, len(items))
		for i, v := range items {
			scope.work(1)
			out[i] = parameterValue(def, v, scope)
		}
		return out
	}
	return parameterValue(def, value, scope)
}

func parameterValue(def *paramDef, value jsval.Value, scope *Scope) P {
	typ := def.typ
	if isSignalObj(value) {
		switch typ {
		case "expr":
			perr("Expression references can not be signals.")
		case "field":
			return scope.fieldRef(value, "")
		case "compare":
			return scope.compareRef(value)
		}
		return scope.signalRef(value.Get("signal").AsString())
	}
	isField := typ == "field"
	expr := def.expr || isField
	switch {
	case expr && isExprObj(value):
		name := ""
		if a := value.Get("as"); a.IsStr() {
			name = a.StrValue()
		}
		return scope.exprRef(value.Get("expr"), name)
	case expr && value.IsObj() && value.Get("field").IsTruthy():
		// fieldRef(value.field, value.as): a truthy `as` of any type names
		// the accessor, as its String()
		name := ""
		if a := value.Get("as"); a.IsTruthy() {
			name = specText(a)
		}
		return specFieldRef(value.Get("field"), name)
	case typ == "expr":
		return pExpr{fn: scope.parseExpressionValue(value)}
	case typ == "data":
		return scope.getDataNamed(value).values
	case isField:
		return specFieldRef(value, "")
	case typ == "compare":
		return scope.compareRef(value)
	}
	return value
}

// specFieldRef is util.js's fieldRef(value, as) as the runtime turns it into
// an accessor (vega-runtime's getField, then vega-util's field): a falsy
// value is no accessor (null), a string is the path, and any other value is
// split as splitAccessPath splits it (jsval.SplitAccessPathOf: an array by
// its joined text cut at its length, a number or boolean to no segment at
// all, which the runtime's code generation then refuses). The accessor of
// a path of other than one segment is named after String(value).
func specFieldRef(v jsval.Value, as string) pField {
	if !v.IsTruthy() {
		return pField{}
	}
	key := fieldCacheKey(specText(v), as)
	if s, ok := v.Strict(); ok {
		return pField{path: s, name: as, key: key}
	}
	segs, err := jsval.SplitAccessPathOf(v, specText(v))
	if err != nil {
		perr("%s", err.Error())
	}
	if len(segs) == 0 {
		// a path of no segment, which SplitFieldPath gives back for "."
		return pField{path: ".", name: as, key: key}
	}
	if as == "" && len(segs) != 1 {
		as = specText(v)
	}
	return pField{path: jsval.FieldPathText(segs), name: as, key: key}
}

func parseIndexParameter(spec jsval.Value, scope *Scope) P {
	from := spec.Get("from")
	if !from.IsStr() {
		perr("Lookup \"from\" parameter must be a string literal.")
	}
	return scope.getData(from.StrValue()).lookupRef(scope, spec.Get("key"))
}

func parseSubParameters(def *paramDef, spec jsval.Value, scope *Scope) P {
	value := spec.Get(def.name)
	if def.array {
		if !value.IsArr() {
			perr("Expected an array of sub-parameters. Instead: %s", quote(value))
		}
		items := value.Items()
		out := make([]P, len(items))
		for i, v := range items {
			scope.work(1)
			out[i] = parseSubParameter(def, v, scope)
		}
		return out
	}
	return parseSubParameter(def, value, scope)
}

func parseSubParameter(def *paramDef, value jsval.Value, scope *Scope) P {
	if value.IsNullish() && len(def.params) > 0 && def.params[0].key != nil && def.params[0].key.Len() > 0 {
		// `pdef.key[k] !== value[k]`: the first key read of null is a
		// TypeError
		perr("Cannot read properties of %s (reading '%s')", value.Kind(), def.params[0].key.KeyAt(0))
	}
	var match *paramDef
	for _, pdef := range def.params {
		ok := true
		if pdef.key != nil {
			for i := 0; i < pdef.key.Len(); i++ {
				if !jsval.SameRef(pdef.key.ValueAt(i), value.Get(pdef.key.KeyAt(i))) {
					ok = false
					break
				}
			}
		}
		if ok {
			match = pdef
			break
		}
	}
	if match == nil {
		perr("Unsupported parameter: %s", quote(value))
	}
	params := parseParameters(match.params, value, scope)
	if match.key != nil {
		for i := 0; i < match.key.Len(); i++ {
			params.set(match.key.KeyAt(i), match.key.ValueAt(i))
		}
	}
	return scope.add(newEntry("params", nil, params))
}

// -- data --------------------------------------------------------------------

// parseData is data.js: `if (data.transform) data.transform.forEach(...)`
// and the same of data.on, which throw for a truthy value that is no array.
func parseData(data jsval.Value, scope *Scope) {
	var transforms []*entry
	if tx := specGet(data, "transform"); tx.IsTruthy() {
		for _, t := range listOf(tx, "data.transform", "forEach") {
			scope.work(1)
			transforms = append(transforms, parseTransform(t, scope))
		}
	}
	nameV := specGet(data, "name")
	if on := specGet(data, "on"); on.IsTruthy() {
		for _, t := range listOf(on, "data.on", "forEach") {
			scope.work(1)
			parseTrigger(t, scope, nameV)
		}
	}
	scope.addDataPipeline(specText(nameV), analyzeData(data, scope, transforms))
}

func collectEntry(ingest *ingestSpec, literal []jsval.Value) *entry {
	e := mk("collect")
	e.ingest = ingest
	e.literal = literal
	e.meta = tmeta{source: true}
	return e
}

// analyzeData builds the operator chain of a data set: its source (values, url
// or other data sets), the transforms with collectors inserted where a
// transform needs a materialised source, and the closing sieve.
func analyzeData(data jsval.Value, scope *Scope, ops []*entry) []*entry {
	var output []*entry
	var source *entry
	var upstream []P
	modify, generate, fromSource := false, false, false

	switch {
	case data.Get("values").IsTruthy(): // JS truthiness: null, 0 and "" mean absent
		if isSignalObj(data.Get("values")) || hasSignal(scope.meter(), data.Get("format")) {
			output = append(output, loadEntry(scope, data))
			source = collectEntry(nil, nil)
			output = append(output, source)
		} else {
			source = collectEntry(&ingestSpec{values: data.Get("values"), format: data.Get("format")}, nil)
			output = append(output, source)
		}
	case data.Get("url").IsTruthy():
		if hasSignal(scope.meter(), data.Get("url")) || hasSignal(scope.meter(), data.Get("format")) {
			output = append(output, loadEntry(scope, data))
			source = collectEntry(nil, nil)
			output = append(output, source)
		} else {
			source = collectEntry(&ingestSpec{url: data.Get("url"), request: true, format: data.Get("format")}, nil)
			output = append(output, source)
		}
	case data.Get("source").IsTruthy():
		for _, d := range arrayOf(data.Get("source")) {
			scope.work(1)
			upstream = append(upstream, scope.getData(d.AsString()).output)
		}
		source = nil
		fromSource = true            // even `source: []` is a (empty) source: the array is truthy
		output = append(output, nil) // populated below
	}

	haveSource := source != nil || fromSource
	for _, t := range ops {
		scope.work(1)
		m := t.meta
		if !haveSource && !m.source {
			source = collectEntry(nil, nil)
			output = append(output, source)
			haveSource = true
		}
		output = append(output, t)
		if m.generates {
			generate = true
		}
		if m.modifies && !generate {
			modify = true
		}
		if m.source {
			haveSource = true
		} else if m.changes {
			haveSource = false
		}
	}

	if fromSource {
		n := len(upstream) - 1
		relay := mk("relay")
		if modify {
			relay.param("derive", jsval.True)
		}
		if n != 0 { // upstream `n ? upstream : upstream[0]`; -1 is truthy
			relay.param("pulse", upstream)
		} else {
			relay.param("pulse", upstream[0])
		}
		output[0] = relay
		if modify || n != 0 {
			rest := append([]*entry{output[0], collectEntry(nil, nil)}, output[1:]...)
			output = rest
		}
	}
	if !haveSource {
		output = append(output, collectEntry(nil, nil))
	}
	output = append(output, mk("sieve"))
	return output
}

func loadEntry(scope *Scope, data jsval.Value) *entry {
	e := mk("load")
	if u := data.Get("url"); u.IsTruthy() {
		e.param("url", scope.property(u))
	}
	if a := data.Get("async"); a.IsTruthy() {
		e.param("async", scope.property(a))
	}
	if v := data.Get("values"); v.IsTruthy() {
		e.param("values", scope.property(v))
	}
	e.param("format", scope.objectProperty(data.Get("format")))
	return e
}

// parseTrigger turns a data trigger into an operator that calls modify() when
// its trigger expression is truthy (trigger.js): the source is built by
// string concatenation, so each member is spliced in as its String().
func parseTrigger(spec jsval.Value, scope *Scope, name jsval.Value) {
	arg := func(k string) string {
		v := specGet(spec, k)
		if v.IsNullish() {
			return "null"
		}
		return specText(v)
	}
	remove, insert, toggle, modify, values := arg("remove"), arg("insert"), arg("toggle"), arg("modify"), arg("values")
	op := scope.add(operatorEntry(nil))
	code := "if(" + specText(specGet(spec, "trigger")) + ",modify(\"" + specText(name) + "\"," +
		insert + "," + remove + "," + toggle + "," + modify + "," + values + "),0)"
	setExprUpdate(op, scope.parseExpression(code))
}
