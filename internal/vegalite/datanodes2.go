package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ---- bin ----

type binComponent struct {
	bin          Value
	field        string
	noField      bool // the field def has no field: upstream throws when it assembles the bin
	as           [][]string
	signal       string
	extentSignal string
	span         string
	formula      string
	formulaAs    string
}

type binNode struct {
	dfBase
	bins *omap[*binComponent]
}

func newBinNode(parent dfNode, bins *omap[*binComponent]) *binNode {
	return initNode(&binNode{bins: bins}, parent)
}

func (b *binComponent) clone() *binComponent {
	c := *b
	c.bin = deepClone(b.bin)
	c.as = make([][]string, len(b.as))
	for i, a := range b.as {
		c.as[i] = append([]string(nil), a...)
	}
	return &c
}

func (n *binNode) clone() dfNode {
	c := newOmap[*binComponent]()
	for _, k := range n.bins.keys {
		c.set(k, n.bins.m[k].clone())
	}
	return initNode(&binNode{bins: c}, nil)
}

func (b *binComponent) toValue() Value {
	o := jsval.NewObject(8)
	o.Set("bin", b.bin)
	o.Set("field", jsval.Str(b.field))
	as := make([]Value, len(b.as))
	for i, a := range b.as {
		as[i] = strsVal(a)
	}
	o.Set("as", jsval.Arr(as))
	if b.signal != "" {
		o.Set("signal", jsval.Str(b.signal))
	}
	if b.extentSignal != "" {
		o.Set("extentSignal", jsval.Str(b.extentSignal))
	}
	if b.span != "" {
		o.Set("span", jsval.Str(b.span))
	}
	if b.formula != "" {
		o.Set("formula", jsval.Str(b.formula))
	}
	if b.formulaAs != "" {
		o.Set("formulaAs", jsval.Str(b.formulaAs))
	}
	return jsval.Obj(o)
}

func (n *binNode) hash() string {
	o := jsval.NewObject(4)
	for _, k := range n.bins.keys {
		o.Set(k, n.bins.m[k].toValue())
	}
	return "Bin " + hashOf(jsval.Obj(o))
}

func (n *binNode) producedFields() *sset {
	s := newSset()
	for _, k := range n.bins.keys {
		for _, as := range n.bins.m[k].as {
			for _, f := range as {
				s.add(f)
			}
		}
	}
	return s
}

func (n *binNode) dependentFields() *sset {
	s := newSset()
	for _, k := range n.bins.keys {
		s.add(n.bins.m[k].field)
	}
	return s
}

func binKey(bin Value, field string) string { return binToString(bin) + "_" + field }

func getBinSignalName(m Model, field string, bin Value) string {
	nb := normalizeBin(bin, "")
	if nb.IsUndefined() {
		nb = mkv()
	}
	return m.b().getName(binKey(nb, field) + "_bins")
}

// createBinComponent builds a bin component from a bin transform or a field
// def. Upstream tells them apart by `'as' in t`, so a field def that carries an
// `as` property is treated as a transform.
func createBinComponent(t Value, bin Value, m Model, _ bool) (string, *binComponent) {
	cc := m.b().ctx

	var as []string
	if t.IsObj() && t.ObjValue().Has("as") {
		a := t.Get("as")
		if a.IsStr() {
			as = []string{a.StrValue(), a.StrValue() + "_end"}
		} else {
			as = []string{a.Index(0).AsString(), a.Index(1).AsString()}
		}
	} else {
		as = []string{vgField(cc, t, fieldRefOption{forAs: true}), vgField(cc, t, fieldRefOption{binSuffix: "end", forAs: true})}
	}
	nb := normalizeBin(bin, "")
	var normalized *Object
	if nb.IsObj() {
		normalized = cloneObj(nb.ObjValue())
	} else {
		normalized = jsval.NewObject(0)
	}
	key := binKey(jsval.Obj(normalized), t.Get("field").AsString())
	b := m.b()
	signal := b.getName(key + "_bins")
	extentSignal := b.getName(key + "_extent")
	span := ""
	if isParameterExtent(normalized.Lookup("extent")) {
		ext := normalized.Lookup("extent")
		span = parseSelectionExtent(m, ext.Get("param").AsString(), ext)
		normalized.Delete("extent")
	}
	return key, &binComponent{bin: jsval.Obj(normalized), field: t.Get("field").AsString(), noField: t.Get("field").IsUndefined(), as: [][]string{as}, signal: signal, extentSignal: extentSignal, span: span}
}

