package aster_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// Sweeps compare the engine with the node oracle over generated cases rather
// than charts somebody drew: one chart per property value a schema declares,
// per unusual data value, per signal write. The cases come from a generator
// under testdata/oracle-node/sweeps, run by the oracle (and cached with its
// answers), so nothing generated is committed. Each sweep has an expectation
// file, testdata/sweeps/<name>.txt (see expect_test.go), and a floor on its
// number of cases, so a generator that silently produces less fails.
var (
	sweepRun    = flag.String("sweep.run", "", "only run sweep cases whose id contains this substring")
	sweepUpdate = flag.Bool("sweep.update", false, "rewrite the sweep expectation files with the observed statuses")
	sweepReport = flag.String("sweep.report", "", "write a per-case TSV report for each sweep into this directory")
	sweepV      = flag.Bool("sweep.v", false, "log every non-ok case's first difference")
)

// sweepMode is what a sweep compares.
type sweepMode int

const (
	compareSVG     sweepMode = iota // render the case, compare the SVG
	compareVega                     // compile a Vega-Lite case, compare the Vega
	compareSignals                  // render, write signals, render again; compare the second SVG
	compareLite                     // a Vega-Lite case: compare the SVG and, as a second status, the compiled Vega
)

type sweep struct {
	name  string
	mode  sweepMode
	floor int // the generator must produce at least this many cases
}

// sweepCase is one generated case.
type sweepCase struct {
	Name     string          `json:"name"`
	Family   string          `json:"family"`
	Property string          `json:"property,omitempty"`
	Lite     bool            `json:"lite,omitempty"`
	Spec     json.RawMessage `json:"spec"`
	// SpecText, when set, is the spec as text, for a value JSON cannot carry
	// through the generator's output (a negative zero); it replaces Spec.
	SpecText string `json:"specText,omitempty"`
	// Writes are the signal writes of a signal-sweep case.
	Writes []oracle.SignalWrite `json:"writes,omitempty"`
	// Zone, when set, is the IANA time zone both sides render in: node runs
	// with that TZ and the engine with WithTimezone.
	Zone string `json:"zone,omitempty"`
}

// sweepSkip is a part of the declared surface the generator did not sweep,
// with the reason; it is counted so coverage is a number.
type sweepSkip struct {
	Family   string `json:"family"`
	Property string `json:"property"`
	Reason   string `json:"reason"`
}

func runSweep(t *testing.T, sw sweep) {
	if testing.Short() {
		t.Skip("slow: a sweep renders thousands of charts")
	}
	o := oracle.For(t, oracle.VL6)
	gen, err := o.Generate(sw.name)
	if err != nil {
		t.Fatalf("generating %s: %v", sw.name, err)
	}
	if gen.Err != "" {
		t.Fatalf("generating %s: %s", sw.name, gen.Err)
	}
	var out struct {
		Cases []sweepCase `json:"cases"`
		Skips []sweepSkip `json:"skips"`
	}
	if err := json.Unmarshal(gen.Data, &out); err != nil {
		t.Fatalf("generator output: %v", err)
	}
	if len(out.Cases) < sw.floor {
		t.Fatalf("%s generated %d cases, fewer than its floor of %d", sw.name, len(out.Cases), sw.floor)
	}
	for i := range out.Cases {
		if out.Cases[i].SpecText != "" {
			out.Cases[i].Spec = json.RawMessage(out.Cases[i].SpecText)
		}
	}
	seen := map[string]bool{}
	for _, c := range out.Cases {
		if seen[c.Name] {
			t.Fatalf("%s: duplicate case name %q", sw.name, c.Name)
		}
		seen[c.Name] = true
	}
	t.Logf("%s: %d cases, %d skipped properties; oracle: %s", sw.name, len(out.Cases), len(out.Skips), o.Version())

	// One oracle and one converter per time zone the cases use.
	type side struct {
		o *oracle.Oracle
		c *aster.Converter
	}
	sides := map[string]side{"": {o, oracleConverter(t, o)}}
	for _, sc := range out.Cases {
		if _, ok := sides[sc.Zone]; !ok {
			zo := oracle.ForZone(t, oracle.VL6, sc.Zone)
			sides[sc.Zone] = side{zo, oracleConverter(t, zo, aster.WithTimezone(sc.Zone))}
		}
	}
	var cases []sweepCase
	for _, sc := range out.Cases {
		if *sweepRun == "" || strings.Contains(sc.Name, *sweepRun) {
			cases = append(cases, sc)
		}
	}
	results := make([]specResult, len(cases))
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				s := sides[cases[i].Zone]
				results[i] = runSweepCase(t, s.o, s.c, sw.mode, cases[i])
			}
		}()
	}
	for i := range cases {
		next <- i
	}
	close(next)
	wg.Wait()

	sweepScoreboard(t, sw, cases, results, out.Skips)
	if *sweepRun == "" || *sweepUpdate {
		checkExpectations(t, filepath.Join("testdata", "sweeps", sw.name+".txt"), *sweepUpdate, results)
	}
}

