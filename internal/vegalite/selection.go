package vegalite

import (
	"strings"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

// Selection parameters — vega-lite/src/compile/selection/*.ts. A selection
// component keeps the parsed parameter properties in props (its keys mirror
// upstream's selection component object) plus typed projection data.

const (
	storeSuffix        = "_store"
	tupleSuffix        = "_tuple"
	modifySuffix       = "_modify"
	tupleFields        = "_tuple_fields"
	brushSuffix        = "_brush"
	scaleTrigger       = "_scale_trigger"
	geoInitTick        = "geo_interval_init_tick"
	initSuffix         = "_init"
	centerSuffix       = "_center"
	toggleSuffix       = "_toggle"
	vlSelectionResolve = "vlSelectionResolve"
	curr               = "_curr"
)

type selProjItem struct {
	field      string
	channel    string
	typ        string
	index      int
	dataSignal string
	visSignal  string
	hasVisual  bool
	geoChannel string
	hasLegend  bool
	isChannel  bool // built from an encoding channel (field, channel, type order)
}

func (p *selProjItem) assembled() Value {
	o := jsval.NewObject(4)
	if p.isChannel {
		o.Set("field", jsval.Str(replacePathInField(p.field)))
		o.Set("channel", strOrUndef(p.channel))
		o.Set("type", jsval.Str(p.typ))
		if p.geoChannel != "" {
			o.Set("geoChannel", jsval.Str(p.geoChannel))
		}
	} else {
		o.Set("type", jsval.Str(p.typ))
		o.Set("field", jsval.Str(replacePathInField(p.field)))
	}
	return jsval.Obj(o)
}

type selProjection struct {
	items          []*selProjItem
	hasChannel     *omap[*selProjItem]
	hasField       map[string]*selProjItem
	hasSelectionID bool
	timeUnit       *timeUnitNode
}

type selectionComponent struct {
	cc           *compileCtx
	name         string
	typ          string
	props        *Object
	project      *selProjection
	materialized *outputNode
	scalesBound  []*selProjItem
}

func (s *selectionComponent) events() Value   { return s.props.Lookup("events") }
func (s *selectionComponent) resolve() string { return s.props.Lookup("resolve").AsString() }

func isLegendBinding(bind Value) bool {
	return (bind.IsStr() && bind.StrValue() == "legend") || (bind.IsObj() && bind.Get("legend").IsTruthy())
}
func isLegendStreamBinding(bind Value) bool { return isLegendBinding(bind) && isObject(bind) }

func isTimerSelection(cc *compileCtx, s *selectionComponent) bool {
	if cc.v5 {
		return false // timer (animation) selections are a 6.x feature
	}
	for _, e := range s.events().Items() {
		if e.IsObj() && e.ObjValue().Has("type") && e.Get("type").IsStr() && e.Get("type").StrValue() == "timer" {
			return true
		}
	}
	return false
}

func disableDirectManipulation(s *selectionComponent, selDef Value) {
	sel := selDef.Get("select")
	if sel.IsStr() || !sel.Get("on").IsTruthy() {
		s.props.Delete("events")
	}
	if sel.IsStr() || !sel.Get("clear").IsTruthy() {
		s.props.Delete("clear")
	}
	if sel.IsStr() || !sel.Get("toggle").IsTruthy() {
		s.props.Delete("toggle")
	}
}

func getFacetModelOf(m Model) *facetModel {
	p := m.b().parent
	for p != nil {
		if f, ok := p.(*facetModel); ok {
			return f
		}
		p = p.b().parent
	}
	return nil
}

func unitName(m Model, escape bool) string {
	name := m.b().name
	if escape {
		name = stringValue(jsval.Str(name))
	}
	if f := getFacetModelOf(m); f != nil {
		for _, channel := range facetChannels {
			if fd := f.facet.Lookup(channel); fd.IsTruthy() {
				name += " + '__facet_" + channel + "_' + (facet[" + stringValue(jsval.Str(f.vgField(channel, fieldRefOption{}))) + "])"
			}
		}
	}
	return name
}

func requiresSelectionID(m Model) bool {
	sel := m.b().comp.selection
	if sel == nil {
		return false
	}
	for _, k := range sel.keyList() {
		if sel.m[k].project.hasSelectionID {
			return true
		}
	}
	return false
}

// ---- parsing ----

func parseUnitSelection(m *unitModel, selDefs []Value) *omap[*selectionComponent] {
	cc := m.b().ctx

	selCmpts := newOmap[*selectionComponent]()
	selectionConfig := m.config.Get("selection")
	if len(selDefs) == 0 {
		return selCmpts
	}
	nTimer := 0
	for _, def := range selDefs {
		cc.check()
		name := varName(def.Get("name").AsString())
		selDef := def.Get("select")
		var typ string
		if selDef.IsStr() {
			typ = selDef.StrValue()
		} else {
			typ = selDef.Get("type").AsString()
		}
		var defaults *Object
		if isObject(selDef) {
			defaults = deepClone(selDef).ObjValue()
		} else {
			defaults = mk("type", typ)
		}
		cfg := selectionConfig.Get(typ)
		for _, key := range keysOf(cfg) {
			if key == "fields" || key == "encodings" {
				continue
			}
			if key == "mark" {
				defaults.Set("mark", jsval.Obj(merged(cfg.Get("mark"), defaults.Lookup("mark"))))
			}
			dv := defaults.Lookup(key)
			if dv.IsUndefined() || (dv.IsBool() && dv.BoolValue()) {
				defaults.Set(key, deepClone(coalesce(cfg.Get(key), dv)))
			}
		}
		props := cloneObj(defaults)
		props.Set("name", jsval.Str(name))
		props.Set("type", jsval.Str(typ))
		props.Set("init", def.Get("value"))
		props.Set("bind", def.Get("bind"))
		if on := defaults.Lookup("on"); on.IsStr() {
			props.Set("events", jsval.Arr(parseSelector(on.StrValue(), "scope")))
		} else {
			props.Set("events", jsval.Arr(arrayOf(deepClone(on))))
		}
		for _, e := range props.Lookup("events").Items() {
			if !e.IsObj() {
				throw("Invalid event stream %s in the selection %q: `on` must be an event selector string or event stream objects.", stringify(e), name)
			}
			if b := e.Get("between"); b.IsTruthy() && (!b.IsArr() || b.Len() != 2 || !b.Index(0).IsObj()) {
				throw("Invalid `between` %s in the selection %q: expected two event streams.", stringify(b), name)
			}
		}
		sc := &selectionComponent{cc: cc, name: name, typ: typ, props: props, project: &selProjection{
			hasChannel: newOmap[*selProjItem](), hasField: map[string]*selProjItem{},
		}}
		selCmpts.set(name, sc)
		if isTimerSelection(cc, sc) {
			nTimer++
			if nTimer > 1 {
				selCmpts.del(name)
				continue
			}
		}
		defClone := deepClone(def)
		for _, c := range selectionCompilers {
			if c.defined(sc) && c.parse != nil {
				c.parse(m, sc, defClone)
			}
		}
	}
	return selCmpts
}

func parseSelectionPredicate(m Model, pred Value, dfnode dfNode, datum string) string {
	var name string
	if pred.IsStr() {
		name = pred.StrValue()
	} else {
		name = pred.Get("param").AsString()
	}
	vname := varName(name)
	store := stringValue(jsval.Str(vname + storeSuffix))
	var sel *selectionComponent
	if m != nil {
		var junk bool
		if sel, junk = m.b().trySelectionComponent(vname); junk {
			// Object.prototype's value has no `project`.
			throw("Cannot read properties of undefined (reading 'timeUnit')")
		}
	}
	if sel == nil {
		return "!!" + vname
	}
	if sel.project.timeUnit != nil {
		child := dfnode
		if child == nil {
			child = m.b().comp.data.raw
		}
		tunode := sel.project.timeUnit.clone().(*timeUnitNode)
		tunode.cc = sel.project.timeUnit.cc
		if child.base().par != nil {
			tunode.insertAsParentOf(child)
		} else {
			child.base().setParent(tunode)
		}
	}
	fn := "vlSelectionTest("
	if sel.project.hasSelectionID {
		fn = "vlSelectionIdTest("
	}
	resolve := ")"
	if sel.resolve() != "global" {
		resolve = ", " + stringValue(sel.props.Lookup("resolve")) + ")"
	}
	test := fn + store + ", " + datum + resolve
	length := "length(data(" + store + "))"
	if pred.Get("empty").IsBool() && !pred.Get("empty").BoolValue() {
		return length + " && " + test
	}
	return "!" + length + " || " + test
}

func parseSelectionExtent(m Model, name string, extent Value) string {
	vname := varName(name)
	encoding := extent.Get("encoding")
	field := extent.Get("field")
	sel, junk := m.b().trySelectionComponent(vname)
	if junk {
		if !field.IsTruthy() {
			throw("Cannot read properties of undefined (reading 'items')")
		}
		// the function's name, or none for Object.prototype itself
		fname := "undefined"
		if vname != "__proto__" {
			fname = vname
			if vname == "constructor" {
				fname = "Object"
			}
		}
		return fname + "[" + stringValue(jsval.Str(replacePathInField(field.AsString()))) + "]"
	}
	if sel == nil {
		return vname
	}
	if !encoding.IsTruthy() && !field.IsTruthy() {
		if len(sel.project.items) == 0 {
			throw("The selection %q has no projected field to take the extent of.", name)
		}
		field = jsval.Str(sel.project.items[0].field)
	} else if encoding.IsTruthy() && !field.IsTruthy() {
		var encs []*selProjItem
		for _, p := range sel.project.items {
			if p.channel == encoding.AsString() {
				encs = append(encs, p)
			}
		}
		if len(encs) != 1 {
			field = jsval.Str(sel.project.items[0].field)
		} else {
			field = jsval.Str(encs[0].field)
		}
	}
	return sel.name + "[" + stringValue(jsval.Str(replacePathInField(field.AsString()))) + "]"
}

func materializeSelections(m *unitModel, main *outputNode) {
	if m.comp.selection == nil {
		return
	}
	for _, name := range m.comp.selection.keyList() {
		m.b().ctx.check()
		sel := m.comp.selection.m[name]
		lookupName := m.getName("lookup_" + name)
		fn := newFilterNode(main, m, mkv("param", name))
		out := newOutputNode(fn, lookupName, dsLookup, m.comp.data.outputNodeRefCounts)
		m.comp.data.outputNodes[lookupName] = out
		sel.materialized = out
	}
}

// ---- compilers ----

type selectionCompiler struct {
	defined         func(s *selectionComponent) bool
	parse           func(m *unitModel, s *selectionComponent, selDef Value)
	signals         func(m *unitModel, s *selectionComponent, signals []Value) []Value
	topLevelSignals func(m Model, s *selectionComponent, signals []Value) []Value
	modifyExpr      func(m *unitModel, s *selectionComponent, expr string) string
	marks           func(m *unitModel, s *selectionComponent, marks []Value) []Value
}

var selectionCompilers []selectionCompiler

func init() {
	selectionCompilers = []selectionCompiler{
		pointCompiler, intervalCompiler, projectCompiler, toggleCompiler, inputsCompiler,
		scalesCompiler, legendsCompiler, clearCompiler, translateCompiler, zoomCompiler, nearestCompiler,
	}
}

// signal helpers

// signalIndex maps signal names to their first position in one signals slice.
// Selection compilers look signals up by name once per selection; scanning the
// list each time made a spec with many params quadratic. The index follows a
// slice that only grows by appending in place and is rebuilt when a different
// slice arrives.
type signalIndex struct {
	base *Value
	n    int
	idx  map[string]int
}

func (c *compileCtx) signalPos(signals []Value, name string) int {
	if len(signals) == 0 {
		return -1
	}
	si := &c.sigIdx
	if si.base != &signals[0] || si.n > len(signals) || si.idx == nil {
		si.base, si.n, si.idx = &signals[0], 0, map[string]int{}
	}
	for ; si.n < len(signals); si.n++ {
		if n := signals[si.n].Get("name"); n.IsStr() {
			if _, ok := si.idx[n.StrValue()]; !ok {
				si.idx[n.StrValue()] = si.n
			}
		}
	}
	if i, ok := si.idx[name]; ok {
		if n := signals[i].Get("name"); n.IsStr() && n.StrValue() == name {
			return i
		}
		si.base = nil // stale: rescan next time
		for j, s := range signals {
			if n := s.Get("name"); n.IsStr() && n.StrValue() == name {
				return j
			}
		}
	}
	return -1
}

func findSignal(cc *compileCtx, signals []Value, name string) *Object {
	if i := cc.signalPos(signals, name); i >= 0 {
		return signals[i].ObjValue()
	}
	return nil
}

func findSignalIndex(cc *compileCtx, signals []Value, name string) int {
	return cc.signalPos(signals, name)
}

// pushOn appends an event handler to a signal's `on` array.
//
// Upstream pushes onto `signal.on` of a signal found by name; when no such
// signal exists (a pan or zoom whose projected channel has no signal, as for
// the geographic latitude channel) that is a TypeError, an error here too.
func pushOn(sg *Object, handler Value) {
	if sg == nil {
		throw("Cannot read properties of undefined (reading 'on')")
	}
	on := sg.Lookup("on")
	items := append(append([]Value{}, on.Items()...), handler)
	sg.Set("on", jsval.Arr(items))
}

func cleanupEmptyOnArray(signals []Value) []Value {
	for _, s := range signals {
		if o := s.ObjValue(); o != nil {
			if on := o.Lookup("on"); on.IsArr() && on.Len() == 0 {
				o.Delete("on")
			}
		}
	}
	return signals
}

func assembleInitExpr(init Value, wrap func(string) string) string {
	if init.IsArr() {
		parts := make([]string, init.Len())
		for i, v := range init.Items() {
			parts[i] = assembleInitExpr(v, wrap)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	} else if isDateTime(init) {
		return wrap(dateTimeToExpr(init))
	}
	return wrap(stringify(init))
}

func assembleInitValue(init Value, loc *time.Location) Value {
	if init.IsArr() {
		items := make([]Value, init.Len())
		for i, v := range init.Items() {
			items[i] = assembleInitValue(v, loc)
		}
		return jsval.Arr(items)
	} else if isDateTime(init) {
		return jsval.Num(dateTimeToTimestamp(init, loc))
	}
	return init
}

func idWrap(s string) string { return s }

func dateTimeToTimestamp(d Value, local *time.Location) float64 {
	parts := dateTimeParts(d, true)
	var n [7]int
	for i := 0; i < 7 && i < len(parts); i++ {
		f := jsval.StringToNumber(parts[i])
		n[i] = int(f)
	}
	// Date(year, month, day, h, m, s, ms) with a two-digit year mapping (0..99 -> 1900+) as JavaScript does.
	y := n[0]
	if y >= 0 && y <= 99 {
		y += 1900
	}
	loc := local
	if loc == nil || d.Get("utc").IsTruthy() {
		loc = time.UTC
	}
	t := time.Date(y, time.Month(n[1]+1), n[2], n[3], n[4], n[5], n[6]*1e6, loc)
	return float64(t.UnixMilli())
}

func assembleUnitSelectionSignals(m *unitModel, signals []Value) []Value {
	if m.comp.selection == nil {
		return cleanupEmptyOnArray(signals)
	}
	// Compilers append to signals in place; work on a private copy.
	cc := m.ctx
	signals = append(make([]Value, 0, len(signals)+8*m.comp.selection.len()), signals...)
	for _, name := range m.comp.selection.keyList() {
		cc.check()
		sel := m.comp.selection.m[name]
		var resolveExpr string
		if sel.resolve() == "global" {
			resolveExpr = "true"
		} else {
			resolveExpr = "{unit: " + unitName(m, true) + "}"
		}
		modifyExpr := sel.name + tupleSuffix + ", " + resolveExpr
		for _, c := range selectionCompilers {
			if !c.defined(sel) {
				continue
			}
			if c.signals != nil {
				signals = c.signals(m, sel, signals)
			}
			if c.modifyExpr != nil {
				modifyExpr = c.modifyExpr(m, sel, modifyExpr)
			}
		}
		signals = append(signals, mkv(
			"name", sel.name+modifySuffix,
			"on", arr(mkv("events", mkv("signal", sel.name+tupleSuffix), "update", "modify("+stringValue(jsval.Str(sel.name+storeSuffix))+", "+modifyExpr+")")),
		))
	}
	return cleanupEmptyOnArray(signals)
}

func assembleFacetSignals(m *facetModel, signals []Value) []Value {
	cc := m.b().ctx

	if m.comp.selection != nil && m.comp.selection.len() > 0 {
		name := stringValue(jsval.Str(m.getName("cell")))
		sg := mkv("name", "facet", "value", mkv(), "on", arr(mkv(
			"events", jsval.Arr(parseSelector(mouseMoveEvent(cc), "scope")),
			"update", "isTuple(facet) ? facet : group("+name+").datum")))
		signals = append([]Value{sg}, signals...)
	}
	return cleanupEmptyOnArray(signals)
}

func assembleTopLevelSignals(m Model, signals []Value) []Value {
	cc := m.b().ctx

	sel := m.b().comp.selection
	hasSelections := false
	if sel != nil {
		signals = append(make([]Value, 0, len(signals)+8*sel.len()), signals...)
	}
	if sel != nil {
		for _, name := range sel.keyList() {
			s := sel.m[name]
			store := stringValue(jsval.Str(s.name + storeSuffix))
			if findSignal(cc, signals, s.name) == nil {
				resolveV := s.props.Lookup("resolve")
				if s.resolve() == "global" {
					resolveV = jsval.Str("union")
				}
				isPoint := ")"
				if s.typ == "point" {
					isPoint = ", true, true)"
				}
				signals = append(signals, mkv("name", s.name, "update", vlSelectionResolve+"("+store+", "+stringValue(resolveV)+isPoint))
			}
			hasSelections = true
			for _, c := range selectionCompilers {
				if c.defined(s) && c.topLevelSignals != nil {
					signals = c.topLevelSignals(m, s, signals)
				}
			}
		}
	}
	if hasSelections {
		if findSignal(cc, signals, "unit") == nil {
			signals = append([]Value{mkv("name", "unit", "value", mkv(), "on", arr(mkv("events", mouseMoveEvent(cc), "update", "isTuple(group()) ? group() : unit")))}, signals...)
		}
	}
	return cleanupEmptyOnArray(signals)
}

func assembleUnitSelectionData(m *unitModel, data []Value) []Value {
	cc := m.b().ctx

	var selectionData, animationData []Value
	haveData := map[string]bool{}
	for _, d := range data {
		if n := d.Get("name"); n.IsStr() {
			haveData[n.StrValue()] = true
		}
	}
	unit := unitName(m, false)
	if m.comp.selection != nil {
		for _, name := range m.comp.selection.keyList() {
			cc.check()
			sel := m.comp.selection.m[name]
			store := mk("name", sel.name+storeSuffix)
			if sel.project.hasSelectionID {
				store.Set("transform", arr(mkv("type", "collect", "sort", mkv("field", selectionID))))
			}
			if init := sel.props.Lookup("init"); init.IsTruthy() {
				var fields []Value
				for _, p := range sel.project.items {
					fields = append(fields, p.assembled())
				}
				var vals []Value
				for _, i := range init.Items() {
					if sel.project.hasSelectionID {
						vals = append(vals, mkv("unit", unit, selectionID, assembleInitValue(i, m.ctx.loc).Index(0)))
					} else {
						vals = append(vals, mkv("unit", unit, "fields", jsval.Arr(fields), "values", assembleInitValue(i, m.ctx.loc)))
					}
				}
				store.Set("values", jsval.Arr(vals))
			}
			if !haveData[sel.name+storeSuffix] {
				selectionData = append(selectionData, jsval.Obj(store))
				haveData[sel.name+storeSuffix] = true
			}
			if isTimerSelection(cc, sel) && len(data) > 0 {
				sourceName := m.lookupDataSource(m.getDataName(dsMain))
				var sourceData Value
				for _, d := range data {
					if d.Get("name").IsStr() && d.Get("name").StrValue() == sourceName {
						sourceData = d
						break
					}
				}
				if sourceData.IsObj() {
					var filterT Value
					var rest []Value
					for _, t := range sourceData.Get("transform").Items() {
						if !filterT.IsObj() && t.Get("type").IsStr() && t.Get("type").StrValue() == "filter" && strings.Contains(t.Get("expr").AsString(), "vlSelectionTest") {
							filterT = t
						} else {
							rest = append(rest, t)
						}
					}
					if filterT.IsObj() {
						sourceData.ObjValue().Set("transform", jsval.Arr(rest))
						animationData = append(animationData, mkv("name", sourceData.Get("name").AsString()+curr, "source", sourceData.Get("name"), "transform", arr(filterT)))
					}
				}
			}
		}
	}
	if cc.v5 {
		// 5.8 appends the stores to the data it was given.
		return append(append([]Value{}, data...), selectionData...)
	}
	out := append(append(selectionData, data...), animationData...)
	return out
}

func assembleUnitSelectionMarks(m *unitModel, marks []Value) []Value {
	if m.comp.selection == nil {
		return marks
	}
	for _, name := range m.comp.selection.keyList() {
		sel := m.comp.selection.m[name]
		for _, c := range selectionCompilers {
			if c.defined(sel) && c.marks != nil {
				marks = c.marks(m, sel, marks)
			}
		}
	}
	return marks
}

func assembleLayerSelectionMarks(m *layerModel, marks []Value) []Value {
	for _, child := range m.kids {
		if u := asUnit(child); u != nil {
			marks = assembleUnitSelectionMarks(u, marks)
		}
	}
	return marks
}

func assembleSelectionScaleDomain(m Model, extent Value, sc *scaleComponent, domain Value) Value {
	parsedExtent := parseSelectionExtent(m, extent.Get("param").AsString(), extent)
	if hasContinuousDomain(sc.get("type").AsString()) && domain.IsArr() && jsGreater(domain.Index(0), domain.Index(1)) {
		return sig("isValid(" + parsedExtent + ") && reverse(" + parsedExtent + ")")
	}
	return sig(parsedExtent)
}

// ---- interactive legend parse ----

func parseInteractiveLegend(m *unitModel, channel string, legendCmpt *legendComponent) {
	field := m.fieldDef(channel).Get("field")
	if m.comp.selection == nil {
		return
	}
	for _, name := range m.comp.selection.keyList() {
		sel := m.comp.selection.m[name]
		proj := sel.project.hasField[field.AsString()]
		if proj == nil {
			proj, _ = sel.project.hasChannel.get(channel)
		}
		if proj != nil && legendsCompiler.defined(sel) {
			cur := legendCmpt.get("selections")
			items := append([]Value{}, cur.Items()...)
			items = append(items, jsval.Str(sel.name))
			legendCmpt.set("selections", jsval.Arr(items), false)
			proj.hasLegend = true
		}
	}
}

// ---- projection ("project" compiler) ----

var projectCompiler = selectionCompiler{
	defined: func(*selectionComponent) bool { return true },
	parse: func(m *unitModel, sel *selectionComponent, selDef Value) {
		name := sel.name
		proj := sel.project
		parsed := map[string]*selProjItem{}
		timeUnits := newOmap[*Object]()
		signals := newSset()
		signalName := func(p *selProjItem, rangeK string) string {
			suffix := p.field
			if rangeK == "visual" {
				suffix = p.channel
			}
			sg := varName(name + "_" + suffix)
			for counter := 1; signals.has(sg); counter++ {
				sg = varName(name + "_" + suffix + "_" + jsval.JSNumberString(float64(counter)))
			}
			signals.add(sg)
			return sg
		}
		typ := sel.typ
		cfg := m.config.Get("selection").Get(typ)
		var init Value
		if !selDef.Get("value").IsUndefined() {
			init = jsval.Arr(arrayOf(selDef.Get("value")))
		}
		var fields, encodings []Value
		if s := selDef.Get("select"); isObject(s) {
			if f := s.Get("fields"); f.IsTruthy() {
				fields = f.Items()
			}
			if e := s.Get("encodings"); e.IsTruthy() {
				encodings = e.Items()
			}
		}
		if fields == nil && encodings == nil && init.IsTruthy() {
			for _, initVal := range init.Items() {
				if !isObject(initVal) {
					continue
				}
				for _, key := range keysOf(initVal) {
					if isSingleDefUnitChannel(m.ctx, key) {
						encodings = append(encodings, jsval.Str(key))
					} else if typ == "interval" {
						encodings = cfg.Get("encodings").Items()
					} else {
						fields = append(fields, jsval.Str(key))
					}
				}
			}
		}
		if fields == nil && encodings == nil {
			encodings = cfg.Get("encodings").Items()
			if cfg.IsObj() && cfg.ObjValue().Has("fields") {
				fields = cfg.Get("fields").Items()
			}
		}
		for _, ch := range encodings {
			channel := ch.AsString()
			fd := m.fieldDef(channel)
			if fd.IsTruthy() {
				field := fd.Get("field")
				if fd.Get("aggregate").IsTruthy() || !field.IsTruthy() {
					continue
				}
				f := field.AsString()
				if fd.Get("timeUnit").IsTruthy() && !isBinnedTimeUnit(m.ctx, fd.Get("timeUnit")) {
					f = m.vgField(channel, fieldRefOption{})
					component := mk("timeUnit", fd.Get("timeUnit"), "as", f, "field", fd.Get("field"))
					timeUnits.set(hashOf(jsval.Obj(component)), component)
				}
				if parsed[f] == nil {
					tplType := "E"
					if typ == "interval" && isScaleChannel(m.ctx, channel) && hasContinuousDomain(m.scaleTypeOf(channel)) {
						tplType = "R"
					} else if fd.Get("bin").IsTruthy() {
						tplType = "R-RE"
					}
					p := &selProjItem{field: f, channel: channel, typ: tplType, index: len(proj.items), isChannel: true}
					p.dataSignal = signalName(p, "data")
					p.visSignal = signalName(p, "visual")
					p.hasVisual = true
					proj.items = append(proj.items, p)
					parsed[f] = p
					proj.hasField[f] = p
					proj.hasSelectionID = proj.hasSelectionID || f == selectionID
					if isGeoPositionChannel(channel) {
						p.geoChannel = channel
						p.channel = getPositionChannelFromLatLong(channel)
						proj.hasChannel.set(p.channel, p)
					} else {
						proj.hasChannel.set(channel, p)
					}
				}
			}
		}
		for _, fv := range fields {
			field := fv.AsString()
			if proj.hasField[field] != nil {
				continue
			}
			p := &selProjItem{typ: "E", field: field, index: len(proj.items)}
			p.dataSignal = signalName(p, "data")
			proj.items = append(proj.items, p)
			proj.hasField[field] = p
			proj.hasSelectionID = proj.hasSelectionID || field == selectionID
		}
		if init.IsTruthy() {
			var out []Value
			for _, v := range init.Items() {
				var row []Value
				for _, p := range proj.items {
					if isObject(v) {
						k := p.channel
						if p.geoChannel != "" {
							k = p.geoChannel
						}
						if x := v.Get(k); !x.IsUndefined() && k != "" {
							row = append(row, x)
						} else {
							row = append(row, v.Get(p.field))
						}
					} else {
						row = append(row, v)
					}
				}
				out = append(out, jsval.Arr(row))
			}
			sel.props.Set("init", jsval.Arr(out))
		}
		if timeUnits.len() > 0 {
			proj.timeUnit = newTimeUnitNode(nil, timeUnits)
		}
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name + tupleFields
		if findSignal(sel.cc, signals, name) != nil || sel.project.hasSelectionID {
			return signals
		}
		items := make([]Value, len(sel.project.items))
		for i, p := range sel.project.items {
			items[i] = p.assembled()
		}
		return append(signals, mkv("name", name, "value", jsval.Arr(items)))
	},
}

// ---- point ----

const (
	animValue      = "anim_value"
	animClock      = "anim_clock"
	easedAnimClock = "eased_anim_clock"
	minExtent      = "min_extent"
	maxRangeExtent = "max_range_extent"
	lastTick       = "last_tick_at"
	isPlaying      = "is_playing"
)

var throttleMs = (1.0 / 60.0) * 1000

func animationSignals(selectionName, scaleName string) []Value {
	return []Value{
		mkv("name", easedAnimClock, "update", animClock),
		mkv("name", selectionName+"_domain", "init", "domain('"+jsName(scaleName)+"')"),
		mkv("name", minExtent, "init", "extent("+selectionName+"_domain)[0]"),
		mkv("name", maxRangeExtent, "init", "extent(range('"+jsName(scaleName)+"'))[1]"),
		mkv("name", animValue, "update", "invert('"+jsName(scaleName)+"', "+easedAnimClock+")"),
	}
}

var pointCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool { return s.typ == "point" },
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		if isTimerSelection(sel.cc, sel) {
			signals = append(signals,
				mkv("name", animClock, "init", "0", "on", arr(mkv(
					"events", mkv("type", "timer", "throttle", throttleMs),
					"update", isPlaying+" ? ("+animClock+" + (now() - "+lastTick+") > "+maxRangeExtent+" ? 0 : "+animClock+" + (now() - "+lastTick+")) : "+animClock))),
				mkv("name", lastTick, "init", "now()", "on", arr(mkv("events", arr(mkv("signal", animClock), mkv("signal", isPlaying)), "update", "now()"))),
				mkv("name", isPlaying, "init", "true"),
			)
		}
		return signals
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		fieldsSg := name + tupleFields
		project := sel.project
		datum := "(item().isVoronoi ? datum.datum : datum)"
		var brushes []string
		for _, n := range m.comp.selection.keysView() {
			c := m.comp.selection.m[n]
			if c.typ == "interval" {
				brushes = append(brushes, "indexof(item().mark.name, '"+c.name+brushSuffix+"') < 0")
			}
		}
		test := "datum && item().mark.marktype !== 'group' && indexof(item().mark.role, 'legend') < 0"
		if len(brushes) > 0 {
			test += " && " + strings.Join(brushes, " && ")
		}
		update := "unit: " + unitName(m, true) + ", "
		switch {
		case project.hasSelectionID:
			update += selectionID + ": " + datum + "[" + stringValue(jsval.Str(selectionID)) + "]"
		case isTimerSelection(sel.cc, sel):
			update += "fields: " + fieldsSg + ", values: [" + animValue + " ? " + animValue + " : " + minExtent + "]"
		default:
			var vals []string
			for _, p := range project.items {
				fd := m.fieldDef(p.channel)
				if fd.Get("bin").IsTruthy() {
					vals = append(vals, "["+datum+"["+stringValue(jsval.Str(m.vgField(p.channel, fieldRefOption{})))+"], "+
						datum+"["+stringValue(jsval.Str(m.vgField(p.channel, fieldRefOption{binSuffix: "end"})))+"]]")
				} else {
					vals = append(vals, datum+"["+stringValue(jsval.Str(p.field))+"]")
				}
			}
			update += "fields: " + fieldsSg + ", values: [" + strings.Join(vals, ", ") + "]"
		}
		if isTimerSelection(sel.cc, sel) {
			out := append(signals, animationSignals(sel.name, m.scaleName(chTime, false))...)
			return append(out, mkv("name", name+tupleSuffix, "on", arr(mkv(
				"events", arr(mkv("signal", easedAnimClock), mkv("signal", animValue)),
				"update", "{"+update+"}", "force", true))))
		}
		events := sel.events()
		var on Value
		if events.IsTruthy() {
			on = arr(mkv("events", events, "update", test+" ? {"+update+"} : null", "force", true))
		} else {
			on = jsval.Arr(nil)
		}
		return append(signals, mkv("name", name+tupleSuffix, "on", on))
	},
}

