package vega

import (
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

// extendObj copies src's entries onto dst (vega-util extend).
func extendObj(dst *jsval.Object, src jsval.Value) *jsval.Object {
	if o := src.ObjValue(); o != nil {
		for i := 0; i < o.Len(); i++ {
			dst.Set(o.KeyAt(i), o.ValueAt(i))
		}
	}
	return dst
}

// extended returns a copy of a with b's entries applied.
func extended(a, b jsval.Value) jsval.Value {
	var o *jsval.Object
	if a.IsObj() {
		o = a.ObjValue().Clone()
	} else {
		o = jsval.NewObject(4)
	}
	return jsval.Obj(extendObj(o, b))
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

func rootEncode(specEncode jsval.Value) jsval.Value {
	base := obj(
		"enter", obj("x", obj("value", jsval.Num(0)), "y", obj("value", jsval.Num(0))),
		"update", obj("width", obj("signal", sv("width")), "height", obj("signal", sv("height"))),
	)
	return extendEncode(base, specEncode, nil)
}

// extendEncode merges the encode sets of extra into encode (util.extendEncode).
func extendEncode(encode, extra jsval.Value, skip map[string]bool) jsval.Value {
	eo := encode.ObjValue()
	xo := extra.ObjValue()
	if xo == nil {
		return encode
	}
	for i := 0; i < xo.Len(); i++ {
		name := xo.KeyAt(i)
		if skip != nil && skip[name] {
			continue
		}
		cur := eo.Lookup(name)
		var dst *jsval.Object
		if cur.IsObj() && !cur.IsNullish() {
			dst = cur.ObjValue().Clone()
		} else {
			dst = jsval.NewObject(4)
		}
		eo.Set(name, jsval.Obj(extendObj(dst, xo.ValueAt(i))))
	}
	return encode
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
func collectSignals(spec, config jsval.Value) []jsval.Value {
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
		name := s.Get("name").AsString()
		if i, ok := pre[name]; ok {
			merged := signals[i].ObjValue().Clone()
			extendObj(merged, s)
			signals[i] = jsval.Obj(merged)
			s = signals[i]
		} else {
			signals = append(signals, s)
		}
		seen[name] = true
	}
	for _, s := range arrayOf(config.Get("signals")) {
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
	signals := collectSignals(spec, config)
	for _, s := range signals {
		parseSignal(s, scope)
	}

	desc := spec.Get("description")
	if !desc.IsTruthy() {
		desc = config.Get("description")
	}
	scope.description = desc
	if lay := config.Get("legend").Get("layout"); config.Get("legend").IsObj() {
		scope.legends = scope.objectProperty(lay)
	}
	scope.locale = config.Get("locale")

	input := scope.add(mk("itemstore"))

	enc := scope.add(newEntry("encode", nil, parseEncode(
		rootEncode(spec.Get("encode")), "group", "frame", spec.Get("style"), scope,
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
			parseSignal(s, scope)
		}
	}
	for _, p := range arrayOf(spec.Get("projections")) {
		parseProjection(p, scope)
	}
	for _, s := range scales {
		initScale(s, scope)
	}
	for _, d := range arrayOf(spec.Get("data")) {
		parseData(d, scope)
	}
	for _, s := range scales {
		parseScale(s, scope)
	}
	upd := signals
	if preprocessed != nil {
		upd = preprocessed
	}
	for _, s := range upd {
		parseSignalUpdates(s, scope)
	}
	for _, a := range arrayOf(spec.Get("axes")) {
		parseAxis(a, scope)
	}
	for _, m := range arrayOf(spec.Get("marks")) {
		parseMark(m, scope)
	}
	for _, l := range arrayOf(spec.Get("legends")) {
		parseLegend(l, scope)
	}
	if t := spec.Get("title"); t.IsTruthy() {
		parseTitle(t, scope)
	}
	scope.parseLambdas()
}

func parseSignal(sig jsval.Value, scope *Scope) {
	name := sig.Get("name").AsString()
	if sig.Get("push").IsStr() && sig.Get("push").StrValue() == "outer" {
		if scope.findSignal(name) == nil {
			perr("No prior signal definition for \"outer\" push: %s", quote(sv(name)))
		}
		for _, prop := range []string{"value", "update", "init", "react", "bind"} {
			if !sig.Get(prop).IsUndefined() {
				perr("Invalid property  for \"outer\" push: %s", quote(sv(prop)))
			}
		}
		return
	}
	op := scope.addSignal(name, sig.Get("value"))
	if r := sig.Get("react"); r.IsBool() && !r.BoolValue() {
		op.noReact = true
	}
	// bind is a UI concern and is ignored for static rendering.
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

func parseSignalUpdates(sig jsval.Value, scope *Scope) {
	name := sig.Get("name").AsString()
	op := scope.getSignal(name)
	expr := sig.Get("update")
	if init := sig.Get("init"); init.IsTruthy() {
		if expr.IsTruthy() {
			perr("Signals can not include both init and update expressions.")
		}
		expr = init
		op.initonly = true
	}
	if expr.IsTruthy() {
		setExprUpdate(op, scope.parseExpression(expr.AsString()))
	}
	for _, on := range arrayOf(sig.Get("on")) {
		parseUpdate(on, scope, op)
	}
}

// parseUpdate handles an `on` handler of a signal. Only internal event sources
// (another signal or a scale changing) fire in a static render; DOM event
// streams never do, so those handlers are dropped.
func parseUpdate(spec jsval.Value, scope *Scope, target *entry) {
	events := spec.Get("events")
	if events.IsUndefined() || events.IsNull() {
		perr("Signal update missing events specification.")
	}
	var sources []jsval.Value
	if !events.IsStr() {
		for _, s := range arrayOf(events) {
			if s.IsObj() && (s.Get("signal").IsTruthy() || s.Get("scale").IsTruthy()) {
				sources = append(sources, s)
			}
		}
	}
	if len(sources) == 0 {
		return
	}
	if enc := spec.Get("encode"); !enc.IsNullish() {
		if spec.Get("update").IsTruthy() {
			perr("Signal encode and update are mutually exclusive.")
		}
		return // encode(item(), ...) needs a DOM event
	}
	upd := &updateSpec{target: target}
	update := spec.Get("update")
	switch {
	case update.IsStr():
		upd.update = scope.parseExpression(update.StrValue())
	case update.IsObj() && !update.Get("expr").IsNullish():
		upd.update = scope.parseExpression(update.Get("expr").AsString())
	case update.IsObj() && !update.Get("value").IsNullish():
		upd.value, upd.hasVal = update.Get("value"), true
	case update.IsObj() && !update.Get("signal").IsNullish():
		upd.viaSignal = scope.signalRef(update.Get("signal").AsString()).(*entry)
	default:
		perr("Invalid signal update specification.")
	}
	if spec.Get("force").IsTruthy() {
		upd.force = true
	}
	var source P
	if len(sources) > 1 {
		code := "["
		for i, s := range sources {
			if i > 0 {
				code += ","
			}
			if sc := s.Get("scale"); sc.IsTruthy() {
				code += `scale("` + sc.AsString() + `")`
			} else {
				code += s.Get("signal").AsString()
			}
		}
		code += "]"
		source = scope.signalRef(code)
	} else {
		s := sources[0]
		if s.Get("signal").IsTruthy() {
			source = scope.signalRef(s.Get("signal").AsString())
		} else {
			source = scope.scaleRef(s.Get("scale").AsString())
		}
	}
	u := *upd
	u.source = source.(*entry)
	scope.addUpdate(&u)
}
