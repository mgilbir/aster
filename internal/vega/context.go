package vega

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/textcut"
	"github.com/mgilbir/aster/internal/transforms"
)

// Context instantiates flow specifications into operators and resolves the
// names visible at one nesting level (vega-runtime's context). A sub-context is
// created for every facet cell; it sees its parent's operators, signals,
// scales and data sets but registers its own.
type rtContext struct {
	view   *runView
	g      *flowGraph
	parent *rtContext

	nodes   map[*entry]*opNode
	signals map[string]*opNode
	scales  map[string]*opNode
	data    map[string]map[string]*opNode

	// group is the group item the operators of this context draw into
	// (`context.group`); it is set by the first Mark operator that binds one.
	group *scene.Item
	// root is the operator of the entry flagged root.
	root *opNode

	subcontexts []*rtContext
	unresolved  []func()

	exprScope *expr.Scope
	depth     int
}

func newContext(v *runView) *rtContext {
	return &rtContext{
		view: v, g: v.g,
		nodes: map[*entry]*opNode{}, signals: map[string]*opNode{}, scales: map[string]*opNode{},
		data: map[string]map[string]*opNode{},
	}
}

// maxContextDepth bounds nested group scopes (a specification can nest groups
// arbitrarily deep).
const maxContextDepth = 64

func (c *rtContext) fork() *rtContext {
	if c.depth+1 > maxContextDepth {
		failLimit("group nesting too deep")
	}
	sub := newContext(c.view)
	sub.parent = c
	sub.depth = c.depth + 1
	c.subcontexts = append(c.subcontexts, sub)
	return sub
}

func (c *rtContext) get(e *entry) *opNode {
	for x := c; x != nil; x = x.parent {
		if n, ok := x.nodes[e]; ok {
			return n
		}
	}
	return nil
}

func (c *rtContext) signal(name string) *opNode {
	for x := c; x != nil; x = x.parent {
		if n, ok := x.signals[name]; ok {
			return n
		}
	}
	return nil
}

func (c *rtContext) scaleNode(name string) *opNode {
	for x := c; x != nil; x = x.parent {
		if n, ok := x.scales[name]; ok {
			return n
		}
	}
	return nil
}

func (c *rtContext) dataNode(name, role string) *opNode {
	for x := c; x != nil; x = x.parent {
		if m, ok := x.data[name]; ok {
			return m[role]
		}
	}
	return nil
}

// dataRoles returns the role table of a data set, or nil.
func (c *rtContext) dataRoles(name string) map[string]*opNode {
	for x := c; x != nil; x = x.parent {
		if m, ok := x.data[name]; ok {
			return m
		}
	}
	return nil
}

// parse instantiates a flow specification in this context.
func (c *rtContext) parse(spec *flowSpec) *rtContext {
	for _, e := range spec.operators {
		// An operator's instantiation (its transform, its tables) costs
		// a microsecond or more: 4 steps.
		c.g.work(4)
		c.parseOperator(e)
	}
	for _, e := range spec.operators {
		c.g.work(1)
		c.parseOperatorParameters(e)
	}
	for _, u := range spec.updates {
		c.g.work(1)
		c.parseUpdate(u)
	}
	c.resolve()
	return c
}

func (c *rtContext) resolve() {
	for _, f := range c.unresolved {
		c.g.work(1)
		f()
	}
	c.unresolved = nil
}

func (c *rtContext) parseOperator(e *entry) {
	var n *opNode
	if e.typ == "operator" || e.typ == "" {
		n = c.g.add("operator", e.value)
		n.ctx = c
		if e.update != nil {
			fn := e.update
			n.update = func(n *opNode, p *opParams) any {
				return fn.eval(c, jsval.Undefined, jsval.Undefined, jsval.Undefined, 0)
			}
		}
	} else {
		mk, ok := transformFactories[e.typ]
		if !ok {
			fail("unrecognized transform type: %s", e.typ)
		}
		n = c.g.add(e.typ, nil)
		n.ctx = c
		n.value, n.tr, n.update = mk(c, n, e)
	}
	c.register(e, n)
}

func (c *rtContext) register(e *entry, n *opNode) {
	c.nodes[e] = n
	if e.root {
		c.root = n
	}
	if e.ingest != nil || e.literal != nil {
		c.view.seedCollect(c, n, e)
	}
	if e.parent != nil {
		link := func() {
			p := c.get(e.parent)
			c.g.connect(p, []*opNode{n})
			n.addTarget(p)
		}
		if c.get(e.parent) != nil {
			link()
		} else {
			c.unresolved = append(c.unresolved, link)
		}
	}
	if e.signalName != "" {
		c.signals[e.signalName] = n
	}
	if e.scaleName != "" {
		c.scales[e.scaleName] = n
	}
	for name, roles := range e.dataRoles {
		m := c.data[name]
		if m == nil {
			m = map[string]*opNode{}
			c.data[name] = m
		}
		for _, r := range roles {
			m[r] = n
		}
	}
}