// ---- interval ----

var intervalCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool { return s.typ == "interval" },
	parse: func(m *unitModel, sel *selectionComponent, selDef Value) {
		if m.hasProjection() {
			def := jsval.NewObject(4)
			if s := selDef.Get("select"); isObject(s) {
				spread(def, s)
			}
			def.Set("fields", arr(selectionID))
			if !def.Lookup("encodings").IsTruthy() {
				if v := selDef.Get("value"); v.IsTruthy() {
					def.Set("encodings", strsVal(keysOf(v)))
				} else {
					def.Set("encodings", arr(chLongitude, chLatitude))
				}
			}
			o := mk("type", "interval")
			spread(o, jsval.Obj(def))
			selDef.ObjValue().Set("select", jsval.Obj(o))
		}
		if sel.props.Lookup("translate").IsTruthy() && !scalesCompiler.defined(sel) {
			filterExpr := "!event.item || event.item.mark.name !== " + stringValue(jsval.Str(sel.name+brushSuffix))
			for _, evt := range sel.events().Items() {
				between := evt.Get("between")
				if !between.IsTruthy() {
					continue
				}
				first := between.Index(0).ObjValue()
				filters := arrayOf(first.Lookup("filter"))
				if !first.Has("filter") || first.Lookup("filter").IsNullish() {
					filters = nil
				}
				found := false
				for _, f := range filters {
					if f.IsStr() && f.StrValue() == filterExpr {
						found = true
					}
				}
				if !found {
					filters = append(append([]Value{}, filters...), jsval.Str(filterExpr))
				}
				first.Set("filter", jsval.Arr(filters))
			}
		}
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		tupleSg := name + tupleSuffix
		var channels []*selProjItem
		for _, k := range sel.project.hasChannel.keys {
			p := sel.project.hasChannel.m[k]
			if p.channel == chX || p.channel == chY {
				channels = append(channels, p)
			}
		}
		var init Value
		if iv := sel.props.Lookup("init"); iv.IsTruthy() {
			init = iv.Index(0)
		} else {
			init = jsval.Null
		}
		initFor := func(p *selProjItem) Value {
			if init.IsArr() {
				return init.Index(p.index)
			}
			return undef
		}
		for _, p := range channels {
			signals = append(signals, intervalChannelSignals(m, sel, p, initFor(p))...)
		}
		if !m.hasProjection() {
			if !scalesCompiler.defined(sel) {
				triggerSg := name + scaleTrigger
				var scaleTriggers []string
				for _, p := range channels {
					scaleName := stringValue(strOrUndef(m.scaleName(p.channel, false)))
					scaleType := m.scaleTypeOf(p.channel)
					toNum := ""
					if hasContinuousDomain(scaleType) {
						toNum = "+"
					}
					dn, vn := p.dataSignal, p.visSignal
					scaleTriggers = append(scaleTriggers, "(!isArray("+dn+") || ("+toNum+"invert("+scaleName+", "+vn+")[0] === "+toNum+dn+"[0] && "+
						toNum+"invert("+scaleName+", "+vn+")[1] === "+toNum+dn+"[1]))")
				}
				if len(scaleTriggers) > 0 {
					var evs []Value
					for _, p := range channels {
						evs = append(evs, mkv("scale", m.scaleName(p.channel, false)))
					}
					signals = append(signals, mkv("name", triggerSg, "value", mkv(), "on", arr(mkv(
						"events", jsval.Arr(evs), "update", strings.Join(scaleTriggers, " && ")+" ? "+triggerSg+" : {}"))))
				}
			}
			var dataSignals []string
			for _, p := range channels {
				dataSignals = append(dataSignals, p.dataSignal)
			}
			update := "unit: " + unitName(m, true) + ", fields: " + name + tupleFields + ", values"
			o := mk("name", tupleSg)
			if init.IsTruthy() {
				o.Set("init", jsval.Str("{"+update+": "+assembleInitExpr(init, idWrap)+"}"))
			}
			if len(dataSignals) > 0 {
				o.Set("on", arr(mkv(
					"events", arr(mkv("signal", strings.Join(dataSignals, " || "))),
					"update", strings.Join(dataSignals, " && ")+" ? {"+update+": ["+strings.Join(dataSignals, ",")+"]} : null")))
			}
			return append(signals, jsval.Obj(o))
		}
		projection := stringValue(jsval.Str(m.projectionName(false)))
		centerSg := m.projectionName(false) + centerSuffix
		x, _ := sel.project.hasChannel.get("x")
		y, _ := sel.project.hasChannel.get("y")
		xvname, yvname := "", ""
		if x != nil {
			xvname = x.visSignal
		}
		if y != nil {
			yvname = y.visSignal
		}
		sizeSg := func(layout string) string { return signalOf(m.getSizeSignalRef(layout)) }
		pick := func(v, d string) string {
			if v != "" {
				return v
			}
			return d
		}
		xs, ys := "0", "0"
		if xvname != "" {
			xs = xvname + "[0]"
		}
		if yvname != "" {
			ys = yvname + "[0]"
		}
		xe, ye := sizeSg("width"), sizeSg("height")
		if xvname != "" {
			xe = xvname + "[1]"
		}
		if yvname != "" {
			ye = yvname + "[1]"
		}
		_ = pick
		bbox := "[[" + xs + ", " + ys + "],[" + xe + ", " + ye + "]]"
		hasInit := init.IsTruthy()
		if hasInit {
			var xinit, yinit Value
			if x != nil {
				xinit = init.Index(x.index)
			} else {
				xinit = jsval.Str(centerSg + "[0]")
			}
			if y != nil {
				yinit = init.Index(y.index)
			} else {
				yinit = jsval.Str(centerSg + "[1]")
			}
			part := func(a Value, isCh bool, k int) string {
				if isCh {
					return a.Index(k).AsString()
				}
				return a.AsString()
			}
			signals = append([]Value{mkv("name", name+initSuffix, "init",
				"[scale("+projection+", ["+part(xinit, x != nil, 0)+", "+part(yinit, y != nil, 0)+"]), "+
					"scale("+projection+", ["+part(xinit, x != nil, 1)+", "+part(yinit, y != nil, 1)+"])]")}, signals...)
			if x == nil || y == nil {
				if findSignal(sel.cc, signals, centerSg) == nil {
					signals = append([]Value{mkv("name", centerSg, "update", "invert("+projection+", ["+sizeSg("width")+"/2, "+sizeSg("height")+"/2])")}, signals...)
				}
			}
		}
		intersect := "intersect(" + bbox + ", {markname: " + stringValue(jsval.Str(m.getName("marks"))) + "}, unit.mark)"
		base := "{unit: " + unitName(m, true) + "}"
		update := "vlSelectionTuples(" + intersect + ", " + base + ")"
		var visualSignals []string
		for _, p := range channels {
			visualSignals = append(visualSignals, p.visSignal)
		}
		var evs []Value
		if len(visualSignals) > 0 {
			evs = append(evs, mkv("signal", strings.Join(visualSignals, " || ")))
		}
		if hasInit {
			evs = append(evs, mkv("signal", geoInitTick))
		}
		return append(signals, mkv("name", tupleSg, "on", arr(mkv("events", jsval.Arr(evs), "update", update))))
	},
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		if u := asUnit(m); u != nil && u.hasProjection() && sel.props.Lookup("init").IsTruthy() {
			if findSignal(sel.cc, signals, geoInitTick) == nil {
				signals = append([]Value{mkv("name", geoInitTick, "value", jsval.Null, "on", arr(mkv(
					"events", "timer{1}", "update", geoInitTick+" === null ? {} : "+geoInitTick)))}, signals...)
			}
		}
		return signals
	},
	marks: func(m *unitModel, sel *selectionComponent, marks []Value) []Value {
		name := sel.name
		x, _ := sel.project.hasChannel.get("x")
		y, _ := sel.project.hasChannel.get("y")
		xvname, yvname := "", ""
		if x != nil {
			xvname = x.visSignal
		}
		if y != nil {
			yvname = y.visSignal
		}
		store := "data(" + stringValue(jsval.Str(sel.name+storeSuffix)) + ")"
		if scalesCompiler.defined(sel) || (x == nil && y == nil) {
			return marks
		}
		updateX, updateY, updateX2, updateY2 := mkv("value", 0), mkv("value", 0), mkv("field", mkv("group", "width")), mkv("field", mkv("group", "height"))
		if x != nil {
			updateX, updateX2 = mkv("signal", xvname+"[0]"), mkv("signal", xvname+"[1]")
		}
		if y != nil {
			updateY, updateY2 = mkv("signal", yvname+"[0]"), mkv("signal", yvname+"[1]")
		}
		update := mk("x", updateX, "y", updateY, "x2", updateX2, "y2", updateY2)
		if sel.resolve() == "global" {
			for _, k := range append([]string(nil), update.Keys()...) {
				t := mk("test", store+".length && "+store+"[0].unit === "+unitName(m, true))
				spread(t, update.Lookup(k))
				update.Set(k, arr(jsval.Obj(t), mkv("value", 0)))
			}
		}
		selMark := sel.props.Lookup("mark")
		fill, fillOpacity, cursor := selMark.Get("fill"), selMark.Get("fillOpacity"), selMark.Get("cursor")
		stroke := omit(selMark, "fill", "fillOpacity", "cursor")
		vgStroke := jsval.NewObject(4)
		for _, k := range stroke.Keys() {
			var tests []string
			if x != nil {
				tests = append(tests, xvname+"[0] !== "+xvname+"[1]")
			}
			if y != nil {
				tests = append(tests, yvname+"[0] !== "+yvname+"[1]")
			}
			vgStroke.Set(k, arr(mkv("test", strings.Join(tests, " && "), "value", stroke.Lookup(k)), mkv("value", jsval.Null)))
		}
		var vgCursor Value
		switch {
		case !cursor.IsNullish():
			vgCursor = cursor
		case !m.ctx.v5 && sel.props.Lookup("translate").IsTruthy():
			vgCursor = jsval.Str("move")
		default:
			vgCursor = jsval.Null
		}
		enter2 := jsval.NewObject(2)
		if vgCursor.IsTruthy() {
			enter2.Set("cursor", mkv("value", vgCursor))
		}
		enter2.Set("fill", mkv("value", "transparent"))
		upd2 := cloneObj(update)
		spread(upd2, jsval.Obj(vgStroke))
		out := []Value{mkv("name", name+brushSuffix+"_bg", "type", "rect", "clip", true,
			"encode", mkv("enter", mkv("fill", mkv("value", fill), "fillOpacity", mkv("value", fillOpacity)), "update", jsval.Obj(update)))}
		out = append(out, marks...)
		out = append(out, mkv("name", name+brushSuffix, "type", "rect", "clip", true,
			"encode", mkv("enter", jsval.Obj(enter2), "update", jsval.Obj(upd2))))
		return out
	},
}

