package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ---- bin ----

type binComponent struct {
	bin          Value
	field        string
	fieldV       Value     // t.field as the spec gave it, which the assembled bin's path functions read
	as           [][]Value // the output field pairs, as upstream holds them
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

func (b *binComponent) clone(cc *compileCtx) *binComponent {
	c := *b
	c.bin = duplicate(cc, b.bin)
	c.as = make([][]Value, len(b.as))
	for i, a := range b.as {
		c.as[i] = append([]Value(nil), a...)
	}
	return &c
}

func (n *binNode) clone() dfNode {
	cc := n.cc
	c := newOmap[*binComponent]()
	for _, k := range n.bins.keysView() {
		cc.work(1)
		c.set(k, n.bins.m[k].clone(n.cc))
	}
	return initNode(&binNode{bins: c}, nil)
}

func (b *binComponent) toValue() Value {
	o := jsval.NewObject(8)
	o.Set("bin", b.bin)
	o.Set("field", jsval.Str(b.field))
	as := make([]Value, len(b.as))
	for i, a := range b.as {
		as[i] = jsval.Arr(a)
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
	cc := n.cc
	o := jsval.NewObject(4)
	for _, k := range n.bins.keysView() {
		cc.work(1)
		o.Set(k, n.bins.m[k].toValue())
	}
	return "Bin " + hashOf(n.cc, jsval.Obj(o))
}

func (n *binNode) producedFields() *sset {
	cc := n.cc
	s := newSset()
	for _, k := range n.bins.keysView() {
		cc.work(1)
		for _, as := range n.bins.m[k].as {
			cc.work(1)
			for _, f := range as {
				s.addValue(f)
			}
		}
	}
	return s
}

func (n *binNode) dependentFields() *sset {
	cc := n.cc
	s := newSset()
	for _, k := range n.bins.keysView() {
		cc.work(1)
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

	// isString(t.as) ? [t.as, `${t.as}_end`] : [t.as[0], t.as[1]], the
	// values as given.
	var as []Value
	if t.IsObj() && t.ObjValue().Has("as") {
		a := t.Get("as")
		if a.IsStr() {
			as = []Value{a, jsval.Str(a.StrValue() + "_end")}
		} else {
			as = []Value{jsProp(a, "0"), jsProp(a, "1")}
		}
	} else {
		as = []Value{jsval.Str(vgField(cc, t, fieldRefOption{forAs: true})), jsval.Str(vgField(cc, t, fieldRefOption{binSuffix: "end", forAs: true}))}
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
		span = parseSelectionExtent(m, ext.Get("param"), ext)
		normalized.Delete("extent")
	}
	return key, &binComponent{bin: jsval.Obj(normalized), field: t.Get("field").AsString(), fieldV: t.Get("field"), as: [][]Value{as}, signal: signal, extentSignal: extentSignal, span: span}
}

func rangeFormula(m fieldDefModel, fd Value, channel string, config Value) (formulaAs, formula string) {
	cc := m.b().ctx

	if binRequiresRange(cc, fd, channel) {
		guide := mkv()
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
		if isTypedFieldDefOf(m.b().ctx, fd) && isBinning(fd.Get("bin")) {
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
	cc := n.cc
	for _, key := range other.bins.keyList() {
		cc.work(1)
		if mine, ok := n.bins.get(key); ok {
			rename(other.bins.m[key].signal, mine.signal)
			seen := map[string]bool{}
			var as [][]Value
			for _, a := range append(append([][]Value{}, mine.as...), other.bins.m[key].as...) {
				cc.work(1)
				h := hashOf(n.cc, jsval.Arr(a))
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
	for _, key := range n.bins.keysView() {
		cc.work(1)
		bin := n.bins.m[key]
		binAs := bin.as[0]
		remainingAs := bin.as[1:]
		extent := bin.bin.Get("extent")
		params := omit(bin.bin, "extent")
		bt := jsval.NewObject(8)
		bt.Set("type", jsval.Str("bin"))
		bt.Set("field", jsval.Str(replacePathInFieldV(bin.fieldV)))
		bt.Set("as", jsval.Arr(binAs))
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
			out = append(out, mkv("type", "extent", "field", replacePathInFieldV(bin.fieldV), "signal", bin.extentSignal))
			bt.Set("extent", mkv("signal", bin.extentSignal))
		}
		out = append(out, jsval.Obj(bt))
		for _, as := range remainingAs {
			cc.work(1)
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
	cc := n.cc
	c := newOmap[*Object]()
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		c.set(k, duplicate(n.cc, jsval.Obj(n.timeUnits.m[k])).ObjValue())
	}
	return initNode(&timeUnitNode{timeUnits: c}, nil)
}

func isTimeUnitTransformComponent(c *Object) bool { return !c.Lookup("as").IsUndefined() }

func (n *timeUnitNode) producedFields() *sset {
	cc := n.cc
	s := newSset()
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		f := n.timeUnits.m[k]
		// f.as as given (5.8 always), else `${field}_end`.
		if cc.v5 || isTimeUnitTransformComponent(f) {
			s.addValue(f.Lookup("as"))
		} else {
			s.add(f.Lookup("field").AsString() + "_end")
		}
	}
	return s
}

func (n *timeUnitNode) dependentFields() *sset {
	cc := n.cc
	s := newSset()
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		s.addValue(n.timeUnits.m[k].Lookup("field"))
	}
	return s
}

func (n *timeUnitNode) hash() string {
	cc := n.cc
	o := jsval.NewObject(4)
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		o.Set(k, jsval.Obj(n.timeUnits.m[k]))
	}
	return "TimeUnit " + hashOf(n.cc, jsval.Obj(o))
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
			tc.set(hashOf(cc, jsval.Obj(component)), component)
		} else if timeUnit.IsTruthy() {
			var component *Object
			u, isUnit := m.(*unitModel)
			if isBinnedTimeUnit(cc, timeUnit) {
				if isUnit {
					bp := getBandPosition(cc, fd, undef, u.markDef, u.config)
					if isRectBasedMark(cc, u.mark()) || bp.IsTruthy() {
						component = mk("timeUnit", normalizeTimeUnit(cc, timeUnit), "field", field)
					}
				}
			} else {
				component = mk("as", vgField(cc, fd, fieldRefOption{forAs: true}), "field", field, "timeUnit", timeUnit)
			}
			if isUnit {
				bp := getBandPosition(cc, fd, undef, u.markDef, u.config)
				if isRectBasedMark(cc, u.mark()) && isXorY(channel) && !(bp.IsNum() && bp.NumValue() == 0.5) {
					if component == nil {
						throw("Cannot set properties of undefined (setting 'rectBandPosition')")
					}
					component.Set("rectBandPosition", bp)
				}
			}
			if component != nil {
				tc.set(hashOf(cc, jsval.Obj(component)), component)
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
	tu.set(hashOf(cc, jsval.Obj(component)), component)
	return newTimeUnitNode(parent, tu)
}

func (n *timeUnitNode) merge(other *timeUnitNode) {
	cc := n.cc
	c := newOmap[*Object]()
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		c.set(k, n.timeUnits.m[k])
	}
	n.timeUnits = c
	for _, k := range other.timeUnits.keysView() {
		cc.work(1)
		if !n.timeUnits.has(k) {
			n.timeUnits.set(k, other.timeUnits.m[k])
		}
	}
	other.moveChildrenLive(n)
	other.remove()
}

func (n *timeUnitNode) removeFormulas(fields *sset) {
	cc := n.cc
	nf := newOmap[*Object]()
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
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

func timeOffsetExpr(timeUnit Value, field Value, reverse bool) string {
	unit, utc := timeUnit.Get("unit").AsString(), timeUnit.Get("utc").IsTruthy()
	smallest := getSmallestTimeUnitPart(unit)
	part, _, ok := getDateTimePartAndStep(smallest, 1)
	if !ok {
		throw("Cannot destructure property 'part' of 'getDateTimePartAndStep(...)' as it is undefined.")
	}
	// getDateTimePartAndStep(unit, step = 1), then `${reverse ? -step :
	// step}`: the step as given, multiplied for quarters and weeks.
	step := timeUnit.Get("step")
	if step.IsUndefined() {
		step = jsval.Int(1)
	}
	switch smallest {
	case "quarter":
		step = jsval.Num(jsval.ToNumber(step) * 3)
	case "week":
		step = jsval.Num(jsval.ToNumber(step) * 7)
	}
	fn := "timeOffset"
	if utc {
		fn = "utcOffset"
	}
	st := step.AsString()
	if reverse {
		st = jsval.JSNumberString(-jsval.ToNumber(step))
	}
	return fn + "('" + part + "', " + accessWithDatumToUnescapedPathV(field) + ", " + st + ")"
}

// interpolateExpr is `${1 - fraction} * ${start} + ${fraction} * ${end}`,
// with JavaScript's arithmetic and string conversion.
func interpolateExpr(start, end string, fraction Value) string {
	return jsval.JSNumberString(1-jsval.ToNumber(fraction)) + " * " + start + " + " + fraction.AsString() + " * " + end
}

// jsNumPlus is JavaScript's a + b for a number a: a string (or an object,
// whose primitive is a string) b concatenates, anything else adds.
func jsNumPlus(a float64, b Value) Value {
	if b.IsStr() || b.IsObj() || b.IsArr() {
		return jsval.Str(jsval.JSNumberString(a) + b.AsString())
	}
	return jsval.Num(a + jsval.ToNumber(b))
}

// jsPlusNum is JavaScript's a + b for a number b: a string (or an object,
// whose primitive is a string) concatenates, anything else adds.
func jsPlusNum(a Value, b float64) Value {
	if a.IsStr() || a.IsObj() || a.IsArr() {
		return jsval.Str(a.AsString() + jsval.JSNumberString(b))
	}
	return jsval.Num(jsval.ToNumber(a) + b)
}

// offsettedRectFormulas: `rectBandPosition !== undefined && rectBandPosition
// !== 0.5`, and `rectBandPosition + 0.5`, as JavaScript computes them for a
// band position of any type.
func offsettedRectFormulas(startField, endField Value, rectBandPosition Value, timeUnit Value) []Value {
	if !rectBandPosition.IsUndefined() && !(rectBandPosition.IsNum() && rectBandPosition.NumValue() == 0.5) {
		startExpr := accessWithDatumToUnescapedPathV(startField)
		endExpr := accessWithDatumToUnescapedPathV(endField)
		f := jsPlusNum(rectBandPosition, 0.5)
		return []Value{
			mkv("type", "formula", "expr", interpolateExpr(timeOffsetExpr(timeUnit, startField, true), startExpr, f), "as", startField.AsString()+"_"+offsettedRectStartSuffix),
			mkv("type", "formula", "expr", interpolateExpr(startExpr, endExpr, f), "as", startField.AsString()+"_"+offsettedRectEndSuffix),
		}
	}
	return nil
}

func (n *timeUnitNode) assemble() []Value {
	cc := n.cc

	var out []Value
	for _, k := range n.timeUnits.keysView() {
		cc.work(1)
		f := n.timeUnits.m[k]
		rect := f.Lookup("rectBandPosition")
		nt := normalizeTimeUnit(cc, f.Lookup("timeUnit"))
		if isTimeUnitTransformComponent(f) {
			// const {unit, utc, ...params} = normalizeTimeUnit(timeUnit)
			if nt.IsNullish() {
				if cc.v5 {
					throw("Cannot destructure property 'unit' of 'normalizeTimeUnit(...)' as it is %s.", nullishName(nt))
				}
				throw("Cannot destructure property 'unit' of 'normalizedTimeUnit' as it is %s.", nullishName(nt))
			}
			field, as := f.Lookup("field"), f.Lookup("as")
			asEnd := jsval.Str(as.AsString() + "_end") // `${as}_end`
			unit, utc := nt.Get("unit"), nt.Get("utc")
			params := omit(nt, "unit", "utc")
			o := jsval.NewObject(8)
			o.Set("field", jsval.Str(replacePathInFieldV(field)))
			o.Set("type", jsval.Str("timeunit"))
			if unit.IsTruthy() {
				o.Set("units", strsVal(getTimeUnitParts(unit.AsString())))
			}
			if utc.IsTruthy() {
				o.Set("timezone", jsval.Str("utc"))
			}
			spread(o, jsval.Obj(params))
			o.Set("as", arr(as, asEnd))
			out = append(out, jsval.Obj(o))
			out = append(out, offsettedRectFormulas(as, asEnd, rect, nt)...)
		} else {
			field := unescapeSingleQuoteAndPathDotV(f.Lookup("field"))
			endAs := field + "_end"
			out = append(out, mkv("type", "formula", "expr", timeOffsetExpr(nt, jsval.Str(field), false), "as", endAs))
			out = append(out, offsettedRectFormulas(jsval.Str(field), jsval.Str(endAs), rect, nt)...)
		}
	}
	return out
}

// ---- aggregate ----

type aggregateNode struct {
	dfBase
	dimensions *sset
	measures   *omap[*omap[*sset]] // field -> op -> output names
	// rawDims are the group-by entries a transform gave that are not
	// strings, by their text: upstream's set holds the values themselves,
	// and assembling reads them as access paths.
	rawDims map[string]Value
	// protoOp says 5.8 assigned meas[field]['__proto__'], which sets the
	// object's prototype to a Set instead of adding an op: the op is not
	// output, and stringifying the measures (the node's hash) fails.
	protoOp bool
}

func newAggregateNode(parent dfNode, dims *sset, measures *omap[*omap[*sset]]) *aggregateNode {
	return initNode(&aggregateNode{dimensions: dims, measures: measures}, parent)
}

// cloneMeasures is duplicate(this.measures): 5.8's clone package leaves
// out a field or an op Object.prototype holds (see skipsInClone58).
func cloneMeasures(cc *compileCtx, m *omap[*omap[*sset]]) *omap[*omap[*sset]] {
	c := newOmap[*omap[*sset]]()
	for _, f := range m.keysView() {
		cc.work(1)
		if cc.v5 && skipsInClone58(cc, f) {
			continue
		}
		ops := newOmap[*sset]()
		for _, op := range m.m[f].keysView() {
			cc.work(1)
			if cc.v5 && skipsInClone58(cc, op) {
				continue
			}
			ops.set(op, m.m[f].m[op].clone())
		}
		c.set(f, ops)
	}
	return c
}

func (n *aggregateNode) clone() dfNode {
	return initNode(&aggregateNode{dimensions: n.dimensions.clone(), measures: cloneMeasures(n.cc, n.measures), rawDims: n.rawDims, protoOp: n.protoOp}, nil)
}

func (n *aggregateNode) groupBy() *sset { return n.dimensions }

// measOps is meas[field] after `meas[field] ??= {}`: the field's own ops,
// or what a field named after an inherited member finds there (a built-in,
// see jsRealm), which keeps it, so the ops are written on the built-in and
// the field is never output.
type measOps struct {
	own *omap[*sset]
	ref jsRef
}

func measField(cc *compileCtx, m *omap[*omap[*sset]], field string) measOps {
	ops, own, ref := m.read(cc.realm(), field)
	if own {
		return measOps{own: ops}
	}
	if ref.host != nil || (ref.found && !isNullishAny(ref.val, true)) {
		return measOps{ref: ref}
	}
	ops = newOmap[*sset]()
	m.assign(field, ops)
	return measOps{own: ops}
}

// set is meas[field][op] = s.
func (t measOps) set(cc *compileCtx, op string, s *sset) {
	if t.own != nil {
		t.own.assign(op, s)
		return
	}
	cc.realm().write(t.ref, op, s)
}

// get is meas[field][op].
func (t measOps) get(cc *compileCtx, op string) (any, bool) {
	if t.own != nil {
		s, own, ref := t.own.read(cc.realm(), op)
		if own {
			return s, true
		}
		if ref.host != nil {
			return ref.host, true
		}
		return ref.val, ref.found
	}
	return cc.realm().get(t.ref, op)
}

// measureSet is `meas[field][op] ??= new Set()`, the set .add then adds to.
func measureSet(cc *compileCtx, m *omap[*omap[*sset]], field, op string) *sset {
	t := measField(cc, m, field)
	cur, found := t.get(cc, op)
	if !isNullishAny(cur, found) {
		// An op named after an inherited member finds that member, which has
		// no add; a Set a field named "__proto__" left on Object.prototype
		// is added to.
		if s, ok := cur.(*sset); ok {
			return s
		}
		throw("meas[field][op].add is not a function")
	}
	s := newSset()
	t.set(cc, op, s)
	return s
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
	case isTypedFieldDefOf(m.b().ctx, fd) && u != nil && hasBandEnd(cc, fd, channelDef2, u.markDef, u.config):
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

func mergeMeasures(cc *compileCtx, parent, child *omap[*omap[*sset]]) {
	r := cc.realm()
	for _, field := range child.keysView() {
		cc.work(1)
		ops := child.m[field]
		for _, op := range ops.keysView() {
			cc.work(1)
			if parent.in(r, field) {
				// parentMeasures[field][op] = new Set([...(parentMeasures[field][op] ?? []), ...ops[op]])
				t := measField(cc, parent, field)
				existing, found := t.get(cc, op)
				merged := newSset()
				if !isNullishAny(existing, found) {
					es, ok := existing.(*sset)
					if !ok {
						throw("(parentMeasures[field][op] ?? []) is not iterable")
					}
					for _, x := range es.list() {
						cc.work(1)
						merged.addValue(es.value(x))
					}
				}
				for _, x := range ops.m[op].list() {
					cc.work(1)
					merged.addValue(ops.m[op].value(x))
				}
				t.set(cc, op, merged)
			} else {
				nf := newOmap[*sset]()
				nf.assign(op, ops.m[op])
				parent.assign(field, nf)
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
	protoOp := false
	m.forEachFieldDef(func(fd Value, channel string) {
		aggregate, field := fd.Get("aggregate"), fd.Get("field")
		if aggregate.IsTruthy() {
			if aggregate.IsStr() && aggregate.StrValue() == "count" {
				measField(cc, meas, "*").set(cc, "count", newSset(vgField(cc, fd, fieldRefOption{forAs: true})))
			} else {
				if isArgminDef(aggregate) || isArgmaxDef(aggregate) {
					op := "argmax"
					if isArgminDef(aggregate) {
						op = "argmin"
					}
					// meas[argField] keys by its text; vgField reads it as given.
					argField := aggregate.Get(op)
					measField(cc, meas, argField.AsString()).set(cc, op, newSset(vgField(cc, mkv("op", op, "field", argField), fieldRefOption{forAs: true})))
				} else {
					t := measField(cc, meas, field.AsString())
					set := newSset(vgField(cc, fd, fieldRefOption{forAs: true}))
					if aggregate.AsString() == "__proto__" && t.own != nil {
						// The ops object's prototype becomes the Set.
						protoOp = true
					}
					t.set(cc, aggregate.AsString(), set)
				}
				if isScaleChannel(cc, channel) {
					if d := m.scaleDomain(channel); d.IsStr() && d.StrValue() == "unaggregated" {
						t := measField(cc, meas, field.AsString())
						t.set(cc, "min", newSset(vgField(cc, mkv("field", field, "aggregate", "min"), fieldRefOption{forAs: true})))
						t.set(cc, "max", newSset(vgField(cc, mkv("field", field, "aggregate", "max"), fieldRefOption{forAs: true})))
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
	n := newAggregateNode(parent, dims, meas)
	n.protoOp = protoOp
	return n
}

func makeAggregateFromTransform(cc *compileCtx, parent dfNode, t Value) *aggregateNode {
	dims := newSset()
	meas := newOmap[*omap[*sset]]()
	protoOp := false
	for _, s := range iterate(t.Get("aggregate"), "t.aggregate") {
		cc.work(1)
		op, field, as := s.Get("op"), s.Get("field"), s.Get("as")
		if op.IsTruthy() {
			// as ? as : vgField(s, {forAs: true}), the value as given.
			name := as
			if !as.IsTruthy() {
				name = jsval.Str(vgField(cc, s, fieldRefOption{forAs: true}))
			}
			one := func() *sset { x := newSset(); x.addValue(name); return x }
			if op.StrValue() == "count" {
				measField(cc, meas, "*").set(cc, "count", one())
			} else if cc.v5 {
				t := measField(cc, meas, field.AsString())
				if op.AsString() == "__proto__" && t.own != nil {
					protoOp = true
				}
				t.set(cc, op.AsString(), one())
			} else {
				measureSet(cc, meas, field.AsString(), op.AsString()).addValue(name)
			}
		}
	}
	var groupby []Value
	if g := t.Get("groupby"); !g.IsNullish() {
		groupby = iterate(g, "t.groupby ?? []")
	}
	var raw map[string]Value
	for _, s := range groupby {
		cc.work(1)
		dims.add(s.AsString())
		if !s.IsStr() {
			if raw == nil {
				raw = map[string]Value{}
			}
			raw[s.AsString()] = s
		}
	}
	if dims.size()+meas.len() == 0 {
		return nil
	}
	n := newAggregateNode(parent, dims, meas)
	n.rawDims = raw
	n.protoOp = protoOp
	return n
}

func (n *aggregateNode) merge(other *aggregateNode) bool {
	if setEqual(n.dimensions, other.dimensions) {
		// Only own ops are copied: other's prototype does not come along.
		mergeMeasures(n.cc, n.measures, other.measures)
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
	cc := n.cc
	s := n.dimensions.clone()
	for _, k := range n.measures.keysView() {
		cc.work(1)
		s.add(k)
	}
	return s
}

func (n *aggregateNode) producedFields() *sset {
	cc := n.cc
	out := newSset()
	for _, field := range n.measures.keysView() {
		cc.work(1)
		for _, op := range n.measures.m[field].keysView() {
			cc.work(1)
			m := n.measures.m[field].m[op]
			if m.size() == 0 {
				out.add(op + "_" + field)
			} else {
				for _, x := range m.list() {
					cc.work(1)
					out.add(x)
				}
			}
		}
	}
	return out
}

func (n *aggregateNode) hash() string {
	cc := n.cc
	if n.protoOp {
		throw("Method Set.prototype.values called on incompatible receiver #<Set>")
	}
	meas := jsval.NewObject(4)
	for _, f := range n.measures.keysView() {
		cc.work(1)
		ops := jsval.NewObject(2)
		for _, op := range n.measures.m[f].keysView() {
			cc.work(1)
			ops.Set(op, jsval.Str(setToHashString(n.cc, n.measures.m[f].m[op])))
		}
		meas.Set(f, jsval.Obj(ops))
	}
	return "Aggregate " + hashOf(n.cc, mkv("dimensions", setToHashString(n.cc, n.dimensions), "measures", jsval.Obj(meas)))
}

func (n *aggregateNode) assemble() Value {
	cc := n.cc
	var ops, fields, as []Value
	for _, field := range n.measures.keysView() {
		cc.work(1)
		for _, op := range n.measures.m[field].keysView() {
			cc.work(1)
			set := n.measures.m[field].m[op]
			for _, alias := range set.list() {
				cc.work(1)
				as = append(as, set.value(alias))
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
		cc.work(1)
		if r, ok := n.rawDims[d]; ok {
			groupby = append(groupby, jsval.Str(replacePathInFieldV(r)))
		} else {
			groupby = append(groupby, jsval.Str(replacePathInField(d)))
		}
	}
	return mkv("type", "aggregate", "groupby", jsval.Arr(groupby), "ops", jsval.Arr(ops), "fields", jsval.Arr(fields), "as", jsval.Arr(as))
}

var _ = strings.Join
