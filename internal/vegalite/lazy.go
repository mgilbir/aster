package vegalite

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Some signal references must be evaluated after the whole tree is parsed,
// because signal names get renamed while child layout sizes merge into their
// parents (upstream's SignalRefWrapper defines `signal` as a getter). A lazy
// signal is an ordinary {signal: "\x00lazy:N"} object whose real expression is
// produced by the function attached to it as its string conversion (the
// object's stringer, which only the compiler sets, so a specification cannot
// forge one); resolveLazy rewrites them in the finished output, and
// deepEqual/signalOf see through them in the meantime. Nothing is kept outside
// the objects themselves, so compilations share no state.

const lazyMarker = "\x00lazy:"

// compileCtx is the per-compilation state shared by every model of one tree.
type compileCtx struct {
	// v5 selects Vega-Lite 5.8.0 behaviour wherever it differs from 6.4.3.
	v5 bool
	// loc is the time zone of local-time datetimes (Options.Location).
	loc *time.Location
	// ctx is the caller's context, checked in loops that can run long.
	ctx context.Context
	// meter charges the compilation's work to the call's ledger (or polls
	// ctx when there is none); see work.
	meter budget.Meter
	// lazySeq numbers the lazy signals; outputSeq the output nodes' hash keys.
	lazySeq, outputSeq int64
	// sigIdx speeds up signal lookups by name (see signalIndex).
	sigIdx signalIndex
	// transforms counts the data transforms parsed so far (see maxTransforms).
	transforms int
	// maxViews is Options.MaxViews (see countViews); configBytes the size
	// of the config and theme every view compiles.
	maxViews    int
	configBytes float64
	// warnFn is Options.Warn.
	warnFn func(string) error
	// titleMerges are the title field-def lists merged so far, by their
	// first element (see mergeTitleFieldDefs); titleAcc the buffers of the
	// titles mergeTitle made, by the title each made last.
	titleMerges map[*Value]*titleMerge
	titleAcc    map[titleKey]*sbuf
	// jsr is the compilation's JavaScript realm (see jsRealm).
	jsr *jsRealm
	// conv is the compilation's conversion scope (jsval.ConversionScope):
	// the objects it gives a prototype convert to text as JavaScript's do
	// until Compile returns.
	conv *jsval.ConversionScope
	// pending is the work counted and not yet charged (see work).
	pending int64
	// ledger is the call's ledger (nil: unlimited), which alloc charges
	// for the compilation's memory; memAhead is what alloc took from it and
	// has not used yet, memUsed what it charged in all (see mem.go).
	ledger            *budget.Ledger
	memAhead, memUsed int64
}

// check panics with the context's error once the caller's context is done.
// Polling is cheap next to the work between two checkpoints.
func (c *compileCtx) check() {
	if c == nil || c.ctx == nil {
		return
	}
	if err := c.ctx.Err(); err != nil {
		panic(cancelError{err})
	}
}

// work charges n steps of a loop whose trip count the specification
// controls, and panics with the context's error once the caller's context is
// done. A step costs an addition and a comparison; the context is polled
// every thousand or so, so every loop that charges its steps stops within
// about a millisecond of the deadline. Charge more than 1 for a step that
// does more work than a few allocations (a model, a selection).
//
// Steps are counted here and handed to the Meter workBatch at a time, so a
// loop of single steps costs an addition and a comparison, inlined; the
// Meter polls every thousand or so as before (flushWork hands over the rest
// when the compilation ends).
func (c *compileCtx) work(n int64) {
	if c == nil {
		return
	}
	c.pending += min(max(n, 0), maxWorkCharge)
	if c.pending >= workBatch {
		c.flushWork()
	}
}

// workBatch is how many steps work counts before handing them to the
// Meter; maxWorkCharge caps one charge, as the Meter does, so the count
// cannot wrap.
const (
	workBatch     = 256
	maxWorkCharge = 1 << 40
)

// flushWork hands the counted steps to the Meter and panics with the
// context's error once the caller's context is done.
func (c *compileCtx) flushWork() {
	n := c.pending
	c.pending = 0
	if err := c.meter.Work(n); err != nil {
		panic(cancelError{err})
	}
}

// ctxErr is the caller's context's error, nil while it runs.
func (c *compileCtx) ctxErr() error {
	if c == nil || c.ctx == nil {
		return nil
	}
	return c.ctx.Err()
}