func intervalChannelSignals(m *unitModel, sel *selectionComponent, proj *selProjItem, init Value) []Value {
	scaledInterval := !m.hasProjection()
	channel := proj.channel
	vname := proj.visSignal
	var scaleName string
	if scaledInterval {
		scaleName = stringValue(strOrUndef(m.scaleName(channel, false)))
	} else {
		scaleName = stringValue(jsval.Str(m.projectionName(false)))
	}
	scaled := func(s string) string { return "scale(" + scaleName + ", " + s + ")" }
	sizeType := "height"
	if channel == chX {
		sizeType = "width"
	}
	size := signalOf(m.getSizeSignalRef(sizeType))
	coord := channel + "(unit)"
	var von []Value
	for _, evt := range sel.events().Items() {
		von = append(von,
			mkv("events", evt.Get("between").Index(0), "update", "["+coord+", "+coord+"]"),
			mkv("events", evt, "update", "["+vname+"[0], clamp("+coord+", 0, "+size+")]"),
		)
	}
	if scaledInterval {
		dname := proj.dataSignal
		hasScales := scalesCompiler.defined(sel)
		scale := m.getScaleComponent(channel)
		scaleType := ""
		if scale != nil {
			scaleType = scale.get("type").AsString()
		}
		var vinit *Object
		if init.IsTruthy() {
			vinit = mk("init", assembleInitExpr(init, scaled))
		} else {
			vinit = mk("value", jsval.Arr(nil))
		}
		var upd string
		if hasContinuousDomain(scaleType) {
			upd = "[" + scaled(dname+"[0]") + ", " + scaled(dname+"[1]") + "]"
		} else {
			upd = "[0, 0]"
		}
		von = append(von, mkv("events", mkv("signal", sel.name+scaleTrigger), "update", upd))
		if hasScales {
			return []Value{mkv("name", dname, "on", jsval.Arr(nil))}
		}
		s1 := mk("name", vname)
		spread(s1, jsval.Obj(vinit))
		s1.Set("on", jsval.Arr(von))
		s2 := mk("name", dname)
		if init.IsTruthy() {
			s2.Set("init", jsval.Str(assembleInitExpr(init, idWrap)))
		}
		s2.Set("on", arr(mkv("events", mkv("signal", vname), "update", vname+"[0] === "+vname+"[1] ? null : invert("+scaleName+", "+vname+")")))
		return []Value{jsval.Obj(s1), jsval.Obj(s2)}
	}
	initIdx := 1
	if channel == chX {
		initIdx = 0
	}
	initSg := sel.name + initSuffix
	s := mk("name", vname)
	if init.IsTruthy() {
		idx := jsval.JSNumberString(float64(initIdx))
		s.Set("init", jsval.Str("["+initSg+"[0]["+idx+"], "+initSg+"[1]["+idx+"]]"))
	} else {
		s.Set("value", jsval.Arr(nil))
	}
	s.Set("on", jsval.Arr(von))
	return []Value{jsval.Obj(s)}
}

