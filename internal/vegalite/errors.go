package vegalite

import (
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
)

// compileError is the value thrown (as a panic) where upstream throws an
// Error. Compile recovers it and returns the message as an ordinary error, so
// nothing reachable from a specification escapes as a panic.
type compileError struct{ msg string }

func (e compileError) Error() string { return e.msg }

func throw(format string, args ...any) {
	panic(compileError{fmt.Sprintf(format, args...)})
}

// limitError is the value thrown where a specification exceeds one of the
// compiler's own limits, which upstream does not have. Compile returns it as
// an error wrapping budget.ErrLimit.
type limitError struct{ msg string }

func (e limitError) Error() string { return e.msg }
func (e limitError) Unwrap() error { return budget.ErrLimit }

func exceeded(format string, args ...any) {
	panic(limitError{fmt.Sprintf(format, args...)})
}

// depthMsg reports a specification that nests composition operators (layer,
// concat, facet, repeat) deeper than maxDepth.
const depthMsg = "vegalite: specification is nested too deeply"

// maxDepth bounds the nesting of composite specifications, far beyond any
// real chart.
const maxDepth = 64

// maxTransforms bounds the data transforms of one specification. Each becomes
// a node in the dataflow tree, in a chain with the transforms before it, and
// the optimizer and the assembler walk that tree recursively: the transform
// count is the recursion depth. Real charts have a few dozen; 10000 keeps the
// walk to a stack of a few MB.
const maxTransforms = 10000

// maxRepeatChildren bounds the views a repeat may expand into (the product of
// its row, column and repeat lists); real charts use a few dozen.
const maxRepeatChildren = 10000
