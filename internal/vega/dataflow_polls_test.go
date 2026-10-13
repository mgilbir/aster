package vega

import (
	"context"
	"fmt"
	"math/bits"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// opPollCtx counts, after the parse stage, the polls of its Err made inside
// each of the dataflow's own operators (by the operator factory's closure on
// the stack), and records the ledger's work at each poll: what is counted
// is where the polls fall and the work between them, both as deterministic
// as the code.
type opPollCtx struct {
	context.Context
	mu   sync.Mutex
	led  *budget.Ledger
	on   bool
	work []int64
	in   map[string]int
}

func (c *opPollCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on {
		return nil
	}
	c.work = append(c.work, c.led.WorkDone())
	pc := make([]uintptr, 32)
	frames := runtime.CallersFrames(pc[:runtime.Callers(2, pc)])
	for {
		f, more := frames.Next()
		if i := strings.Index(f.Function, "vega.fac"); i >= 0 {
			name, _, _ := strings.Cut(f.Function[i+len("vega."):], ".")
			c.in[name]++
			break
		}
		if !more {
			break
		}
	}
	return nil
}

// shuffledRows is n rows of rowsText's two fields and a third, h, which
// orders them in a shuffle (a multiple of i modulo a prime): a sort by h,
// and then by a, has no runs to exploit.
func shuffledRows(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"a":%d,"b":"x%d","h":%d}`, i, i, (i*7919)%100_003)
	}
	b.WriteByte(']')
	return b.String()
}

// dataflowRows is the rows of the spec below.
const dataflowRows = 100_000

// dataflowSpec runs each of the dataflow's own operators over 100,000 rows:
// a collect that sorts, a derived data set (a relay that copies), the sorted
// domain (values) of a band scale of 100,000 bands, a lookup index
// (tupleindex), and a mark that joins, stores, encodes and sorts its 100,000
// items.
func dataflowSpec(t *testing.T) string {
	return `{"width":400,"height":400,"data":[
		{"name":"t","values":` + shuffledRows(dataflowRows) + `,"transform":[{"type":"collect","sort":{"field":"h"}}]},
		{"name":"d","source":"t","transform":[{"type":"formula","as":"c","expr":"datum.a*2"}]},
		{"name":"e","values":[{"k":1}],"transform":[{"type":"lookup","from":"t","key":"a","fields":["k"],"as":["r"]}]}],
	"scales":[{"name":"x","type":"band","domain":{"data":"d","field":"b","sort":true},"range":"width"}],
	"marks":[{"type":"symbol","from":{"data":"d"},"sort":{"field":"datum.a"},
		"encode":{"update":{"x":{"scale":"x","field":"b"},"y":{"value":1},"size":{"value":4}}}}]}`
}

// The dataflow's own operators charge their per-row loops, a batch of rows
// at a time, so that a render of many rows polls its context through each
// of them, not only between operators (ADR 0010). Before, the dataflow
// polled every 256 operator visits, the data join every 1,024 rows, and
// collect, the item store, the mark, encode, the scale and the sorts not at
// all: on this spec 55 to 99 ms went by without a poll (from the data join's
// last poll to the bounds of the mark), now 3 to 7 ms, a collection of the
// garbage at most. Counted, not timed: each operator
// polls at least once per 2,048 of the rows it handles (a copy once per
// chunk), and no two polls are
// more than maxWorkBetweenPolls steps of work apart. A sort polls through
// its comparisons.
func TestDataflowOperatorsPollThroughout(t *testing.T) {
	spec := vegaSpec(t, dataflowSpec(t))
	ctx := &opPollCtx{Context: context.Background(), in: map[string]int{}}
	ctx.led = budget.NewLedger(ctx, budget.LedgerLimits{})
	_, err := Render(budget.WithLedger(ctx, ctx.led), spec, Options{Stages: func(name string, _ time.Duration) {
		if name == "parse" {
			ctx.mu.Lock()
			ctx.on = true
			ctx.work = append(ctx.work, ctx.led.WorkDone())
			ctx.mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("polls by operator: %v", ctx.in)
	for _, op := range []string{"facCollect", "facRelay", "facValues", "facTupleIndex", "facDataJoin", "facItemStore", "facMark", "facEncode", "facSortItems", "facScale"} {
		min := dataflowRows / 2048
		switch op {
		case "facItemStore":
			// A first run only copies its items, a chunk at a time.
			min = dataflowRows / copyChunk
		case "facCollect", "facValues", "facSortItems":
			// A sort compares n log n times, a quarter of that at least
			// (TimSort finds no runs here); a step a comparison.
			min = dataflowRows * bits.Len(dataflowRows) / 4 / 1024
		}
		if ctx.in[op] < min {
			t.Errorf("%s polled %d times over %d rows; want at least %d", op, ctx.in[op], dataflowRows, min)
		}
	}
	worst := int64(0)
	for i := 1; i < len(ctx.work); i++ {
		worst = max(worst, ctx.work[i]-ctx.work[i-1])
	}
	t.Logf("%d polls after the parse, at most %d steps apart", len(ctx.work)-1, worst)
	if worst > maxWorkBetweenPolls {
		t.Errorf("%d steps of work between two polls; want at most %d", worst, maxWorkBetweenPolls)
	}
}

// maxWorkBetweenPolls bounds the work charged between two polls: the meter
// polls every 1,024 steps, and a charge is at most a batch of rowBatch rows
// of a few steps each, or a chunk of a copy (copyChunk/16 steps). 2,720 is
// the most here; a loop charged once for all its rows would be 100,000 or
// more.
const maxWorkBetweenPolls = 4 * 1024
