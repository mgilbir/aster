package vegalite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// complexitySpecs are families of specifications, each growing in one
// dimension that once drove a super-linear algorithm in the compiler. The
// output of each is upstream's; only the algorithm that produces it changed
// (ADR 0011), so the guard is the growth of the compile time.
func complexitySpecs() map[string]func(n int) Value {
	data := func() Value { return mkv("values", arr(mkv("a", 1, "b", 2))) }
	enc := func() Value {
		return mkv("x", mkv("field", "a", "type", "quantitative"), "y", mkv("field", "b", "type", "quantitative"))
	}
	list := func(n int, f func(i int) Value) Value {
		items := make([]Value, n)
		for i := range items {
			items[i] = f(i)
		}
		return jsval.Arr(items)
	}
	return map[string]func(n int) Value{
		// C4: one inline row whose keys are array indices in descending
		// order; the input clone puts them in JavaScript's order.
		"clone-index-keys": func(n int) Value {
			row := jsval.NewObject(n)
			for i := n; i > 0; i-- {
				row.Set(strconv.Itoa(i), jsval.Int(0))
			}
			return mkv("data", mkv("values", arr(jsval.Obj(row))), "mark", "point", "encoding", enc())
		},
		// C4: `{...markDef.box}` of a string spreads its characters as index
		// keys.
		"spread-string": func(n int) Value {
			return mkv("data", data(), "mark", mkv("type", "boxplot", "box", strings.Repeat("a", n)),
				"encoding", mkv("x", mkv("field", "a", "type", "nominal"), "y", mkv("field", "b", "type", "quantitative")))
		},
		// C4: the ancestor parse (a split) gains a derived field per
		// calculate, each an index key smaller than all before it.
		"split-index-keys": func(n int) Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"transform", list(n, func(i int) Value { return mkv("calculate", "1", "as", strconv.Itoa(n-i)) }))
		},
		// C4: the ancestor parse gains many fields at once, explicit first
		// and then derived (a delete from the explicit side per field).
		"split-flip": func(n int) Value {
			parse := jsval.NewObject(n)
			ops := make([]Value, n)
			for i := range n {
				f := fmt.Sprintf("f%d", i)
				parse.Set(f, jsval.Str("number"))
				ops[i] = mkv("op", "rank", "as", f)
			}
			return mkv("data", mkv("values", arr(mkv("a", 1)), "format", mkv("parse", jsval.Obj(parse))),
				"mark", "point", "encoding", enc(), "transform", arr(mkv("window", jsval.Arr(ops))))
		},
		// C4: the omap of a filter-invalid node keyed by index-like fields
		// in descending order.
		"omap-index-keys": func(n int) Value {
			e := jsval.NewObject(2)
			e.Set("x", mkv("field", "a", "type", "quantitative"))
			e.Set("tooltip", list(n, func(i int) Value {
				return mkv("field", strconv.Itoa(n-i), "type", "quantitative", "aggregate", "max")
			}))
			return mkv("data", data(), "mark", "point", "encoding", jsval.Obj(e))
		},
		// C16: projected fields whose signal names collide (every name of
		// non-word characters becomes underscores).
		"selection-signal-names": func(n int) Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"params", arr(mkv("name", "s", "select", mkv("type", "point",
					"fields", list(n, func(i int) Value { return jsval.Str(string(rune(0x4e00 + i))) })))))
		},
		// X13: an axis title merged over many layers, each with its own
		// field.
		"layer-titles": func(n int) Value {
			return mkv("data", data(), "layer", list(n, func(i int) Value {
				return mkv("mark", "point", "encoding", mkv("x", mkv("field", fmt.Sprintf("f%d", i), "type", "quantitative")))
			}))
		},
		// X13: a selection per view of a concatenation; each view's
		// selection data is assembled against all the data so far.
		"concat-selection-data": func(n int) Value {
			return mkv("data", data(), "hconcat", list(n, func(i int) Value {
				return mkv("mark", "point", "encoding", enc(),
					"params", arr(mkv("name", fmt.Sprintf("s%d", i), "select", "point")))
			}))
		},
		// C9: a filter per field, each with its implicit parse, which
		// MoveParseUp bubbles up and merges into one.
		"filter-parse-chain": func(n int) Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"transform", list(n, func(i int) Value { return mkv("filter", mkv("field", fmt.Sprintf("f%d", i), "equal", i)) }))
		},
		// C9: a chain of time units, each a field of its own, which
		// RemoveDuplicateTimeUnits walks with the fields above each.
		"timeunit-chain": func(n int) Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"transform", list(n, func(i int) Value { return mkv("timeUnit", "year", "field", "a", "as", fmt.Sprintf("t%d", i)) }))
		},
		// C9: a parent deriving n fields (its ancestor parse) over n layers,
		// each of which copied it.
		"ap-layers": func(n int) Value {
			return mkv("data", data(), "transform", list(n, func(i int) Value { return mkv("calculate", "1", "as", fmt.Sprintf("c%d", i)) }),
				"layer", list(n, func(int) Value { return mkv("mark", "point") }))
		},
		// C26: a time unit per layer on a field of its own: the time-unit
		// nodes become siblings, are merged one at a time, and each branch
		// is walked with all their fields.
		"layer-timeunits": func(n int) Value {
			return mkv("data", data(), "layer", list(n, func(i int) Value {
				return mkv("mark", "point", "encoding", mkv("x", mkv("field", fmt.Sprintf("t%d", i), "type", "temporal", "timeUnit", "month"),
					"y", mkv("field", "b", "type", "quantitative")))
			}))
		},
		// C11: a calculate whose `as` is a path of n pieces, then a filter
		// whose implicit date parse moves up past it (fieldIntersection).
		"dotted-as": func(n int) Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(), "transform", arr(
				mkv("calculate", "1", "as", strings.TrimSuffix(strings.Repeat("a.", n), ".")),
				mkv("filter", mkv("field", "b", "equal", mkv("year", 2000)))))
		},
		// A layer per field on a shared x scale: the domains of the layers
		// are merged one at a time (domainsTieBreaker).
		"layer-domains": func(n int) Value {
			return mkv("data", data(), "layer", list(n, func(i int) Value {
				return mkv("mark", "point", "encoding", mkv("x", mkv("field", fmt.Sprintf("x%d", i), "type", "quantitative")))
			}))
		},
		// A layer per axis title on a shared axis: the titles are merged one
		// at a time (mergeTitle).
		"layer-axis-titles": func(n int) Value {
			return mkv("data", data(), "layer", list(n, func(i int) Value {
				return mkv("mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative", "axis", mkv("title", fmt.Sprintf("a title of forty bytes or so, no. %06d", i)))))
			}))
		},
	}
}

