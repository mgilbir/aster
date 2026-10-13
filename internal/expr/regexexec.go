package expr

import (
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	ecma262 "github.com/mgilbir/goecma262"
	"github.com/mgilbir/goecma262/vm"
)

// matcher is p's Regexp as the call runs a search of n bytes with it: one
// that stops once the call's context is done, and holds for backtracking at
// most what the call's ledger has left (jsval.MatchMemory). It is a copy of
// the pattern's shared Regexp made with WithContext, so the compiled
// program, shared through the pattern cache by every goroutine, is never
// changed. The Scope keeps the copy for the next search with the same
// program, so a filter testing every datum makes one (a g literal's fresh
// object each evaluation included); its context is the Scope's own,
// whatever Context is then.
// The copy's lastIndex is not used: the searches here start where they are
// told (FindStringSubmatchPositionsFromErr) or at 0, and the g and y flags'
// lastIndex stays in p.Re. Without a context it is p.Re.
func (s *Scope) matcher(p *jsval.Pattern, n int) *ecma262.Regexp {
	if s.Context == nil {
		return p.Re
	}
	if base := p.Shared(); s.reOf != base {
		s.re, s.reOf = base.WithContext(scopeContext{s}), base
	}
	s.re.SetMaxMemory(jsval.MatchMemory(budget.LedgerFrom(s.Context).Room(), n))
	return s.re
}

// scopeContext is the Context of a Scope, read when it is used, so that a
// Regexp the Scope keeps follows the call it evaluates for.
type scopeContext struct{ s *Scope }

func (c scopeContext) Deadline() (time.Time, bool) { return c.s.Context.Deadline() }
func (c scopeContext) Done() <-chan struct{}       { return c.s.Context.Done() }
func (c scopeContext) Err() error                  { return c.s.Context.Err() }
func (c scopeContext) Value(key any) any           { return c.s.Context.Value(key) }

// slicePos is str[a:b] for positions that may fall between the halves of a
// pair (a match without the u flag can begin or end there): the half the
// slice holds is kept, as three WTF-8 bytes.
func slicePos(str string, a, b int) string {
	if a >= b {
		return ""
	}
	amid, bmid := vm.BetweenSurrogates(str, a), vm.BetweenSurrogates(str, b)
	if !amid && !bmid {
		return str[a:b]
	}
	out := make([]byte, 0, b-a+6)
	if amid {
		_, lo := pairHalves(str, a-2)
		out = appendUnit(out, lo)
		a += 2
	}
	if bmid {
		out = append(out, str[a:b-2]...)
		hi, _ := pairHalves(str, b-2)
		return string(appendUnit(out, hi))
	}
	return string(append(out, str[a:b]...))
}
