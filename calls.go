package aster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/text"
)

// ContextOption gives one call a context of its own; see WithContext. It is a
// RenderOption, a PNGOption and a PDFOption, so every method takes it.
type ContextOption struct{ ctx context.Context }

// WithContext makes one call run under ctx, as well as under the Converter's
// WithTimeout and Close: the call ends when the first of them does. When ctx
// is canceled the call stops where it is, frees what it holds and fails with
// an error wrapping context.Canceled (or ctx's own error: its deadline's
// context.DeadlineExceeded); the Converter and its other calls go on. A server
// passes the request's context, so that a client that goes away stops its
// render. The call's Loader methods are given a context derived from ctx, so
// they see its values and its deadline. A nil ctx is context.Background.
func WithContext(ctx context.Context) ContextOption { return ContextOption{ctx} }

func (o ContextOption) applyRender(c *renderConfig) { c.ctx = o.ctx }
func (o ContextOption) applyPNG(c *pngConfig)       { o.applyRender(&c.render) }
func (o ContextOption) applyPDF(c *pdfConfig)       { o.applyRender(&c.render) }

// lifecycle counts a Converter's calls in flight, so that Close waits for
// them before it closes the Loader, and knows which goroutines are running a
// callback of a call (an error handler, a Loader method), from which Close
// must not wait: the call it would wait for is the one it is part of (C37).
// It holds no lock while a call runs, so a call may start another, and Close
// may be called from anywhere, without deadlock.
type lifecycle struct {
	mu     sync.Mutex
	n      int  // calls in flight
	closed bool // Close was called
	// idle is made by the first Close and closed once no call is in flight
	// and the Loader is closed.
	idle chan struct{}
	// inside counts, by goroutine, the callbacks of calls it is running.
	inside map[uint64]int

	loaderOnce sync.Once
	loaderErr  error
	errTaken   bool // a Close has returned loaderErr
}

// enter registers a call; it reports false once the Converter is closed.
func (c *Converter) enter() (release func(), ok bool) {
	l := &c.life
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, false
	}
	l.n++
	return c.release, true
}

// release ends a call. The last call to end after Close closes the Loader:
// Close may have returned without waiting, when it was called from a callback.
func (c *Converter) release() {
	l := &c.life
	l.mu.Lock()
	l.n--
	last := l.closed && l.n == 0
	l.mu.Unlock()
	if last {
		// idle is closed whatever the Loader's Close does, so that no Close
		// waits for it forever (C17).
		defer close(l.idle)
		c.closeLoader()
	}
}

// closeLoader closes the Loader, once, if it is an io.Closer: as a callback,
// so that a panic in it is its error and a Close it calls does not wait for
// itself.
func (c *Converter) closeLoader() {
	c.life.loaderOnce.Do(func() {
		if closer, ok := c.cfg.loader.(io.Closer); ok {
			c.life.loaderErr = c.callback("the Loader's Close", closer.Close)
		}
	})
}

// Close releases all resources held by the Converter. It is safe to call
// multiple times, concurrently with rendering calls, and from inside one: it
// cancels the calls in flight (they fail with context.Canceled), waits for
// them to finish, closes the Loader if it implements io.Closer, and returns
// the Loader's Close error; every later call fails with ErrClosed.
//
// Called from inside a call, from its error handler or one of its Loader
// methods, Close cannot wait for the call it is part of: it cancels every
// call, makes later calls fail, and returns nil at once; the Loader is closed
// when the last call in flight ends, and a later Close from outside the calls
// waits for that and returns its error.
func (c *Converter) Close() error {
	if c == nil || c.cfg == nil {
		return nil // not made by New: it holds nothing
	}
	l := &c.life
	g := goid()
	l.mu.Lock()
	first := !l.closed
	if first {
		l.closed = true
		l.idle = make(chan struct{})
	}
	none := first && l.n == 0
	idle := l.idle
	inside := l.inside[g] > 0
	l.mu.Unlock()
	if c.cancelAll != nil {
		c.cancelAll()
	}
	if none {
		func() {
			defer close(idle)
			c.closeLoader()
		}()
	}
	if inside {
		return nil
	}
	<-idle
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.errTaken {
		return nil // only the first Close reports it
	}
	l.errTaken = true
	return l.loaderErr
}

