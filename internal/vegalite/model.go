package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The model tree: vega-lite/src/compile/model.ts. A normalized spec becomes a
// tree of unit, layer, facet and concat models; parsing fills each model's
// component (scales, axes, legends, data, ...) and assembly turns components
// into a Vega spec.

// Model is implemented by unitModel, layerModel, facetModel and concatModel.
type Model interface {
	b() *modelBase
	children() []Model

	parseData()
	parseSelections()
	parseLayoutSize()
	parseMarkGroup()
	parseAxesAndHeaders()

	assembleSelectionTopLevelSignals(signals []Value) []Value
	assembleSignals() []Value
	assembleSelectionData(acc *selDataAcc)
	assembleGroupStyle() Value
	assembleLayout() Value
	assembleDefaultLayout() *Object
	assembleLayoutSignals() []Value
	assembleMarks() []Value
	assembleTitle() Value
	assembleLegends() []Value
}

// nameMap records renames (scale, projection and signal names change when
// child specs merge into their parent).
type nameMap struct{ m map[string]string }

func newNameMap() *nameMap { return &nameMap{m: map[string]string{}} }

func (n *nameMap) rename(old, nw string) { n.m[old] = nw }
func (n *nameMap) has(name string) bool  { return n.m[name] != "" }
func (n *nameMap) get(name string) string {
	// The new name may itself have been renamed; follow the chain (bounded, as a
	// cycle would otherwise never end).
	for i := 0; i < 1000; i++ {
		next := n.m[name]
		if next == "" || next == name {
			break
		}
		name = next
	}
	return name
}

type resolveIndex struct {
	scale, axis, legend map[string]string
	// prim holds resolve.scale, .axis and .legend of the specification
	// when it is there but not an object: upstream reads and writes it as an
	// object, and writing a property of a primitive (or reading one of null)
	// is a TypeError (see read and write).
	prim [3]*Value
}

// The kinds of resolveIndex.prim.
const (
	resolveScale = iota
	resolveAxis
	resolveLegend
)

// read is upstream reading resolve[kind][channel].
func (r *resolveIndex) read(kind int, channel string) {
	if v := r.prim[kind]; v != nil && v.IsNull() {
		throw("Cannot read properties of null (reading '%s')", channel)
	}
}

// write is upstream assigning resolve[kind][channel].
func (r *resolveIndex) write(kind int, channel string) {
	v := r.prim[kind]
	switch {
	case v == nil:
	case v.IsNull():
		throw("Cannot set properties of null (setting '%s')", channel)
	default:
		typ := "string"
		switch {
		case v.IsNum():
			typ = "number"
		case v.IsBool():
			typ = "boolean"
		}
		throw("Cannot create property '%s' on %s '%s'", channel, typ, v.AsString())
	}
}

func (r *resolveIndex) clone() *resolveIndex {
	c := &resolveIndex{scale: map[string]string{}, axis: map[string]string{}, legend: map[string]string{}, prim: r.prim}
	for k, v := range r.scale {
		c.scale[k] = v
	}
	for k, v := range r.axis {
		c.axis[k] = v
	}
	for k, v := range r.legend {
		c.legend[k] = v
	}
	return c
}

type component struct {
	data          *dataComponent
	layoutSize    *split
	layoutHeaders map[string]*layoutHeaderComponent
	mark          []Value
	scales        *omap[*scaleComponent]
	projection    *projectionComponent
	selection     *omap[*selectionComponent]
	axes          *omap[[]*axisComponent]
	legends       *omap[*legendComponent]
	resolve       *resolveIndex
}

type modelBase struct {
	ctx         *compileCtx
	self        Model
	typ         string
	name        string
	nameFalsy   bool // the spec's name is a falsy value with text (0, false)
	parent      Model
	config      Value
	size        *Object
	title       Value
	description Value
	data        Value
	transforms  []Value
	layout      *Object
	scaleNames  *nameMap
	projNames   *nameMap
	signalNames *nameMap
	comp        *component
	view        Value
}