func rangeFormula(m fieldDefModel, fd Value, channel string, config Value) (formulaAs, formula string) {
	cc := m.b().ctx

	if binRequiresRange(cc, fd, channel) {
		var guide Value = mkv()
		if u, ok := m.(*unitModel); ok {
			if a := u.axis(channel); a.IsTruthy() {
				guide = a
			} else if l := u.legend(channel); l.IsTruthy() {
				guide = l
			}
		}
		startField := vgField(cc, fd, fieldRefOption{expr: "datum"})
		endField := vgField(cc, fd, fieldRefOption{expr: "datum", binSuffix: "end"})
		return vgField(cc, fd, fieldRefOption{binSuffix: "range", forAs: true}), binFormatExpression(startField, endField, guide.Get("format"), guide.Get("formatType"), config)
	}
	return "", ""
}

func makeBinFromEncoding(parent dfNode, m fieldDefModel) dfNode {
	bins := reduceFieldDef(m, func(idx *omap[*binComponent], fd Value, channel string) *omap[*binComponent] {
		if isTypedFieldDef(fd) && isBinning(fd.Get("bin")) {
			key, comp := createBinComponent(fd, fd.Get("bin"), m, false)
			if existing, ok := idx.get(key); ok {
				// {...binComponent, ...binComponentIndex[key], ...rangeFormula}
				comp = overlayBinComponent(comp, existing)
			}
			if fa, f := rangeFormula(m, fd, channel, m.b().config); fa != "" || f != "" {
				comp.formulaAs, comp.formula = fa, f
			}
			idx.set(key, comp)
		}
		return idx
	}, newOmap[*binComponent]())
	if bins.len() == 0 {
		return nil
	}
	return newBinNode(parent, bins)
}

func overlayBinComponent(base, over *binComponent) *binComponent {
	c := *base
	c.bin, c.field, c.as = over.bin, over.field, over.as
	if over.signal != "" {
		c.signal = over.signal
	}
	if over.extentSignal != "" {
		c.extentSignal = over.extentSignal
	}
	if over.span != "" {
		c.span = over.span
	}
	if over.formula != "" {
		c.formula, c.formulaAs = over.formula, over.formulaAs
	}
	return &c
}

func makeBinFromTransform(parent dfNode, t Value, m Model) *binNode {
	key, comp := createBinComponent(t, t.Get("bin"), m, t.IsObj() && t.ObjValue().Has("as"))
	bins := newOmap[*binComponent]()
	bins.set(key, comp)
	return newBinNode(parent, bins)
}

func (n *binNode) merge(other *binNode, rename func(oldName, newName string)) {
	for _, key := range other.bins.keyList() {
		if mine, ok := n.bins.get(key); ok {
			rename(other.bins.m[key].signal, mine.signal)
			seen := map[string]bool{}
			var as [][]string
			for _, a := range append(append([][]string{}, mine.as...), other.bins.m[key].as...) {
				h := hashOf(strsVal(a))
				if !seen[h] {
					seen[h] = true
					as = append(as, a)
				}
			}
			mine.as = as
		} else {
			n.bins.set(key, other.bins.m[key])
		}
	}
	other.moveChildrenLive(n)
	other.remove()
}

