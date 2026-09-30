package vega

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// This file holds the parse-time model: dataflow entries (upstream's
// serialisable operator descriptions), parameter references and the Scope the
// spec parsers register them in (vega-parser Scope, DataScope, util).

// P is a parameter of an entry. The concrete kinds are:
//
//	jsval.Value / string / float64 / bool ... a constant
//	*entry                                    the value of another operator
//	[]P                                       an array of parameters
//	pField, pCompare, pKey, pExpr, pEncode    accessors resolved at instantiation
//	pContext, pSubflow, pTupleID              runtime services
type P any

// pField is a field accessor reference; name is the output name override.
type pField struct{ path, name string }

// pCompare is a comparator reference. Elements are field paths (string) or
// operators carrying an accessor or an order.
type pCompare struct{ fields, orders []P }

// pKey is a key-function reference over field paths.
type pKey struct {
	fields []P
	flat   bool
}

// pExpr wraps an expression used as a parameter (an accessor over a datum).
type pExpr struct{ fn *exprFn }

// pEncode is the encoder table of an Encode operator.
type pEncode struct {
	sets  map[string]*encodeSet
	order []string
}

type pContext struct{}
type pSubflow struct{ spec *flowSpec }
type pTupleID struct{}

// paramList is an ordered name -> P map.
type paramList struct {
	m smallMap[P]
}

func newParamList() *paramList { return &paramList{} }

func (l *paramList) set(name string, v P) { l.m.set(name, v) }

func (l *paramList) get(name string) P { return l.m.at(name) }

func (l *paramList) has(name string) bool { return l.m.has(name) }

func (l *paramList) delete(name string) { l.m.delete(name) }

// tmeta is a transform's definition metadata.
type tmeta struct {
	source, generates, changes, modifies, nomod bool
}

// entry describes one operator of the dataflow.
type entry struct {
	typ    string
	value  any
	params *paramList

	update   *exprFn // operator update expression
	noReact  bool
	initonly bool
	root     bool
	// signalName and scaleName register the operator under that name in the
	// context it is instantiated in; dataRoles register it as the given roles
	// of a named data set.
	signalName string
	scaleName  string
	dataRoles  map[string][]string
	// parent names an operator that must run after this one (children of a
	// group register their sieve with the group's layout/bound operator).
	parent *entry

	meta tmeta

	// collect initial data: literal tuples, ingested values or a request.
	literal []jsval.Value
	ingest  *ingestSpec
}

// ingestSpec describes tuples a Collect starts with.
type ingestSpec struct {
	values  jsval.Value
	url     jsval.Value
	request bool
	format  jsval.Value
}

func newEntry(typ string, value any, params *paramList) *entry {
	return &entry{typ: typ, value: value, params: params}
}

func operatorEntry(value any) *entry { return newEntry("operator", value, nil) }

// mk builds an entry of the given type from alternating name, value pairs.
func mk(typ string, kv ...any) *entry {
	e := &entry{typ: typ}
	if len(kv) > 0 {
		e.params = newParamList()
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == nil {
				continue
			}
			e.params.set(kv[i].(string), kv[i+1])
		}
	}
	return e
}

func (e *entry) param(name string, v P) *entry {
	if e.params == nil {
		e.params = newParamList()
	}
	e.params.set(name, v)
	return e
}

// flowSpec is the serialised form upstream calls the runtime specification:
// the operators of one scope, instantiated once per (sub)context.
type flowSpec struct {
	operators []*entry
	updates   []*updateSpec
}

// updateSpec is an event-driven signal update. Only internal streams (signal
// and scale changes) fire in a static render.
type updateSpec struct {
	source *entry // operator whose change fires the update
	target *entry
	update *exprFn // handler expression (evaluated with `event` unset)
	value  jsval.Value
	hasVal bool
	force  bool
	// viaOp: the handler is `_.$value` of a proxied signal ({signal: ...}).
	viaSignal *entry
}

// dataScope tracks the operators of a data set (vega-parser DataScope).
type dataScope struct {
	scope     *Scope
	input     *entry
	output    *entry
	values    *entry
	aggregate *entry
	index     map[string]*entry

	extent, domain, vals, lookup, indata map[string]P
	counts                               map[string]*countsRef
}

