package vega

import (
	"fmt"

	"github.com/mgilbir/aster/purego/internal/expr"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
	"github.com/mgilbir/aster/purego/internal/transforms"
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
		fail("group nesting too deep")
	}
	sub := newContext(c.view)
	sub.parent = c
	sub.depth = c.depth + 1
	c.subcontexts = append(c.subcontexts, sub)
	return sub
}

// detach disconnects every operator of a sub-context from the graph.
func (c *rtContext) detach(sub *rtContext) {
	for i, s := range c.subcontexts {
		if s == sub {
			c.subcontexts = append(c.subcontexts[:i], c.subcontexts[i+1:]...)
			break
		}
	}
	for _, n := range sub.nodes {
		n.targets = nil
	}
	for _, n := range sub.nodes {
		n.detach()
	}
	sub.nodes = nil
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
		c.parseOperator(e)
	}
	for _, e := range spec.operators {
		c.parseOperatorParameters(e)
	}
	for _, u := range spec.updates {
		c.parseUpdate(u)
	}
	c.resolve()
	return c
}

func (c *rtContext) resolve() {
	for _, f := range c.unresolved {
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
				return fn.eval(c, jsval.Undefined, jsval.Undefined, jsval.Undefined)
			}
		}
	} else {
		mk, ok := transformFactories[e.typ]
		if !ok {
			switch e.typ {
			case "label", "wordcloud", "heatmap":
				// These draw to a canvas, which a headless upstream render
				// does not have either.
				fail("the %s transform needs a canvas and is not supported", e.typ)
			}
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
		n := c.get(v)
		if n == nil {
			fail("operator not defined (%s)", v.typ)
		}
		return n
	case []P:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = c.parseParameter(e, b)
		}
		return out
	case pField:
		if v.path == "" {
			return transforms.Field{}
		}
		return fieldAccessor(v.path, v.name)
	case pCompare:
		return c.compareFn(v)
	case pKey:
		return c.keyFn(v)
	case pExpr:
		for _, d := range v.fn.deps {
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
	f := transforms.FieldOf(path)
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
	return toValue(n.value), true
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

// Data implements expr.DataProvider: the current tuples of a data set.
func (c *rtContext) Data(name string) (jsval.Value, bool) {
	n := c.dataNode(name, "values")
	if n == nil {
		return jsval.Arr(nil), true
	}
	switch v := n.value.(type) {
	case []jsval.Value:
		return jsval.Arr(v), true
	case []*scene.Item:
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
	return int(jsval.ToNumber(cnt)), true
}

// IsTuple implements expr.TupleChecker.
func (c *rtContext) IsTuple(v jsval.Value) bool { return v.IsObj() }

// Log implements expr.Logger.
func (c *rtContext) Log(level expr.LogLevel, args []jsval.Value) {}

// exprFn is a compiled expression together with the operators it depends on.
type exprFn struct {
	prog   *expr.Program
	src    string
	name   string
	fields []string
	deps   []depRef
	// usesItem/usesEvent report which free variables the expression reads.
	usesItem bool
}

type depRef struct {
	name string
	e    *entry
}

// eval evaluates the expression in context c.
func (f *exprFn) eval(c *rtContext, datum, item, event jsval.Value) jsval.Value {
	s := c.scope()
	s.Datum, s.Item, s.Event = datum, item, event
	v, err := f.prog.Eval(s)
	if err != nil {
		failErr(fmt.Errorf("%w (in expression %s)", err, f.src))
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
	return b.fn.eval(b.ctx, datum, jsval.Undefined, jsval.Undefined)
}

// accessor adapts the expression to a transforms accessor.
func (b *boundExpr) accessor() transforms.Field {
	name := b.fn.name
	if name == "" {
		name = b.fn.src
	}
	return transforms.NamedField(name, b.fn.fields, b.call)
}

// parseExpression compiles an expression and resolves its dependencies against
// the scope, as vega-functions' parseExpression does. Unknown signal names are
// an error, as upstream.
func (s *Scope) parseExpression(code string) *exprFn {
	prog, err := expr.CompileCached(code)
	if err != nil {
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
				addScale(name)
			}
		}
	}
	for _, name := range d.Signals {
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