func (n *binNode) assemble() []Value {
	cc := n.cc

	var out []Value
	for _, key := range n.bins.keys {
		bin := n.bins.m[key]
		binAs := bin.as[0]
		remainingAs := bin.as[1:]
		extent := bin.bin.Get("extent")
		params := omit(bin.bin, "extent")
		if bin.noField {
			throw("Cannot read properties of undefined (reading 'length')") // splitAccessPath(undefined)
		}
		bt := jsval.NewObject(8)
		bt.Set("type", jsval.Str("bin"))
		bt.Set("field", jsval.Str(replacePathInField(bin.field)))
		bt.Set("as", strsVal(binAs))
		bt.Set("signal", strOrUndef(bin.signal))
		if !isParameterExtent(extent) {
			bt.Set("extent", extent)
		} else {
			bt.Set("extent", jsval.Null)
		}
		if bin.span != "" {
			bt.Set("span", mkv("signal", "span("+bin.span+")"))
		}
		spread(bt, jsval.Obj(params))
		if !extent.IsTruthy() && bin.extentSignal != "" {
			out = append(out, mkv("type", "extent", "field", replacePathInField(bin.field), "signal", bin.extentSignal))
			bt.Set("extent", mkv("signal", bin.extentSignal))
		}
		out = append(out, jsval.Obj(bt))
		for _, as := range remainingAs {
			for i := 0; i < 2; i++ {
				out = append(out, mkv("type", "formula", "expr", vgField(cc, mkv("field", binAs[i]), fieldRefOption{expr: "datum"}), "as", as[i]))
			}
		}
		if bin.formula != "" {
			out = append(out, mkv("type", "formula", "expr", bin.formula, "as", bin.formulaAs))
		}
	}
	return out
}

// ---- timeunit ----

type timeUnitNode struct {
	dfBase
	timeUnits *omap[*Object]
}

func newTimeUnitNode(parent dfNode, tu *omap[*Object]) *timeUnitNode {
	return initNode(&timeUnitNode{timeUnits: tu}, parent)
}

func (n *timeUnitNode) clone() dfNode {
	c := newOmap[*Object]()
	for _, k := range n.timeUnits.keys {
		c.set(k, deepClone(jsval.Obj(n.timeUnits.m[k])).ObjValue())
	}
	return initNode(&timeUnitNode{timeUnits: c}, nil)
}

func isTimeUnitTransformComponent(c *Object) bool { return !c.Lookup("as").IsUndefined() }

func (n *timeUnitNode) producedFields() *sset {
	s := newSset()
	for _, k := range n.timeUnits.keys {
		f := n.timeUnits.m[k]
		if isTimeUnitTransformComponent(f) {
			s.add(f.Lookup("as").AsString())
		} else {
			s.add(f.Lookup("field").AsString() + "_end")
		}
	}
	return s
}

func (n *timeUnitNode) dependentFields() *sset {
	s := newSset()
	for _, k := range n.timeUnits.keys {
		s.add(n.timeUnits.m[k].Lookup("field").AsString())
	}
	return s
}

func (n *timeUnitNode) hash() string {
	o := jsval.NewObject(4)
	for _, k := range n.timeUnits.keys {
		o.Set(k, jsval.Obj(n.timeUnits.m[k]))
	}
	return "TimeUnit " + hashOf(jsval.Obj(o))
}

const (
	offsettedRectStartSuffix = "offsetted_rect_start"
	offsettedRectEndSuffix   = "offsetted_rect_end"
)