func (m *modelBase) b() *modelBase { return m }

func newModelBase(cc *compileCtx, self Model, spec Value, typ string, parent Model, parentGivenName string, config Value, resolve *resolveIndex, view Value) *modelBase {
	m := &modelBase{ctx: cc, self: self, typ: typ, parent: parent, config: config}
	m.view = replaceExprRef(cc, view, 0)
	if view.IsUndefined() {
		m.view = undef
	}
	if n := spec.Get("name"); !n.IsNullish() {
		m.name = n.AsString()
		m.nameFalsy = !n.IsTruthy()
	} else {
		m.name = parentGivenName
	}
	switch t := spec.Get("title"); {
	case isText(t):
		m.title = mkv("text", t)
	case t.IsTruthy():
		m.title = replaceExprRef(cc, t, 0)
	}
	if parent != nil {
		pb := parent.b()
		m.scaleNames, m.projNames, m.signalNames = pb.scaleNames, pb.projNames, pb.signalNames
	} else {
		m.scaleNames, m.projNames, m.signalNames = newNameMap(), newNameMap(), newNameMap()
	}
	m.data = spec.Get("data")
	m.description = spec.Get("description")
	m.transforms = normalizeTransformList(cc, spec.Get("transform"))
	if typ == "layer" || typ == "unit" {
		m.layout = jsval.NewObject(0)
	} else {
		m.layout = extractCompositionLayout(spec, typ, config)
	}
	dc := &dataComponent{sources: &sourceList{}}
	if parent != nil {
		pd := parent.b().comp.data
		dc.sources = pd.sources
		dc.outputNodes = pd.outputNodes
		dc.outputNodeRefCounts = pd.outputNodeRefCounts
		dc.isFaceted = isFacetSpec(spec) || (pd.isFaceted && spec.Get("data").IsUndefined())
	} else {
		dc.outputNodes = map[string]sourceGetter{}
		dc.outputNodeRefCounts = map[string]int{}
		dc.isFaceted = isFacetSpec(spec)
	}
	res := &resolveIndex{scale: map[string]string{}, axis: map[string]string{}, legend: map[string]string{}}
	if resolve != nil {
		res = resolve.clone()
	}
	m.comp = &component{
		data:       dc,
		layoutSize: newSplit(),
		layoutHeaders: map[string]*layoutHeaderComponent{
			"row": {}, "column": {}, "facet": {},
		},
		resolve: res,
		axes:    newOmap[[]*axisComponent](),
		legends: newOmap[*legendComponent](),
	}
	return m
}

func normalizeTransformList(cc *compileCtx, tx Value) []Value {
	// spec.transform ?? [], then transform.map; 5.8 tests each with
	// `'filter' in t`.
	if tx.IsNullish() {
		return nil
	}
	var out []Value
	for _, t := range listOf(tx, "transform", "map") {
		cc.work(1)
		if cc.v5 && t.IsArr() {
			// 'filter' in t holds for an array (Array.prototype.filter), so
			// 5.8 makes it a filter whose predicate is that function; its
			// expression reads the field of the function, undefined, as an
			// access path.
			throw("Cannot read properties of undefined (reading 'length')")
		}
		if (cc.v5 && jsIn(t, "filter")) || (!cc.v5 && hasProperty(t, "filter")) {
			out = append(out, mkv("filter", normalizeLogicalComposition(t.Get("filter"), func(f Value) Value { return normalizePredicate(cc, f) }, 0)))
		} else {
			out = append(out, t)
		}
	}
	return out
}

func isFacetModel(m Model) bool  { _, ok := m.(*facetModel); return ok }
func isConcatModel(m Model) bool { _, ok := m.(*concatModel); return ok }
func isLayerModel(m Model) bool  { _, ok := m.(*layerModel); return ok }

func asUnit(m Model) *unitModel {
	u, _ := m.(*unitModel)
	return u
}

