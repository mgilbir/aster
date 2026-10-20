package vega

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// envProbe is a render's probe: what the heap retains, after a collection,
// and what the call's ledger holds, at the moment a data set evaluated after
// the hostile calls reports its warning. The modify() change sets the calls
// record are still pending then: they are applied after the propagation.
type envProbe struct {
	heap, charged int64
	seen          bool
}

// envRows is how many rows each case evaluates its calls for.
const envRows = 500

// envCall is an expression environment method's case: the specification's
// own data and scales (decls), and an expression calling the method, which
// each of envRows rows evaluates k times.
type envCall struct {
	decls string
	call  string
	k     int // the calls a row of the first render; 16 when 0
	// unreachable says why a render cannot call the method: nothing to meter.
	unreachable string
}

// envSpec has a data set d of envRows rows, a projection p, scales x (band,
// envRows bands) and c (linear), a tree data set tr, a selection store st,
// then the case's declarations; data set h evaluates the call k times per
// row, discarding the results, and data set probe, which reads h, warns.
func envSpec(c envCall, k int) string {
	calls := make([]string, k)
	for i := range calls {
		calls[i] = c.call
	}
	return fmt.Sprintf(`{"width":100,"height":100,
	"projections":[{"name":"p","type":"mercator"}],
	"signals":[{"name":"s","value":[1,2,3]}],
	"data":[
		{"name":"d","transform":[{"type":"sequence","start":0,"stop":%[1]d,"as":"a"},{"type":"formula","as":"p","expr":"datum.a == 0 ? null : floor((datum.a - 1) / 2)"}]},
		{"name":"tr","source":"d","transform":[{"type":"stratify","key":"a","parentKey":"p"}]},
		{"name":"st","values":[{"unit":"u","fields":[{"field":"a","type":"E"}],"values":[1]}]}
		%[2]s,
		{"name":"h","source":"d","transform":[{"type":"formula","as":"z","expr":"[%[3]s].length && 0"}]},
		{"name":"probe","values":[{}],"transform":[{"type":"formula","as":"w","expr":"warn('probe', length(data('h')))"}]}],
	"scales":[{"name":"x","type":"band","domain":{"data":"d","field":"a"},"range":"width"},
		{"name":"c","type":"linear","domain":[0,10],"range":["red","blue"]}]}`, envRows, c.decls, strings.Join(calls, ","))
}

// envCalls has a case for every method of the expression environment
// (rtContext): each calls the method as hostile repetition would.
var envCalls = map[string]envCall{
	"Signal":          {call: "s"},
	"Data":            {call: "data('d')"},
	"InData":          {call: "indata('d', 'a', 1)"},
	"IsTuple":         {call: "isTuple(datum)"},
	"Log":             {call: "warn('w')"},
	"EventFunction":   {call: "x()", unreachable: "a headless event never has the vega extension: x() and the rest throw before the method"},
	"ContainerSize":   {call: "containerSize()"},
	"Screen":          {call: "screen()"},
	"WindowSize":      {call: "windowSize()"},
	"Encode":          {call: "encode(datum, 'update', 1)"},
	"InScope":         {call: "inScope(datum)"},
	"Intersect":       {call: "intersect([0,0,1,1])"},
	"IntersectLasso":  {call: "intersectLasso('m', [])"},
	"GeoArea":         {call: "geoArea('p', {type: 'Sphere'})"},
	"GeoBounds":       {call: "geoBounds('p', {type: 'Sphere'})"},
	"GeoCentroid":     {call: "geoCentroid('p', {type: 'Sphere'})"},
	"GeoScale":        {call: "geoScale('p')"},
	"GeoTranslate":    {call: "geoTranslate('p')"},
	"GeoShape":        {call: "geoShape('p', {type: 'Sphere'})", k: 64},
	"PathShape":       {call: "pathShape('M0,0L1,1')", k: 64},
	"TreePath":        {call: "treePath('tr', 5, 400)"},
	"TreeAncestors":   {call: "treeAncestors('tr', 400)"},
	"Scale":           {call: "scale('x', datum.a)"},
	"Invert":          {call: "invert('x', [0, 100])"},
	"Domain":          {call: "domain('x')"},
	"Range":           {call: "range('x')"},
	"Bandwidth":       {call: "bandwidth('x')"},
	"Copy":            {call: "copy('x')"},
	"EncodeScale":     {call: "_scale('x', datum.a)"},
	"EncodeRange":     {call: "_range('x')"},
	"EncodeBandwidth": {call: "_bandwidth('x')"},
	"Gradient":        {call: "gradient('c', [0,0], [1,0], 300)", k: 4},
	"Modify":          {decls: `,{"name":"m","values":[{"a":1}]}`, call: "modify('m', null, data('d'))", k: 4},
	"SetData":         {decls: `,{"name":"m","values":[{"a":1}]}`, call: "setdata('m', data('d'))", k: 4},
	"UnitCounts":      {call: "vlSelectionTest('st', datum, 'intersect')"},
	"Modify/values":   {decls: `,{"name":"m","values":[{"a":1}]}`, call: "modify('m', null, null, null, datum, {a: 2, b: 3})"},
}

// probeRender renders the case with k calls a row and probes it.
func probeRender(t *testing.T, c envCall, k int) envProbe {
	t.Helper()
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	ctx := budget.WithLedger(context.Background(), led)
	var p envProbe
	_, err := Render(ctx, vegaSpec(t, envSpec(c, k)), Options{Report: func(err error) error {
		if w, ok := err.(Warning); ok && strings.HasPrefix(string(w), "EXPRESSION probe") {
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			p = envProbe{heap: int64(ms.HeapAlloc), charged: led.Bytes(), seen: true}
		}
		return nil
	}})
	if err != nil {
		t.Fatalf("k=%d: %v", k, err)
	}
	if !p.seen {
		t.Fatalf("k=%d: the probe did not run", k)
	}
	return p
}

