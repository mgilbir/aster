package expr

import (
	"reflect"

	"github.com/mgilbir/aster/internal/jsval"
	ecmacompiler "github.com/mgilbir/goecma262/compiler"
)

// patternBytes estimates the bytes a compiled pattern holds, for the caches
// that keep patterns: a short source can compile to a program of hundreds of
// thousands of instructions ((a{1000}){190} is 190,572, about 32 MB).
func patternBytes(p *jsval.Pattern) int64 {
	n := int64(ecmacompiler.MaxProgramSize)
	if p.Re != nil {
		// goecma262 does not report the length of its program, so it is read
		// from the unexported field; should a new version rename it, the
		// estimate is the largest program goecma262 compiles, which keeps
		// every pattern out of the caches rather than under-counting it (and
		// TestPatternSize fails).
		if f := reflect.ValueOf(p.Re).Elem().FieldByName("code"); f.IsValid() && f.Kind() == reflect.Slice {
			n = int64(f.Cap())
		}
	}
	return patternBaseBytes + int64(len(p.Source)+len(p.Flags)) + n*instructionBytes
}

// instructionBytes is what one instruction of a goecma262 program holds once
// analysed: the instruction (about 120 bytes) and the per-instruction tables
// of its vm.Program, plus the classes and sets some carry. Measured on
// goecma262 v0.2.0 at 167 to 259 bytes an instruction; TestPatternSize keeps
// the estimate at or above the heap a compile really holds.
const instructionBytes = 256

// patternBaseBytes is what a pattern holds besides its instructions.
const patternBaseBytes = 1024
