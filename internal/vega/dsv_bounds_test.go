package vega

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// A delimiter is its first character, however many bytes it takes, and a
// header is joined with the whole delimiter, as vega-loader does: what node
// reads from testdata/dsv-delimiters.vg.json.
func TestDSVDelimiters(t *testing.T) {
	b, err := os.ReadFile("testdata/dsv-delimiters.vg.json")
	if err != nil {
		t.Fatal(err)
	}
	v := debugView(t, mustJSON(t, string(b)))
	for name, want := range map[string]string{
		"a": `[{"x":"1","y":"2"},{"x":"3","y":"4"}]`,
		"b": `[{"p":"1","":"","q":"2"},{"p":"3","":"","q":"4"}]`,
		"c": `[{"p":"1","X\"q\"":"2"}]`,
	} {
		d, _ := v.root.Data(name)
		if got := string(jsval.AppendJSON(nil, d)); got != want {
			t.Errorf("data %s = %s, want %s", name, got, want)
		}
	}
}

// Delimited text is charged row by row, each row its weight before it is
// made: a header of many columns over blank lines (each line a row with a
// field per column) stops once the rows weigh what the budget allows. The
// guard compares what the parse allocates with what the same parse
// allocates under a budget that only counts rows, which allows sixteen cells
// per row (all the unfixed parse knew): the weighed rows stop at about half
// as many rows, whatever the collector does.
func TestDSVChargesRowsBeforeMakingThem(t *testing.T) {
	cols := make([]string, 30_000)
	for i := range cols {
		cols[i] = fmt.Sprintf("c%d", i)
	}
	text := strings.Join(cols, ",") + "\n" + strings.Repeat("\n", 2000)
	alloc := func(bud *budget.Budget) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, _, err := parseDSVLimit(context.Background(), text, ",", bud)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, budget.ErrLimit) {
			t.Fatalf("err = %v, want a limit", err)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	// WithMemoryLimit(64 MiB)'s rows: 131,072 of 512 bytes
	weighed := alloc(&budget.Budget{MaxRows: 131_072, RowBytes: 512})
	counted := alloc(&budget.Budget{MaxRows: 131_072})
	if float64(weighed) > 0.7*float64(counted) {
		t.Errorf("weighed rows allocated %d MB, rows counted by cells %d MB: want the weight to stop it sooner", weighed>>20, counted>>20)
	}
}

// The columns of delimited text are bounded, and the error says so.
func TestDSVColumnsAreBounded(t *testing.T) {
	text := strings.Repeat(",", maxDSVColumns) + "\n1\n"
	_, _, err := parseDSVLimit(context.Background(), text, ",", nil)
	if !errors.Is(err, budget.ErrLimit) || !strings.Contains(err.Error(), "columns") {
		t.Fatalf("err = %v, want the column limit", err)
	}
	if _, _, err := parseDSVLimit(context.Background(), strings.Repeat(",", maxDSVColumns-1)+"\n1\n", ",", nil); err != nil {
		t.Fatalf("%d columns: %v", maxDSVColumns, err)
	}
}

// Without a row weight (no WithMemoryLimit) the cells are bounded, and the
// error names them, not rows.
func TestDSVCellsAreNamed(t *testing.T) {
	text := strings.Repeat("c,", 99) + "c\n" + strings.Repeat("1\n", 2000)
	_, _, err := parseDSVLimit(context.Background(), text, ",", &budget.Budget{MaxRows: 1000})
	if !errors.Is(err, budget.ErrLimit) || !strings.Contains(err.Error(), "cells") {
		t.Fatalf("err = %v, want the cell limit", err)
	}
}

// Upstream ingests the rows of a url as they arrive, after the dataflow is
// made: a null row rejects that load, not the View. It is a failed load,
// reported, and the data set is empty.
func TestNullRowInLoadedJSONIsAFailedLoad(t *testing.T) {
	spec := `{"data":[{"name":"d","url":"d.json"}],
	  "marks":[{"type":"text","encode":{"update":{"text":{"signal":"length(data('d'))"}}}}]}`
	var got []error
	res, err := Render(context.Background(), mustJSON(t, spec), Options{
		Loader: mapLoader{"d.json": `[{"a":1}, null, {"a":2}]`},
		Report: func(err error) error { got = append(got, err); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var le *LoadError
	if len(got) != 1 || !errors.As(got[0], &le) || le.URL != "d.json" || !strings.Contains(le.Error(), "Cannot read properties of null") {
		t.Fatalf("reported %v, want the failed load", got)
	}
	if n := marks(res)[0].Items[0].Text().AsString(); n != "0" {
		t.Errorf("%s rows, want none", n)
	}
	// inline, the View itself throws
	_, err = Render(context.Background(), mustJSON(t, `{"data":[{"name":"d","values":[{"a":1},null],"format":{"type":"json"}}]}`), Options{})
	if err == nil || !strings.Contains(err.Error(), "Cannot read properties of null") {
		t.Errorf("inline null row: err = %v", err)
	}
}
