package vega

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/geo"
	"sort"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms/hierarchy"
)

// This file is the dataflow kernel: vega-dataflow's Dataflow, Operator and
// Pulse reduced to what a static render needs.
//
// Upstream propagates incremental add/rem/mod tuple sets. The transforms
// package is one-shot (complete input in, complete output out), so a tuple
// flow here carries the whole current data set and an operator that runs
// recomputes from it; what stays faithful is *when* operators run: the same
// rank-ordered, stamp-guarded propagation, the same parameter modification
// tracking, and the same touch/skip/runAfter protocol that autosize relies on
// to converge. Visual items keep add/rem/mod sets because their encoders and
// data join have per-item lifecycle (enter sets run on added items only).

// Pulse is what flows along the pulse edges between operators.
type flowPulse struct {
	stamp int
	// encode names an encoding set to force ("enter" re-runs enter sets);
	// upstream threads it through every pulse of a run.
	encode string
	// changed is Pulse.changed(): the producer added, removed or modified
	// something. Synthesized pulses (an operator that did not run this stamp)
	// are unchanged.
	changed bool

	// Tuple flows.
	tuples []jsval.Value
	// tree is the hierarchy a tree data set carries (`source.root`).
	tree *hierarchy.Tree
	// multi holds the per-source tuple lists of an operator with several
	// pulse sources (data derived from multiple `source` data sets).
	multi [][]jsval.Value

	// Item flows: the full item list plus this pulse's lifecycle sets.
	items         []*scene.Item
	add, rem, mod []*scene.Item
	// reflow marks that every item counts as modified (Pulse.reflow()).
	reflow bool
}

// stopPulse is StopPropagation: targets are not enqueued.
var stopPulse = &flowPulse{stamp: -1}

func (p *flowPulse) fork() *flowPulse {
	q := *p
	q.add, q.rem, q.mod = nil, nil, nil
	return &q
}

// visitItems calls f for the items a visit of the given kinds would reach. A
// reflow pulse treats the whole source as modified, as Pulse.visit(REFLOW)
// does for the tuples that are not already added or modified.
func (p *flowPulse) modOrReflow() []*scene.Item {
	if !p.reflow {
		return p.mod
	}
	if len(p.add)+len(p.mod) == len(p.items) {
		return p.mod
	}
	if len(p.add) == 0 {
		return p.items
	}
	added := make(map[*scene.Item]struct{}, len(p.add))
	for _, it := range p.add {
		added[it] = struct{}{}
	}
	out := make([]*scene.Item, 0, len(p.items))
	for _, it := range p.items {
		if _, ok := added[it]; !ok {
			out = append(out, it)
		}
	}
	return out
}

// Params holds an operator's marshalled parameter values together with which
// of them changed since the previous marshalling (vega-dataflow Parameters).
type opParams struct {
	vals smallMap[any]
	mod  []bool // parallel to vals
}

func newParams() *opParams { return &opParams{} }

// Get returns a parameter value, or nil.
func (p *opParams) Get(name string) any { return p.vals.at(name) }

// Value returns the parameter as a jsval.Value (Undefined when absent or of
// another type).
func (p *opParams) Value(name string) jsval.Value {
	if v, ok := p.vals.at(name).(jsval.Value); ok {
		return v
	}
	return jsval.Undefined
}

// Has reports that the parameter is defined.
func (p *opParams) Has(name string) bool { return p.vals.has(name) }

// Modified tests whether the named parameters changed; with no names it tests
// every parameter.
func (p *opParams) Modified(names ...string) bool {
	if len(names) == 0 {
		for _, m := range p.mod {
			if m {
				return true
			}
		}
		return false
	}
	for _, n := range names {
		if i := p.vals.find(n); i >= 0 && p.mod[i] {
			return true
		}
	}
	return false
}

func (p *opParams) store(name string, v any) (int, bool) {
	i, added := p.vals.set(name, v)
	if added {
		p.mod = append(p.mod, false)
	}
	return i, added
}

func (p *opParams) set(name string, v any, force bool) {
	i := p.vals.find(name)
	if i < 0 || force || !sameValue(p.vals.valAt(i), v) {
		i, _ = p.store(name, v)
		p.mod[i] = true
	}
}

