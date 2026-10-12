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
// families whose quadratic algorithm (C4) was ordering that charges nothing
// (spread, a split and an omap order their keys without a compilation to
// charge) but allocates its entries each time: `{...markDef.box}` of a
// string, the ancestor parse gaining index keys in descending order or
// flipping fields from explicit to derived, and a filter-invalid omap keyed
// by index-like fields. Outside the race detector the bytes are the same on
// every machine; clean, the quadratic parts measure -0.09 to 0.34 bytes per
// n², and 44 to 169 with the eager ordering planted back. The race
// detector's sync.Pool makes allocation vary, so the guard is skipped under
// it.
func TestCompileAllocGrowsLinearly(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocation varies under the race detector")
	}
	const maxQuad = 5.0
	specs := complexitySpecs()
	for name, n := range map[string]int{"spread-string": 2000, "split-index-keys": 1000, "split-flip": 1000, "omap-index-keys": 1000} {
		t.Run(name, func(t *testing.T) {
			a1, a2 := compileAlloc(t, specs[name](n)), compileAlloc(t, specs[name](2*n))
			quad := float64(a2-2*a1) / float64(2*n*n)
			t.Logf("n=%d: %d bytes, 2n: %d; quadratic part %.3f bytes per n²", n, a1, a2, quad)
			if quad > maxQuad {
				t.Errorf("%s: the bytes allocated have a quadratic part of %.3f per n² (limit %.0f): super-linear", name, quad, maxQuad)
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
}
