package text

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// stopOf runs f and returns the error of the *budget.Stop it panics with, or
// nil when it returns.
func stopOf(t *testing.T, f func()) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(*budget.Stop)
			if !ok {
				t.Fatalf("panicked with %T %v, want *budget.Stop", r, r)
			}
			err = s.Err
		}
	}()
	f()
	return nil
}

func TestBoundedShapesAsUnbounded(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := NewShapingBudget(context.Background(), ShapingLimits{})
	bm := m.Bounded(b)
	for _, s := range []string{"Hello, world", "Agé ́ ffi", "مرحبا بالعالم", "日本語 😀"} {
		runs, w := m.ShapeText(s, "12px sans-serif")
		bruns, bw := bm.ShapeText(s, "12px sans-serif")
		if w != bw || !reflect.DeepEqual(runs, bruns) {
			t.Errorf("%q: bounded shaping differs: %v %v, want %v %v", s, bw, bruns, w, runs)
		}
	}
	if b.Used() <= 0 {
		t.Errorf("shaping charged no work")
	}
}

func TestBoundedCacheHitIsFree(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.MeasureText("cached", "10px sans-serif")
	b := NewShapingBudget(context.Background(), ShapingLimits{Work: 1})
	if err := stopOf(t, func() { m.Bounded(b).MeasureText("cached", "10px sans-serif") }); err != nil {
		t.Fatalf("a cached width must not shape: %v", err)
	}
}

func TestBoundedWorkSpent(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := NewShapingBudget(context.Background(), ShapingLimits{Work: 1})
	err = stopOf(t, func() { m.Bounded(b).MeasureText("too much work", "10px sans-serif") })
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("got %v, want a limit error", err)
	}
	// Once spent, the next run is refused before forme is asked.
	err = stopOf(t, func() { m.Bounded(b).MeasureText("more", "10px sans-serif") })
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("got %v, want a limit error", err)
	}
}

func TestBoundedContextDone(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := NewShapingBudget(ctx, ShapingLimits{})
	err = stopOf(t, func() { m.Bounded(b).ShapeText("cancelled", "10px sans-serif") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestBoundedRunMemory(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := NewShapingBudget(context.Background(), ShapingLimits{RunBytes: 100 * glyphBytes})
	if err := stopOf(t, func() { m.Bounded(b).MeasureText(strings.Repeat("a", 50), "10px sans-serif") }); err != nil {
		t.Fatalf("50 glyphs fit in 100: %v", err)
	}
	err = stopOf(t, func() { m.Bounded(b).MeasureText(strings.Repeat("a", 500), "10px sans-serif") })
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("got %v, want a limit error", err)
	}
}

func TestBudgetInContext(t *testing.T) {
	if ShapingBudgetFrom(context.Background()) != nil {
		t.Fatal("no budget in a plain context")
	}
	b := NewShapingBudget(context.Background(), ShapingLimits{})
	if ShapingBudgetFrom(WithShapingBudget(context.Background(), b)) != b {
		t.Fatal("budget lost")
	}
	var m *Measurer
	if m.Bounded(b) != nil {
		t.Fatal("a nil measurer's view must be nil")
	}
}
