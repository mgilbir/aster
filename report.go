package aster

import (
	"context"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/vega"
)

// callReport is what one call tells its error handler (WithErrorHandler) of,
// and decides with it whether the call goes on. Data and runtime errors are
// decided as they arise, on the calling goroutine; images, which are fetched
// and decoded inside a stage, are collected and decided once the stage is
// over (flushImages), also on the calling goroutine.
type callReport struct {
	c       *Converter        // whose calls the handler is a callback of
	handler func(error) error // nil: errors end the call, warnings are dropped

	mu     sync.Mutex
	images []*LoadError // image failures, the first for each URL, in order
	seen   map[string]bool
	// limit is the first limit an image reached: it ends the call however
	// the image was reached (C34), and no handler can let it go.
	limit error
}

type callReportKey struct{}

func reportFrom(ctx context.Context) *callReport {
	r, _ := ctx.Value(callReportKey{}).(*callReport)
	return r
}

// maxWarningText is the most bytes of a warning's text the handler hears:
// warnings carry text from the specification (a field name, a transform, an
// expression's arguments), from Vega, from the Vega-Lite compiler or from a
// refused link, and every one passes through decide (docs/limits.md,
// "warnings"). Vega cuts its own to the same length, with the same rule, so
// that it counts distinct warnings by what the handler will see.
const maxWarningText = 1024

// cutWarning is s cut to at most maxWarningText bytes, at a character
// boundary, with an ellipsis when anything was cut. It is internal/vega's
// cutText, and like it, cutting a cut text again changes nothing.
func cutWarning(s string) string {
	n := maxWarningText
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// decide is the handler's answer for err; without a handler a Warning goes on
// and anything else ends the call. A Warning reaches the handler cut to
// maxWarningText.
func (r *callReport) decide(err error) error {
	if w, ok := err.(Warning); ok {
		err = Warning(cutWarning(string(w)))
	}
	if r == nil || r.handler == nil {
		var w Warning
		if errors.As(err, &w) {
			return nil
		}
		return err
	}
	if r.c == nil {
		return r.handler(err)
	}
	var out error
	r.c.callback(func() { out = r.handler(err) })
	return out
}

// vega is vega.Options.Report: the dataflow's own types become the public
// ones before the handler sees them.
func (r *callReport) vega(err error) error {
	switch e := err.(type) {
	case *vega.LoadError:
		err = &LoadError{Resource: "data", URL: e.URL, Err: e.Err}
	case vega.Warning:
		err = Warning(e)
	}
	return r.decide(err)
}

// image records that the image at url could not be loaded or read. Only the
// first failure of a URL is kept. A limit is kept apart: the first one ends
// the call when the stage is over (flushImages), whatever stage reached it
// and whether or not the stage could return it itself (C34: the SVG writer,
// reading an image's size, could not, and the image vanished).
func (r *callReport) image(url string, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if errors.Is(err, ErrLimit) {
		if r.limit == nil {
			r.limit = err
		}
		return
	}
	if r.seen[url] {
		return
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[url] = true
	r.images = append(r.images, &LoadError{Resource: "image", URL: url, Err: err})
}

// warn tells the handler of a warning, which never ends the call unless the
// handler says so.
func (r *callReport) warn(msg string) error { return r.decide(Warning(msg)) }

// flushImages decides the image failures recorded since the last flush: a
// limit ends the call, and so does the first failure the handler does not
// let go (without a handler, the first).
func (r *callReport) flushImages() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	pending, limit := r.images, r.limit
	r.images = nil
	r.mu.Unlock()
	if limit != nil {
		return limit
	}
	for _, le := range pending {
		if err := r.decide(le); err != nil {
			return err
		}
	}
	return nil
}