// callback runs f, a call into the caller's code: a Loader method (Sanitize,
// Load, Close), the error handler, a WithSignal value's MarshalJSON. It is the
// one boundary every such call goes through, whatever goroutine the engine
// runs it on (the image fetches of PNG and PDF output have their own), and it
// gives them one contract:
//
//   - the goroutine is marked as inside a callback, so that a Close f makes
//     does not wait for the call it is part of (see lifecycle);
//   - a panic in f is recovered and is f's error, a *callbackPanic naming
//     what panicked (C15, C17): a bug in the caller's code fails what it was
//     asked for, as an error it returned would, and never the process.
//
// A custom projection's functions run on the calling goroutine, which call
// marks for the whole call, under the render's own recover: they are too hot
// to go through this one by one.
func (c *Converter) callback(what string, f func() error) (err error) {
	defer c.markInside()()
	defer func() {
		if r := recover(); r != nil {
			err = &callbackPanic{what: what, value: r}
		}
	}()
	return f()
}

// markInside marks the running goroutine as running a callback of a call,
// for Close, until the function it returns is called.
func (c *Converter) markInside() (unmark func()) {
	l := &c.life
	g := goid()
	l.mu.Lock()
	if l.inside == nil {
		l.inside = map[uint64]int{}
	}
	l.inside[g]++
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		if l.inside[g]--; l.inside[g] == 0 {
			delete(l.inside, g)
		}
		l.mu.Unlock()
	}
}

// callbackPanic is the error a callback's panic becomes (see callback).
type callbackPanic struct {
	what  string // "the Loader's Load"
	value any    // what it panicked with
}

func (e *callbackPanic) Error() string { return fmt.Sprintf("aster: %s panicked: %v", e.what, e.value) }

// Unwrap is the value the callback panicked with, when it is an error.
func (e *callbackPanic) Unwrap() error {
	err, _ := e.value.(error)
	return err
}

// goid is the running goroutine's id, read from its stack trace's header
// ("goroutine 18 [running]:"). Close needs to know whether it runs inside a
// callback of a call, and Go has no other way to tell one goroutine from
// another; it costs about a microsecond, once per callback.
func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	const prefix = "goroutine "
	if len(b) <= len(prefix) {
		return 0
	}
	b = b[len(prefix):]
	n := 0
	for n < len(b) && b[n] >= '0' && b[n] <= '9' {
		n++
	}
	id, _ := strconv.ParseUint(string(b[:n]), 10, 64)
	return id
}

// trackedLoader is the Converter's Loader as the engine calls it: each
// method runs as a callback (see lifecycle).
type trackedLoader struct {
	c *Converter
	l Loader
}

func (t trackedLoader) Sanitize(ctx context.Context, uri string) (s string, err error) {
	err = t.c.callback("the Loader's Sanitize", func() (err error) {
		s, err = t.l.Sanitize(ctx, uri)
		return err
	})
	return s, err
}

func (t trackedLoader) Load(ctx context.Context, uri string) (b []byte, err error) {
	err = t.c.callback("the Loader's Load", func() (err error) {
		b, err = t.l.Load(ctx, uri)
		return err
	})
	return b, err
}

// errNotNew is what the methods of a Converter that New did not make return:
// its zero value, or a nil pointer.
var errNotNew = errors.New("aster: the Converter was not made by New")

// errTimeout is the cause of a call's context when WithTimeout ends it, which
// tells it from the deadline of a context the caller gave (WithContext).
var errTimeout = errors.New("aster: the converter's WithTimeout")

