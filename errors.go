package aster

import "github.com/mgilbir/aster/internal/budget"

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
// WithMemoryLimit tightens the memory-related limits. A render that runs out
// of time (WithTimeout) is reported with context.DeadlineExceeded instead,
// and one interrupted by Close with context.Canceled.
var ErrLimit = budget.ErrLimit