type countsRef struct {
	agg *entry
	ref P
}

func newDataScope(s *Scope, input, output, values, aggr *entry) *dataScope {
	return &dataScope{scope: s, input: input, output: output, values: values, aggregate: aggr, index: map[string]*entry{}}
}

// dataScopeFromEntries wires a pipeline: each entry's pulse is the previous
// entry's operator, and the last two entries are the output and the values.
func dataScopeFromEntries(s *Scope, entries []*entry) *dataScope {
	n := len(entries)
	values := entries[n-1]
	var output *entry
	if n >= 2 {
		output = entries[n-2]
	}
	input := entries[0]
	var aggr *entry
	if input != nil && input.typ == "load" && n > 1 {
		input = entries[1]
	}
	s.add(entries[0])
	for i := 1; i < n; i++ {
		entries[i].param("pulse", entries[i-1])
		s.add(entries[i])
		if entries[i].typ == "aggregate" {
			aggr = entries[i]
		}
	}
	return newDataScope(s, input, output, values, aggr)
}

var reNonWord = regexp.MustCompile(`\W+`)

// aggrField names the output field of an aggregate over (op, field): the
// operator name and the field with runs of non-word characters collapsed to
// underscores and trimmed of leading/trailing ones.
func aggrField(op, field jsval.Value) string {
	var b strings.Builder
	hasOp := !op.IsNullish() && op.IsTruthy()
	if hasOp {
		if op.IsObj() && op.Get("signal").IsTruthy() {
			b.WriteString("$" + op.Get("signal").AsString())
		} else {
			b.WriteString(op.AsString())
		}
	}
	hasField := !field.IsNullish() && field.IsTruthy()
	if hasOp && hasField {
		b.WriteByte('_')
	}
	if hasField {
		if field.IsObj() && field.Get("signal").IsTruthy() {
			b.WriteString("$" + field.Get("signal").AsString())
		} else if field.IsStr() {
			s := reNonWord.ReplaceAllString(field.StrValue(), "_")
			s = strings.Trim(s, "_")
			b.WriteString(s)
		}
	}
	return b.String()
}

func sortKey(sort jsval.Value) string {
	if !sort.IsObj() {
		return ""
	}
	o := "+"
	if sort.Get("order").IsStr() && sort.Get("order").StrValue() == "descending" {
		o = "-"
	}
	return o + aggrField(sort.Get("op"), sort.Get("field"))
}

func (d *dataScope) cache(m *map[string]P, scope *Scope, optype string, field jsval.Value, counts jsval.Value, useCounts bool, index bool) P {
	if *m == nil {
		*m = map[string]P{}
	}
	sk := ""
	if useCounts {
		sk = sortKey(counts)
	}
	var key string
	keyed := field.IsStr()
	if keyed {
		scope = d.scope
		key = field.StrValue()
		if sk != "" {
			key += "|" + sk
		}
		if v, ok := (*m)[key]; ok {
			return v
		}
	}
	var params *paramList
	if useCounts && counts.IsObj() || (useCounts && counts.Kind() == jsval.KindBool && counts.BoolValue()) {
		params = newParamList()
		params.set("field", keyFieldRef)
		params.set("pulse", d.countsRef(scope, field, counts))
	} else {
		params = newParamList()
		params.set("field", scope.fieldRef(field, ""))
		params.set("pulse", d.output)
	}
	if sk != "" {
		params.set("sort", scope.sortRef(counts))
	}
	op := scope.add(newEntry(optype, nil, params))
	if index {
		d.index[field.AsString()] = op
	}
	if keyed {
		(*m)[key] = op
	}
	return op
}

var keyFieldRef = pField{path: "key"}

func (d *dataScope) countsRef(scope *Scope, field jsval.Value, sort jsval.Value) P {
	if d.counts == nil {
		d.counts = map[string]*countsRef{}
	}
	var key string
	keyed := field.IsStr()
	if keyed {
		scope = d.scope
		key = field.StrValue()
		if v, ok := d.counts[key]; ok {
			if sort.IsObj() && sort.Get("field").IsTruthy() {
				addSortField(scope, v.agg.params, sort)
			}
			return v.ref
		}
	}
	p := newParamList()
	p.set("groupby", scope.fieldRef(field, "key"))
	p.set("pulse", d.output)
	if sort.IsObj() && sort.Get("field").IsTruthy() {
		addSortField(scope, p, sort)
	}
	a := scope.add(newEntry("aggregate", nil, p))
	v := scope.add(mk("collect", "pulse", a))
	cr := &countsRef{agg: a, ref: v}
	if keyed {
		d.counts[key] = cr
	}
	return cr.ref
}

