package vegalite

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// countCtx counts the polls of its Err and ends, if stop is set, at the
// stop-th. With mallocs set it records, at each poll, the heap objects the
// process has allocated: the work between two polls, counted as
// deterministically as the code allows (run alone, not under -race).
type countCtx struct {
	context.Context
	mu      sync.Mutex
	polls   int
	stop    int
	mallocs bool
	at      []uint64
	bytes   []uint64
}

func (c *countCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if c.mallocs {
		objs, bytes := heapAllocs()
		c.at, c.bytes = append(c.at, objs), append(c.bytes, bytes)
	}
	if c.stop > 0 && c.polls >= c.stop {
		return context.Canceled
	}
	return nil
}

// heapAllocs is the heap objects and bytes the process has allocated.
func heapAllocs() (objects, bytes uint64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Mallocs, ms.TotalAlloc
}

// maxCompileObjectsBetweenPolls bounds the heap objects a compilation
// allocates between two polls of its context. Metered, the families below
// allocate at most about 10,300 between polls; unmetered, a config, parse or
// selection given as a string of 20,000 characters allocated 21,000 to
// 61,000 in one stretch (and ran for a second, quadratic in the string), an
// event selector of 50,000 parts 300,000, and a field path of 5,000 pieces 25
// million.
const maxCompileObjectsBetweenPolls = 20_000

// maxCompileBytesBetweenPolls bounds the bytes allocated between two polls:
// a stretch can allocate one large value (the guard expression of a long
// field path, 10 MB here, made at once), but a loop charged a step for work
// linear in the specification (reading a long string once a key) allocates
// hundreds of megabytes. 10.4 MB is the most of the families below.
const maxCompileBytesBetweenPolls = 32 << 20

// pollFamilies are specifications whose compilation is long in one way the
// specification controls, each a stretch that did not poll (or was charged
// a step for work linear in the specification): string-valued objects whose
// keys are read one at a time, a field path's guards, an event selector,
// config keys merged, deleted or read per axis, a parse of many fields.
func pollFamilies() map[string]func() Value {
	list := func(n int, f func(i int) Value) Value {
		items := make([]Value, n)
		for i := range items {
			items[i] = f(i)
		}
		return jsval.Arr(items)
	}
	keys := func(n int, f func(i int) Value) Value {
		o := jsval.NewObject(n)
		for i := range n {
			o.Set(fmt.Sprintf("k%d", i), f(i))
		}
		return jsval.Obj(o)
	}
	data := func() Value { return mkv("values", arr(mkv("a", 1, "b", 2))) }
	enc := func() Value {
		return mkv("x", mkv("field", "a", "type", "quantitative"), "y", mkv("field", "b", "type", "quantitative"))
	}
	unit := func(kv ...any) Value {
		return mkv(append([]any{"data", data(), "mark", "point", "encoding", enc()}, kv...)...)
	}
	return map[string]func() Value{
		"config-string":    func() Value { return unit("config", strings.Repeat("c", 20000)) },
		"selection-string": func() Value { return unit("selection", strings.Repeat("s", 20000)) },
		"parse-string": func() Value {
			return mkv("data", mkv("values", arr(mkv("a", 1)), "format", mkv("parse", strings.Repeat("p", 20000))), "mark", "point", "encoding", enc())
		},
		"flatten-string-as": func() Value {
			return unit("transform", arr(mkv("flatten", list(5000, func(i int) Value { return jsval.Str(fmt.Sprintf("f%d", i)) }), "as", strings.Repeat("x", 20000))))
		},
		"deep-field": func() Value {
			return mkv("data", data(), "mark", "point", "encoding", mkv("x", mkv("field", strings.TrimSuffix(strings.Repeat("a.", 2000), "."), "type", "quantitative")))
		},
		"event-selector": func() Value {
			return unit("params", arr(mkv("name", "s", "select", mkv("type", "point", "on", strings.TrimSuffix(strings.Repeat("click,", 50000), ",")))))
		},
		"config-empty-keys": func() Value { return unit("config", keys(20000, func(int) Value { return mkv() })) },
		"axis-conditional": func() Value {
			return unit("config", mkv("axis", keys(20000, func(int) Value { return mkv("condition", mkv("test", "true", "value", 1), "value", 2) })))
		},
		"concat-axis-config": func() Value {
			return mkv("data", data(), "config", mkv("axisX", keys(5000, func(i int) Value { return jsval.Int(i) })),
				"hconcat", list(200, func(int) Value { return mkv("mark", "point", "encoding", enc()) }))
		},
		"parse-common": func() Value {
			return mkv("data", mkv("values", arr(mkv("a", 1)), "format", mkv("parse", keys(20000, func(int) Value { return jsval.Str("number") }))),
				"layer", list(4, func(int) Value { return mkv("mark", "point", "encoding", enc()) }))
		},
		"domains": func() Value {
			vals := func() Value { return list(200000, func(i int) Value { return jsval.Int(i) }) }
			return mkv("data", data(), "mark", "point", "encoding", mkv(
				"x", mkv("field", "a", "type", "quantitative", "scale", mkv("domain", vals(), "range", vals()))))
		},
	}
}