func (m *modelBase) getName(text string) string {
	// (this.name ? `${this.name}_` : ''): a name of 0 or false is no prefix.
	prefix := ""
	if m.name != "" && !m.nameFalsy {
		prefix = m.name + "_"
	}
	return varName(prefix + text)
}

func (m *modelBase) getDataName(t dataSourceType) string {
	return m.getName(strings.ToLower(t.String()))
}

func (m *modelBase) requestDataName(t dataSourceType) string {
	full := m.getDataName(t)
	m.comp.data.outputNodeRefCounts[full]++
	return full
}

func (m *modelBase) getSignalName(old string) string { return m.signalNames.get(old) }
func (m *modelBase) renameSignal(old, nw string)     { m.signalNames.rename(old, nw) }
func (m *modelBase) renameScale(old, nw string)      { m.scaleNames.rename(old, nw) }
func (m *modelBase) renameProjection(old, nw string) { m.projNames.rename(old, nw) }

// lookupDataSource is the data set an output node of that name reads, as
// upstream holds its name (see outputNode.source).
func (m *modelBase) lookupDataSource(name string) Value {
	node, ok := m.comp.data.outputNodes[name]
	if !ok || node == nil {
		return jsval.Str(name)
	}
	return node.getSource()
}

func (m *modelBase) width() Value  { return m.getSizeSignalRef("width") }
func (m *modelBase) height() Value { return m.getSizeSignalRef("height") }

func (m *modelBase) getSizeSignalRef(layoutSizeType string) Value {
	cc := m.b().ctx

	if m.parent != nil && isFacetModel(m.parent) {
		sizeType := getSizeTypeFromLayoutSizeType(layoutSizeType)
		channel := getPositionScaleChannel(sizeType)
		if sc, ok := m.comp.scales.get(channel); ok && sc != nil && !sc.merged {
			typ := sc.get("type").AsString()
			rng := sc.get("range")
			if hasDiscreteDomain(typ) && isVgRangeStep(m.ctx, rng) {
				scaleName := sc.get("name").AsString()
				domain := assembleDomain(m.self, channel)
				field := getFieldFromDomain(cc, domain)
				if field != "" {
					fieldRef := vgField(cc, mkv("aggregate", "distinct", "field", field), fieldRefOption{expr: "datum"})
					return sig(sizeExpr(scaleName, sc, fieldRef))
				}
				cc.warn(msgUnknownField(channel))
				return jsval.Null
			}
		}
	}
	return sig(m.signalNames.get(m.getName(layoutSizeType)))
}

// isVgRangeStep is 6.x's hasProperty(range, 'step'), and 5.8's
// !!range['step'], which reads the property of an undefined range.
func isVgRangeStep(cc *compileCtx, rng Value) bool {
	if cc.v5 {
		return jsGet(rng, "step").IsTruthy()
	}
	return hasProperty(rng, "step")
}

func getSizeTypeFromLayoutSizeType(t string) string {
	switch t {
	case "childWidth":
		return "width"
	case "childHeight":
		return "height"
	}
	return t
}

// scaleTypeOf is the type of the channel's scale, "" when the channel has none
// (for example when the specification sets `scale: null`).
func (m *modelBase) scaleTypeOf(channel string) string {
	if sc := m.getScaleComponent(channel); sc != nil {
		return sc.get("type").AsString()
	}
	return ""
}

func (m *modelBase) getScaleComponent(channel string) *scaleComponent {
	if m.comp.scales == nil {
		throw("getScaleComponent cannot be called before parseScale().")
	}
	if local, ok := m.comp.scales.get(channel); ok && local != nil && !local.merged {
		return local
	}
	if m.parent != nil {
		return m.parent.b().getScaleComponent(channel)
	}
	return nil
}

func (m *modelBase) getScaleType(channel string) string {
	if sc := m.getScaleComponent(channel); sc != nil {
		return sc.get("type").AsString()
	}
	return ""
}

