package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

func buildModel(cc *compileCtx, spec Value, parent Model, parentGivenName string, unitSize *Object, config Value, depth int) Model {
	cc.check()
	if depth > maxDepth {
		exceeded(depthMsg)
	}
	switch {
	case isFacetSpec(spec):
		return newFacetModel(cc, spec, parent, parentGivenName, config, depth)
	case isLayerSpec(spec):
		return newLayerModel(cc, spec, parent, parentGivenName, unitSize, config, depth)
	case isUnitSpec(spec):
		return newUnitModel(cc, spec, parent, parentGivenName, unitSize, config)
	case isAnyConcatSpec(spec):
		return newConcatModel(cc, spec, parent, parentGivenName, config, depth)
	}
	throw("Invalid spec: %s", stringify(spec))
	return nil
}

// layerModel overlays child views sharing one coordinate system.
type layerModel struct {
	*modelBase
	kids []Model
}

func newLayerModel(cc *compileCtx, spec Value, parent Model, parentGivenName string, parentGivenSize *Object, config Value, depth int) *layerModel {
	if depth > maxDepth { // layers nest without going through buildModel
		exceeded(depthMsg)
	}
	l := &layerModel{}
	var resolve *resolveIndex
	if r := spec.Get("resolve"); r.IsObj() {
		resolve = resolveFromSpec(r)
	}
	l.modelBase = newModelBase(cc, l, spec, "layer", parent, parentGivenName, config, resolve, spec.Get("view"))
	layoutSize := cloneObj(parentGivenSize)
	if v := spec.Get("width"); v.IsTruthy() {
		layoutSize.Set("width", v)
	}
	if v := spec.Get("height"); v.IsTruthy() {
		layoutSize.Set("height", v)
	}
	for i, layer := range spec.Get("layer").Items() {
		cc.check()
		name := l.getName("layer_" + jsval.JSNumberString(float64(i)))
		switch {
		case isLayerSpec(layer):
			l.kids = append(l.kids, newLayerModel(cc, layer, l, name, layoutSize, config, depth+1))
		case isUnitSpec(layer):
			l.kids = append(l.kids, newUnitModel(cc, layer, l, name, layoutSize, config))
		default:
			throw("Invalid spec: %s", stringify(layer))
		}
	}
	return l
}

func resolveFromSpec(r Value) *resolveIndex {
	res := &resolveIndex{map[string]string{}, map[string]string{}, map[string]string{}}
	fill := func(dst map[string]string, v Value) {
		for _, k := range keysOf(v) {
			s := v.Get(k)
			switch {
			case s.IsStr():
				dst[k] = s.StrValue()
			case s.IsTruthy():
				// An invalid (non-string) resolve mode is truthy but equals neither
				// "shared" nor "independent".
				dst[k] = "\x00invalid"
			}
		}
	}
	fill(res.scale, r.Get("scale"))
	fill(res.axis, r.Get("axis"))
	fill(res.legend, r.Get("legend"))
	return res
}

func (l *layerModel) children() []Model { return l.kids }

func (l *layerModel) parseData() {
	l.comp.data = parseDataFor(l)
	for _, c := range l.kids {
		l.b().ctx.check()
		c.parseData()
	}
}

func (l *layerModel) parseLayoutSize() { parseLayerLayoutSize(l) }

func (l *layerModel) parseSelections() {
	l.comp.selection = newOmap[*selectionComponent]()
	for _, c := range l.kids {
		l.b().ctx.check()
		c.parseSelections()
		cs := c.b().comp.selection
		for _, k := range cs.keyList() {
			v, _ := cs.get(k)
			l.comp.selection.set(k, v)
		}
	}
}

func (l *layerModel) parseMarkGroup() {
	for _, c := range l.kids {
		l.b().ctx.check()
		c.parseMarkGroup()
	}
}

func (l *layerModel) parseAxesAndHeaders() { parseLayerAxes(l) }

func (l *layerModel) assembleSelectionTopLevelSignals(signals []Value) []Value {
	for _, c := range l.kids {
		l.b().ctx.check()
		signals = c.assembleSelectionTopLevelSignals(signals)
	}
	return signals
}

func (l *layerModel) assembleSignals() []Value {
	signals := assembleAxisSignals(l)
	for _, c := range l.kids {
		l.b().ctx.check()
		signals = append(signals, c.assembleSignals()...)
	}
	return signals
}

func (l *layerModel) assembleLayoutSignals() []Value {
	signals := assembleLayoutSignals(l)
	for _, c := range l.kids {
		l.b().ctx.check()
		signals = append(signals, c.assembleLayoutSignals()...)
	}
	return signals
}

func (l *layerModel) assembleSelectionData(data []Value) []Value {
	for _, c := range l.kids {
		l.b().ctx.check()
		data = c.assembleSelectionData(data)
	}
	return data
}

func (l *layerModel) assembleGroupStyle() Value {
	seen := map[string]bool{}
	var styles []string
	for _, c := range l.kids {
		l.b().ctx.check()
		for _, s := range arrayOf(c.assembleGroupStyle()) {
			if !seen[s.AsString()] {
				seen[s.AsString()] = true
				styles = append(styles, s.AsString())
			}
		}
	}
	switch {
	case len(styles) > 1:
		return strsVal(styles)
	case len(styles) == 1:
		return jsval.Str(styles[0])
	}
	return undef
}