// compileAlloc is the least, over three compilations of spec, of the bytes
// a compilation allocates: the same on every machine outside the race
// detector (whose sync.Pool drops a random share of its items), the least
// of three to leave out what other goroutines allocate meanwhile.
func compileAlloc(t *testing.T, spec Value) int64 {
	t.Helper()
	least := int64(math.MaxInt64)
	for range 3 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		if _, err := Compile(spec, Options{}); err != nil {
			t.Fatalf("compile: %v", err)
		}
		runtime.ReadMemStats(&after)
		least = min(least, int64(after.TotalAlloc-before.TotalAlloc))
	}
	return least
}

// compileWork is the work a compilation of spec charges to its ledger: the
// steps of every loop whose trip count the specification controls, the
// same on every machine.
func compileWork(t *testing.T, spec Value) int64 {
	t.Helper()
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	if _, err := Compile(spec, Options{Context: budget.WithLedger(context.Background(), l)}); err != nil {
		t.Fatalf("compile: %v", err)
	}
	return l.WorkDone()
}

// The complexity guards. Each family of complexitySpecs drove a quadratic
// algorithm the compiler no longer has; the guard measures a quantity that
// is the same on every machine, at n and 2n, and bounds its quadratic
// coefficient, (M(2n) - 2·M(n)) / 2n²: zero (or nearly) for linear work, the
// quadratic algorithm's own constant otherwise. No guard times anything,
// because a time ratio on a shared CI runner is noise as much as signal.
// Each bound was checked by planting the quadratic algorithm back.

// TestCompileWorkGrowsLinearly bounds the work charged to the ledger, for
// the families whose quadratic algorithm did charged work: the input clone
// putting index keys in JavaScript's order on every insert (C4), the
// selection signal counters (C16), the layer titles and the concatenated
// selection data (X13). It runs under the race detector too. The
// concatenated views keep upstream's own quadratic step, cleanupEmptyOnArray
// over every signal so far once per view (0.5 per n²), so their bound is
// 0.6; planted back, the algorithms measured 0.50 (clone), 0.50 (counters)
// and 1.00 (titles, selection data) more.
func TestCompileWorkGrowsLinearly(t *testing.T) {
	specs := complexitySpecs()
	for name, c := range map[string]struct {
		n       int
		maxQuad float64
	}{
		"clone-index-keys":       {1000, 0.25},
		"selection-signal-names": {500, 0.25},
		"layer-titles":           {500, 0.25},
		"concat-selection-data":  {500, 0.6},
		// C9, C26 (audit 2026-10-18): planted back, 1.50 (parse merges
		// and intersections), 1.00 (ancestor parse copies), 1.51 (sibling
		// time units) and 1.31 (merged titles).
		"filter-parse-chain": {500, 0.25},
		"ap-layers":          {500, 0.25},
		"layer-timeunits":    {500, 0.25},
		"layer-axis-titles":  {500, 0.25},
	} {
		t.Run(name, func(t *testing.T) {
			w1, w2 := compileWork(t, specs[name](c.n)), compileWork(t, specs[name](2*c.n))
			quad := float64(w2-2*w1) / float64(2*c.n*c.n)
			t.Logf("n=%d: %d steps, 2n: %d; quadratic part %.3f steps per n²", c.n, w1, w2, quad)
			if quad > c.maxQuad {
				t.Errorf("%s: the work charged has a quadratic part of %.3f steps per n² (limit %.2f): super-linear", name, quad, c.maxQuad)
			}
		})
	}
}