func addSortField(scope *Scope, p *paramList, sort jsval.Value) {
	as := aggrField(sort.Get("op"), sort.Get("field"))
	if ops, ok := p.get("ops").([]P); ok {
		names, _ := p.get("as").([]P)
		for _, n := range names {
			if s, ok := n.(string); ok && s == as {
				return
			}
		}
		_ = ops
	} else {
		p.set("ops", []P{"count"})
		p.set("fields", []P{nil})
		p.set("as", []P{"count"})
	}
	if op := sort.Get("op"); op.IsTruthy() {
		ops := p.get("ops").([]P)
		fields := p.get("fields").([]P)
		names := p.get("as").([]P)
		if op.IsObj() && op.Get("signal").IsTruthy() {
			ops = append(ops, scope.signalRef(op.Get("signal").AsString()))
		} else {
			ops = append(ops, op.AsString())
		}
		fields = append(fields, scope.fieldRef(sort.Get("field"), ""))
		names = append(names, as)
		p.set("ops", ops)
		p.set("fields", fields)
		p.set("as", names)
	}
}

func (d *dataScope) tuplesRef() P { return d.values }

func (d *dataScope) extentRef(scope *Scope, field jsval.Value) P {
	return d.cache(&d.extent, scope, "extent", field, jsval.Undefined, false, false)
}

func (d *dataScope) domainRef(scope *Scope, field jsval.Value) P {
	return d.cache(&d.domain, scope, "values", field, jsval.Undefined, false, false)
}

func (d *dataScope) valuesRef(scope *Scope, field jsval.Value, sort jsval.Value) P {
	c := sort
	if !sort.IsObj() {
		c = jsval.True
	}
	return d.cache(&d.vals, scope, "values", field, c, true, false)
}

func (d *dataScope) lookupRef(scope *Scope, field jsval.Value) P {
	return d.cache(&d.lookup, scope, "tupleindex", field, jsval.Undefined, false, false)
}

func (d *dataScope) indataRef(scope *Scope, field jsval.Value) P {
	return d.cache(&d.indata, scope, "tupleindex", field, jsval.True, true, true)
}

// Scope is the parse-time scope (vega-parser Scope): the registries of signals,
// scales, data sets and fields visible at one nesting level, and the operators
// created at that level. Sub-scopes see their parents' names.
type Scope struct {
	config jsval.Value
	opts   *parseOptions

	parentScope *Scope
	signals     map[string]*entry
	lambdas     map[string]*entry
	scales      map[string]*entry
	data        map[string]*dataScope
	fields      map[string]P

	operators []*entry
	updates   []*updateSpec

	root        *entry
	description jsval.Value
	legends     P
	locale      jsval.Value

	subid   int
	nextsub *int

	parents  []P
	encodes  []P
	lookups  []P
	markpath *[]int

	// handles resolve the opaque operator references guide plans carry;
	// fieldRefs numbers the anonymous data sets of multi-domain scales.
	handles   map[string]*entry
	fieldRefs int

	// depth bounds group nesting.
	depth int
}

type parseOptions struct {
	maxDepth int
}

func newScope(config jsval.Value, opts *parseOptions) *Scope {
	zero := 0
	mp := []int{}
	return &Scope{
		config: config, opts: opts,
		signals: map[string]*entry{}, lambdas: map[string]*entry{}, scales: map[string]*entry{},
		data: map[string]*dataScope{}, fields: map[string]P{},
		nextsub: &zero, markpath: &mp,
	}
}

func (s *Scope) fork() *Scope {
	*s.nextsub++
	return &Scope{
		config: s.config, opts: s.opts, legends: s.legends, locale: s.locale,
		parentScope: s,
		signals:     map[string]*entry{}, lambdas: map[string]*entry{}, scales: map[string]*entry{},
		data: map[string]*dataScope{}, fields: map[string]P{},
		subid: *s.nextsub, nextsub: s.nextsub,
		parents:  append([]P(nil), s.parents...),
		encodes:  append([]P(nil), s.encodes...),
		lookups:  append([]P(nil), s.lookups...),
		markpath: s.markpath,
		depth:    s.depth + 1,
	}
}