// put records a constant parameter.
func (p *opParams) put(name string, v any) { p.store(name, v) }

// count and nameAt enumerate the parameters in definition order.
func (p *opParams) count() int          { return p.vals.len() }
func (p *opParams) nameAt(i int) string { return p.vals.keyAt(i) }

func (p *opParams) clear() { clear(p.mod) }

// sameValue is JavaScript strict identity for parameter values: primitives by
// value, arrays and objects by reference.
func sameValue(a, b any) bool {
	switch x := a.(type) {
	case jsval.Value:
		y, ok := b.(jsval.Value)
		if !ok {
			return false
		}
		return jsval.SameRef(x, y)
	case []jsval.Value:
		y, ok := b.([]jsval.Value)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	case []*scene.Item:
		y, ok := b.([]*scene.Item)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	case []any:
		return false // rebuilt on every marshal
	case nil:
		return b == nil
	}
	defer func() { recover() }() // uncomparable dynamic types compare as different
	return a == b
}

// argop is one operator-valued parameter: name (and array index, or -1)
// resolved to the operator whose value is pulled at marshal time.
type argop struct {
	op    *opNode
	name  string
	index int
}

// transform is the behaviour of an operator that processes pulses.
type transform interface {
	transform(n *opNode, p *opParams, pulse *flowPulse) *flowPulse
}

// Node is an operator instance (vega-dataflow Operator).
type opNode struct {
	g    *flowGraph
	ctx  *rtContext
	id   int
	name string // entry type, for diagnostics

	rank, qrank int
	stamp       int
	skip        bool
	modified    bool

	value any
	// update is the value-update function of a plain operator (signals,
	// scales' proxies...); tr is a transform's behaviour. Exactly one is set
	// or neither (a value holder).
	update func(n *opNode, p *opParams) any
	tr     transform

	argops   []argop
	argval   *opParams
	initonly bool

	targets  []*opNode
	tree     *hierarchy.Tree // the hierarchy of a tree data set (Sieve, Collect)
	source   []*opNode       // `pulse` parameter operators
	pulse    *flowPulse      // last output
	dynTargs *[]*opNode
}

// Value returns the operator's value.
func (n *opNode) Value() any { return n.value }

func (n *opNode) sig() jsval.Value {
	if v, ok := n.value.(jsval.Value); ok {
		return v
	}
	return jsval.Undefined
}

func (n *opNode) addTarget(t *opNode) {
	for _, x := range n.targets {
		if x == t {
			return
		}
	}
	n.targets = append(n.targets, t)
}

func (n *opNode) removeTarget(t *opNode) {
	for i, x := range n.targets {
		if x == t {
			n.targets = append(n.targets[:i], n.targets[i+1:]...)
			return
		}
	}
}

// setValue sets the value and reports whether it changed by strict identity.
func (n *opNode) setValue(v any) bool {
	if sameValue(n.value, v) && n.value != nil == (v != nil) {
		return false
	}
	n.value = v
	return true
}

// parameters registers the operator-valued entries of raw as dependencies and
// records the constants, like Operator.parameters. raw values that are *Node
// (or []any containing them) are operator references.
func (n *opNode) parameters(raw *smallMap[any], react bool, initonly bool) []*opNode {
	if n.argval == nil {
		n.argval = newParams()
		n.argval.vals.grow(raw.len())
		n.argval.mod = make([]bool, 0, raw.len())
	}
	var deps []*opNode
	add := func(name string, index int, v any) {
		if op, ok := v.(*opNode); ok {
			if op != n {
				if react {
					op.addTarget(n)
				}
				deps = append(deps, op)
			}
			n.argops = append(n.argops, argop{op: op, name: name, index: index})
			return
		}
		if index >= 0 {
			arr, _ := n.argval.vals.at(name).([]any)
			arr[index] = v
			return
		}
		n.argval.put(name, v)
	}
	for i := 0; i < raw.len(); i++ {
		name, v := raw.keyAt(i), raw.valAt(i)
		if name == "pulse" {
			switch ps := v.(type) {
			case *opNode:
				if ps != n {
					ps.addTarget(n)
					deps = append(deps, ps)
				}
				n.source = []*opNode{ps}
			case []any:
				n.source = nil
				for _, e := range ps {
					if op, ok := e.(*opNode); ok && op != n {
						op.addTarget(n)
						deps = append(deps, op)
						n.source = append(n.source, op)
					}
				}
			}
			continue
		}
		if arr, ok := v.([]any); ok {
			n.argval.put(name, make([]any, len(arr)))
			for i, e := range arr {
				add(name, i, e)
			}
			continue
		}
		add(name, -1, v)
	}
	n.marshall(-1)
	n.argval.clear()
	n.initonly = initonly
	return deps
}

