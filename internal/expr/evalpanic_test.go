package expr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

type panicEnv struct{ v any }

func (e panicEnv) Signal(string) (jsval.Value, bool) { return jsval.Undefined, false }
func (e panicEnv) Data(string) (jsval.Value, bool)   { panic(e.v) }

type ownPanic struct{ err error }

// Eval recovers only what it raises (and a *budget.Stop, which is a limit or
// the deadline): a runtime callback's own panic reaches the caller's recover,
// which knows what it means, instead of becoming an "internal error" that the
// dataflow would log and carry on past (X11, C43).
func TestEvalLeavesForeignPanics(t *testing.T) {
	p, err := Compile("data('t')")
	if err != nil {
		t.Fatal(err)
	}
	stop := &budget.Stop{Err: fmt.Errorf("%w: rows", budget.ErrLimit)}
	if _, err := p.Eval(NewScope(panicEnv{stop})); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("a *budget.Stop: %v, want ErrLimit", err)
	}
	own := &ownPanic{errors.New("operator failed")}
	got := func() (r any) {
		defer func() { r = recover() }()
		p.Eval(NewScope(panicEnv{own}))
		return nil
	}()
	if got != own {
		t.Errorf("a callback's own panic: recovered %v, want it raised again", got)
	}
}
