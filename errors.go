package aster

import (
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
)

// ErrLimit is wrapped by every error that reports a specification exceeding
// one of the engine's resource limits: data rows, loaded bytes, scene items,
// ticks and legend entries, nesting depth, expression size, the SVG, PNG and
// PDF output, and the other bounds that keep a hostile specification from
// exhausting memory or time. Upstream Vega has no such limits, so an input
// that hits one is one the engine refuses rather than one it renders wrongly:
//
//	if errors.Is(err, aster.ErrLimit) {
//	    // the specification asks for more than this converter allows
//	}
//
// WithMemoryLimit sets the memory-related limits (docs/limits.md lists them
// all). A render that runs out of time (WithTimeout) is reported with
// context.DeadlineExceeded instead, and one interrupted by Close with
// context.Canceled.
var ErrLimit = budget.ErrLimit

// ErrClosed is returned by every rendering method of a Converter after Close.
var ErrClosed = errors.New("aster: converter is closed")

// LoadError reports a resource a render needed and could not get: data a
// specification names by URL, or an image it draws. The Loader refused it or
// failed to load it, or it loaded but could not be read (data that does not
// parse in its format; an image that is not a PNG, JPEG or GIF).
//
// Upstream Vega logs such a failure and renders without the resource, as a
// browser draws a broken image. By default a render instead fails with the
// LoadError (wrapped), so that a chart is never silently missing its data:
//
//	var le *aster.LoadError
//	if errors.As(err, &le) {
//	    log.Printf("%s %s: %v", le.Resource, le.URL, le.Err)
//	}
//
// WithErrorHandler is told of each one instead, and can let the render go on.
type LoadError struct {
	// Resource is "data" or "image".
	Resource string
	// URL is the resource's URL as the specification (or the SVG) names it;
	// "" for a data set whose url is not a string.
	URL string
	// Err is the Loader's error, or the reason the bytes could not be read.
	Err error
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("%s %q could not be loaded: %v", e.Resource, e.URL, e.Err)
}

func (e *LoadError) Unwrap() error { return e.Err }

// Warning is a message Vega logs as a warning ("Infinite extent for field
// ...", "Unsupported scale property: ...", an expression's warn()), one the
// Vega-Lite compiler logs, or a link the Loader refused and the output
// therefore lacks. A warning never fails a
// render by itself; it is only seen through WithErrorHandler, its text cut
// to 1 KiB, a Vega warning once per distinct text and call (see
// docs/errors.md).
type Warning string

func (w Warning) Error() string { return string(w) }