func (c *rtContext) parseOperatorParameters(e *entry) {
	if e.params == nil {
		return
	}
	n := c.nodes[e]
	if n == nil {
		fail("invalid operator id")
	}
	b := &paramBuilder{}
	b.raw.grow(e.params.m.len())
	c.parseParameters(e.params, b)
	deps := n.parameters(&b.raw, !e.noReact, e.initonly)
	c.g.connect(n, deps)
}

// paramBuilder accumulates the resolved parameters of one operator, including
// the dependencies that expressions contribute.
type paramBuilder struct {
	raw smallMap[any]
}

func (b *paramBuilder) set(name string, v any) { b.raw.set(name, v) }

func (c *rtContext) parseParameters(l *paramList, b *paramBuilder) {
	for i := 0; i < l.m.len(); i++ {
		c.g.work(1)
		b.set(l.m.keyAt(i), c.parseParameter(l.m.valAt(i), b))
	}
}

func (c *rtContext) parseParameter(p P, b *paramBuilder) any {
	switch v := p.(type) {
	case nil:
		return nil
	case *entry:
		if v == nil {
			return nil
		}
		if v == undefinedRef {
			fail("Operator not defined: undefined")
		}
		n := c.get(v)
		if n == nil {
			fail("operator not defined (%s)", v.typ)
		}
		return n
	case []P:
		out := make([]any, len(v))
		for i, e := range v {
			c.g.work(1)
			out[i] = c.parseParameter(e, b)
		}
		return out
	case pField:
		if v.path == "" {
			return transforms.Field{}
		}
		segs, err := jsval.SplitFieldPath(v.path)
		if err != nil {
			fail("%s", err.Error())
		}
		if len(segs) == 0 {
			// The runtime builds the accessor as Function("_", "return _[]")
			// for a path with no segment.
			fail("Unexpected token ']'")
		}
		return fieldAccessor(v.path, v.name)
	case pCompare:
		return c.compareFn(v)
	case pKey:
		return c.keyFn(v)
	case pExpr:
		for _, d := range v.fn.deps {
			c.g.work(1)
			if d.e == undefinedRef {
				fail("Operator not defined: undefined")
			}
			n := c.get(d.e)
			if n == nil {
				continue
			}
			b.set(d.name, n)
		}
		return &boundExpr{fn: v.fn, ctx: c}
	case pEncode:
		return c.bindEncoders(v, b)
	case pContext:
		return c
	case pSubflow:
		return c.makeSubflow(v.spec)
	case pTupleID:
		return tupleIDMarker{}
	}
	return p
}

type tupleIDMarker struct{}

// fieldAccessor is vega-util's field(path, name).
func fieldAccessor(path, name string) transforms.Field {
	f := transforms.FieldOfStrict(path)
	if name != "" {
		f.Name = name
	}
	return f
}

func (c *rtContext) parseUpdate(u *updateSpec) {
	src := c.get(u.source)
	if src == nil {
		fail("update source not defined")
	}
	tgt := c.get(u.target)
	if tgt == nil {
		fail("update target not defined")
	}
	c.view.addSignalListener(c, src, tgt, u)
}

// -- expression environment --------------------------------------------------

// scope returns the expression scope bound to this context.
func (c *rtContext) scope() *expr.Scope {
	if c.exprScope == nil {
		s := expr.NewScope(c)
		s.Locale = c.view.locale
		s.Context = c.view.ctx
		s.Now = c.view.now
		s.Rand = c.view.rand
		s.Strings = c.view.strs
		c.exprScope = s
	}
	return c.exprScope
}

// Signal implements expr.Env.
func (c *rtContext) Signal(name string) (jsval.Value, bool) {
	n := c.signal(name)
	if n == nil {
		return jsval.Undefined, false
	}
	return c.view.exprValue(n.value), true
}