func (s *Scope) isSubscope() bool { return s.subid > 0 }

func (s *Scope) toRuntime() *flowSpec {
	s.finish()
	return &flowSpec{operators: s.operators, updates: s.updates}
}

// finish records on each entry the names it is registered under so that the
// runtime can build its name tables.
func (s *Scope) finish() {
	if s.root != nil {
		s.root.root = true
	}
	for name, e := range s.signals {
		e.signalName = name
	}
	for name, e := range s.scales {
		e.scaleName = name
	}
	annotate := func(e *entry, name, role string) {
		if e == nil {
			return
		}
		if e.dataRoles == nil {
			e.dataRoles = map[string][]string{}
		}
		e.dataRoles[name] = append(e.dataRoles[name], role)
	}
	for name, ds := range s.data {
		annotate(ds.input, name, "input")
		annotate(ds.output, name, "output")
		annotate(ds.values, name, "values")
		for f, e := range ds.index {
			annotate(e, name, "index:"+f)
		}
	}
}

func (s *Scope) add(e *entry) *entry {
	s.operators = append(s.operators, e)
	return e
}

func (s *Scope) proxy(e P) *entry {
	return s.add(mk("proxy", "value", e))
}

func (s *Scope) addUpdate(u *updateSpec) { s.updates = append(s.updates, u) }

func (s *Scope) pushState(encode, parent, lookup P) {
	s.encodes = append(s.encodes, s.add(mk("sieve", "pulse", encode)))
	s.parents = append(s.parents, parent)
	if lookup != nil {
		s.lookups = append(s.lookups, s.proxy(lookup))
	} else {
		s.lookups = append(s.lookups, nil)
	}
	*s.markpath = append(*s.markpath, -1)
}

func (s *Scope) popState() {
	s.encodes = s.encodes[:len(s.encodes)-1]
	s.parents = s.parents[:len(s.parents)-1]
	s.lookups = s.lookups[:len(s.lookups)-1]
	*s.markpath = (*s.markpath)[:len(*s.markpath)-1]
}

func (s *Scope) parent() P {
	if len(s.parents) == 0 {
		return nil
	}
	return s.parents[len(s.parents)-1]
}

func (s *Scope) encode() P {
	if len(s.encodes) == 0 {
		return nil
	}
	return s.encodes[len(s.encodes)-1]
}

func (s *Scope) lookup() P {
	if len(s.lookups) == 0 {
		return nil
	}
	return s.lookups[len(s.lookups)-1]
}

func (s *Scope) nextMarkpath() int {
	p := *s.markpath
	p[len(p)-1]++
	return p[len(p)-1]
}

// -- name lookup through the scope chain ------------------------------------

func (s *Scope) findSignal(name string) *entry {
	for c := s; c != nil; c = c.parentScope {
		if e, ok := c.signals[name]; ok {
			return e
		}
	}
	return nil
}

func (s *Scope) findScale(name string) *entry {
	for c := s; c != nil; c = c.parentScope {
		if e, ok := c.scales[name]; ok {
			return e
		}
	}
	return nil
}

func (s *Scope) findData(name string) *dataScope {
	for c := s; c != nil; c = c.parentScope {
		if e, ok := c.data[name]; ok {
			return e
		}
	}
	return nil
}

func (s *Scope) findLambda(name string) *entry {
	for c := s; c != nil; c = c.parentScope {
		if e, ok := c.lambdas[name]; ok {
			return e
		}
	}
	return nil
}

func (s *Scope) findField(name string) P {
	for c := s; c != nil; c = c.parentScope {
		if e, ok := c.fields[name]; ok {
			return e
		}
	}
	return nil
}

// parseError is an error in the specification.
type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

func perr(format string, args ...any) {
	panic(&parseError{msg: fmt.Sprintf(format, args...)})
}

func quote(v jsval.Value) string { return string(jsval.AppendJSON(nil, v)) }

// -- references --------------------------------------------------------------