// marshall pulls the current values of operator parameters, marking those that
// changed (or whose operator was modified during stamp) as modified.
func (n *opNode) marshall(stamp int) *opParams {
	if n.argval == nil {
		n.argval = newParams()
	}
	for _, a := range n.argops {
		mod := a.op.modified && a.op.stamp == stamp
		if a.index >= 0 {
			arr := n.argval.vals.at(a.name).([]any)
			if arr == nil {
				continue
			}
			if !sameValue(arr[a.index], a.op.value) || mod {
				arr[a.index] = a.op.value
				n.argval.mod[n.argval.vals.find(a.name)] = true
			}
		} else {
			n.argval.set(a.name, a.op.value, mod)
		}
	}
	if n.initonly && stamp >= 0 {
		for _, a := range n.argops {
			a.op.removeTarget(n)
		}
		n.argops = nil
		n.update = nil
	}
	return n.argval
}

// run is Operator.run / Transform.run.
func (n *opNode) run(p *flowPulse) *flowPulse {
	if p.stamp < n.stamp {
		return stopPulse
	}
	var rv *flowPulse
	if n.skip {
		n.skip = false
	} else {
		rv = n.evaluate(p)
	}
	if n.tr == nil {
		if rv == nil {
			rv = p
		}
		n.pulse = rv
		return rv
	}
	if rv == nil {
		rv = p
	}
	if rv != stopPulse {
		n.pulse = rv
	}
	return rv
}

func (n *opNode) evaluate(p *flowPulse) *flowPulse {
	if n.tr != nil {
		params := n.marshall(p.stamp)
		out := n.tr.transform(n, params, p)
		params.clear()
		return out
	}
	if upd := n.update; upd != nil {
		// captured first: an initonly operator drops its update when it marshals
		params := n.marshall(p.stamp)
		v := upd(n, params)
		params.clear()
		if !sameValue(v, n.value) || (v == nil) != (n.value == nil) {
			n.value = v
		} else if !n.modified {
			return stopPulse
		}
	}
	return nil
}

// detach unregisters n from the operators it listens to.
func (n *opNode) detach() {
	for _, a := range n.argops {
		a.op.removeTarget(n)
	}
	for _, s := range n.source {
		s.removeTarget(n)
	}
	n.pulse = nil
	n.source = nil
}

// nodeHeap is the evaluation priority queue: lowest rank first, creation
// order between equal ranks.
type nodeHeap []*opNode

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].qrank != h[j].qrank {
		return h[i].qrank < h[j].qrank
	}
	return h[i].id < h[j].id
}
func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(*opNode)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type postrun struct {
	priority int
	seq      int
	fn       func(*flowGraph)
}

// opError is the panic payload for errors raised while an operator runs
// (expression exceptions, invalid parameters). Graph.evaluate turns it into
// the error of the run, as upstream's catch clause does.
type opError struct{ err error }

func fail(format string, args ...any) { panic(&opError{fmt.Errorf(format, args...)}) }

func failErr(err error) { panic(&opError{err}) }

// failLimit raises an error for an exceeded engine limit. Unlike the errors a
// specification causes, it ends the render (see fatal).
func failLimit(format string, args ...any) {
	panic(&opError{fmt.Errorf("%w: "+format, append([]any{budget.ErrLimit}, args...)...)})
}

