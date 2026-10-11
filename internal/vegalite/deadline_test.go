package vegalite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// pollGapCtx is a context that times the gaps between its polls. A deadline
// that falls inside a gap is noticed only at the gap's end, so the longest
// gap over a whole compilation is the worst overshoot past any deadline that
// could have been set: one run measures every deadline at once, where a sweep
// of real deadlines would sample a few. With ASTER_GAP_STACKS set it records
// where each long gap starts and ends, to find the loop that does not poll.
type pollGapCtx struct {
	context.Context
	mu      sync.Mutex
	last    time.Time
	max     time.Duration
	polls   int
	stacks  bool
	at      string // the caller of the latest poll (with stacks)
	maxSpan string // where the longest gap started and ended (with stacks)
}

func (c *pollGapCtx) Err() error {
	c.poll()
	return c.Context.Err()
}

func (c *pollGapCtx) poll() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	var at string
	if c.stacks {
		pcs := make([]uintptr, 12)
		fr := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
		var parts []string
		for {
			f, more := fr.Next()
			if fn, ok := strings.CutPrefix(f.Function, "github.com/mgilbir/aster/internal/vegalite."); ok {
				parts = append(parts, fn)
			}
			if !more {
				break
			}
		}
		at = strings.Join(parts, " < ")
	}
	if g := now.Sub(c.last); g > c.max {
		c.max = g
		c.maxSpan = "from " + c.at + "\nto " + at
	}
	c.at, c.last = at, now
}

// deadlineSpecs are specifications whose compilation does work in
// proportion to one dimension of the spec, pushed far past any real chart.
func deadlineSpecs() map[string]func() Value {
	data := func() Value { return mkv("values", arr(mkv("a", 1, "b", 2), mkv("a", 2, "b", 3))) }
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
	return map[string]func() Value{
		"params": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"params", list(100000, func(i int) Value { return mkv("name", fmt.Sprintf("p%d", i), "select", "point") }))
		},
		"layers": func() Value {
			return mkv("data", data(), "layer", list(100000, func(int) Value { return mkv("mark", "point") }))
		},
		"transforms": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"transform", list(9000, func(i int) Value {
					return mkv("calculate", fmt.Sprintf("datum.a + %d", i), "as", fmt.Sprintf("c%d", i))
				}))
		},
		"transforms-over-limit": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"transform", list(20000, func(i int) Value {
					return mkv("calculate", fmt.Sprintf("datum.a + %d", i), "as", fmt.Sprintf("c%d", i))
				}))
		},
		"concat": func() Value {
			return mkv("data", data(), "hconcat", list(5000, func(int) Value { return mkv("mark", "point", "encoding", enc()) }))
		},
		"tooltip": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
				"tooltip", list(200000, func(i int) Value { return mkv("field", fmt.Sprintf("f%d", i), "type", "nominal") })))
		},
		"values": func() Value {
			return mkv("data", mkv("values", list(300000, func(i int) Value { return mkv("a", i, "b", i%7) })),
				"mark", "point", "encoding", enc())
		},
		"selection-fields": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"params", arr(mkv("name", "s", "select", mkv("type", "point", "fields", list(200000, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) })))))
		},
		"concat-params": func() Value {
			return mkv("data", data(),
				"params", list(100, func(i int) Value { return mkv("name", fmt.Sprintf("p%d", i), "select", "interval") }),
				"hconcat", list(500, func(int) Value { return mkv("mark", "point", "encoding", enc()) }))
		},
		"vars": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"params", list(100000, func(i int) Value { return mkv("name", fmt.Sprintf("v%d", i), "value", i) }))
		},
		"domains": func() Value {
			vals := func() Value { return list(800000, func(i int) Value { return jsval.Int(i) }) }
			return mkv("data", data(), "mark", "point", "encoding", mkv(
				"x", mkv("field", "a", "type", "quantitative", "scale", mkv("domain", vals()), "axis", mkv("values", vals())),
				"color", mkv("field", "b", "type", "ordinal", "scale", mkv("domain", vals()), "legend", mkv("values", vals()))))
		},
		"filters": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(), "transform", arr(
				mkv("filter", mkv("field", "a", "oneOf", list(800000, func(i int) Value { return jsval.Int(i) }))),
				mkv("filter", mkv("and", list(80000, func(i int) Value { return mkv("field", "a", "gt", i) })))))
		},
		"transform-lists": func() Value {
			names := func(n int, p string) Value {
				return list(n, func(i int) Value { return jsval.Str(fmt.Sprintf("%s%d", p, i)) })
			}
			return mkv("data", data(), "mark", "point", "encoding", enc(), "transform", arr(
				mkv("fold", names(200000, "f")),
				mkv("aggregate", list(80000, func(i int) Value {
					return mkv("op", "sum", "field", fmt.Sprintf("f%d", i), "as", fmt.Sprintf("s%d", i))
				}), "groupby", names(80000, "g")),
				mkv("window", list(80000, func(i int) Value { return mkv("op", "rank", "as", fmt.Sprintf("r%d", i)) }), "groupby", names(80000, "h"), "sort", list(80000, func(i int) Value { return mkv("field", fmt.Sprintf("k%d", i)) }))))
		},
		"selection-init": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", enc(),
				"params", arr(mkv("name", "s", "select", mkv("type", "point", "fields", arr("a")),
					"value", list(200000, func(i int) Value { return mkv("a", i) }))))
		},
		"datasets": func() Value {
			ds := jsval.NewObject(50000)
			for i := range 50000 {
				ds.Set(fmt.Sprintf("d%d", i), arr(mkv("a", i)))
			}
			return mkv("datasets", jsval.Obj(ds), "data", mkv("name", "d7"), "mark", "point", "encoding", enc())
		},
		"layer-repeat": func() Value {
			return mkv("data", data(), "repeat", mkv("layer", list(2000, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) })),
				"spec", mkv("mark", "point", "encoding", mkv("x", mkv("field", mkv("repeat", "layer"), "type", "quantitative"))))
		},
		"conditions": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative"),
				"color", mkv("value", "black", "condition", list(100000, func(i int) Value { return mkv("test", fmt.Sprintf("datum.a > %d", i), "value", "red") }))))
		},
		"nested-repeat": func() Value {
			fields := list(6, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) })
			inner := mkv("mark", "point", "encoding", mkv(
				"x", mkv("field", mkv("repeat", "row"), "type", "quantitative"),
				"y", mkv("field", mkv("repeat", "column"), "type", "quantitative")))
			s := inner
			for _, ch := range []string{"column", "row", "column", "row"} {
				s = mkv("repeat", mkv(ch, fields), "spec", s)
			}
			s.ObjValue().Set("data", data())
			return s
		},
	}
}