// ---- toggle ----

var toggleCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool {
		return s.typ == "point" && !isTimerSelection(s.cc, s) && s.props.Lookup("toggle").IsTruthy()
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		return append(signals, mkv("name", sel.name+toggleSuffix, "value", false,
			"on", arr(mkv("events", sel.events(), "update", sel.props.Lookup("toggle")))))
	},
	modifyExpr: func(m *unitModel, sel *selectionComponent, _ string) string {
		tpl := sel.name + tupleSuffix
		signal := sel.name + toggleSuffix
		var mid string
		if sel.resolve() == "global" {
			mid = signal + " ? null : true, "
		} else {
			mid = signal + " ? null : {unit: " + unitName(m, true) + "}, "
		}
		return signal + " ? null : " + tpl + ", " + mid + signal + " ? " + tpl + " : null"
	},
}

// ---- input bindings ----

var inputsCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool {
		bind := s.props.Lookup("bind")
		return s.typ == "point" && s.resolve() == "global" && bind.IsTruthy() && !(bind.IsStr() && bind.StrValue() == "scales") && !isLegendBinding(bind)
	},
	parse: func(m *unitModel, sel *selectionComponent, selDef Value) { disableDirectManipulation(sel, selDef) },
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		bind := sel.props.Lookup("bind")
		var init Value
		if iv := sel.props.Lookup("init"); iv.IsTruthy() {
			init = iv.Index(0)
		}
		datum := "datum"
		if nearestCompiler.defined(sel) {
			datum = "(item().isVoronoi ? datum.datum : datum)"
		}
		for i, p := range sel.project.items {
			sgname := varName(name + "_" + p.field)
			if findSignal(sel.cc, signals, sgname) == nil {
				o := mk("name", sgname)
				if init.IsTruthy() {
					o.Set("init", jsval.Str(assembleInitExpr(init.Index(i), idWrap)))
				} else {
					o.Set("value", jsval.Null)
				}
				if ev := sel.events(); ev.IsTruthy() {
					o.Set("on", arr(mkv("events", ev, "update", "datum && item().mark.marktype !== 'group' ? "+datum+"["+stringValue(jsval.Str(p.field))+"] : null")))
				} else {
					o.Set("on", jsval.Arr(nil))
				}
				b := coalesce(bind.Get(p.field), bind.Get(p.channel), bind)
				o.Set("bind", b)
				signals = append([]Value{jsval.Obj(o)}, signals...)
			}
		}
		return signals
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		signal := findSignal(sel.cc, signals, name+tupleSuffix)
		fields := name + tupleFields
		var values []string
		for _, p := range sel.project.items {
			values = append(values, varName(name+"_"+p.field))
		}
		var valid []string
		for _, v := range values {
			valid = append(valid, v+" !== null")
		}
		if len(values) > 0 && signal != nil {
			signal.Set("update", jsval.Str(strings.Join(valid, " && ")+" ? {fields: "+fields+", values: ["+strings.Join(values, ", ")+"]} : null"))
		}
		if signal != nil {
			signal.Delete("value")
			signal.Delete("on")
		}
		return signals
	},
}