func (l *layerModel) assembleTitle() Value {
	if t := l.modelBase.assembleTitle(); t.IsTruthy() {
		return t
	}
	for _, c := range l.kids {
		l.b().ctx.check()
		if t := c.assembleTitle(); t.IsTruthy() {
			return t
		}
	}
	return undef
}

func (l *layerModel) assembleLayout() Value { return jsval.Null }

func (l *layerModel) assembleMarks() []Value {
	var marks []Value
	for _, c := range l.kids {
		l.b().ctx.check()
		marks = append(marks, c.assembleMarks()...)
	}
	return assembleLayerSelectionMarks(l, marks)
}

func (l *layerModel) assembleLegends() []Value {
	legends := assembleLegends(l)
	for _, c := range l.kids {
		l.b().ctx.check()
		legends = append(legends, c.assembleLegends()...)
	}
	return legends
}

// ---- concat ----

type concatModel struct {
	*modelBase
	kids []Model
}

func newConcatModel(cc *compileCtx, spec Value, parent Model, parentGivenName string, config Value, depth int) *concatModel {
	c := &concatModel{}
	var resolve *resolveIndex
	if r := spec.Get("resolve"); r.IsObj() {
		resolve = resolveFromSpec(r)
	}
	c.modelBase = newModelBase(cc, c, spec, "concat", parent, parentGivenName, config, resolve, undef)
	var childSpecs Value
	switch {
	case isVConcatSpec(spec):
		childSpecs = spec.Get("vconcat")
	case isHConcatSpec(spec):
		childSpecs = spec.Get("hconcat")
	default:
		childSpecs = spec.Get("concat")
	}
	for i, child := range childSpecs.Items() {
		cc.check()
		c.kids = append(c.kids, buildModel(cc, child, c, c.getName("concat_"+jsval.JSNumberString(float64(i))), nil, config, depth+1))
	}
	return c
}

func (c *concatModel) children() []Model { return c.kids }

func (c *concatModel) parseData() {
	c.comp.data = parseDataFor(c)
	for _, k := range c.kids {
		c.b().ctx.check()
		k.parseData()
	}
}

func (c *concatModel) parseSelections() {
	c.comp.selection = newOmap[*selectionComponent]()
	for _, k := range c.kids {
		c.b().ctx.check()
		k.parseSelections()
		cs := k.b().comp.selection
		for _, key := range cs.keyList() {
			v, _ := cs.get(key)
			c.comp.selection.set(key, v)
		}
	}
}

func (c *concatModel) parseMarkGroup() {
	for _, k := range c.kids {
		c.b().ctx.check()
		k.parseMarkGroup()
	}
}

func (c *concatModel) parseAxesAndHeaders() {
	for _, k := range c.kids {
		c.b().ctx.check()
		k.parseAxesAndHeaders()
	}
}

func (c *concatModel) parseLayoutSize() { parseConcatLayoutSize(c) }

func (c *concatModel) assembleSelectionTopLevelSignals(signals []Value) []Value {
	for _, k := range c.kids {
		c.b().ctx.check()
		signals = k.assembleSelectionTopLevelSignals(signals)
	}
	return signals
}

func (c *concatModel) assembleSignals() []Value {
	for _, k := range c.kids {
		c.b().ctx.check()
		k.assembleSignals()
	}
	return nil
}

func (c *concatModel) assembleLayoutSignals() []Value {
	signals := assembleLayoutSignals(c)
	for _, k := range c.kids {
		c.b().ctx.check()
		signals = append(signals, k.assembleLayoutSignals()...)
	}
	return signals
}

func (c *concatModel) assembleSelectionData(data []Value) []Value {
	for _, k := range c.kids {
		c.b().ctx.check()
		data = k.assembleSelectionData(data)
	}
	return data
}

func (c *concatModel) assembleMarks() []Value {
	var out []Value
	for _, k := range c.kids {
		c.b().ctx.check()
		title := k.assembleTitle()
		style := k.assembleGroupStyle()
		encodeEntry := k.b().assembleGroupEncodeEntry(false)
		o := mk("type", "group", "name", k.b().getName("group"))
		if title.IsTruthy() {
			o.Set("title", title)
		}
		if style.IsTruthy() {
			o.Set("style", style)
		}
		if encodeEntry.IsTruthy() {
			o.Set("encode", mkv("update", encodeEntry))
		}
		spread(o, jsval.Obj(assembleGroup(k, nil)))
		out = append(out, jsval.Obj(o))
	}
	return out
}

func (c *concatModel) assembleGroupStyle() Value { return undef }

func (c *concatModel) assembleDefaultLayout() *Object {
	o := jsval.NewObject(3)
	if cols := c.layout.Lookup("columns"); !cols.IsNullish() {
		o.Set("columns", cols)
	}
	o.Set("bounds", jsval.Str("full"))
	o.Set("align", jsval.Str("each"))
	return o
}

func (c *concatModel) assembleTitle() Value     { return c.modelBase.assembleTitle() }
func (c *concatModel) assembleLegends() []Value { return c.modelBase.assembleLegends() }
func (c *concatModel) assembleLayout() Value    { return c.modelBase.assembleLayout() }
