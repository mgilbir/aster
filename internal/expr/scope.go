package expr

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// Error is a JavaScript exception raised while evaluating an expression, such
// as reading a property of null (TypeError) or building an invalid regular
// expression (SyntaxError).
type Error struct {
	Name string // "TypeError", "RangeError", "SyntaxError", "Error"
	Msg  string
}

func (e *Error) Error() string { return e.Name + ": " + e.Msg }

func throw(name, format string, args ...any) {
	panic(&Error{Name: name, Msg: fmt.Sprintf(format, args...)})
}

func typeError(format string, args ...any) { throw("TypeError", format, args...) }

// Random is the source behind random(), sampleNormal and friends. Vega
// exposes vega-statistics' setRandom for reproducible output; here the source
// is set per Scope.
type Random struct {
	// Source returns uniform values in [0, 1). Nil uses math/rand/v2.
	Source func() float64
	// spare holds the second value of a Box-Muller pair (upstream's
	// module-level nextSample); NaN when empty.
	spare    float64
	hasSpare bool
}

// NewLCG returns a Source that is vega-statistics' `lcg(seed)`: the glibc
// linear congruential generator, so a seeded run matches Vega's seeded run.
func NewLCG(seed float64) func() float64 {
	return func() float64 {
		seed = math.Mod(float64(1103515245*seed)+12345, 2147483647) // no FMA: the product rounds first, as in JS
		return seed / 2147483647
	}
}

func (r *Random) next() float64 {
	if r == nil || r.Source == nil {
		return rand.Float64()
	}
	return r.Source()
}

// Scope is the mutable context one expression evaluates in: the datum, event
// and item variables of the generated function, the runtime Env, and the
// ambient services (clock, local time zone, random source, cancellation).
//
// A Scope is not safe for concurrent use; a compiled Program is, given one
// Scope per goroutine. Reuse one Scope across many evaluations (setting Datum
// between them) to avoid allocation.
type Scope struct {
	// Datum, Event and Item are the `datum`, `event` and `item` variables.
	// Unset ones read as undefined.
	Datum, Event, Item jsval.Value
	// NoDatum, NoEvent and NoItem mark the variables the compiled function has
	// no parameter for, which JavaScript reports as ReferenceErrors when the
	// expression reads them: operator updates take none of the three, handlers
	// only event and datum, parameter expressions datum, encoders item and
	// datum.
	NoDatum, NoEvent, NoItem bool

	// Locale supplies number and time formatting (format, timeFormat, ...) and
	// the local time zone of the date functions (date, year, hours, datetime,
	// ...): the view's locale. Nil means format.DefaultLocale(), en-US with UTC
	// as the local zone, which keeps rendering independent of the host.
	Locale *format.Locale
	// Now supplies the current time in epoch milliseconds for now() and
	// datetime(). Nil uses the system clock.
	Now func() float64
	// Rand supplies randomness. Nil uses the global generator.
	Rand *Random
	// Context is checked periodically inside long loops (sequence, pad,
	// sort). Nil means never cancelled.
	Context context.Context
	// Strings is the render-wide budget for the large strings expressions
	// build; nil charges nothing (the per-string limit still applies).
	Strings *StringBudget

	env Env
	// capability views of env, resolved once in SetEnv
	data    DataProvider
	indata  InDataProvider
	units   UnitIndexProvider
	writer  DataWriter
	scales  ScaleProvider
	geo     GeoProvider
	tree    TreeProvider
	view    ViewProvider
	logger  Logger
	tuples  TupleChecker
	dflt    *format.Locale
	stack   []jsval.Value
	checked int
}

// NewScope makes a Scope bound to env (which may be nil).
func NewScope(env Env) *Scope {
	s := &Scope{}
	s.SetEnv(env)
	return s
}

// SetEnv binds the runtime environment and resolves its optional capabilities.
func (s *Scope) SetEnv(env Env) {
	s.env = env
	s.data, _ = env.(DataProvider)
	s.indata, _ = env.(InDataProvider)
	s.units, _ = env.(UnitIndexProvider)
	s.writer, _ = env.(DataWriter)
	s.scales, _ = env.(ScaleProvider)
	s.geo, _ = env.(GeoProvider)
	s.tree, _ = env.(TreeProvider)
	s.view, _ = env.(ViewProvider)
	s.logger, _ = env.(Logger)
	s.tuples, _ = env.(TupleChecker)
}

// Env returns the bound environment.
func (s *Scope) Env() Env { return s.env }

func (s *Scope) locale() *format.Locale {
	if s.Locale != nil {
		return s.Locale
	}
	if s.dflt == nil {
		s.dflt = format.DefaultLocale() // built once so its format caches persist
	}
	return s.dflt
}

// zone is the local calendar of the timeXxx functions.
func (s *Scope) zone() format.Zone { return s.locale().Local }

func (s *Scope) nowMs() float64 {
	if s.Now != nil {
		return s.Now()
	}
	return float64(time.Now().UnixMilli())
}

// tick is called once per iteration of loops that can run long; it consults
// the context every 1024 calls.
func (s *Scope) tick() {
	if s.Context == nil {
		return
	}
	s.checked++
	if s.checked&1023 != 0 {
		return
	}
	if err := s.Context.Err(); err != nil {
		panic(&cancelled{err})
	}
}

type cancelled struct{ err error }

// StringBudget bounds the total size of the large strings one render's
// expressions build. Strings up to smallString bytes are free (a chart makes
// millions of labels); a larger one is charged when it is made, so the memory
// an expression chain can hold is bounded however many signals build on each
// other.
type StringBudget struct {
	left int64
}

// smallString is the size up to which a string is not charged.
const smallString = 4096

// NewStringBudget makes a budget of n bytes; n <= 0 is unlimited.
func NewStringBudget(n int64) *StringBudget {
	if n <= 0 {
		return &StringBudget{left: math.MaxInt64}
	}
	return &StringBudget{left: n}
}

// checkLen throws the RangeError V8 throws for a string that is too long, and
// charges the budget for a large one. n is the byte length about to be built;
// call it before the allocation.
func (s *Scope) checkLen(n int) {
	if n > MaxStringLength {
		throw("RangeError", "Invalid string length")
	}
	s.chargeBytes(n)
}

// chargeItems charges the budget for an array of n elements an expression is
// about to build (sequence and friends).
func (s *Scope) chargeItems(n int) {
	const perItem = 24
	if n > 256 {
		s.chargeBytes(n * perItem)
	}
}

func (s *Scope) chargeBytes(n int) {
	if n <= smallString || s.Strings == nil {
		return
	}
	if int64(n) > s.Strings.left {
		throw("RangeError", "Invalid string length: the expression string budget of this render is used up")
	}
	s.Strings.left -= int64(n)
}