// Every method of the expression environment that allocates or keeps state
// for an expression either charges the call's ledger for what it keeps, or
// keeps nothing once its result is dropped (C4, C5, audit 2026-10-18:
// geoShape() handles and modify() change sets were kept for the whole
// render, outside every budget). The methods are enumerated from the
// environment's type, so a new one fails here until it has a case. Each case
// calls the method k and 2k times a row over envRows rows, dropping the
// results; what the heap retains more at 2k (after a collection, measured
// while the change sets are still pending) must be charged to the ledger:
// at most twice the extra charge, and a MiB of slack.
func TestEveryEnvMethodIsMetered(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("the heap the race detector keeps is not the render's")
	}
	typ := reflect.TypeOf(&rtContext{})
	var methods []string
	for i := range typ.NumMethod() {
		methods = append(methods, typ.Method(i).Name)
	}
	sort.Strings(methods)
	for _, m := range methods {
		if _, ok := envCalls[m]; !ok {
			t.Errorf("the environment method %s has no case", m)
		}
	}
	for _, name := range sortedKeys(envCalls) {
		c := envCalls[name]
		if c.unreachable != "" {
			continue
		}
		k := c.k
		if k == 0 {
			k = 16
		}
		t.Run(name, func(t *testing.T) {
			a, b := probeRender(t, c, k), probeRender(t, c, 2*k)
			retained, charged := b.heap-a.heap, b.charged-a.charged
			t.Logf("%s: %d more calls retain %d KiB, charged %d KiB", name, k*envRows, retained>>10, charged>>10)
			if retained > 2*charged+1<<20 {
				t.Errorf("%d more calls of %s retain %d KiB but charge %d KiB", k*envRows, c.call, retained>>10, charged>>10)
			}
		})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// viewItems is how many items the cases of TestViewsHandedToExpressionsAreCharged draw.
const viewItems = 4000

// viewCase renders a mark m of viewItems symbols whose size reads size, then
// a text mark whose text reads text (encoded after m's items exist); it
// returns what the heap retains after a collection, and what the ledger
// holds, once the dataflow has run (the view, and every copy it keeps for
// the run, still alive).
func viewCase(t *testing.T, size, text string) envProbe {
	t.Helper()
	spec := fmt.Sprintf(`{"width":100,"height":100,
	"data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":%d,"as":"a"}]}],
	"marks":[{"type":"symbol","name":"m","from":{"data":"d"},"encode":{"enter":{"x":{"field":"a"},"size":{"signal":%q}}}},
		{"type":"text","encode":{"enter":{"text":{"signal":%q}}}}]}`, viewItems, size, text)
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	var p envProbe
	_, err := Render(budget.WithLedger(context.Background(), led), vegaSpec(t, spec), Options{Stages: func(name string, _ time.Duration) {
		if name == "dataflow" {
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			p = envProbe{heap: int64(ms.HeapAlloc), charged: led.Bytes(), seen: true}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.seen {
		t.Fatal("the probe did not run")
	}
	return p
}

// What the view copies out of its state to hand an expression, and keeps
// for the run, is charged to the call's ledger: an item's tuple view
// (`data()` of a mark, `item.mark.items`) and the array of a mark's items.
// Each case renders the same chart with and without the expression that
// reads them; what the heap retains more must be charged (at most twice the
// extra charge, and a MiB of slack).
func TestViewsHandedToExpressionsAreCharged(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("the heap the race detector keeps is not the render's")
	}
	for _, c := range []struct{ name, size, expr string }{
		{"data of a mark", "4", "length(data('m'))"},
		{"mark.items", "length(item.mark.items) * 0 + 4", "'1'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := viewCase(t, "4", "'1'")
			got := viewCase(t, c.size, c.expr)
			retained, charged := got.heap-base.heap, got.charged-base.charged
			t.Logf("%s: retains %d KiB more, charged %d KiB more", c.name, retained>>10, charged>>10)
			if retained > 2*charged+1<<20 {
				t.Errorf("%s retains %d KiB but charges %d KiB", c.name, retained>>10, charged>>10)
			}
		})
	}
}

// An operator's value that is a list of values reaches an expression as an
// array copied out of it, charged as the evaluator's arrays are; a list of
// tuples (a collect's, through its signal) is shared, as upstream's is.
func TestOperatorValuesHandedToExpressionsAreCharged(t *testing.T) {
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	v := newView(budget.WithLedger(context.Background(), led), Options{}, jsval.Undefined)
	v.g = newGraph(v.ctx)
	list := make([]any, 10_000)
	for i := range list {
		list[i] = jsval.Int(i)
	}
	before := led.Bytes()
	if got := v.exprValue(list); got.Len() != len(list) {
		t.Fatalf("a list of %d values reads as %d", len(list), got.Len())
	}
	if charged, want := led.Bytes()-before, int64(len(list))*envItem; charged < want {
		t.Errorf("a list of %d values copied for an expression charged %d bytes; want %d", len(list), charged, want)
	}
	tuples := []jsval.Value{jsval.Int(1), jsval.Int(2)}
	before = led.Bytes()
	if got := v.exprValue(tuples); &got.Items()[0] != &tuples[0] || led.Bytes() != before {
		t.Errorf("a list of tuples was copied or charged; upstream shares it")
	}
}