// TestCompileAllocGrowsLinearly bounds the bytes allocated, for the
// optimizer's and the parser's copies per node, child or frame (C9, C11,
// C26, audit 2026-10-18: a chain of filters' parse merges, a chain of time
// units, the ancestor parse per layer, sibling time units, a dotted field's
// prefixes, the union of layer domains, merged axis titles; planted back,
// they measure 8.9 to 377 bytes per n², the prefixes 1.5), and for the
// families whose quadratic algorithm (C4) was ordering that charges nothing
// (spread, a split and an omap order their keys without a compilation to
// charge) but allocates its entries each time: `{...markDef.box}` of a
// string, the ancestor parse gaining index keys in descending order or
// flipping fields from explicit to derived, and a filter-invalid omap keyed
// by index-like fields. Outside the race detector the bytes are the same on
// every machine; clean, the quadratic parts measure -0.08 to 0.34 bytes per
// n², and 23 to 137 with the eager ordering planted back. The race
// detector's sync.Pool makes allocation vary, so the guard is skipped under
// it.
func TestCompileAllocGrowsLinearly(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocation varies under the race detector")
	}
	specs := complexitySpecs()
	for name, c := range map[string]struct {
		n       int
		maxQuad float64
	}{
		"spread-string": {2000, 5}, "split-index-keys": {1000, 5}, "split-flip": {1000, 5}, "omap-index-keys": {1000, 5},
		"filter-parse-chain": {500, 5}, "timeunit-chain": {500, 5}, "ap-layers": {500, 5}, "layer-timeunits": {250, 5},
		"layer-domains": {500, 5}, "layer-axis-titles": {500, 5},
		// A path's prefixes are 1.5 bytes per piece², whose square is n².
		"dotted-as": {2000, 0.5},
	} {
		n, maxQuad := c.n, c.maxQuad
		t.Run(name, func(t *testing.T) {
			a1, a2 := compileAlloc(t, specs[name](n)), compileAlloc(t, specs[name](2*n))
			quad := float64(a2-2*a1) / float64(2*n*n)
			t.Logf("n=%d: %d bytes, 2n: %d; quadratic part %.3f bytes per n²", n, a1, a2, quad)
			if quad > maxQuad {
				t.Errorf("%s: the bytes allocated have a quadratic part of %.3f per n² (limit %.1f): super-linear", name, quad, maxQuad)
			}
		})
	}
}

// TestViewBudget checks that the views a repeat multiplies are counted
// before they are built (C15): nested repeats past Options.MaxViews fail
// with ErrLimit, the same specification within it compiles.
func TestViewBudget(t *testing.T) {
	fields := func(n int) Value {
		items := make([]Value, n)
		for i := range items {
			items[i] = jsval.Str(fmt.Sprintf("f%d", i))
		}
		return jsval.Arr(items)
	}
	nested := func(levels, n int) Value {
		s := mkv("mark", "point", "encoding", mkv("x", mkv("field", mkv("repeat", "repeat"), "type", "quantitative")))
		for range levels {
			s = mkv("repeat", fields(n), "spec", s)
		}
		s.ObjValue().Set("data", mkv("values", arr(mkv("f0", 1))))
		return s
	}
	layered := mkv("data", mkv("values", arr(mkv("a", 1))),
		"repeat", mkv("row", fields(30), "column", fields(30), "layer", fields(1000)),
		"spec", mkv("mark", "point", "encoding", mkv("x", mkv("field", mkv("repeat", "layer"), "type", "quantitative"))))
	for name, spec := range map[string]Value{"nested": nested(4, 30), "layer-rows-columns": layered} {
		_, err := Compile(spec, Options{})
		if !errors.Is(err, budget.ErrLimit) || !strings.Contains(err.Error(), "views") {
			t.Errorf("%s: err = %v, want the view limit", name, err)
		}
	}
	if _, err := Compile(nested(2, 30), Options{MaxViews: 1000}); err != nil {
		t.Errorf("930 views under a limit of 1000: %v", err)
	}
	if _, err := Compile(nested(2, 30), Options{MaxViews: 900}); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("930 views under a limit of 900: err = %v, want ErrLimit", err)
	}
	// C10 (audit 2026-10-18): every top-level selection is copied into
	// every unit, which then weighs one more for each (and for its text):
	// 30 repeated views and 40 selections weigh 31 + 30·40 and a little.
	selections := func(n int) Value {
		s := nested(1, 30)
		params := make([]Value, n)
		for i := range params {
			params[i] = mkv("name", fmt.Sprintf("s%d", i), "select", "point")
		}
		s.ObjValue().Set("params", jsval.Arr(params))
		return s
	}
	if _, err := Compile(selections(40), Options{MaxViews: 1000}); !errors.Is(err, budget.ErrLimit) || !strings.Contains(err.Error(), "weighing 12") {
		t.Errorf("30 views and 40 selections under a limit of 1000: err = %v, want ErrLimit at a weight above 1231", err)
	}
	if _, err := Compile(selections(25), Options{MaxViews: 1000}); err != nil {
		t.Errorf("30 views and 25 selections (781 and a little) under a limit of 1000: %v", err)
	}
}