func makeTimeUnitFromEncoding(parent dfNode, m fieldDefModel) dfNode {
	cc := m.b().ctx

	formula := reduceFieldDef(m, func(tc *omap[*Object], fd Value, channel string) *omap[*Object] {
		field, timeUnit := fd.Get("field"), fd.Get("timeUnit")
		if timeUnit.IsTruthy() && cc.v5 {
			component := mk("as", vgField(cc, fd, fieldRefOption{forAs: true}), "field", field, "timeUnit", timeUnit)
			tc.set(hashOf(jsval.Obj(component)), component)
		} else if timeUnit.IsTruthy() {
			var component *Object
			u, isUnit := m.(*unitModel)
			if isBinnedTimeUnit(cc, timeUnit) {
				if isUnit {
					bp := getBandPosition(cc, fd, undef, u.markDef, u.config)
					if isRectBasedMark(cc, u.mark()) || (bp.IsTruthy() && bp.NumValue() != 0) {
						component = mk("timeUnit", normalizeTimeUnit(cc, timeUnit), "field", field)
					}
				}
			} else {
				component = mk("as", vgField(cc, fd, fieldRefOption{forAs: true}), "field", field, "timeUnit", timeUnit)
			}
			if isUnit {
				bp := getBandPosition(cc, fd, undef, u.markDef, u.config)
				if isRectBasedMark(cc, u.mark()) && isXorY(channel) && !(bp.IsNum() && bp.NumValue() == 0.5) {
					if component != nil {
						component.Set("rectBandPosition", bp)
					}
				}
			}
			if component != nil {
				tc.set(hashOf(jsval.Obj(component)), component)
			}
		}
		return tc
	}, newOmap[*Object]())
	if formula.len() == 0 {
		return nil
	}
	return newTimeUnitNode(parent, formula)
}

func makeTimeUnitFromTransform(cc *compileCtx, parent dfNode, t Value) *timeUnitNode {
	timeUnit := t.Get("timeUnit")
	other := omit(t, "timeUnit")
	component := cloneObj(other)
	component.Set("timeUnit", normalizeTimeUnit(cc, timeUnit))
	tu := newOmap[*Object]()
	tu.set(hashOf(jsval.Obj(component)), component)
	return newTimeUnitNode(parent, tu)
}

func (n *timeUnitNode) merge(other *timeUnitNode) {
	c := newOmap[*Object]()
	for _, k := range n.timeUnits.keys {
		c.set(k, n.timeUnits.m[k])
	}
	n.timeUnits = c
	for _, k := range other.timeUnits.keys {
		if !n.timeUnits.has(k) {
			n.timeUnits.set(k, other.timeUnits.m[k])
		}
	}
	other.moveChildrenLive(n)
	other.remove()
}

func (n *timeUnitNode) removeFormulas(fields *sset) {
	nf := newOmap[*Object]()
	for _, k := range n.timeUnits.keys {
		c := n.timeUnits.m[k]
		var fieldAs string
		if isTimeUnitTransformComponent(c) {
			fieldAs = c.Lookup("as").AsString()
		} else {
			fieldAs = c.Lookup("field").AsString() + "_end"
		}
		if !fields.has(fieldAs) {
			nf.set(k, c)
		}
	}
	n.timeUnits = nf
}

func timeOffsetExpr(timeUnit Value, field string, reverse bool) string {
	unit, utc := timeUnit.Get("unit").AsString(), timeUnit.Get("utc").IsTruthy()
	smallest := getSmallestTimeUnitPart(unit)
	step := 1.0
	if s := timeUnit.Get("step"); s.IsNum() {
		step = s.NumValue()
	}
	part, st := getDateTimePartAndStep(smallest, step)
	fn := "timeOffset"
	if utc {
		fn = "utcOffset"
	}
	if reverse {
		st = -st
	}
	return fn + "('" + part + "', " + accessWithDatumToUnescapedPath(field) + ", " + jsval.JSNumberString(st) + ")"
}

func interpolateExpr(start, end string, fraction float64) string {
	return jsval.JSNumberString(1-fraction) + " * " + start + " + " + jsval.JSNumberString(fraction) + " * " + end
}

