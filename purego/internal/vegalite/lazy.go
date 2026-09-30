package vegalite

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Some signal references must be evaluated after the whole tree is parsed,
// because signal names get renamed while child layout sizes merge into their
// parents (upstream's SignalRefWrapper defines `signal` as a getter). A lazy
// signal is an ordinary {signal: "\x00lazy:N"} object whose real expression is
// produced by a registered function; resolveLazy rewrites them in the finished
// output, and deepEqual/signalOf see through them in the meantime.

const lazyMarker = "\x00lazy:"

var (
	lazyFuncs sync.Map // *Object -> func() string
	lazySeq   atomic.Int64
)

// compileCtx is the per-compilation state shared by every model of one tree.
type compileCtx struct {
	lazies []*Object
	// loc is the time zone of local-time datetimes (Options.Location).
	loc *time.Location
}

// release forgets every lazy signal registered during the compilation.
func (c *compileCtx) release() {
	for _, o := range c.lazies {
		lazyFuncs.Delete(o)
	}
	c.lazies = nil
}

// lazySignal makes a signal reference whose expression is computed on demand.
func lazySignal(c *compileCtx, fn func() string) Value {
	o := mk("signal", lazyMarker+strconv.FormatInt(lazySeq.Add(1), 10))
	lazyFuncs.Store(o, fn)
	c.lazies = append(c.lazies, o)
	return jsval.Obj(o)
}

func lazyFn(o *Object) (func() string, bool) {
	s := o.Lookup("signal")
	if !s.IsStr() || !strings.HasPrefix(s.StrValue(), lazyMarker) {
		return nil, false
	}
	f, ok := lazyFuncs.Load(o)
	if !ok {
		return nil, false
	}
	return f.(func() string), true
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

// resolveLazy replaces every lazy signal in v with its evaluated expression
// and forgets the registrations it consumed.
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
			lazyFuncs.Delete(o)
		}
		for i := 0; i < o.Len(); i++ {
			resolveLazy(o.ValueAt(i))
		}
	}
}