// fatal reports whether an error raised during evaluation ends the render.
// Upstream's dataflow catches what an operator throws, logs it and carries
// on (Dataflow.evaluate's catch clause, and asyncCallback for post-run
// callbacks), so an error a specification causes (an expression exception,
// an unknown projection) leaves a partial render, as in upstream. The limits
// that bound the engine's work, cancellation and the engine's own bugs do not
// have an upstream counterpart and stay fatal.
func fatal(err error, internal bool) bool {
	var le *geo.LimitError
	return internal || errors.Is(err, budget.ErrLimit) || errors.As(err, &le) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Graph is the dataflow graph and its scheduler (vega-dataflow Dataflow).
type flowGraph struct {
	ctx      context.Context
	clock    int
	rankCtr  int
	idCtr    int
	touched  []*opNode
	touchSet map[*opNode]struct{}
	heap     nodeHeap
	cur      *flowPulse
	input    map[*opNode]*flowPulse
	postrun  []postrun
	seq      int
	// visits counts operator evaluations, to bound runaway re-evaluation.
	view      *runView
	visits    int
	maxVisits int
	warnings  []string
}

func newGraph(ctx context.Context) *flowGraph {
	return &flowGraph{ctx: ctx, touchSet: map[*opNode]struct{}{}, input: map[*opNode]*flowPulse{}}
}

var errReentrant = errors.New("vega: dataflow already running")

// add creates an operator, assigns it the next rank and touches it.
func (g *flowGraph) add(name string, value any) *opNode {
	g.idCtr++
	n := &opNode{g: g, id: g.idCtr, name: name, value: value, stamp: -1, rank: -1, qrank: -1}
	g.rankCtr++
	n.rank = g.rankCtr
	g.touch(n)
	return n
}

// connect reranks target above its sources when needed so propagation stays
// topologically ordered.
func (g *flowGraph) connect(target *opNode, sources []*opNode) {
	for _, s := range sources {
		if target.rank < s.rank {
			g.rerank(target)
			return
		}
	}
}

func (g *flowGraph) rerank(op *opNode) {
	queue := []*opNode{op}
	guard := 0
	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		g.rankCtr++
		cur.rank = g.rankCtr
		var list []*opNode
		if cur.dynTargs != nil {
			list = *cur.dynTargs
		} else {
			list = cur.targets
		}
		for i := len(list) - 1; i >= 0; i-- {
			t := list[i]
			if t == op {
				fail("cycle detected in dataflow graph")
			}
			queue = append(queue, t)
		}
		if guard++; guard > 1<<22 {
			failLimit("dataflow graph too large to rank")
		}
	}
}

func (g *flowGraph) touch(n *opNode) {
	if g.cur != nil {
		g.enqueue(n, false)
		return
	}
	if _, ok := g.touchSet[n]; !ok {
		g.touchSet[n] = struct{}{}
		g.touched = append(g.touched, n)
	}
}

// update sets a signal-like operator's value and touches it when it changed
// (or force).
func (g *flowGraph) update(n *opNode, v any, force, skip bool) {
	if n.setValue(v) || force {
		g.touch(n)
		if skip {
			n.skip = true
		}
	}
}

func (g *flowGraph) enqueue(n *opNode, force bool) {
	q := n.stamp < g.clock
	if q {
		n.stamp = g.clock
	}
	if q || force {
		n.qrank = n.rank
		heap.Push(&g.heap, n)
	}
}

// runAfter schedules a callback for after the current propagation; outside
// propagation it runs immediately unless enqueue is set.
func (g *flowGraph) runAfter(fn func(*flowGraph), enqueue bool, priority int) {
	if g.cur != nil || enqueue {
		g.seq++
		g.postrun = append(g.postrun, postrun{priority: priority, seq: g.seq, fn: fn})
		return
	}
	fn(g)
}

// getPulse provides the input pulse for op: its sources' current pulses, or a
// synthesized unchanged pulse carrying the source data from their last run.
func (g *flowGraph) getPulse(op *opNode, encode string) *flowPulse {
	if len(op.source) > 1 {
		mp := &flowPulse{stamp: g.clock, encode: encode}
		for _, s := range op.source {
			sp := s.pulse
			var tl []jsval.Value
			if sp != nil && sp != stopPulse {
				tl = sp.tuples
				if sp.stamp == g.clock && sp.changed {
					mp.changed = true
				}
			}
			mp.multi = append(mp.multi, tl)
		}
		return mp
	}
	if p, ok := g.input[op]; ok {
		return p
	}
	var s *opNode
	if len(op.source) == 1 {
		s = op.source[0]
	}
	return g.singlePulse(s)
}