// walker is the call's jsval.Walker (its ledger and deadline), with which
// the compiler takes the String of a specification's value it builds text
// from (a joined title): the walk is metered and its text charged as the
// engine's walks are (ADR 0018).
func (c *compileCtx) walker() jsval.Walker {
	if c == nil {
		return jsval.Walker{}
	}
	return jsval.WalkerFor(c.ctx)
}

// warn logs a warning as upstream's log.warn does: the caller's Options.Warn
// decides whether the compilation goes on.
func (c *compileCtx) warn(msg string) {
	if c == nil || c.warnFn == nil {
		return
	}
	if err := c.warnFn(msg); err != nil {
		panic(cancelError{err})
	}
}

// modelStep is the work charged per child model in a loop over a model's
// children, and per selection where every selection compiler runs on it:
// each step runs a phase of the child or a fixed table of compilers, tens of
// microseconds, so the deadline is polled every few dozen of them.
const modelStep = 32

// cancelError is the panic value Compile turns back into the context's error.
type cancelError struct{ err error }

// lazySignal makes a signal reference whose expression is computed on demand.
func lazySignal(c *compileCtx, fn func() string) Value {
	c.lazySeq++
	o := mk("signal", lazyMarker+strconv.FormatInt(c.lazySeq, 10))
	o.SetStringer(func(*Object) string { return lazyMarker + fn() })
	return jsval.Obj(o)
}

func lazyFn(o *Object) (func() string, bool) {
	s := o.Lookup("signal")
	if !s.IsStr() || !strings.HasPrefix(s.StrValue(), lazyMarker) {
		return nil, false
	}
	v := jsval.Obj(o)
	if !strings.HasPrefix(v.AsString(), lazyMarker) {
		return nil, false
	}
	return func() string { return strings.TrimPrefix(v.AsString(), lazyMarker) }, true
}

// signalOf reads ref.signal, evaluating a lazy signal.
func signalOf(v Value) string {
	if v.IsNullish() { // ref.signal of no ref
		throw("Cannot read properties of %s (reading 'signal')", nullishName(v))
	}
	if v.IsObj() {
		if fn, ok := lazyFn(v.ObjValue()); ok {
			return fn()
		}
	}
	return v.Get("signal").AsString()
}

// resolveLazy replaces every lazy signal in v with its evaluated expression.
// The compiled specification may share objects (a config's or a
// definition's, reused by every mark that reads them), so it is a graph, not
// a tree, and a walk of the tree it stands for is exponential in the depth
// of sharing. The walk is made as a tree walk up to resolveLazyTreeVisits
// values, which an output's usual size stays far below and which costs no
// allocation; past them it starts again with a visited set, so each array and
// object is visited once. Resolving is idempotent (a resolved signal is no
// longer lazy), so the second walk only finishes the first.
func resolveLazy(cc *compileCtx, v Value) {
	left := resolveLazyTreeVisits
	if resolveLazyTree(cc, v, &left) {
		return
	}
	seen := map[jsval.InternKey]struct{}{}
	var walk func(v Value)
	walk = func(v Value) {
		cc.work(1)
		switch v.Kind() {
		case jsval.KindArr, jsval.KindObj:
			k := v.MapKey()
			if _, ok := seen[k]; ok {
				return
			}
			seen[k] = struct{}{}
		}
		switch v.Kind() {
		case jsval.KindArr:
			for _, it := range v.Items() {
				walk(it)
			}
		case jsval.KindObj:
			resolveLazyObject(v.ObjValue())
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				walk(o.ValueAt(i))
			}
		}
	}
	walk(v)
}

// resolveLazyTreeVisits bounds resolveLazy's tree walk (see resolveLazy).
const resolveLazyTreeVisits = 1 << 20

// resolveLazyTree walks v as a tree, resolving lazy signals, and reports
// false once it has visited *left values without finishing.
func resolveLazyTree(cc *compileCtx, v Value, left *int) bool {
	if *left <= 0 {
		return false
	}
	*left--
	cc.work(1)
	switch v.Kind() {
	case jsval.KindArr:
		for _, it := range v.Items() {
			if !resolveLazyTree(cc, it, left) {
				return false
			}
		}
	case jsval.KindObj:
		o := v.ObjValue()
		resolveLazyObject(o)
		for i := 0; i < o.Len(); i++ {
			if !resolveLazyTree(cc, o.ValueAt(i), left) {
				return false
			}
		}
	}
	return true
}

// resolveLazyObject evaluates o's expression when it is a lazy signal.
func resolveLazyObject(o *Object) {
	if fn, ok := lazyFn(o); ok {
		o.Set("signal", jsval.Str(fn()))
		o.SetStringer(nil)
	}
}
