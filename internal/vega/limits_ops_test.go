package vega

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// Every operator counts against MaxOperators, whether the parse produces it
// (an entry per signal) or a facet cell instantiates it later.
func TestOperatorLimit(t *testing.T) {
	signals := make([]string, 200)
	for i := range signals {
		signals[i] = fmt.Sprintf(`{"name":"s%d","value":1}`, i)
	}
	for name, spec := range map[string]string{
		"signals": `{"signals":[` + strings.Join(signals, ",") + `]}`,
		"facet cells": `{"data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":300,"as":"x"}]}],
		  "marks":[{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"x"}},
		    "marks":[{"type":"rect","from":{"data":"f"}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			opts := Options{Limits: Limits{MaxOperators: 100}}
			_, err := Render(context.Background(), mustJSON(t, spec), opts)
			if !errors.Is(err, budget.ErrLimit) {
				t.Errorf("err = %v, want the limit error", err)
			}
		})
	}
}