// trySelectionComponent is getSelectionComponent for callers that catch the
// throw. Upstream keeps a model's selections in a plain object, so a name every
// object inherits (toString, __proto__, ...) that the model does not define
// finds Object.prototype's value rather than nothing: it is no selection
// (junk is true, and sel nil), and does not throw.
func (m *modelBase) trySelectionComponent(variableName string) (sel *selectionComponent, junk bool) {
	if m.comp.selection != nil {
		sel, own, ref := m.comp.selection.read(m.b().ctx.realm(), variableName)
		if own && sel != nil {
			return sel, false
		}
		// A selection named "__proto__" became the object's prototype, so
		// that name reads it back.
		if s, ok := ref.val.(*selectionComponent); ok && !own {
			return s, false
		}
	}
	if isInheritedKey(variableName) {
		return nil, true
	}
	if m.parent != nil {
		return m.parent.b().trySelectionComponent(variableName)
	}
	return nil, false
}

// scaleName returns the (possibly renamed) name of the channel's scale; "" when there is none.
func (m *modelBase) scaleName(channel string, parse bool) string {
	cc := m.b().ctx

	if parse {
		return m.getName(channel)
	}
	has := false
	if isChannel(cc, channel) && isScaleChannel(cc, channel) && m.comp.scales != nil {
		if sc, ok := m.comp.scales.get(channel); ok && sc != nil {
			has = true
		}
	}
	if has || m.scaleNames.has(m.getName(channel)) {
		return m.scaleNames.get(m.getName(channel))
	}
	return ""
}

func (m *modelBase) projectionName(parse bool) string {
	if parse {
		return m.getName("projection")
	}
	if (m.comp.projection != nil && !m.comp.projection.merged) || m.projNames.has(m.getName("projection")) {
		return m.projNames.get(m.getName("projection"))
	}
	return ""
}

func (m *modelBase) hasAxisOrientSignalRef() bool {
	cc := m.ctx
	for _, ch := range []string{"x", "y"} {
		axes, _ := m.comp.axes.get(ch)
		for _, a := range axes {
			cc.work(1)
			if a.hasOrientSignalRef() {
				return true
			}
		}
	}
	return false
}

// ---- parse pipeline ----

// parse runs upstream's Model.parse in its fixed order.
func parseModel(m Model) {
	b := m.b()
	b.ctx.check()
	parseScales(m, false)
	m.parseLayoutSize() // depends on scale
	b.renameTopLevelLayoutSizeSignal()
	m.parseSelections()
	parseProjection(m)
	m.parseData() // depends on markDef, selections, projection
	m.parseAxesAndHeaders()
	parseLegend(m)
	m.parseMarkGroup()
}

func (m *modelBase) renameTopLevelLayoutSizeSignal() {
	if m.getName("width") != "width" {
		m.renameSignal(m.getName("width"), "width")
	}
	if m.getName("height") != "height" {
		m.renameSignal(m.getName("height"), "height")
	}
}

// ---- assemble ----

func (m *modelBase) assembleGroupEncodeEntry(isTopLevel bool) Value {
	cc := m.ctx
	encodeEntry := jsval.NewObject(4)
	if m.view.IsTruthy() {
		base := omit(m.view, "style")
		for _, p := range base.Keys() {
			cc.work(1)
			if v := base.Lookup(p); !v.IsUndefined() {
				encodeEntry.Set(p, signalOrValueRef(m.ctx, v))
			}
		}
	}
	if !isTopLevel {
		if m.description.IsTruthy() {
			encodeEntry.Set("description", signalOrValueRef(m.ctx, m.description))
		}
		if m.typ == "unit" || m.typ == "layer" {
			o := mk("width", m.getSizeSignalRef("width"), "height", m.getSizeSignalRef("height"))
			spread(o, jsval.Obj(encodeEntry))
			return jsval.Obj(o)
		}
	}
	if encodeEntry.Len() == 0 {
		return undef
	}
	return jsval.Obj(encodeEntry)
}