// ---- scale bindings ----

func isTopLevelLayer(m Model) bool {
	cc := m.b().ctx

	p := m.b().parent
	if cc.v5 {
		// 5.8 writes `!parent.parent ?? isTopLevelLayer(parent.parent)`, which
		// never recurses: only a layer at the very top counts.
		return p != nil && isLayerModel(p) && p.b().parent == nil
	}
	return p != nil && isLayerModel(p) && (p.b().parent == nil || isTopLevelLayer(p.b().parent))
}

func selDomain(m *unitModel, channel string) string {
	return "domain(" + stringValue(strOrUndef(m.scaleName(channel, false))) + ")"
}

var scalesCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool {
		bind := s.props.Lookup("bind")
		return s.typ == "interval" && s.resolve() == "global" && bind.IsStr() && bind.StrValue() == "scales"
	},
	parse: func(m *unitModel, sel *selectionComponent, _ Value) {
		sel.scalesBound = nil
		for _, proj := range sel.project.items {
			channel := proj.channel
			if !isScaleChannel(m.ctx, channel) {
				continue
			}
			scale := m.getScaleComponent(channel)
			scaleType := ""
			if scale != nil {
				scaleType = scale.get("type").AsString()
			}
			if scale == nil || !hasContinuousDomain(scaleType) {
				continue
			}
			scale.set("selectionExtent", mkv("param", sel.name, "field", proj.field), true)
			sel.scalesBound = append(sel.scalesBound, proj)
		}
	},
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		var bound []*selProjItem
		for _, proj := range sel.scalesBound {
			if findSignal(sel.cc, signals, proj.dataSignal) == nil {
				bound = append(bound, proj)
			}
		}
		if m.b().parent == nil || isTopLevelLayer(m) || len(bound) == 0 {
			return signals
		}
		namedSg := findSignal(sel.cc, signals, sel.name)
		update := namedSg.Lookup("update").AsString()
		if strings.Contains(update, vlSelectionResolve) {
			var parts []string
			for _, proj := range bound {
				parts = append(parts, stringValue(jsval.Str(replacePathInField(proj.field)))+": "+proj.dataSignal)
			}
			namedSg.Set("update", jsval.Str("{"+strings.Join(parts, ", ")+"}"))
		} else {
			for _, proj := range bound {
				mapping := stringValue(jsval.Str(replacePathInField(proj.field))) + ": " + proj.dataSignal
				if !strings.Contains(update, mapping) {
					update = update[:len(update)-1] + ", " + mapping + "}"
				}
			}
			namedSg.Set("update", jsval.Str(update))
		}
		for _, proj := range bound {
			signals = append(signals, mkv("name", proj.dataSignal))
		}
		return signals
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		if m.parent != nil && !isTopLevelLayer(m) {
			for _, proj := range sel.scalesBound {
				signal := findSignal(sel.cc, signals, proj.dataSignal)
				if signal == nil {
					// `signals.find(...)` is undefined: upstream's assignment
					// to its `push` property is a TypeError.
					throw("Cannot set properties of undefined (setting 'push')")
				}
				signal.Set("push", jsval.Str("outer"))
				signal.Delete("value")
				signal.Delete("update")
			}
		}
		return signals
	},
}