// opContext bounds one public call — every stage of it together — by the
// converter's timeout, by Close, and by the caller's context when there is
// one (WithContext). The context carries the call's ledger (internal/budget),
// which every stage charges the work and memory the input makes it spend;
// the call's shaping budget, so its text is shaped under the same timeout
// and within one allowance of work; its report (see callReport); and the
// resources it has resolved (see callImages).
func (c *Converter) opContext(parent context.Context) (context.Context, context.CancelFunc) {
	var ctx context.Context
	var cancel context.CancelFunc
	if parent == nil {
		ctx, cancel = context.WithCancel(c.base)
	} else {
		// The caller's context is the parent, so that its values reach the
		// Loader and its end ends the call; Close ends it as well.
		var cancelCall context.CancelFunc
		ctx, cancelCall = context.WithCancel(parent)
		stop := context.AfterFunc(c.base, cancelCall)
		cancel = func() { stop(); cancelCall() }
	}
	if c.cfg.timeout > 0 {
		var cancelT context.CancelFunc
		ctx, cancelT = context.WithTimeoutCause(ctx, c.cfg.timeout, errTimeout)
		outer := cancel
		cancel = func() { cancelT(); outer() }
	}
	ctx = budget.WithLedger(ctx, budget.NewLedger(ctx, c.lim.ledger))
	ctx = text.WithShapingBudget(ctx, text.NewShapingBudget(ctx, c.lim.shaping))
	ctx = context.WithValue(ctx, callReportKey{}, &callReport{c: c})
	return context.WithValue(ctx, callImagesKey{}, c.newCallImages()), cancel
}

// stageErr wraps a stage's error. When the call's context has ended, the
// error wraps the context's error as well, named once: a call past its
// WithTimeout says so, and one past the deadline of the caller's context
// (WithContext) says that, so a caller can tell a slow chart from a broken
// one (X9: the deadline was wrapped in front of an error whose text already
// said it, and the message said it twice).
func (c *Converter) stageErr(ctx context.Context, stage string, err error) error {
	cerr := ctx.Err()
	switch {
	case cerr == nil:
		return fmt.Errorf("aster: %s: %w", stage, err)
	case errors.Is(cerr, context.DeadlineExceeded):
		msg := "aster: " + stage + " ran past the deadline of the call's context"
		if context.Cause(ctx) == errTimeout {
			msg = fmt.Sprintf("aster: %s timed out after %v", stage, c.cfg.timeout)
		}
		return &ctxError{msg: msg, err: err, cause: cerr}
	case errors.Is(err, cerr):
		return fmt.Errorf("aster: %s: %w", stage, err)
	default:
		return &ctxError{msg: "aster: " + stage + " canceled", err: err, cause: cerr}
	}
}

// ctxError is a stage's error in a call whose context ended: it wraps both
// the error and the context's, whose end its message names once, whatever
// the error's own text says.
type ctxError struct {
	msg   string
	err   error
	cause error // context.Canceled or context.DeadlineExceeded
}

func (e *ctxError) Error() string   { return e.msg + ": " + e.err.Error() }
func (e *ctxError) Unwrap() []error { return []error{e.err, e.cause} }

// call runs f as one public call: registered with the lifecycle, under the
// call's context (opContext), with rc's signals and error handler. A call
// whose context ended returns no output, even when its last stage finished
// regardless.
func call[T any](c *Converter, rc renderConfig, f func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	if c == nil || c.cfg == nil {
		return zero, errNotNew
	}
	release, ok := c.enter()
	if !ok {
		return zero, ErrClosed
	}
	defer release()
	// The call's own goroutine runs only the engine and the callbacks it
	// makes (the error handler, a projection's functions, the Loader): a
	// Close from it is from inside the call, and must not wait for it.
	defer c.markInside()()
	ctx, cancel := c.opContext(rc.ctx)
	defer cancel()
	ctx, err := c.withRender(ctx, rc)
	if err != nil {
		return zero, err
	}
	out, err := f(ctx)
	if err == nil && ctx.Err() != nil {
		err = c.stageErr(ctx, "rendering", ctx.Err())
	}
	if err != nil {
		return zero, err
	}
	return out, nil
}

// svgInput is rc for a method whose input is SVG, which renders no
// specification: its signals are not set (see WithSignal).
func svgInput(rc renderConfig) renderConfig {
	rc.signals = nil
	return rc
}