// exprValue is an operator's value as an expression reads it, a transform's
// through its `signal`: shared where upstream shares it (a value, a collect's
// tuple list, which upstream's signal holds by reference), and a list of
// values copied into an array charged as the evaluator charges its own.
func (v *runView) exprValue(x any) jsval.Value {
	switch x := x.(type) {
	case jsval.Value:
		return x
	case []jsval.Value:
		return jsval.Arr(x)
	case []any:
		v.chargeItems(len(x))
		out := make([]jsval.Value, len(x))
		for i, e := range x {
			// an element is a value, or a parameter's list as the
			// specification wrote it (toValue)
			out[i] = toValue(e)
		}
		return jsval.Arr(out)
	}
	return jsval.Undefined
}

// toValue views an operator value as a jsval.Value when it is one.
func toValue(x any) jsval.Value {
	switch v := x.(type) {
	case jsval.Value:
		return v
	case []any:
		out := make([]jsval.Value, len(v))
		for i, e := range v {
			out[i] = toValue(e)
		}
		return jsval.Arr(out)
	}
	return jsval.Undefined
}

// Data implements expr.DataProvider: the current tuples of a data set
// (vega-functions' data(): `data ? data.values.value : []`). A data set whose
// values operator (a Sieve, made with the value null) has not run yet reads
// as null, so length() of it is upstream's TypeError.
func (c *rtContext) Data(name string) (jsval.Value, bool) {
	n := c.dataNode(name, "values")
	if n == nil {
		return jsval.Arr(nil), true
	}
	if n.value == nil {
		return jsval.Null, true
	}
	switch v := n.value.(type) {
	case []jsval.Value:
		return jsval.Arr(v), true
	case []*scene.Item:
		c.view.chargeItems(len(v))
		return jsval.Arr(c.view.itemTuples(v)), true
	}
	return jsval.Arr(nil), true
}

// InData implements expr.InDataProvider.
func (c *rtContext) InData(name, field string, value jsval.Value) (int, bool) {
	n := c.dataNode(name, "index:"+field)
	if n == nil {
		return 0, false
	}
	idx, _ := n.value.(*tupleIndex)
	if idx == nil {
		return 0, false
	}
	t, ok := idx.lookup.Get(value)
	if !ok {
		return 0, false
	}
	cnt := t.Get("count")
	if cnt.IsNullish() {
		return 0, false
	}
	// an aggregate's count, a whole number of rows; converted without the
	// platform's out-of-range conversion all the same
	return clampInt(jsval.ToNumber(cnt)), true
}

// IsTuple implements expr.TupleChecker: vega-dataflow's isTuple, a value
// that has a tuple id. A data tuple has one once ingested, and a scenegraph
// item is a tuple upstream; an object an expression or a specification made
// is not one, nor is a function.
func (c *rtContext) IsTuple(v jsval.Value) bool {
	o := v.ObjValue()
	return o != nil && (o.TupleID() != 0 || c.view.itemOf(v) != nil)
}

// Log implements expr.Logger: warn() is a warning, as upstream's logs it
// with the view's logger ("EXPRESSION" and the arguments). info() and debug()
// are below the warning level, which is all the view reports, and upstream's
// logger at that level drops them too.
func (c *rtContext) Log(level expr.LogLevel, args []jsval.Value) {
	if level != expr.LogWarn {
		return
	}
	c.g.warn(string(logText(c.g.walk, args, MaxWarningText)))
}

// logText is "EXPRESSION" and each argument as String() gives it, separated
// by spaces, as the console prints a warning's strings; it stops once it is
// longer than limit, which report cuts it to, so a warning that names a
// large array costs no more than one that does not (w, the call's Walker,
// writes only the start of each value's text).
func logText(w jsval.Walker, args []jsval.Value, limit int) []byte {
	b := []byte("EXPRESSION")
	for _, a := range args {
		if len(b) > limit {
			break
		}
		b = append(b, ' ')
		b = w.AppendStringPrefix(b, a, limit-len(b)+1)
	}
	return b
}

// exprFn is a compiled expression together with the operators it depends on.
type exprFn struct {
	prog   *expr.Program
	src    string
	fields []string
	deps   []depRef
	// usesItem/usesEvent report which free variables the expression reads.
	usesItem bool
}

// undefinedRef stands for an operator reference whose id is undefined (see
// parseExpression); the runtime cannot resolve it.
var undefinedRef = &entry{typ: "undefined"}

type depRef struct {
	name string
	e    *entry
}

// exprVars are the free variables a context defines for an expression: the
// parameters of the function upstream compiles it into.
type exprVars uint8

const (
	varDatum exprVars = 1 << iota
	varItem
	varEvent
)