// ---- legend bindings ----

var legendsCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool {
		bind := s.props.Lookup("bind")
		spec := s.resolve() == "global" && bind.IsTruthy() && isLegendBinding(bind)
		projLen := len(s.project.items) == 1 && s.project.items[0].field != selectionID
		return spec && projLen
	},
	parse: func(m *unitModel, sel *selectionComponent, selDef Value) {
		selDef2 := deepClone(selDef)
		if s := selDef2.Get("select"); s.IsStr() {
			selDef2.ObjValue().Set("select", mkv("type", s, "toggle", sel.props.Lookup("toggle")))
		} else {
			o := cloneObj(s.ObjValue())
			o.Set("toggle", sel.props.Lookup("toggle"))
			selDef2.ObjValue().Set("select", jsval.Obj(o))
		}
		disableDirectManipulation(sel, selDef2)
		if s := selDef.Get("select"); isObject(s) && (s.Get("on").IsTruthy() || s.Get("clear").IsTruthy()) {
			legendFilter := `event.item && indexof(event.item.mark.role, "legend") < 0`
			for _, evt := range sel.events().Items() {
				filters := append([]Value{}, arrayOf(evt.Get("filter"))...)
				found := false
				for _, f := range filters {
					if f.IsStr() && f.StrValue() == legendFilter {
						found = true
					}
				}
				if !found {
					filters = append(filters, jsval.Str(legendFilter))
				}
				evt.ObjValue().Set("filter", jsval.Arr(filters))
			}
		}
		evt := jsval.Str("click")
		if isLegendStreamBinding(sel.props.Lookup("bind")) {
			evt = sel.props.Lookup("bind").Get("legend")
		}
		var stream Value
		if evt.IsStr() {
			stream = jsval.Arr(parseSelector(evt.StrValue(), "view"))
		} else {
			stream = jsval.Arr(arrayOf(evt))
		}
		sel.props.Set("bind", mkv("legend", mkv("merge", stream)))
	},
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		selName := sel.name
		var stream Value
		if isLegendStreamBinding(sel.props.Lookup("bind")) {
			stream = sel.props.Lookup("bind").Get("legend")
		}
		markName := func(name string) func(Value) Value {
			return func(s Value) Value {
				ds := deepClone(s)
				ds.ObjValue().Set("markname", jsval.Str(name))
				return ds
			}
		}
		for _, proj := range sel.project.items {
			if !proj.hasLegend {
				continue
			}
			prefix := varName(proj.field) + "_legend"
			sgName := selName + "_" + prefix
			if findSignal(sel.cc, signals, sgName) == nil {
				var events []Value
				for _, suffix := range []string{"_symbols", "_labels", "_entries"} {
					for _, s := range stream.Get("merge").Items() {
						events = append(events, markName(prefix+suffix)(s))
					}
				}
				o := mk("name", sgName)
				if !sel.props.Lookup("init").IsTruthy() {
					o.Set("value", jsval.Null)
				}
				o.Set("on", arr(
					mkv("events", jsval.Arr(events), "update", legendSelectionUpdate(m.b().ctx), "force", true),
					mkv("events", stream.Get("merge"), "update", "!event.item || !datum ? null : "+sgName, "force", true),
				))
				signals = append([]Value{jsval.Obj(o)}, signals...)
			}
		}
		return signals
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		tuple := findSignal(sel.cc, signals, name+tupleSuffix)
		fields := name + tupleFields
		var values, valid []string
		for _, p := range sel.project.items {
			if p.hasLegend {
				values = append(values, varName(name+"_"+varName(p.field)+"_legend"))
			}
		}
		for _, v := range values {
			valid = append(valid, v+" !== null")
		}
		update := strings.Join(valid, " && ") + " ? {fields: " + fields + ", values: [" + strings.Join(values, ", ") + "]} : null"
		if sel.events().IsTruthy() && len(values) > 0 {
			var evs []Value
			for _, v := range values {
				evs = append(evs, mkv("signal", v))
			}
			pushOn(tuple, mkv("events", jsval.Arr(evs), "update", update))
		} else if len(values) > 0 {
			tuple.Set("update", jsval.Str(update))
			tuple.Delete("value")
			tuple.Delete("on")
		}
		toggle := findSignal(sel.cc, signals, name+toggleSuffix)
		var events Value
		if isLegendStreamBinding(sel.props.Lookup("bind")) {
			events = sel.props.Lookup("bind").Get("legend")
		}
		if toggle != nil {
			on := toggle.Lookup("on")
			first := on.Index(0)
			if !sel.events().IsTruthy() {
				o := cloneObj(first.ObjValue())
				o.Set("events", events)
				items := append([]Value{jsval.Obj(o)}, on.Items()[1:]...)
				toggle.Set("on", jsval.Arr(items))
			} else {
				o := cloneObj(first.ObjValue())
				o.Set("events", events)
				pushOn(toggle, jsval.Obj(o))
			}
		}
		return signals
	},
}

