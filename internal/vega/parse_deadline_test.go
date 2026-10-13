package vega

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
	"github.com/mgilbir/aster/internal/vegalite"
)

// pollCtx counts the polls of its Err and ends, if stop is set, at the
// stop-th: polls are counted, not timed, so no machine's speed changes it.
// With mallocs set it records, at each poll, how many heap objects the
// process has allocated: a count of the work done between two polls that
// is as deterministic as the code (run alone, and not under -race).
type pollCtx struct {
	context.Context
	mu      sync.Mutex
	polls   int
	stop    int
	mallocs bool
	at      []uint64
}

func (c *pollCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if c.mallocs {
		c.at = append(c.at, mallocs())
	}
	if c.stop > 0 && c.polls >= c.stop {
		return context.Canceled
	}
	return nil
}

func mallocs() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Mallocs
}

// maxObjectsBetweenPolls bounds the heap objects a render allocates between
// two polls of its context while it parses its specification and builds the
// dataflow. Metered (ADR 0010), the stage allocates at most a few thousand
// between polls (7,200 is the most of the cases below, in params-4000);
// unmetered, it allocated an object or more per definition, row or key with
// no poll at all. The bound sits between, far from both.
const maxObjectsBetweenPolls = 20_000

// pollCase is a specification whose parse, before its dataflow runs, is
// long in one of the ways a specification controls.
type pollCase struct {
	name string
	spec func(t *testing.T) jsval.Value
	// minPolls is how many polls the stage must make, at least: one per
	// so many of the definitions, rows or keys the case has.
	minPolls int
}