func (m *modelBase) assembleLayout() Value {
	if m.layout == nil {
		return undef
	}
	spacing := m.layout.Lookup("spacing")
	layout := omit(jsval.Obj(m.layout), "spacing")
	titleBand := assembleLayoutTitleBand(m.comp.layoutHeaders, m.config)
	out := jsval.NewObject(8)
	out.Set("padding", spacing)
	spread(out, jsval.Obj(m.self.assembleDefaultLayout()))
	spread(out, jsval.Obj(layout))
	if titleBand.IsTruthy() {
		out.Set("titleBand", titleBand)
	}
	return jsval.Obj(out)
}

func (m *modelBase) assembleDefaultLayout() *Object { return jsval.NewObject(0) }

func (m *modelBase) assembleHeaderMarks() []Value {
	var marks []Value
	for _, ch := range facetChannels {
		if m.comp.layoutHeaders[ch].title.IsTruthy() {
			marks = append(marks, assembleTitleGroup(m.self, ch))
		}
	}
	for _, ch := range headerChannels {
		marks = append(marks, assembleHeaderGroups(m.self, ch)...)
	}
	return marks
}

func (m *modelBase) assembleAxes() []Value {
	cc := m.b().ctx
	return assembleAxes(cc, m.comp.axes, m.config)
}

func (m *modelBase) assembleLegends() []Value { return assembleLegends(m.self) }

func (m *modelBase) assembleProjections() []Value { return assembleProjections(m.self) }

func (m *modelBase) assembleTitle() Value {
	title := jsval.NewObject(4)
	var encoding Value
	noEnc := mkv()
	if m.title.IsTruthy() {
		encoding = m.title.Get("encoding")
		noEnc = jsval.Obj(omit(m.title, "encoding"))
	}
	spread(title, jsval.Obj(extractTitleConfig(m.config.Get("title")).nonMarkTitleProperties))
	spread(title, noEnc)
	if encoding.IsTruthy() {
		title.Set("encode", mkv("update", encoding))
	}
	tv := jsval.Obj(title)
	if tv.Get("text").IsTruthy() {
		if m.typ == "unit" || m.typ == "layer" {
			anchor := tv.Get("anchor")
			if anchor.IsUndefined() || (anchor.IsStr() && anchor.StrValue() == "middle") {
				if title.Lookup("frame").IsNullish() {
					title.Set("frame", jsval.Str("group"))
				}
			}
		} else {
			if title.Lookup("anchor").IsNullish() {
				title.Set("anchor", jsval.Str("start"))
			}
		}
		if title.Len() == 0 {
			return undef
		}
		return tv
	}
	return undef
}

// assembleGroup builds the group properties (signals, layout, marks, scales,
// axes, legends) in upstream's order.
func assembleGroup(m Model, signals []Value) *Object {
	b := m.b()
	b.ctx.check()
	group := jsval.NewObject(6)
	signals = append(append([]Value{}, signals...), m.assembleSignals()...)
	if len(signals) > 0 {
		group.Set("signals", jsval.Arr(signals))
	}
	if layout := m.assembleLayout(); layout.IsTruthy() {
		group.Set("layout", layout)
	}
	marks := append(append([]Value{}, b.assembleHeaderMarks()...), m.assembleMarks()...)
	group.Set("marks", jsval.Arr(marks))
	// Only the root or a facet's child owns its scales; otherwise they are merged into the parent's.
	if b.parent == nil || isFacetModel(b.parent) {
		if scales := assembleScales(m); len(scales) > 0 {
			group.Set("scales", jsval.Arr(scales))
		}
	}
	if axes := b.assembleAxes(); len(axes) > 0 {
		group.Set("axes", jsval.Arr(axes))
	}
	if legends := m.assembleLegends(); len(legends) > 0 {
		group.Set("legends", jsval.Arr(legends))
	}
	return group
}
