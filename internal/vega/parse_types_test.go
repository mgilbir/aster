package vega

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// format.parse "auto" parses Object.keys of the object upstream's
// inferTypes builds: each field once, array-index keys first and ascending,
// the rest in the order they first appear, and no __proto__, whose
// assignment of a string sets nothing. Node 24 lists the keys below for
// fields.reduce((t, k) => (t[k] = "string", t), {}).
func TestTypeKeysAreObjectKeys(t *testing.T) {
	v := newView(context.Background(), Options{}, jsval.Undefined)
	fields := []string{"b", "10", "2", "b", "01", "4294967295", "4294967294", "__proto__", "a", "-1", "1.5", "0"}
	want := []string{"0", "2", "10", "4294967294", "b", "01", "4294967295", "a", "-1", "1.5"}
	if got := v.typeKeys(fields); !slices.Equal(got, want) {
		t.Errorf("typeKeys(%q) = %q; want %q", fields, got, want)
	}
}

// The parsers run in that order, so it is the field a missing one is added
// in and the field an illegal pattern names (upstream, with node 24:
// "Illegal format pattern: 2:bad2", and the rows below, whose array-index
// key "2" comes first, as a JavaScript object lists its keys: C154, audit
// 2026-10-18).
func TestParseRunsInObjectKeysOrder(t *testing.T) {
	v := newView(context.Background(), Options{}, jsval.Undefined)
	read := func(values, schema string) ([]jsval.Value, error) {
		d, err := jsval.ParseJSONString(values)
		if err != nil {
			t.Fatal(err)
		}
		s, err := jsval.ParseJSONString(schema)
		if err != nil {
			t.Fatal(err)
		}
		return v.readRows(d, nil, s)
	}
	if _, err := read(`"[{\"b\":\"1\"}]"`, `{"type":"json","parse":{"b":"bad","2":"bad2"}}`); err == nil || err.Error() != "Illegal format pattern: 2:bad2" {
		t.Errorf("error %v; want Illegal format pattern: 2:bad2", err)
	}
	rows, err := read(`"[{\"b\":\"1\",\"2\":\"x\",\"__proto__\":\"1\"},{\"c\":1}]"`, `{"type":"json","parse":"auto"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(jsval.AppendJSON(nil, jsval.Arr(rows))), `[{"2":"x","b":1,"__proto__":"1"},{"2":null,"c":1,"b":null}]`; got != want {
		t.Errorf("rows %s; want %s", got, want)
	}
}

// Finding the fields' repeats is linear in the fields, and charged as it
// goes (ADR 0010): a header of n columns, each twice, is charged about
// twice the work of one of n/2. Before, each field was looked for in the
// list of those already found, n²/2 comparisons for n distinct fields,
// uncharged. Counted in the ledger's work, not timed: the quadratic
// coefficient (W(2n) − 2·W(n)) / 2n² is about 1/n here and about 1/2 for a
// search that charged each comparison.
func TestParseAutoFieldsAreLinear(t *testing.T) {
	work := func(n int) int64 {
		led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		v := newView(budget.WithLedger(context.Background(), led), Options{}, jsval.Undefined)
		cols := make([]string, 0, 2*n)
		row := jsval.NewObject(n)
		for i := range n {
			c := fmt.Sprintf("c%d", i)
			cols = append(cols, c)
			row.Set(c, jsval.Str("1"))
		}
		cols = append(cols, cols...)
		if err := v.applyParse([]jsval.Value{jsval.Obj(row)}, cols, jsval.Str("auto")); err != nil {
			t.Fatal(err)
		}
		// The view's meter holds under a poll's worth (1,024) that has
		// not reached the ledger, against some 150,000 charged.
		return led.WorkDone()
	}
	const n = 8000
	w1, w2 := work(n), work(2*n)
	coef := float64(w2-2*w1) / (2 * n * n)
	t.Logf("work %d for %d fields, %d for %d: quadratic coefficient %.2g", w1, 2*n, w2, 4*n, coef)
	if coef > 0.01 {
		t.Errorf("twice the fields charge %d work, %.2f times %d: a quadratic coefficient of %.2g, want under 0.01", w2, float64(w2)/float64(w1), w1, coef)
	}
}

// A JSON document a data set holds as a string is parsed where it is: the
// string, as long as the document, is not copied first (nor charged for
// a copy). Counted in bytes allocated (not under -race): a megabyte of
// white space around one value allocates a few hundred bytes, where the
// copy was a megabyte.
func TestJSONStringValuesAreNotCopied(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("-race changes what is allocated")
	}
	doc := jsval.Str("[" + strings.Repeat(" ", 1<<20) + "1]")
	v := newView(context.Background(), Options{}, jsval.Undefined)
	v.parseJSONText(doc, nil)
	runtime.GC()
	before := totalAlloc()
	v.parseJSONText(doc, nil)
	if got := totalAlloc() - before; got > 64<<10 {
		t.Errorf("parsing a %d-byte JSON string allocated %d bytes; want under 64 KiB", len(doc.StrValue()), got)
	}
}