func isSignalObj(v jsval.Value) bool { return v.IsObj() && v.Get("signal").IsTruthy() }

func isExprObj(v jsval.Value) bool { return v.IsObj() && v.Get("expr").IsTruthy() }

func hasSignal(v jsval.Value) bool {
	if isSignalObj(v) {
		return true
	}
	switch v.Kind() {
	case jsval.KindObj:
		o := v.ObjValue()
		for i := 0; i < o.Len(); i++ {
			if hasSignal(o.ValueAt(i)) {
				return true
			}
		}
	case jsval.KindArr:
		for _, it := range v.Items() {
			if hasSignal(it) {
				return true
			}
		}
	}
	return false
}

func (s *Scope) fieldRef(field jsval.Value, name string) P {
	if field.IsStr() {
		return pField{path: field.StrValue(), name: name}
	}
	if !isSignalObj(field) {
		perr("Unsupported field reference: %s", quote(field))
	}
	sname := field.Get("signal").AsString()
	if f := s.findField(sname); f != nil {
		return f
	}
	params := newParamList()
	params.set("name", s.signalRef(sname))
	if name != "" {
		params.set("as", name)
	}
	f := s.add(newEntry("field", nil, params))
	s.fields[sname] = f
	return f
}

func (s *Scope) compareRef(cmp jsval.Value) P {
	signal := false
	check := func(v jsval.Value) P {
		if isSignalObj(v) {
			signal = true
			return s.signalRef(v.Get("signal").AsString())
		}
		if isExprObj(v) {
			signal = true
			return s.exprRef(v.Get("expr").AsString(), "")
		}
		return v
	}
	arr := func(v jsval.Value) []P {
		var out []P
		if v.IsUndefined() {
			return out
		}
		if v.IsArr() {
			for _, it := range v.Items() {
				out = append(out, check(it))
			}
			return out
		}
		return append(out, check(v))
	}
	fields := arr(cmp.Get("field"))
	orders := arr(cmp.Get("order"))
	if signal {
		return s.add(mk("compare", "fields", fields, "orders", orders))
	}
	return pCompare{fields: fields, orders: orders}
}

func (s *Scope) keyRef(fields jsval.Value, flat bool) P {
	signal := false
	var out []P
	add := func(v jsval.Value) {
		if isSignalObj(v) {
			signal = true
			e := s.findSignal(v.Get("signal").AsString())
			out = append(out, e)
			return
		}
		out = append(out, v)
	}
	if fields.IsArr() {
		for _, it := range fields.Items() {
			add(it)
		}
	} else if !fields.IsUndefined() {
		add(fields)
	}
	if signal {
		e := mk("key", "fields", out)
		if flat {
			e.param("flat", jsval.True)
		}
		return s.add(e)
	}
	return pKey{fields: out, flat: flat}
}

func (s *Scope) sortRef(sort jsval.Value) P {
	if !sort.IsObj() {
		return nil
	}
	a := aggrField(sort.Get("op"), sort.Get("field"))
	o := sort.Get("order")
	if o.IsUndefined() || o.IsNull() || !o.IsTruthy() {
		o = jsval.Str("ascending")
	}
	if isSignalObj(o) {
		return s.add(mk("compare", "fields", []P{jsval.Str(a)}, "orders", s.signalRef(o.Get("signal").AsString())))
	}
	return pCompare{fields: []P{jsval.Str(a)}, orders: []P{o}}
}

// -- signals -----------------------------------------------------------------

func (s *Scope) hasOwnSignal(name string) bool { _, ok := s.signals[name]; return ok }

func (s *Scope) addSignal(name string, value any) *entry {
	if s.hasOwnSignal(name) {
		perr("Duplicate signal name: %s", quote(jsval.Str(name)))
	}
	var e *entry
	if ee, ok := value.(*entry); ok {
		e = ee
	} else {
		e = s.add(operatorEntry(value))
	}
	s.signals[name] = e
	return e
}

func (s *Scope) getSignal(name string) *entry {
	e := s.findSignal(name)
	if e == nil {
		perr("Unrecognized signal name: %s", quote(jsval.Str(name)))
	}
	return e
}

