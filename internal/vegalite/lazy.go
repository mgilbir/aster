package vegalite

import (
	"context"
	"strconv"
	"strings"
	"time"

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
	// lazySeq numbers the lazy signals; outputSeq the output nodes' hash keys.
	lazySeq, outputSeq int64
	// sigIdx speeds up signal lookups by name (see signalIndex).
	sigIdx signalIndex
	// transforms counts the data transforms parsed so far (see maxTransforms).
	transforms int
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
	if v.IsObj() {
		if fn, ok := lazyFn(v.ObjValue()); ok {
			return fn()
		}
	}
	return v.Get("signal").AsString()
}

// resolveLazy replaces every lazy signal in v with its evaluated expression.
func resolveLazy(v Value) {
	switch v.Kind() {
	case jsval.KindArr:
		for _, it := range v.Items() {
			resolveLazy(it)
		}
	case jsval.KindObj:
		o := v.ObjValue()
		if fn, ok := lazyFn(o); ok {
			o.Set("signal", jsval.Str(fn()))
			o.SetStringer(nil)
		}
		for i := 0; i < o.Len(); i++ {
			resolveLazy(o.ValueAt(i))
		}
	}
}