func runSweepCase(t *testing.T, o *oracle.Oracle, c *aster.Converter, mode sweepMode, sc sweepCase) specResult {
	r := specResult{id: sc.Name}
	var want oracle.Result
	var err error
	var got string
	var gerr error
	switch mode {
	case compareVega:
		return compareCompiled(t, o, c, sc)
	case compareLite:
		r = runSweepCase(t, o, c, compareSVG, sc)
		if r.status == "oracle-failure" {
			return r
		}
		// As compareOne does: the compiled Vega counts only when upstream
		// compiled the case and the engine did too.
		if v := compareCompiled(t, o, c, sc); v.status == "vega-differ" {
			if r.status == "ok" {
				r.status, r.detail = "vega-differ", ""
			} else {
				r.status += "+vega-differ"
			}
			r.detail += vegaDetailMarker + v.detail
		}
		return r
	case compareSignals:
		want, err = o.Signals(sc.Spec, sc.Writes)
		ws := make([][2]string, len(sc.Writes))
		for i, w := range sc.Writes {
			ws[i] = [2]string{w.Name, string(w.Value)}
		}
		got, gerr = c.VegaToSVGAfterSignalWritesForTest(sc.Spec, ws)
	default:
		want, err = o.SVG(sc.Lite, sc.Spec)
		if sc.Lite {
			got, gerr = c.VegaLiteToSVG(sc.Spec)
		} else {
			got, gerr = c.VegaToSVG(sc.Spec)
		}
	}
	if err != nil {
		t.Errorf("%s: oracle: %v", sc.Name, err)
		r.status = "oracle-failure"
		return r
	}
	switch {
	case gerr != nil && want.Err != "":
		r.status, r.svg = "ok", "both-error" // agreement: upstream refuses it too
	case gerr != nil:
		r.status, r.svg, r.detail = "engine-error", "engine-error", firstLine(gerr.Error())
	case want.Err != "":
		r.status, r.svg, r.detail = "node-error", "node-error", firstLine(want.Err)
	default:
		d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
		switch {
		case err != nil:
			r.status, r.svg, r.detail = "differ", "differ", err.Error()
		case d.Identical:
			r.status, r.svg = "ok", "identical"
		case d.Equal:
			r.status, r.svg = "ok", "equal"
		default:
			r.status, r.svg, r.detail = "differ", "differ", fmt.Sprintf("%d diffs; first: %s", d.DiffCount, firstLine(d.Diff))
		}
	}
	return r
}

// compareCompiled compares the engine's Vega-Lite compilation of a case with
// upstream's, exactly (as the corpus comparison does).
func compareCompiled(t *testing.T, o *oracle.Oracle, c *aster.Converter, sc sweepCase) specResult {
	r := specResult{id: sc.Name}
	want, err := o.Compile(sc.Spec)
	if err != nil {
		t.Errorf("%s: oracle: %v", sc.Name, err)
		r.status = "oracle-failure"
		return r
	}
	got, gerr := c.VegaLiteToVega(sc.Spec)
	switch {
	case gerr != nil && want.Err != "":
		r.status, r.svg = "ok", "both-error"
	case gerr != nil:
		r.status, r.svg, r.detail = "engine-error", "engine-error", firstLine(gerr.Error())
	case want.Err != "":
		r.status, r.svg, r.detail = "node-error", "node-error", firstLine(want.Err)
	default:
		g, err1 := jsval.ParseJSON(got)
		w, err2 := jsval.ParseJSON(want.Vega)
		switch {
		case err1 != nil || err2 != nil:
			r.status, r.svg, r.detail = "vega-differ", "differ", fmt.Sprintf("unparsable: %v / %v", err1, err2)
		case jsval.Equal(g, w):
			r.status, r.svg = "ok", "identical"
		default:
			r.status, r.svg, r.detail = "vega-differ", "differ", firstJSONDiff(g, w, "")
		}
	}
	return r
}

// vegaDetailMarker separates the SVG difference from the compiled-Vega
// difference in the detail of a case that has both.
const vegaDetailMarker = " | vega: "

