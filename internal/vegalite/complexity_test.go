package vegalite

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
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

// compileTime is the best of three compilations of spec.
func compileTime(t *testing.T, spec Value) time.Duration {
	t.Helper()
	best := time.Duration(1<<63 - 1)
	for range 3 {
		start := time.Now()
		if _, err := Compile(spec, Options{}); err != nil {
			t.Fatalf("compile: %v", err)
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

// TestCompileTimeGrowsLinearly compiles each family at n and 4n and fails
// when the time grows by more than a linear (or n log n) algorithm allows:
// four times the input should take about four times as long (they measure
// 3 to 5), a quadratic algorithm sixteen, a cubic one sixty-four. Where the
// quadratic part is small next to the linear one at these sizes (the X13
// families) it still shows as 8 to 9.
func TestCompileTimeGrowsLinearly(t *testing.T) {
	sizes := map[string]int{
		"clone-index-keys":       400,
		"spread-string":          2000,
		"split-index-keys":       400,
		"split-flip":             1000,
		"omap-index-keys":        500,
		"selection-signal-names": 1000,
		"layer-titles":           1000,
		"concat-selection-data":  1000,
	}
	if testing.Short() {
		for k := range sizes {
			sizes[k] /= 2
		}
	}
	const maxRatio = 7
	for name, f := range complexitySpecs() {
		t.Run(name, func(t *testing.T) {
			n := sizes[name]
			small, large := compileTime(t, f(n)), compileTime(t, f(4*n))
			ratio := float64(large) / float64(max(small, time.Millisecond))
			t.Logf("n=%d: %v, 4n: %v (×%.1f)", n, small, large, ratio)
			if ratio > maxRatio {
				t.Errorf("compile time grew ×%.1f for 4× the input (limit ×%d): super-linear", ratio, maxRatio)
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
