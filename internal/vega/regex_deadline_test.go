package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// A call that ends while an expression's regular expression literal waits to
// be compiled reports the call's error, not a parse error of the expression:
// the literal's compile polls the deadline before it starts.
func TestRegexLiteralStopsAtCancellation(t *testing.T) {
	spec, err := jsval.ParseJSONString(`{"signals": [{"name": "s", "update": "test(/(a{1000}){150}/, 'a')"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = budget.WithLedger(ctx, budget.NewLedger(ctx, budget.LedgerLimits{}))
	if _, err := Render(ctx, spec, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