func (g *flowGraph) singlePulse(s *opNode) *flowPulse {
	if s != nil && s.pulse != nil && s.pulse.stamp == g.clock {
		return s.pulse
	}
	p := &flowPulse{stamp: g.clock, encode: g.cur.encode}
	if s != nil && s.pulse != nil && s.pulse != stopPulse {
		p.tuples = s.pulse.tuples
		p.items = s.pulse.items
		p.tree = s.pulse.tree
	}
	return p
}

// evaluate runs one propagation, like Dataflow.evaluate. encode is threaded
// through every pulse of the run.
func (g *flowGraph) evaluate(encode string) (err error) {
	if g.cur != nil {
		return errReentrant
	}
	if len(g.touched) == 0 {
		return nil
	}
	g.clock++
	g.cur = &flowPulse{stamp: g.clock, encode: encode}
	for _, op := range g.touched {
		g.enqueue(op, true)
	}
	g.touched = g.touched[:0]
	clear(g.touchSet)

	internal := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				g.heap = g.heap[:0]
				switch e := r.(type) {
				case *opError:
					err = e.err
				case *geo.LimitError:
					err = e.Err
				case *scale.Error, *expr.Error:
					err = e.(error) // an exception a scale or an expression threw
				case error:
					err, internal = fmt.Errorf("%w\n%s", e, shortStack()), true
				default:
					err, internal = fmt.Errorf("vega: internal error: %v\n%s", r, shortStack()), true
				}
			}
		}()
		for g.heap.Len() > 0 {
			op := heap.Pop(&g.heap).(*opNode)
			if op.rank != op.qrank {
				g.enqueue(op, true)
				continue
			}
			g.visits++
			if g.visits&255 == 0 {
				if cerr := g.ctx.Err(); cerr != nil {
					panic(&opError{cerr})
				}
			}
			if g.maxVisits > 0 && g.visits > g.maxVisits {
				failLimit("dataflow evaluation limit")
			}
			next := op.run(g.getPulse(op, encode))
			if next != stopPulse {
				if op.dynTargs != nil {
					for _, t := range *op.dynTargs {
						g.enqueue(t, false)
					}
				} else {
					for i := 0; i < len(op.targets); i++ {
						g.enqueue(op.targets[i], false)
					}
				}
			}
		}
	}()

	g.input = map[*opNode]*flowPulse{}
	g.cur = nil
	if err != nil {
		g.postrun = nil
		if fatal(err, internal) {
			return err
		}
		g.warn(err.Error())
		return nil
	}
	if len(g.postrun) > 0 {
		pr := g.postrun
		g.postrun = nil
		sort.SliceStable(pr, func(i, j int) bool { return pr[i].priority > pr[j].priority })
		for _, p := range pr {
			if e := g.safely(p.fn); e != nil {
				return e
			}
		}
	}
	return nil
}

// safely runs a post-run callback; like upstream's asyncCallback it logs the
// errors a specification causes and returns only fatal ones.
func (g *flowGraph) safely(fn func(*flowGraph)) (err error) {
	internal := false
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*opError); ok {
				err = e.err
			} else if le, ok := r.(*geo.LimitError); ok {
				err = le.Err
			} else if se, ok := r.(*scale.Error); ok {
				err = se
			} else if ee, ok := r.(*expr.Error); ok {
				err = ee
			} else {
				err, internal = fmt.Errorf("vega: internal error: %v", r), true
			}
			if !fatal(err, internal) {
				g.warn(err.Error())
				err = nil
			}
		}
	}()
	fn(g)
	return nil
}

// run is Dataflow.run: evaluate synchronously.
func (g *flowGraph) run(encode string) error { return g.evaluate(encode) }

// pulseInput registers a pre-built input pulse for op (Dataflow.pulse).
func (g *flowGraph) pulseInput(op *opNode, p *flowPulse) {
	g.touch(op)
	p.stamp = g.clock
	if g.cur == nil {
		p.stamp = g.clock + 1
	}
	g.input[op] = p
}