// TestDeadlineIsHonouredThroughoutCompile checks that every phase of a
// compilation whose work grows with the specification polls the context:
// for each large specification, no stretch of the compilation runs longer
// than maxGap without a poll, so a deadline anywhere ends it within maxGap.
func TestDeadlineIsHonouredThroughoutCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles specifications with 100,000 views and parameters")
	}
	const maxGap = 200 * time.Millisecond
	specs := deadlineSpecs()
	for _, name := range deadlineSpecNames {
		t.Run(name, func(t *testing.T) {
			spec := specs[name]()
			parent, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			gc := &pollGapCtx{Context: parent, stacks: os.Getenv("ASTER_GAP_STACKS") != ""}
			ctx := budget.WithLedger(gc, budget.NewLedger(gc, budget.LedgerLimits{}))
			start := time.Now()
			gc.last = start
			_, err := Compile(spec, Options{Context: ctx})
			gc.poll() // the stretch from the last poll to the return counts too
			t.Logf("%v in %v, %d polls, longest gap %v", err, time.Since(start), gc.polls, gc.max)
			if gc.max > maxGap {
				t.Errorf("%v without polling the context (limit %v)\n%s", gc.max, maxGap, gc.maxSpan)
			}
		})
	}
}

// TestDeadlineSweep sets real deadlines across the compilation of the
// largest specifications and checks that each returns the context's error
// promptly past it.
func TestDeadlineSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles specifications with 100,000 views and parameters")
	}
	const maxOvershoot = 200 * time.Millisecond
	specs := deadlineSpecs()
	for _, name := range []string{"params", "layers", "concat-params"} {
		spec := specs[name]()
		for _, d := range []time.Duration{20 * time.Millisecond, 100 * time.Millisecond, 400 * time.Millisecond, 1200 * time.Millisecond, 2400 * time.Millisecond} {
			ctx, cancel := context.WithTimeout(context.Background(), d)
			start := time.Now()
			_, err := Compile(spec, Options{Context: ctx})
			el := time.Since(start)
			cancel()
			if err == nil {
				continue // finished before the deadline
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s at %v: %v", name, d, err)
			}
			if el > d+maxOvershoot {
				t.Errorf("%s: returned %v after a %v deadline", name, el-d, d)
			}
		}
	}
}

var deadlineSpecNames = []string{
	"params", "layers", "transforms", "transforms-over-limit", "concat", "nested-repeat", "tooltip", "values",
	"selection-fields", "concat-params", "vars", "domains", "filters", "transform-lists", "selection-init", "datasets", "layer-repeat", "conditions",
}