// evalProgram evaluates prog in c's scope, which every evaluation in the
// context shares. It is the one place the scope is reached, and it sets the
// whole frame: datum, item and event, and which of them the generated
// function has (an expression that reads one it lacks is a ReferenceError),
// so nothing the last evaluation set can leak into this one.
func (c *rtContext) evalProgram(prog *expr.Program, datum, item, event jsval.Value, vars exprVars) (jsval.Value, error) {
	s := c.scope()
	s.Datum, s.Item, s.Event = datum, item, event
	s.NoDatum, s.NoItem, s.NoEvent = vars&varDatum == 0, vars&varItem == 0, vars&varEvent == 0
	return prog.Eval(s)
}

// eval evaluates the expression in context c.
func (f *exprFn) eval(c *rtContext, datum, item, event jsval.Value, vars exprVars) jsval.Value {
	v, err := c.evalProgram(f.prog, datum, item, event, vars)
	if err != nil {
		// The source is the specification's, and an error a facet cell
		// raises is raised in every cell: it is cut like a warning's text.
		failErr(fmt.Errorf("%w (in expression %s)", err, textcut.Cut(f.src, MaxWarningText)))
	}
	return v
}

// boundExpr is an expression instantiated in a context: the accessor an `expr`
// parameter resolves to.
type boundExpr struct {
	fn  *exprFn
	ctx *rtContext
}

func (b *boundExpr) call(datum jsval.Value) jsval.Value {
	return b.fn.eval(b.ctx, datum, jsval.Undefined, jsval.Undefined, varDatum)
}

// accessor adapts the expression to a transforms accessor. It has no name:
// the runtime builds an expression's accessor with none (vega-runtime's
// getExpression ignores the `$name` an `as` gives the parameter), so a
// transform that names its output after it writes "undefined".
func (b *boundExpr) accessor() transforms.Field {
	f := transforms.NamedField("", b.fn.fields, b.call)
	f.Unnamed = true
	return f
}

// parseExpression compiles an expression and resolves its dependencies against
// the scope, as vega-functions' parseExpression does. Unknown signal names are
// an error, as upstream.
func (s *Scope) parseExpression(code string) *exprFn {
	// The compile, a hit's hash and the walks below are linear in the
	// source, which expr.MaxLength bounds: a compile costs microseconds,
	// counted as 8 steps and one per 8 bytes.
	s.work(8 + len(code)/8)
	prog, err := expr.CompileCachedContext(s.opts.ctx, code)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// The call ended while a regular expression literal waited to
			// be compiled: the call's error, not the expression's.
			panic(&budget.Stop{Err: err})
		}
		if errors.Is(err, budget.ErrLimit) {
			perrLimit("Expression parse error: %v (in %.80q)", err, code)
		}
		perr("Expression parse error: %s", code)
	}
	d := prog.Deps()
	f := &exprFn{prog: prog, src: code, fields: d.Fields}
	seen := map[string]bool{}
	add := func(name string, e *entry) {
		if seen[name] {
			return
		}
		seen[name] = true
		f.deps = append(f.deps, depRef{name: name, e: e})
	}
	for _, name := range d.RequiredData {
		s.getData(name)
	}
	for _, name := range d.Data {
		if ds := s.findData(name); ds != nil {
			if e, ok := ds.tuplesRef().(*entry); ok {
				add(":"+name, e)
			}
		}
	}
	for _, ix := range d.Indexes {
		ds := s.getData(ix.Data)
		if e, ok := ds.indataRef(s, jsval.Str(ix.Field)).(*entry); ok {
			add("@"+ix.Field, e)
		}
	}
	addScale := func(name string) {
		if e := s.findScale(name); e != nil {
			add("%"+name, e)
		}
	}
	for _, name := range d.Scales {
		addScale(name)
	}
	if d.AllScales {
		for c := s; c != nil; c = c.parentScope {
			for name := range c.scales {
				s.work(1)
				addScale(name)
			}
		}
	}
	for _, name := range d.Signals {
		if expr.IsObjectPrototypeName(name) && s.findSignal(name) == nil {
			// scope.getSignal(name) finds the inherited property of the plain
			// signals object, truthy, so no "Unrecognized signal name"; the
			// reference built from it has no operator id. The parse goes on,
			// and the runtime fails to resolve the reference when it builds
			// the operator (at once for the view, when a subflow is
			// instantiated for a group, where the dataflow logs it).
			add("$"+name, undefinedRef)
			continue
		}
		e := s.getSignal(name)
		add("$"+name, e)
	}
	prog.AST().Walk(func(n *expr.Node) bool {
		if n.Kind == expr.KindIdentifier && n.Name == "item" {
			f.usesItem = true
			return true
		}
		return false
	})
	return f
}