// ---- clear ----

var clearCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool {
		c := s.props.Lookup("clear")
		return !c.IsUndefined() && !(c.IsBool() && !c.BoolValue()) && !isTimerSelection(s.cc, s)
	},
	parse: func(m *unitModel, sel *selectionComponent, _ Value) {
		if c := sel.props.Lookup("clear"); c.IsTruthy() {
			if c.IsStr() {
				sel.props.Set("clear", jsval.Arr(parseSelector(c.StrValue(), "view")))
			}
		}
	},
	topLevelSignals: func(m Model, sel *selectionComponent, signals []Value) []Value {
		if inputsCompiler.defined(sel) {
			for _, p := range sel.project.items {
				idx := findSignalIndex(sel.cc, signals, varName(sel.name+"_"+p.field))
				if idx != -1 {
					pushOn(signals[idx].ObjValue(), mkv("events", sel.props.Lookup("clear"), "update", "null"))
				}
			}
		}
		return signals
	},
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		addClear := func(idx int, update string) {
			if idx != -1 && signals[idx].Get("on").IsTruthy() {
				pushOn(signals[idx].ObjValue(), mkv("events", sel.props.Lookup("clear"), "update", update))
			}
		}
		if sel.typ == "interval" {
			for _, p := range sel.project.items {
				vIdx := findSignalIndex(sel.cc, signals, p.visSignal)
				addClear(vIdx, "[0, 0]")
				if vIdx == -1 {
					addClear(findSignalIndex(sel.cc, signals, p.dataSignal), "null")
				}
			}
		} else {
			tIdx := findSignalIndex(sel.cc, signals, sel.name+tupleSuffix)
			addClear(tIdx, "null")
			if toggleCompiler.defined(sel) {
				tIdx = findSignalIndex(sel.cc, signals, sel.name+toggleSuffix)
				addClear(tIdx, "false")
			}
		}
		return signals
	},
}

// ---- translate / zoom ----

const (
	translateAnchor = "_translate_anchor"
	translateDelta  = "_translate_delta"
	zoomAnchor      = "_zoom_anchor"
	zoomDelta       = "_zoom_delta"
)

func panFnFor(boundScales bool, scale *scaleComponent, prefix string) (fn, arg string) {
	scaleType := ""
	if scale != nil {
		scaleType = scale.get("type").AsString()
	}
	fn = prefix + "Linear"
	if boundScales && scale != nil {
		switch scaleType {
		case "log":
			fn = prefix + "Log"
		case "symlog":
			fn = prefix + "Symlog"
		case "pow":
			fn = prefix + "Pow"
		}
	}
	if boundScales {
		switch scaleType {
		case "pow":
			arg = ", " + coalesce(scale.get("exponent"), jsval.Int(1)).AsString()
		case "symlog":
			arg = ", " + coalesce(scale.get("constant"), jsval.Int(1)).AsString()
		}
	}
	return
}

var translateCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool { return s.typ == "interval" && s.props.Lookup("translate").IsTruthy() },
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		boundScales := scalesCompiler.defined(sel)
		anchor := name + translateAnchor
		x, _ := sel.project.hasChannel.get("x")
		y, _ := sel.project.hasChannel.get("y")
		events := parseSelector(sel.props.Lookup("translate").AsString(), "scope")
		for _, e := range events {
			if b := e.Get("between"); !b.IsArr() || b.Len() == 0 || !b.Index(0).IsObj() {
				throw("Invalid translate selector %s in the selection %q: expected a drag selector such as \"[mousedown, window:mouseup] > window:mousemove!\".", stringify(sel.props.Lookup("translate")), name)
			}
		}
		if !boundScales {
			for _, e := range events {
				e.Get("between").Index(0).ObjValue().Set("markname", jsval.Str(name+brushSuffix))
			}
		}
		var between []Value
		for _, e := range events {
			between = append(between, e.Get("between").Index(0))
		}
		upd := "{x: x(unit), y: y(unit)"
		if x != nil {
			if boundScales {
				upd += ", extent_x: " + selDomain(m, chX)
			} else {
				upd += ", extent_x: slice(" + x.visSignal + ")"
			}
		}
		if y != nil {
			if boundScales {
				upd += ", extent_y: " + selDomain(m, chY)
			} else {
				upd += ", extent_y: slice(" + y.visSignal + ")"
			}
		}
		upd += "}"
		signals = append(signals,
			mkv("name", anchor, "value", mkv(), "on", arr(mkv("events", jsval.Arr(between), "update", upd))),
			mkv("name", name+translateDelta, "value", mkv(), "on", arr(mkv("events", jsval.Arr(events), "update", "{x: "+anchor+".x - x(unit), y: "+anchor+".y - y(unit)}"))),
		)
		if x != nil {
			translateOnDelta(m, sel, x, "width", signals)
		}
		if y != nil {
			translateOnDelta(m, sel, y, "height", signals)
		}
		return signals
	},
}

func translateOnDelta(m *unitModel, sel *selectionComponent, proj *selProjItem, size string, signals []Value) {
	name := sel.name
	anchor := name + translateAnchor
	delta := name + translateDelta
	channel := proj.channel
	boundScales := scalesCompiler.defined(sel)
	target := proj.visSignal
	if boundScales {
		target = proj.dataSignal
	}
	signal := findSignal(sel.cc, signals, target)
	sizeSg := signalOf(m.getSizeSignalRef(size))
	scaleCmpt := m.getScaleComponent(channel)
	reversed := undef
	if scaleCmpt != nil {
		reversed = scaleCmpt.get("reverse")
	}
	sign := ""
	if boundScales {
		if channel == chX {
			if !reversed.IsTruthy() {
				sign = "-"
			}
		} else if reversed.IsTruthy() {
			sign = "-"
		}
	}
	extent := anchor + ".extent_" + channel
	var denom string
	if boundScales {
		denom = sizeSg
	} else {
		denom = "span(" + extent + ")"
	}
	offset := sign + delta + "." + channel + " / " + denom
	panFn, arg := panFnFor(boundScales, scaleCmpt, "pan")
	update := panFn + "(" + extent + ", " + offset + arg + ")"
	up := update
	if !boundScales {
		up = "clampRange(" + update + ", 0, " + sizeSg + ")"
	}
	pushOn(signal, mkv("events", mkv("signal", delta), "update", up))
}

var zoomCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool { return s.typ == "interval" && s.props.Lookup("zoom").IsTruthy() },
	signals: func(m *unitModel, sel *selectionComponent, signals []Value) []Value {
		name := sel.name
		boundScales := scalesCompiler.defined(sel)
		delta := name + zoomDelta
		x, _ := sel.project.hasChannel.get("x")
		y, _ := sel.project.hasChannel.get("y")
		sx, sy := "", ""
		if n := m.scaleName(chX, false); n != "" {
			sx = stringValue(jsval.Str(n))
		}
		if n := m.scaleName(chY, false); n != "" {
			sy = stringValue(jsval.Str(n))
		}
		events := parseSelector(sel.props.Lookup("zoom").AsString(), "scope")
		if !boundScales {
			for _, e := range events {
				e.ObjValue().Set("markname", jsval.Str(name+brushSuffix))
			}
		}
		var update string
		if !boundScales {
			update = "{x: x(unit), y: y(unit)}"
		} else {
			var parts []string
			if sx != "" {
				parts = append(parts, "x: invert("+sx+", x(unit))")
			}
			if sy != "" {
				parts = append(parts, "y: invert("+sy+", y(unit))")
			}
			update = "{" + strings.Join(parts, ", ") + "}"
		}
		signals = append(signals,
			mkv("name", name+zoomAnchor, "on", arr(mkv("events", jsval.Arr(events), "update", update))),
			mkv("name", delta, "on", arr(mkv("events", jsval.Arr(events), "force", true, "update", "pow(1.001, event.deltaY * pow(16, event.deltaMode))"))),
		)
		if x != nil {
			zoomOnDelta(m, sel, x, "width", signals)
		}
		if y != nil {
			zoomOnDelta(m, sel, y, "height", signals)
		}
		return signals
	},
}

func zoomOnDelta(m *unitModel, sel *selectionComponent, proj *selProjItem, size string, signals []Value) {
	name := sel.name
	channel := proj.channel
	boundScales := scalesCompiler.defined(sel)
	target := proj.visSignal
	if boundScales {
		target = proj.dataSignal
	}
	signal := findSignal(sel.cc, signals, target)
	sizeSg := signalOf(m.getSizeSignalRef(size))
	scaleCmpt := m.getScaleComponent(channel)
	var base string
	if boundScales {
		base = selDomain(m, channel)
	} else {
		base = signal.Lookup("name").AsString()
	}
	delta := name + zoomDelta
	anchor := name + zoomAnchor + "." + channel
	zoomFn, arg := panFnFor(boundScales, scaleCmpt, "zoom")
	update := zoomFn + "(" + base + ", " + anchor + ", " + delta + arg + ")"
	up := update
	if !boundScales {
		up = "clampRange(" + update + ", 0, " + sizeSg + ")"
	}
	pushOn(signal, mkv("events", mkv("signal", delta), "update", up))
}

// ---- nearest ----

var nearestCompiler = selectionCompiler{
	defined: func(s *selectionComponent) bool { return s.typ == "point" && s.props.Lookup("nearest").IsTruthy() },
	parse: func(m *unitModel, sel *selectionComponent, _ Value) {
		for _, s := range sel.events().Items() {
			s.ObjValue().Set("markname", jsval.Str(m.getName("voronoi")))
		}
	},
	marks: func(m *unitModel, sel *selectionComponent, marks []Value) []Value {
		x, _ := sel.project.hasChannel.get("x")
		y, _ := sel.project.hasChannel.get("y")
		if isPathMarkName(m.mark()) {
			return marks
		}
		upd := mk("fill", mkv("value", "transparent"), "strokeWidth", mkv("value", 0.35), "stroke", mkv("value", "transparent"), "isVoronoi", mkv("value", true))
		spreadV(upd, tooltipEncode(m, true))
		xe, ye := "0", "0"
		if x != nil || y == nil {
			xe = "datum.datum.x || 0"
		}
		if y != nil || x == nil {
			ye = "datum.datum.y || 0"
		}
		cellDef := mkv(
			"name", m.getName("voronoi"), "type", "path", "interactive", true,
			"from", mkv("data", m.getName("marks")),
			"encode", mkv("update", jsval.Obj(upd)),
			"transform", arr(mkv("type", "voronoi", "x", mkv("expr", xe), "y", mkv("expr", ye), "size", arr(m.getSizeSignalRef("width"), m.getSizeSignalRef("height")))),
		)
		if !m.ctx.v5 {
			// 6.x hides the voronoi cells from assistive technology.
			o := mk("name", m.getName("voronoi"), "type", "path", "interactive", true, "aria", false)
			spread(o, jsval.Obj(omit(cellDef, "name", "type", "interactive")))
			cellDef = jsval.Obj(o)
		}
		index := 0
		exists := false
		firstMarkName := ""
		if len(m.comp.mark) > 0 {
			firstMarkName = m.comp.mark[0].Get("name").AsString()
		}
		for i, mk_ := range marks {
			n := mk_.Get("name").AsString()
			if n == firstMarkName {
				index = i
			} else if strings.Contains(n, "voronoi") {
				exists = true
			}
		}
		if !exists {
			out := append([]Value{}, marks[:index+1]...)
			out = append(out, cellDef)
			out = append(out, marks[index+1:]...)
			return out
		}
		return marks
	},
}
