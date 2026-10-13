package expr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	ecma262 "github.com/mgilbir/goecma262"
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

// limitPanic carries an engine resource limit out of an evaluation. Unlike an
// Error it is no exception upstream would throw (upstream has no such limits):
// Eval returns it as an error wrapping budget.ErrLimit, which ends the render.
type limitPanic struct{ err error }

// exceeded ends the evaluation with a resource-limit error.
func exceeded(format string, args ...any) {
	panic(&limitPanic{fmt.Errorf("%w: "+format, append([]any{budget.ErrLimit}, args...)...)})
}

// failFormat raises a formatting error: a resource limit as such, anything
// else as the Error upstream's formatter throws.
func failFormat(err error) {
	if errors.Is(err, budget.ErrLimit) {
		panic(&limitPanic{err})
	}
	throw("Error", "%v", err)
}

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
	// Context is the call the evaluation belongs to. Evaluation work is
	// charged to the budget.Ledger it carries (or the context polled, when it
	// has none), so a timeout or a cancellation ends a long loop; the regular
	// expressions an evaluation compiles are charged to the ledger's bytes.
	// Nil means never cancelled and nothing charged.
	Context context.Context
	// Strings is the render-wide budget for the large strings expressions
	// build; nil charges nothing (the per-string limit still applies).
	Strings *StringBudget

	env Env
	// capability views of env, resolved once in SetEnv
	data   DataProvider
	indata InDataProvider
	units  UnitIndexProvider
	writer DataWriter
	scales ScaleProvider
	geo    GeoProvider
	tree   TreeProvider
	view   ViewProvider
	logger Logger
	tuples TupleChecker
	dflt   *format.Locale
	stack  []jsval.Value
	// pending is the work charged since the last flush to the ledger.
	pending int64
	// re is the copy of reOf that matcher last made.
	re, reOf *ecma262.Regexp
	// searchSteps counts the comparison steps of the Knuth-Morris-Pratt
	// searches (searchFirst, searchLast and their tables). Nothing reads it
	// but the tests, which check that a search is linear by counting its
	// steps at n and 2n instead of timing it.
	searchSteps int64
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

// workFlush is how much work an evaluation charges locally before it reports
// it to the call's ledger, which polls the context: a unit is one step of a
// loop (an element, a character, 64 bytes scanned), tens of nanoseconds, so a
// flush comes every few microseconds of work at most.
const workFlush = 1 << 10

// work charges n units of evaluation work. Every loop whose trip count the
// input controls charges its steps here, and so does every call of a builtin
// (see compiler.call), so a timeout or a Close ends the evaluation wherever it
// is (ADR 0010).
func (s *Scope) work(n int) {
	if s.pending += int64(n); s.pending >= workFlush {
		s.flushWork()
	}
}

// flushWork reports the pending work to the ledger, which polls the context.
func (s *Scope) flushWork() {
	n := s.pending
	s.pending = 0
	if s.Context == nil {
		return
	}
	m := budget.NewMeter(s.Context)
	if err := m.Work(n); err != nil {
		panic(&cancelled{err})
	}
}

// workBytes charges a pass over n bytes of a string.
func (s *Scope) workBytes(n int) { s.work(1 + n>>6) }

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
const smallString = 512

// NewStringBudget makes a budget of n bytes; n <= 0 is unlimited.
func NewStringBudget(n int64) *StringBudget {
	if n <= 0 {
		return &StringBudget{left: math.MaxInt64}
	}
	return &StringBudget{left: n}
}

// room is how many more bytes the budget can pay for.
func (b *StringBudget) room() int64 {
	if b == nil {
		return math.MaxInt64
	}
	return b.left
}

// chargeBytes charges the budget for a string of n bytes about to be made;
// one of smallString bytes or less is free.
func (s *Scope) chargeBytes(n int) {
	if n > smallString {
		s.chargeRaw(int64(n))
	}
}

// chargeRaw charges n bytes whatever their size: the growth of a buffer
// whose string is already past smallString.
func (s *Scope) chargeRaw(n int64) {
	if s.Strings == nil || n <= 0 {
		return
	}
	if n > s.Strings.left {
		exceeded("the expression string budget of this render is used up")
	}
	s.Strings.left -= n
}