// firstJSONDiff names the first place two JSON values differ, in key order of
// want then got.
func firstJSONDiff(got, want jsval.Value, path string) string {
	if jsval.Equal(got, want) {
		return ""
	}
	switch {
	case got.IsObj() && want.IsObj():
		for _, k := range want.ObjValue().Keys() {
			if d := firstJSONDiff(got.Get(k), want.Get(k), path+"."+k); d != "" {
				return d
			}
		}
		for _, k := range got.ObjValue().Keys() {
			if !want.ObjValue().Has(k) {
				return fmt.Sprintf("%s.%s: extra %s", path, k, short(got.Get(k)))
			}
		}
		return path + ": key order"
	case got.IsArr() && want.IsArr():
		ga, wa := got.Items(), want.Items()
		for i := 0; i < min(len(ga), len(wa)); i++ {
			if d := firstJSONDiff(ga[i], wa[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return fmt.Sprintf("%s: length %d, want %d", path, len(ga), len(wa))
	}
	return fmt.Sprintf("%s: %s, want %s", path, short(got), short(want))
}

func short(v jsval.Value) string {
	s := string(jsval.AppendJSON(nil, v))
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// causeShape strips the numbers and quoted strings from a difference, so
// differences with one cause tally together.
var causeNoise = regexp.MustCompile(`"[^"]*"|-?\d+(\.\d+)?(e-?\d+)?|\[\d+\]`)

func causeShape(detail string) string {
	if i := strings.Index(detail, "first: "); i >= 0 {
		detail = detail[i+len("first: "):]
	}
	s := causeNoise.ReplaceAllString(detail, "#")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func sweepScoreboard(t *testing.T, sw sweep, cases []sweepCase, results []specResult, skips []sweepSkip) {
	type tally struct{ n, ok, bothErr int }
	families := map[string]*tally{}
	var order []string
	causes := map[string]int{}
	for i, r := range results {
		f := cases[i].Family
		tl := families[f]
		if tl == nil {
			tl = &tally{}
			families[f] = tl
			order = append(order, f)
		}
		tl.n++
		if r.status == "ok" {
			tl.ok++
			if r.svg == "both-error" {
				tl.bothErr++
			}
			continue
		}
		if svg, vg, both := strings.Cut(r.detail, vegaDetailMarker); both || strings.HasSuffix(r.status, "vega-differ") {
			if both && svg != "" {
				causes[strings.TrimSuffix(r.status, "+vega-differ")+": "+causeShape(svg)]++
			}
			if !both {
				vg = svg
			}
			causes["vega-differ: "+causeShape(vg)]++
		} else {
			causes[r.status+": "+causeShape(r.detail)]++
		}
		if *sweepV {
			t.Logf("%s: %s: %s", r.id, r.status, r.detail)
		}
	}
	sort.Strings(order)
	var b strings.Builder
	total, ok := 0, 0
	fmt.Fprintf(&b, "\n%-36s %6s %6s %8s\n", "family", "cases", "ok", "refused")
	for _, f := range order {
		tl := families[f]
		total += tl.n
		ok += tl.ok
		fmt.Fprintf(&b, "%-36s %6d %6d %8d\n", f, tl.n, tl.ok, tl.bothErr)
	}
	fmt.Fprintf(&b, "%-36s %6d %6d\n", "total", total, ok)
	type cause struct {
		shape string
		n     int
	}
	var cs []cause
	for s, n := range causes {
		cs = append(cs, cause{s, n})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].n > cs[j].n || cs[i].n == cs[j].n && cs[i].shape < cs[j].shape })
	if len(cs) > 0 {
		b.WriteString("\nranked causes:\n")
		for _, c := range cs[:min(25, len(cs))] {
			fmt.Fprintf(&b, "%6d  %s\n", c.n, c.shape)
		}
	}
	if len(skips) > 0 {
		reasons := map[string]int{}
		for _, s := range skips {
			reasons[s.Reason]++
		}
		b.WriteString("\nskipped properties by reason:\n")
		var rs []string
		for r := range reasons {
			rs = append(rs, r)
		}
		sort.Slice(rs, func(i, j int) bool { return reasons[rs[i]] > reasons[rs[j]] })
		for _, r := range rs {
			fmt.Fprintf(&b, "%6d  %s\n", reasons[r], r)
		}
	}
	t.Log(b.String())
	if *sweepReport != "" {
		var rep strings.Builder
		rep.WriteString("id\tfamily\tstatus\tresult\tdetail\n")
		for i, r := range results {
			fmt.Fprintf(&rep, "%s\t%s\t%s\t%s\t%s\n", r.id, cases[i].Family, r.status, r.svg, strings.ReplaceAll(r.detail, "\t", " "))
		}
		if err := os.MkdirAll(*sweepReport, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(*sweepReport, sw.name+".tsv"), []byte(rep.String()), 0o644)
		}
	}
}