func offsettedRectFormulas(startField, endField string, rectBandPosition Value, timeUnit Value) []Value {
	if rectBandPosition.IsNum() && rectBandPosition.NumValue() != 0.5 {
		startExpr := accessWithDatumToUnescapedPath(startField)
		endExpr := accessWithDatumToUnescapedPath(endField)
		f := rectBandPosition.NumValue() + 0.5
		return []Value{
			mkv("type", "formula", "expr", interpolateExpr(timeOffsetExpr(timeUnit, startField, true), startExpr, f), "as", startField+"_"+offsettedRectStartSuffix),
			mkv("type", "formula", "expr", interpolateExpr(startExpr, endExpr, f), "as", startField+"_"+offsettedRectEndSuffix),
		}
	}
	return nil
}

func (n *timeUnitNode) assemble() []Value {
	cc := n.cc

	var out []Value
	for _, k := range n.timeUnits.keys {
		f := n.timeUnits.m[k]
		rect := f.Lookup("rectBandPosition")
		nt := normalizeTimeUnit(cc, f.Lookup("timeUnit"))
		if isTimeUnitTransformComponent(f) {
			if f.Lookup("field").IsUndefined() {
				throw("Cannot read properties of undefined (reading 'length')") // splitAccessPath(undefined)
			}
			field, as := f.Lookup("field").AsString(), f.Lookup("as").AsString()
			unit, utc := nt.Get("unit"), nt.Get("utc")
			params := omit(nt, "unit", "utc")
			o := jsval.NewObject(8)
			o.Set("field", jsval.Str(replacePathInField(field)))
			o.Set("type", jsval.Str("timeunit"))
			if unit.IsTruthy() {
				o.Set("units", strsVal(getTimeUnitParts(unit.AsString())))
			}
			if utc.IsTruthy() {
				o.Set("timezone", jsval.Str("utc"))
			}
			spread(o, jsval.Obj(params))
			o.Set("as", arr(as, as+"_end"))
			out = append(out, jsval.Obj(o))
			out = append(out, offsettedRectFormulas(as, as+"_end", rect, nt)...)
		} else {
			field := unescapeSingleQuoteAndPathDot(f.Lookup("field").AsString())
			endAs := field + "_end"
			out = append(out, mkv("type", "formula", "expr", timeOffsetExpr(nt, field, false), "as", endAs))
			out = append(out, offsettedRectFormulas(field, endAs, rect, nt)...)
		}
	}
	return out
}

// ---- aggregate ----

type aggregateNode struct {
	dfBase
	dimensions *sset
	measures   *omap[*omap[*sset]] // field -> op -> output names
}

func newAggregateNode(parent dfNode, dims *sset, measures *omap[*omap[*sset]]) *aggregateNode {
	return initNode(&aggregateNode{dimensions: dims, measures: measures}, parent)
}

func cloneMeasures(m *omap[*omap[*sset]]) *omap[*omap[*sset]] {
	c := newOmap[*omap[*sset]]()
	for _, f := range m.keys {
		ops := newOmap[*sset]()
		for _, op := range m.m[f].keys {
			ops.set(op, m.m[f].m[op].clone())
		}
		c.set(f, ops)
	}
	return c
}

func (n *aggregateNode) clone() dfNode {
	return initNode(&aggregateNode{dimensions: n.dimensions.clone(), measures: cloneMeasures(n.measures)}, nil)
}

func (n *aggregateNode) groupBy() *sset { return n.dimensions }

func measureSet(m *omap[*omap[*sset]], field, op string) *sset {
	ops, ok := m.get(field)
	if !ok {
		ops = newOmap[*sset]()
		m.set(field, ops)
	}
	s, ok := ops.get(op)
	if !ok {
		s = newSset()
		ops.set(op, s)
	}
	return s
}

func ensureMeasureField(m *omap[*omap[*sset]], field string) *omap[*sset] {
	ops, ok := m.get(field)
	if !ok {
		ops = newOmap[*sset]()
		m.set(field, ops)
	}
	return ops
}

