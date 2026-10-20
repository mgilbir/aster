package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// TestSchemeSamplesPastTheBoundFail is C100: an interpolating scheme sampled
// for more colours than the engine keeps fails with a limit error; it was an
// empty colour range, silently. A fractional count for an ordinal scale is
// upstream's RangeError (new Array(2.5)).
func TestSchemeSamplesPastTheBoundFail(t *testing.T) {
	spec := func(count string) string {
		return `{"width":10,"height":10,"scales":[{"name":"c","type":"ordinal","domain":["a","b"],
			"range":{"scheme":"viridis","count":` + count + `}}],
			"marks":[{"type":"rect","encode":{"enter":{"fill":{"scale":"c","value":"a"}}}}]}`
	}
	if _, err := renderJSON(t, context.Background(), spec("2e6")); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("schemeCount 2e6: %v; want a limit error", err)
	}
	// A count past 2^32 - 1, like a fractional one, is upstream's
	// RangeError (new Array(1e300)), not the engine's bound.
	for _, count := range []string{"2.5", "1e300"} {
		var reported error
		v, err := jsval.ParseJSONString(spec(count))
		if err != nil {
			t.Fatal(err)
		}
		_, err = Render(context.Background(), v, Options{Report: func(e error) error { reported = e; return e }})
		if err == nil || reported == nil || errors.Is(err, budget.ErrLimit) {
			t.Errorf("schemeCount %s: %v (reported %v); want upstream's Invalid array length", count, err, reported)
		}
	}
	if _, err := renderJSON(t, context.Background(), spec("7")); err != nil {
		t.Errorf("schemeCount 7: %v", err)
	}
}
