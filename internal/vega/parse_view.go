package vega

import (
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// This file ports vega-parser's parsers/view.js, scope.js, signal.js,
// signal-updates.js and update.js: the top-level view, the order in which a
// scope's definitions are parsed, and signals.

func obj(kv ...any) jsval.Value { return jsval.Obj(jsval.ObjectOf(kv...)) }

func sv(s string) jsval.Value { return jsval.Str(s) }

// arrayOf is vega-util's array(): undefined/null give an empty list, an array
// is itself, anything else is wrapped.
func arrayOf(v jsval.Value) []jsval.Value {
	switch v.Kind() {
	case jsval.KindUndefined, jsval.KindNull:
		return nil
	case jsval.KindArr:
		return v.Items()
	}
	return []jsval.Value{v}
}

// extendObj copies src's entries onto dst (vega-util extend: `for (k in
// src) dst[k] = src[k]`, so a string's or an array's indexes too), charging
// each to m (nil: no meter).
//
// An own "__proto__" member (JSON.parse makes one) is assigned as
// `dst.__proto__ = v`: an object (or null) becomes dst's prototype, whose
// members later reads and for-in find after dst's own; they are added as
// members here (no reader of these objects tells own from inherited), and
// any other value is ignored, as the assignment ignores it.
func extendObj(m *budget.Meter, dst *jsval.Object, src jsval.Value) *jsval.Object {
	var proto jsval.Value
	forInEach(m, src, func(k string, v jsval.Value) {
		if k == "__proto__" {
			if v.IsObj() || v.IsArr() || v.IsNull() {
				proto = v
			}
			return
		}
		dst.Set(k, v)
	})
	forInEach(m, proto, func(k string, v jsval.Value) {
		if !dst.Has(k) {
			dst.Set(k, v)
		}
	})
	return dst
}

// extended returns a copy of a with b's entries applied.
func extended(m *budget.Meter, a, b jsval.Value) jsval.Value {
	var o *jsval.Object
	if a.IsObj() {
		charge(m, a.ObjValue().Len())
		o = a.ObjValue().Clone()
	} else {
		o = jsval.NewObject(4)
	}
	return jsval.Obj(extendObj(m, o, b))
}

func isObject(v jsval.Value) bool { return v.IsObj() || v.IsArr() }

// valueOr is upstream's `value(specValue, defaultValue)`: the default when the
// spec value is null or undefined.
func valueOr(v, d jsval.Value) jsval.Value {
	if v.IsNullish() {
		return d
	}
	return v
}

func rootEncode(m *budget.Meter, specEncode jsval.Value) jsval.Value {
	base := obj(
		"enter", obj("x", obj("value", jsval.Num(0)), "y", obj("value", jsval.Num(0))),
		"update", obj("width", obj("signal", sv("width")), "height", obj("signal", sv("height"))),
	)
	return extendEncode(m, base, specEncode, nil)
}

// extendEncode merges the encode sets of extra into encode (util.extendEncode:
// `for (name in extra) encode[name] = extend(encode[name] || {}, extra[name])`).
func extendEncode(m *budget.Meter, encode, extra jsval.Value, skip map[string]bool) jsval.Value {
	eo := encode.ObjValue()
	forInEach(m, extra, func(name string, set jsval.Value) {
		if skip != nil && skip[name] {
			return
		}
		cur := eo.Lookup(name)
		var dst *jsval.Object
		switch {
		case cur.IsObj():
			charge(m, cur.ObjValue().Len())
			dst = cur.ObjValue().Clone()
		case cur.IsTruthy():
			// extend() assigns to a primitive, which strict code refuses.
			if k, ok := firstForInKey(set); ok {
				perr("Cannot create property '%s' on %s", k, primitiveDesc(cur))
			}
			return
		default:
			dst = jsval.NewObject(4)
		}
		eo.Set(name, jsval.Obj(extendObj(m, dst, set)))
	})
	return encode
}

// firstForInKey is the first key for-in enumerates of v, if any.
func firstForInKey(v jsval.Value) (string, bool) {
	key, found := "", false
	forInEach(nil, v, func(k string, _ jsval.Value) {
		if !found {
			key, found = k, true
		}
	})
	return key, found
}

// primitiveDesc describes a primitive as V8's "Cannot create property"
// TypeError does: its type and its text.
func primitiveDesc(v jsval.Value) string {
	switch v.Kind() {
	case jsval.KindStr:
		return "string '" + v.StrValue() + "'"
	case jsval.KindNum:
		return "number '" + jsval.JSNumberString(v.NumValue()) + "'"
	case jsval.KindBool:
		return "boolean '" + strconv.FormatBool(v.BoolValue()) + "'"
	}
	return "'" + specText(v) + "'"
}

func parseAutosize(v jsval.Value) jsval.Value {
	if v.IsObj() || v.IsArr() {
		return v
	}
	if !v.IsTruthy() {
		return obj("type", sv("pad"))
	}
	return obj("type", v)
}

func numOr0(v jsval.Value) jsval.Value {
	n := jsval.ToNumber(v)
	if n == 0 || n != n {
		return jsval.Num(0)
	}
	return jsval.Num(n)
}

func paddingObject(v jsval.Value) jsval.Value {
	return obj("top", v, "bottom", v, "left", v, "right", v)
}

func parsePadding(spec jsval.Value) jsval.Value {
	if !isObject(spec) {
		return paddingObject(numOr0(spec))
	}
	if isSignalObj(spec) {
		return spec
	}
	return obj(
		"top", numOr0(spec.Get("top")), "bottom", numOr0(spec.Get("bottom")),
		"left", numOr0(spec.Get("left")), "right", numOr0(spec.Get("right")),
	)
}

func signalObject(name string, v jsval.Value) jsval.Value {
	if isSignalObj(v) {
		return obj("name", sv(name), "update", v.Get("signal"))
	}
	return obj("name", sv(name), "value", v)
}

// collectSignals gathers the top-level signals. The built-in ones (background,
// autosize, padding, width, height) take their initial value from the spec, or
// the config, and a spec signal of the same name is merged over them; config
// signals are added only when the spec does not define the name.
func collectSignals(m *budget.Meter, spec, config jsval.Value) []jsval.Value {
	get := func(name string) jsval.Value { return valueOr(spec.Get(name), config.Get(name)) }
	orZero := func(v jsval.Value) jsval.Value {
		if v.IsTruthy() {
			return v
		}
		return jsval.Num(0)
	}
	signals := []jsval.Value{
		signalObject("background", get("background")),
		signalObject("autosize", parseAutosize(get("autosize"))),
		signalObject("padding", parsePadding(get("padding"))),
		signalObject("width", orZero(get("width"))),
		signalObject("height", orZero(get("height"))),
	}
	pre := map[string]int{}
	for i, s := range signals {
		pre[s.Get("name").StrValue()] = i
	}
	seen := map[string]bool{}
	for _, s := range arrayOf(spec.Get("signals")) {
		charge(m, 1)
		name := s.Get("name").AsString()
		if i, ok := pre[name]; ok {
			merged := signals[i].ObjValue().Clone()
			extendObj(m, merged, s)
			signals[i] = jsval.Obj(merged)
		} else {
			signals = append(signals, s)
		}
		seen[name] = true
	}
	for _, s := range arrayOf(config.Get("signals")) {
		charge(m, 1)
		name := s.Get("name").AsString()
		if _, ok := pre[name]; ok || seen[name] {
			continue
		}
		signals = append(signals, s)
	}
	return signals
}

// parseView builds the operator graph of a whole specification.
func parseView(spec jsval.Value, scope *Scope) {
	config := scope.config

	scope.root = scope.add(operatorEntry(nil))
	signals := collectSignals(scope.meter(), spec, config)
	for _, s := range signals {
		scope.work(1)
		parseSignal(s, scope)
	}

	desc := spec.Get("description")
	if !desc.IsTruthy() {
		desc = config.Get("description")
	}
	scope.description = desc
	// `scope.legends = scope.objectProperty(config.legend && config.legend.layout)`:
	// a falsy legend config is itself the layout parameter
	if lv := config.Get("legend"); lv.IsTruthy() {
		scope.legends = scope.objectProperty(jsval.Member(lv, "layout"))
	} else {
		scope.legends = scope.objectProperty(lv)
	}
	scope.locale = config.Get("locale")

	input := scope.add(mk("itemstore"))

	enc := scope.add(newEntry("encode", nil, parseEncode(
		rootEncode(scope.meter(), spec.Get("encode")), "group", true, "frame", spec.Get("style"), scope,
		plist("pulse", input),
	)))

	layout := mk("viewlayout",
		"layout", scope.objectProperty(spec.Get("layout")),
		"legends", scope.legends,
		"autosize", scope.signalRef("autosize"),
		"mark", scope.root,
		"pulse", enc,
	)
	parent := scope.add(layout)
	scope.operators = scope.operators[:len(scope.operators)-1]

	scope.pushState(enc, parent, nil)
	parseScopeSpec(spec, scope, signals)
	scope.operators = append(scope.operators, parent)

	op := scope.add(mk("bound", "mark", scope.root, "pulse", parent))
	op = scope.add(mk("render", "pulse", op))
	op = scope.add(mk("sieve", "pulse", op))

	scope.addData("root", newDataScope(scope, input, input, op, nil))
}

// parseScopeSpec parses the definitions of one scope in upstream's order:
// signals, projections, scale initialisation, data, scales, signal updates,
// axes, marks, legends, title.
func parseScopeSpec(spec jsval.Value, scope *Scope, preprocessed []jsval.Value) {
	if scope.depth > maxContextDepth {
		perrLimit("group nesting too deep")
	}
	signals := arrayOf(spec.Get("signals"))
	scales := arrayOf(spec.Get("scales"))

	if preprocessed == nil {
		for _, s := range signals {
			scope.work(1)
			parseSignal(s, scope)
		}
	}
	for _, p := range arrayOf(spec.Get("projections")) {
		scope.work(1)
		parseProjection(p, scope)
	}
	for _, s := range scales {
		scope.work(1)
		initScale(s, scope)
	}
	for _, d := range arrayOf(spec.Get("data")) {
		scope.work(1)
		parseData(d, scope)
	}
	for _, s := range scales {
		scope.work(1)
		parseScale(s, scope)
	}
	upd := signals
	if preprocessed != nil {
		upd = preprocessed
	}
	for _, s := range upd {
		scope.work(1)
		parseSignalUpdates(s, scope)
	}
	for _, a := range arrayOf(spec.Get("axes")) {
		scope.work(1)
		parseAxis(a, scope)
	}
	for _, m := range arrayOf(spec.Get("marks")) {
		scope.work(1)
		parseMark(m, scope)
	}
	for _, l := range arrayOf(spec.Get("legends")) {
		scope.work(1)
		parseLegend(l, scope)
	}
	if t := spec.Get("title"); t.IsTruthy() {
		parseTitle(t, scope)
	}
	scope.parseLambdas()
}

// parseSignal is signal.js: a signal definition, or an "outer" push of a
// signal an enclosing scope defines. Its name is any value, read as the
// property key String(name).
func parseSignal(sig jsval.Value, scope *Scope) {
	nameV := specGet(sig, "name")
	name := specText(nameV)
	if strictIs(specGet(sig, "push"), "outer") {
		if !scope.hasSignalOrInherited(name) {
			perr("No prior signal definition for \"outer\" push: %s", stringValue(nameV))
		}
		for _, prop := range []string{"value", "update", "init", "react", "bind"} {
			if !specGet(sig, prop).IsUndefined() {
				perr("Invalid property  for \"outer\" push: %s", stringValue(sv(prop)))
			}
		}
		return
	}
	op := scope.addSignal(name, specGet(sig, "value"))
	if r := specGet(sig, "react"); r.IsBool() && !r.BoolValue() {
		op.noReact = true
	}
	// A binding is a UI concern, ignored by a static render; a nested scope
	// has no binding list, which upstream's addBinding refuses.
	if specGet(sig, "bind").IsTruthy() && scope.parentScope != nil {
		perr("Nested signals do not support binding: %s", stringValue(nameV))
	}
}

// setExprUpdate installs a compiled update expression and its dependencies on
// an operator entry.
func setExprUpdate(e *entry, f *exprFn) {
	e.update = f
	for _, d := range f.deps {
		if e.params == nil {
			e.params = newParamList()
		}
		e.params.set(d.name, d.e)
	}
}

// parseSignalUpdates is signal-updates.js: a signal's update (or init)
// expression and its event handlers.
func parseSignalUpdates(sig jsval.Value, scope *Scope) {
	nameV := specGet(sig, "name")
	op := scope.getSignalNamed(nameV)
	expr := specGet(sig, "update")
	if init := specGet(sig, "init"); init.IsTruthy() {
		if expr.IsTruthy() {
			perr("Signals can not include both init and update expressions.")
		}
		expr = init
		if op != nil {
			op.initonly = true
		}
	}
	if expr.IsTruthy() {
		f := scope.parseExpressionValue(expr)
		if op != nil {
			setExprUpdate(op, f)
		}
	}
	if on := specGet(sig, "on"); on.IsTruthy() {
		for _, u := range listOf(on, "signal.on", "forEach") {
			scope.work(1)
			parseUpdate(u, scope, op)
		}
	}
}

// parseUpdate is update.js: an `on` handler of a signal. Only internal event
// sources (another signal or a scale changing) fire in a static render; the
// DOM and timer streams never do, but every handler is parsed as upstream
// parses it (its event selector, its streams and filters, its update), so a
// handler upstream refuses fails here too. A nil target is a signal whose
// name upstream resolves to an Object.prototype member: no operator to
// update.
func parseUpdate(spec jsval.Value, scope *Scope, target *entry) {
	encode := specGet(spec, "encode")
	events := specGet(spec, "events")
	update := specGet(spec, "update")
	if !events.IsTruthy() { // `if (!events)`
		perr("Signal update missing events specification.")
	}
	if events.IsStr() {
		source := "view"
		if scope.parentScope != nil {
			source = "scope"
		}
		events = jsval.Arr(parseEventSelector(scope, events.StrValue(), source))
	}
	var sources, streams []jsval.Value
	for _, s := range arrayOf(events) {
		scope.work(1)
		if specGet(s, "signal").IsTruthy() || specGet(s, "scale").IsTruthy() {
			sources = append(sources, s)
		} else {
			streams = append(streams, s)
		}
	}
	if !encode.IsNullish() {
		if update.IsTruthy() {
			perr("Signal encode and update are mutually exclusive.")
		}
		update = sv("encode(item()," + stringValue(encode) + ")")
	}
	upd := &updateSpec{target: target}
	switch {
	case update.IsStr():
		upd.update = scope.parseExpression(update.StrValue())
	case !specGet(update, "expr").IsNullish():
		upd.update = scope.parseExpressionValue(specGet(update, "expr"))
	case !specGet(update, "value").IsNullish():
		upd.value, upd.hasVal = specGet(update, "value"), true
	case !specGet(update, "signal").IsNullish():
		upd.viaSignal = entryOf(scope.signalRef(specText(specGet(update, "signal"))))
	default:
		perr("Invalid signal update specification.")
	}
	if specGet(spec, "force").IsTruthy() {
		upd.force = true
	}
	var internal P
	if len(sources) > 1 {
		// mergeSources: '[' + sources.map(s => s.scale ? 'scale("' + s.scale
		// + '")' : s.signal) + ']', the array joined with commas (null and
		// undefined as nothing). Built in one buffer: concatenating per
		// source is quadratic in the number of sources.
		var code strings.Builder
		code.WriteByte('[')
		for i, s := range sources {
			scope.work(1)
			if i > 0 {
				code.WriteByte(',')
			}
			if sc := specGet(s, "scale"); sc.IsTruthy() {
				code.WriteString(`scale("` + specText(sc) + `")`)
			} else {
				code.WriteString(specText(specGet(s, "signal")))
			}
		}
		code.WriteByte(']')
		internal = scope.signalRef(code.String())
	} else if len(sources) == 1 {
		s := sources[0]
		if sig := specGet(s, "signal"); sig.IsTruthy() {
			internal = scope.signalRef(specText(sig))
		} else {
			internal = scope.scaleRef(specText(specGet(s, "scale")))
		}
	}
	// The DOM and timer streams: parsed, never fired.
	if len(streams) > 1 {
		parseEventStream(obj("merge", jsval.Arr(streams)), scope, 0)
	} else if len(streams) == 1 {
		parseEventStream(streams[0], scope, 0)
	}
	if internal == nil || target == nil {
		return
	}
	src, ok := internal.(*entry)
	if !ok {
		return
	}
	u := *upd
	u.source = src
	scope.addUpdate(&u)
}