func addDimension(dims *sset, channel string, fd Value, m Model) {
	cc := m.b().ctx

	u := asUnit(m)
	var channelDef2 Value
	if u != nil {
		if sec := getSecondaryRangeChannel(channel); sec != "" {
			channelDef2 = u.encoding.Get(sec)
		}
	}
	switch {
	case isTypedFieldDef(fd) && u != nil && hasBandEnd(cc, fd, channelDef2, u.markDef, u.config):
		dims.add(vgField(cc, fd, fieldRefOption{}))
		dims.add(vgField(cc, fd, fieldRefOption{suffix: "end"}))
		bp := getBandPosition(cc, fd, undef, u.markDef, u.config)
		if !cc.v5 && isRectBasedMark(cc, u.mark()) && !(bp.IsNum() && bp.NumValue() == 0.5) && isXorY(channel) {
			dims.add(vgField(cc, fd, fieldRefOption{suffix: offsettedRectStartSuffix}))
			dims.add(vgField(cc, fd, fieldRefOption{suffix: offsettedRectEndSuffix}))
		}
		if fd.Get("bin").IsTruthy() && binRequiresRange(cc, fd, channel) {
			dims.add(vgField(cc, fd, fieldRefOption{binSuffix: "range"}))
		}
	case isGeoPositionChannel(channel):
		posChannel := getPositionChannelFromLatLong(channel)
		dims.add(m.b().getName(posChannel))
	default:
		dims.add(vgField(cc, fd, fieldRefOption{}))
	}
	if isScaleFieldDef(fd) && isFieldRange(fd.Get("scale").Get("range")) {
		dims.add(fd.Get("scale").Get("range").Get("field").AsString())
	}
}

func mergeMeasures(parent, child *omap[*omap[*sset]]) {
	for _, field := range child.keys {
		ops := child.m[field]
		for _, op := range ops.keys {
			if pf, ok := parent.get(field); ok {
				existing, _ := pf.get(op)
				merged := newSset()
				for _, x := range existing.list() {
					merged.add(x)
				}
				for _, x := range ops.m[op].list() {
					merged.add(x)
				}
				pf.set(op, merged)
			} else {
				nf := newOmap[*sset]()
				nf.set(op, ops.m[op])
				parent.set(field, nf)
			}
		}
	}
}

func makeAggregateFromEncoding(parent dfNode, m *unitModel) *aggregateNode {
	cc := m.b().ctx

	isAgg := false
	m.forEachFieldDef(func(fd Value, _ string) {
		if fd.Get("aggregate").IsTruthy() {
			isAgg = true
		}
	})
	if !isAgg {
		return nil
	}
	meas := newOmap[*omap[*sset]]()
	dims := newSset()
	m.forEachFieldDef(func(fd Value, channel string) {
		aggregate, field := fd.Get("aggregate"), fd.Get("field")
		if aggregate.IsTruthy() {
			if aggregate.IsStr() && aggregate.StrValue() == "count" {
				ops := ensureMeasureField(meas, "*")
				ops.set("count", newSset(vgField(cc, fd, fieldRefOption{forAs: true})))
			} else {
				if isArgminDef(aggregate) || isArgmaxDef(aggregate) {
					op := "argmax"
					if isArgminDef(aggregate) {
						op = "argmin"
					}
					argField := aggregate.Get(op).AsString()
					ops := ensureMeasureField(meas, argField)
					ops.set(op, newSset(vgField(cc, mkv("op", op, "field", argField), fieldRefOption{forAs: true})))
				} else {
					ops := ensureMeasureField(meas, field.AsString())
					ops.set(aggregate.AsString(), newSset(vgField(cc, fd, fieldRefOption{forAs: true})))
				}
				if isScaleChannel(cc, channel) {
					if d := m.scaleDomain(channel); d.IsStr() && d.StrValue() == "unaggregated" {
						ops := ensureMeasureField(meas, field.AsString())
						ops.set("min", newSset(vgField(cc, mkv("field", field, "aggregate", "min"), fieldRefOption{forAs: true})))
						ops.set("max", newSset(vgField(cc, mkv("field", field, "aggregate", "max"), fieldRefOption{forAs: true})))
					}
				}
			}
		} else {
			addDimension(dims, channel, fd, m)
		}
	})
	if dims.size()+meas.len() == 0 {
		return nil
	}
	return newAggregateNode(parent, dims, meas)
}