func vegaSpec(t *testing.T, text string) jsval.Value {
	t.Helper()
	v, err := jsval.ParseJSONString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// rowsText is n rows of two fields, as JSON.
func rowsText(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"a":%d,"b":"x%d"}`, i, i)
	}
	b.WriteByte(']')
	return b.String()
}

var pollCases = []pollCase{
	{"params-4000", func(t *testing.T) jsval.Value {
		// 4,000 Vega-Lite selections: 4,000 signals, their updates and
		// the operators they need (the bounded corpus's input).
		b, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "security", "bounded", "params-4000.vl.json"))
		if err != nil {
			t.Fatal(err)
		}
		vl, err := jsval.ParseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		vg, err := vegalite.Compile(vl, vegalite.Options{})
		if err != nil {
			t.Fatal(err)
		}
		return vg
	}, 4000 / 16},
	{"inline-rows", func(t *testing.T) jsval.Value {
		// The rows are ingested (copied, weighed, checked) as the
		// dataflow is built.
		return vegaSpec(t, `{"data":[{"name":"t","values":`+rowsText(100_000)+`}]}`)
	}, 100_000 / 1024},
	{"json-string-rows", func(t *testing.T) jsval.Value {
		// A JSON document in a string is parsed as the dataflow is built.
		s := string(jsval.AppendJSON(nil, jsval.Str(rowsText(100_000))))
		return vegaSpec(t, `{"data":[{"name":"t","format":{"type":"json"},"values":`+s+`}]}`)
	}, 100_000 / 1024},
	{"parsed-rows", func(t *testing.T) jsval.Value {
		// format.parse copies every row and converts its 20 fields. The
		// copies come from a slab and the numbers allocate nothing, so
		// it is the polls that show a loop that does not poll.
		var rows, parse strings.Builder
		rows.WriteByte('[')
		for i := range 20_000 {
			if i > 0 {
				rows.WriteByte(',')
			}
			rows.WriteByte('{')
			for f := range 20 {
				if f > 0 {
					rows.WriteByte(',')
				}
				fmt.Fprintf(&rows, `"f%d":"%d"`, f, i+f)
			}
			rows.WriteByte('}')
		}
		rows.WriteByte(']')
		for f := range 20 {
			if f > 0 {
				parse.WriteByte(',')
			}
			fmt.Fprintf(&parse, `"f%d":"number"`, f)
		}
		return vegaSpec(t, `{"data":[{"name":"t","format":{"parse":{`+parse.String()+`}},"values":`+rows.String()+`}]}`)
	}, 20_000 * 20 / 1024},
	{"config-keys", func(t *testing.T) jsval.Value {
		// A config of 200,000 keys, merged over the defaults.
		var b strings.Builder
		b.WriteString(`{"config":{`)
		for i := range 200_000 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"k%d":%d`, i, i)
		}
		b.WriteString(`}}`)
		return vegaSpec(t, b.String())
	}, 200_000 / 1024},
	{"axes-config", func(t *testing.T) jsval.Value {
		// 300 axes, each of which copies an axis config of 3,000 keys
		// (merged with axisBottom's).
		var b strings.Builder
		b.WriteString(`{"config":{"axisBottom":{"tickSize":4},"axis":{`)
		for i := range 3000 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"k%d":%d`, i, i)
		}
		b.WriteString(`}},"scales":[{"name":"x","domain":[0,1],"range":[0,100]}],"axes":[`)
		for i := range 300 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"scale":"x","orient":"bottom"}`)
		}
		b.WriteString(`]}`)
		return vegaSpec(t, b.String())
	}, 300 * 3000 / 1024},
	{"axes-signal-orient", func(t *testing.T) jsval.Value {
		// 100 axes whose orient is a signal, each of which folds the
		// 2,000 keys of axisX and axisY into conditional expressions.
		var keys strings.Builder
		for i := range 2000 {
			if i > 0 {
				keys.WriteByte(',')
			}
			fmt.Fprintf(&keys, `"k%d":%d`, i, i)
		}
		var b strings.Builder
		b.WriteString(`{"signals":[{"name":"o","value":"bottom"}],"config":{"axisX":{` + keys.String() + `},"axisY":{` + keys.String() + `}},"scales":[{"name":"x","domain":[0,1],"range":[0,100]}],"axes":[`)
		for i := range 100 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"scale":"x","orient":{"signal":"o"}}`)
		}
		b.WriteString(`]}`)
		return vegaSpec(t, b.String())
	}, 100 * 2000 / 1024},
}

// renderPolled renders spec under ctx with the call's ledger in it, and
// reports the polls ctx had seen when the parse and build were over (the
// "parse" stage).
func renderPolled(ctx *pollCtx, spec jsval.Value) (parsePolls int, parseEnd uint64, err error) {
	parsePolls = -1
	led := budget.NewLedger(ctx, budget.LedgerLimits{})
	_, err = Render(budget.WithLedger(ctx, led), spec, Options{Stages: func(name string, _ time.Duration) {
		if name == "parse" {
			ctx.mu.Lock()
			parsePolls = ctx.polls
			if ctx.mallocs {
				parseEnd = mallocs()
			}
			ctx.mu.Unlock()
		}
	}})
	return parsePolls, parseEnd, err
}

// A render polls its context throughout the parse of its specification and
// the build of its dataflow, whatever makes them long: thousands of signals
// and their updates, rows ingested or parsed, a large config merged or
// copied per axis. Before, that stage did not poll at all, and a deadline
// waited for it (0.82 s past it, under -race, for params-4000 in the
// bounded corpus). Counted, not timed: the stage polls at least once per
// so many of the case's definitions, rows or keys; no stretch of it
// allocates more than maxObjectsBetweenPolls heap objects without a poll;
// and a context that ends at the stage's middle poll stops the render.
func TestParsePollsThroughout(t *testing.T) {
	for _, tc := range pollCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec(t)
			full := &pollCtx{Context: context.Background(), mallocs: !testutil.RaceEnabled}
			start := mallocs()
			parsePolls, parseEnd, err := renderPolled(full, spec)
			if err != nil {
				t.Fatal(err)
			}
			if parsePolls < 0 {
				t.Fatal("the render reported no parse stage")
			}
			t.Logf("%d polls while parsing and building, %d in all", parsePolls, full.polls)
			if parsePolls < tc.minPolls {
				t.Errorf("%d polls while parsing and building; want at least %d: a stretch of it does not poll", parsePolls, tc.minPolls)
			}
			if full.mallocs {
				marks := append(append([]uint64{start}, full.at[:parsePolls]...), parseEnd)
				worst, at := uint64(0), 0
				for i := 1; i < len(marks); i++ {
					if d := marks[i] - marks[i-1]; d > worst {
						worst, at = d, i
					}
				}
				t.Logf("at most %d heap objects between two polls (before poll %d of %d)", worst, at, parsePolls)
				if worst > maxObjectsBetweenPolls {
					t.Errorf("%d heap objects allocated between polls %d and %d of the parse; want at most %d", worst, at-1, at, maxObjectsBetweenPolls)
				}
			}
			half := &pollCtx{Context: context.Background(), stop: max(parsePolls/2, 1)}
			if _, _, err := renderPolled(half, spec); !errors.Is(err, context.Canceled) {
				t.Errorf("a context ended at poll %d of the parse's %d did not stop the render: %v", half.stop, parsePolls, err)
			}
		})
	}
}

// totalAlloc is the bytes the process has allocated.
func totalAlloc() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc
}

// The parse builds the expression of a signal update with many event
// sources, and of an encoding's rules after an unconditional one, in one
// buffer: built a piece at a time it was quadratic in the sources or the
// field path's segments. Counted in bytes allocated (not under -race, which
// changes them): twice the input allocates about twice the bytes (a
// quadratic build, four times).
func TestParseBuildsExpressionsLinearly(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("-race changes what is allocated")
	}
	for _, tc := range []struct {
		name string
		spec func(n int) string
	}{
		{"event-sources", func(n int) string {
			var sig, ev strings.Builder
			for i := range n {
				fmt.Fprintf(&sig, `{"name":"signal_%d","value":%d},`, i, i)
				if i > 0 {
					ev.WriteByte(',')
				}
				fmt.Fprintf(&ev, `{"signal":"signal_%d"}`, i)
			}
			return `{"signals":[` + sig.String() + `{"name":"t","value":0,"on":[{"events":[` + ev.String() + `],"update":"1"}]}]}`
		}},
		{"rule-segments", func(n int) string {
			path := strings.TrimSuffix(strings.Repeat("field.", n), ".")
			return `{"marks":[{"type":"rect","encode":{"enter":{"x":[{"test":"false","field":"` + path + `"},{"value":1},{"value":2}]}}}]}`
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bytesFor := func(n int) uint64 {
				spec := vegaSpec(t, tc.spec(n))
				runtime.GC()
				before := totalAlloc()
				// A path this deep nests past expr.MaxDepth, which ends the
				// render with a limit, after the expression is built.
				if _, err := Render(context.Background(), spec, Options{}); err != nil && !errors.Is(err, budget.ErrLimit) {
					t.Fatal(err)
				}
				return totalAlloc() - before
			}
			small, large := bytesFor(2000), bytesFor(4000)
			ratio := float64(large) / float64(small)
			t.Logf("%d bytes for 2,000, %d for 4,000: %.2f", small, large, ratio)
			if ratio > 3 {
				t.Errorf("twice the input allocates %.2f times the bytes; want about 2 (a quadratic build is 4)", ratio)
			}
		})
	}
}