// The compilation polls its context throughout, however a specification
// makes it long (ADR 0010): no stretch of it allocates more than
// maxCompileObjectsBetweenPolls heap objects between two polls, and a
// context that ends at its middle poll stops it. Counted, not timed, like
// internal/vega's TestParsePollsThroughout.
func TestCompilePollsThroughout(t *testing.T) {
	for name, spec := range pollFamilies() {
		t.Run(name, func(t *testing.T) {
			s := spec()
			full := &countCtx{Context: context.Background(), mallocs: !testutil.RaceEnabled}
			startObjs, startBytes := heapAllocs()
			ctx := budget.WithLedger(full, budget.NewLedger(full, budget.LedgerLimits{}))
			_, err := Compile(s, Options{Context: ctx})
			endObjs, endBytes := heapAllocs()
			if err != nil {
				// A selection given as a string is upstream's TypeError,
				// past its normalization: the stretch measured is up to it.
				t.Logf("compiles to an error: %v", err)
			}
			if full.mallocs {
				worstGap := func(start, end uint64, at []uint64) (worst uint64, before int) {
					marks := append(append([]uint64{start}, at...), end)
					for i := 1; i < len(marks); i++ {
						if d := marks[i] - marks[i-1]; d > worst {
							worst, before = d, i
						}
					}
					return worst, before
				}
				objs, at := worstGap(startObjs, endObjs, full.at)
				bytes, bat := worstGap(startBytes, endBytes, full.bytes)
				t.Logf("%d polls, at most %d heap objects (before poll %d) and %d bytes (before poll %d) between two", full.polls, objs, at, bytes, bat)
				if objs > maxCompileObjectsBetweenPolls {
					t.Errorf("%d heap objects allocated between polls %d and %d; want at most %d", objs, at-1, at, maxCompileObjectsBetweenPolls)
				}
				if bytes > maxCompileBytesBetweenPolls {
					t.Errorf("%d bytes allocated between polls %d and %d; want at most %d", bytes, bat-1, bat, maxCompileBytesBetweenPolls)
				}
			}
			half := &countCtx{Context: context.Background(), stop: max(full.polls/2, 1)}
			hctx := budget.WithLedger(half, budget.NewLedger(half, budget.LedgerLimits{}))
			if _, err := Compile(s, Options{Context: hctx}); !errors.Is(err, context.Canceled) {
				t.Errorf("a context ended at poll %d of %d did not stop the compilation: %v", half.stop, full.polls, err)
			}
		})
	}
}

// The guards of a field path's prefixes are written once each, in one
// buffer, as before: the same expression for every path.
func TestAccessPathWithDatumUnchanged(t *testing.T) {
	old := func(pieces []string, datum string) string {
		prefixes := make([]string, 0, len(pieces))
		for i := 1; i <= len(pieces); i++ {
			qs := make([]string, i)
			for j := 0; j < i; j++ {
				qs[j] = jsonString(pieces[j])
			}
			prefixes = append(prefixes, datum+"["+strings.Join(qs, "][")+"]")
		}
		return strings.Join(prefixes, " && ")
	}
	for n := range 40 {
		pieces := make([]string, n)
		for i := range pieces {
			pieces[i] = strings.Repeat(string(rune('a'+i%26)), i%5) + []string{"", `"`, `\`, "é", "\n"}[i%5]
		}
		for _, datum := range []string{"datum", "d", ""} {
			if got, want := accessPathWithDatumPieces(nil, pieces, datum), old(pieces, datum); got != want {
				t.Fatalf("%d pieces, datum %q: %q, want %q", n, datum, got, want)
			}
		}
	}
}