func makeAggregateFromTransform(cc *compileCtx, parent dfNode, t Value) *aggregateNode {
	dims := newSset()
	meas := newOmap[*omap[*sset]]()
	for _, s := range t.Get("aggregate").Items() {
		op, field, as := s.Get("op"), s.Get("field"), s.Get("as")
		if op.IsTruthy() {
			name := as.AsString()
			if !as.IsTruthy() {
				name = vgField(cc, s, fieldRefOption{forAs: true})
			}
			if op.StrValue() == "count" {
				ops := ensureMeasureField(meas, "*")
				ops.set("count", newSset(name))
			} else if cc.v5 {
				ensureMeasureField(meas, field.AsString()).set(op.AsString(), newSset(name))
			} else {
				measureSet(meas, field.AsString(), op.AsString()).add(name)
			}
		}
	}
	for _, s := range t.Get("groupby").Items() {
		dims.add(s.AsString())
	}
	if dims.size()+meas.len() == 0 {
		return nil
	}
	return newAggregateNode(parent, dims, meas)
}

func (n *aggregateNode) merge(other *aggregateNode) bool {
	if setEqual(n.dimensions, other.dimensions) {
		mergeMeasures(n.measures, other.measures)
		return true
	}
	return false
}

func (n *aggregateNode) addDimensions(fields []string) {
	for _, f := range fields {
		n.dimensions.add(f)
	}
}

func (n *aggregateNode) dependentFields() *sset {
	s := n.dimensions.clone()
	for _, k := range n.measures.keys {
		s.add(k)
	}
	return s
}

func (n *aggregateNode) producedFields() *sset {
	out := newSset()
	for _, field := range n.measures.keys {
		for _, op := range n.measures.m[field].keys {
			m := n.measures.m[field].m[op]
			if m.size() == 0 {
				out.add(op + "_" + field)
			} else {
				for _, x := range m.list() {
					out.add(x)
				}
			}
		}
	}
	return out
}

func (n *aggregateNode) hash() string {
	meas := jsval.NewObject(4)
	for _, f := range n.measures.keys {
		ops := jsval.NewObject(2)
		for _, op := range n.measures.m[f].keys {
			ops.Set(op, jsval.Str(setToHashString(n.measures.m[f].m[op])))
		}
		meas.Set(f, jsval.Obj(ops))
	}
	return "Aggregate " + hashOf(mkv("dimensions", setToHashString(n.dimensions), "measures", jsval.Obj(meas)))
}

func (n *aggregateNode) assemble() Value {
	var ops, fields, as []Value
	for _, field := range n.measures.keys {
		for _, op := range n.measures.m[field].keys {
			for _, alias := range n.measures.m[field].m[op].list() {
				as = append(as, jsval.Str(alias))
				ops = append(ops, jsval.Str(op))
				if field == "*" {
					fields = append(fields, jsval.Null)
				} else {
					fields = append(fields, jsval.Str(replacePathInField(field)))
				}
			}
		}
	}
	var groupby []Value
	for _, d := range n.dimensions.list() {
		groupby = append(groupby, jsval.Str(replacePathInField(d)))
	}
	return mkv("type", "aggregate", "groupby", jsval.Arr(groupby), "ops", jsval.Arr(ops), "fields", jsval.Arr(fields), "as", jsval.Arr(as))
}

var _ = strings.Join
