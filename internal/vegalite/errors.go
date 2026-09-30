package vegalite

import (
	"errors"
	"fmt"
)

// compileError is the value thrown (as a panic) where upstream throws an
// Error. Compile recovers it and returns the message as an ordinary error, so
// nothing reachable from a specification escapes as a panic.
type compileError struct{ msg string }

func (e compileError) Error() string { return e.msg }

func throw(format string, args ...any) {
	panic(compileError{fmt.Sprintf(format, args...)})
}

// errDepth is returned when a specification nests composition operators
// (layer, concat, facet, repeat) deeper than maxDepth.
var errDepth = errors.New("vegalite: specification is nested too deeply")

// maxDepth bounds the nesting of composite specifications, far beyond any
// real chart.
const maxDepth = 64

// maxRepeatChildren bounds the views a repeat may expand into (the product of
// its row, column and repeat lists); real charts use a few dozen.
const maxRepeatChildren = 10000