// signalRef references a signal by name, or, when the text is not a signal name,
// an anonymous operator (a "lambda") that evaluates it as an expression once
// the whole scope is parsed.
func (s *Scope) signalRef(name string) P {
	if e := s.findSignal(name); e != nil {
		return e
	}
	if e := s.findLambda(name); e != nil {
		if _, own := s.lambdas[name]; own {
			return e
		}
		return e
	}
	e := s.add(operatorEntry(nil))
	s.lambdas[name] = e
	return e
}

func (s *Scope) parseLambdas() {
	for name, op := range s.lambdas {
		setExprUpdate(op, s.parseExpression(name))
	}
}

func (s *Scope) property(spec jsval.Value) P {
	if isSignalObj(spec) {
		return s.signalRef(spec.Get("signal").AsString())
	}
	return spec
}

// objectProperty turns an object or array with embedded signals into a lambda
// that rebuilds it; a plain value is returned as is.
func (s *Scope) objectProperty(spec jsval.Value) P {
	if !spec.IsObj() && !spec.IsArr() {
		return spec
	}
	if spec.IsObj() && spec.Get("signal").IsTruthy() {
		return s.signalRef(spec.Get("signal").AsString())
	}
	return s.signalRef(propertyLambda(spec))
}

func (s *Scope) exprRef(code string, name string) P {
	fn := s.parseExpression(code)
	if name != "" {
		fn.name = name
	}
	return s.add(mk("expression", "expr", pExpr{fn: fn}))
}

// -- scales and projections --------------------------------------------------

func (s *Scope) addScaleProj(name string, e *entry) {
	if _, ok := s.scales[name]; ok {
		perr("Duplicate scale or projection name: %s", quote(jsval.Str(name)))
	}
	s.scales[name] = s.add(e)
}

func (s *Scope) getScale(name string) *entry {
	e := s.findScale(name)
	if e == nil {
		perr("Unrecognized scale name: %s", quote(jsval.Str(name)))
	}
	return e
}

func (s *Scope) scaleRef(name string) P { return s.getScale(name) }

func (s *Scope) scaleType(name string) string {
	e := s.getScale(name)
	if e.params != nil {
		if v, ok := e.params.get("type").(jsval.Value); ok {
			return v.AsString()
		}
		if v, ok := e.params.get("type").(string); ok {
			return v
		}
	}
	return ""
}

// -- data --------------------------------------------------------------------

func (s *Scope) addData(name string, d *dataScope) *dataScope {
	if _, ok := s.data[name]; ok {
		perr("Duplicate data set name: %s", quote(jsval.Str(name)))
	}
	s.data[name] = d
	return d
}

func (s *Scope) getData(name string) *dataScope {
	d := s.findData(name)
	if d == nil {
		perr("Undefined data set name: %s", quote(jsval.Str(name)))
	}
	return d
}

func (s *Scope) addDataPipeline(name string, entries []*entry) *dataScope {
	if _, ok := s.data[name]; ok {
		perr("Duplicate data set name: %s", quote(jsval.Str(name)))
	}
	return s.addData(name, dataScopeFromEntries(s, entries))
}

// propertyLambda writes the expression that rebuilds an object or array whose
// leaves may be signal expressions.
func propertyLambda(spec jsval.Value) string {
	var b strings.Builder
	var write func(v jsval.Value)
	write = func(v jsval.Value) {
		if v.IsObj() || v.IsArr() {
			if v.IsObj() && v.Get("signal").IsTruthy() {
				b.WriteString(v.Get("signal").AsString())
				return
			}
			if v.IsArr() {
				b.WriteByte('[')
				for i, it := range v.Items() {
					if i > 0 {
						b.WriteByte(',')
					}
					write(it)
				}
				b.WriteByte(']')
				return
			}
			b.WriteByte('{')
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(stringValue(jsval.Str(o.KeyAt(i))))
				b.WriteByte(':')
				write(o.ValueAt(i))
			}
			b.WriteByte('}')
			return
		}
		b.WriteString(stringValue(v))
	}
	write(spec)
	return b.String()
}

// stringValue is vega-util's stringValue: JSON, with U+2028/9 escaped.
func stringValue(v jsval.Value) string {
	s := quote(v)
	s = strings.ReplaceAll(s, " ", ` `)
	return strings.ReplaceAll(s, " ", ` `)
}
