package purego_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/purego"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/svgdiff"
)

var (
	compareSets    = flag.String("compare.sets", "vg-fixtures,vl-fixtures,vl-examples,vl-convert,vg-gallery,regress-vg,regress-vl", "comma-separated corpus sets to compare")
	compareFilter  = flag.String("compare.run", "", "only compare specs whose name contains this substring")
	compareReport  = flag.String("compare.report", "", "write a per-spec markdown report to this path")
	compareVerbose = flag.Bool("compare.v", false, "log every spec's outcome")
)

// corpusSet is one directory of specs rendered by both engines.
type corpusSet struct {
	name string
	glob string
	lite bool
}

var corpusSets = []corpusSet{
	{"vg-fixtures", "testdata/corpus/vega/*.vg.json", false},
	{"vg-gallery", "testdata/corpus/vg-gallery/*.vg.json", false},
	{"regress-vg", "testdata/corpus/regress/*.vg.json", false},
	{"regress-vl", "testdata/corpus/regress/*.vl.json", true},
	{"vl-fixtures", "testdata/corpus/vegalite/*.vl.json", true},
	{"vl-examples", "../testdata/vega-lite/v6.4.3/specs/*.vl.json", true},
	{"vl-convert", "../testdata/vl-convert/*.vl.json", true},
}

// oracleDir caches the reference engine's output; it is git-ignored and
// rebuilt on demand, keyed by a hash of the spec.
const oracleDir = "testdata/oracle-cache"

// oracleEngineVersion is part of every cache key. Bump it whenever the
// reference engine's output changes for the same spec (a re-vendored Vega, a
// change to its runtime or text measurement), so stale renderings are never
// compared.
const oracleEngineVersion = "vega-6.4.0/vl-6.4.3/exact-text-1"

type outcome int

const (
	outIdentical outcome = iota
	outEqual
	outDiffer
	outPuregoError
	outOracleError
	outRefDiverges
)

var outcomeNames = []string{"identical", "equal", "differ", "purego-error", "oracle-error", "ref-diverges"}

// referenceDiverges lists specs where the reference engine (QuickJS) is the
// one that departs from upstream Vega running in V8, checked against
// node-rendered output: purego is expected to differ from the reference here.
var referenceDiverges = map[string]string{
	"histogram_nonlinear":          "QuickJS formats Infinity without Intl (\"Infinity\" vs V8's \"∞\")",
	"trail_color":                  "QuickJS trigonometry differs from V8 in the last bit",
	"trail":                        "QuickJS trigonometry differs from V8 in the last bit",
	"bar_grouped_thin":             "QuickJS sorts an inconsistent mixed-type comparator differently from V8's TimSort",
	"bar_grouped_thin_minBandSize": "QuickJS sorts an inconsistent mixed-type comparator differently from V8's TimSort",
	// Found by the differential fuzzer (testdata/corpus/regress); each confirmed
	// against upstream in node (testdata/nodesvg.mjs).
	"string-domain-iterates-chars":          "QuickJS's Date.prototype.toString names the zone \"(UTC)\" where V8 says \"(Coordinated Universal Time)\", so a domain made of the characters of that string has different letters",
	"filter-timeunit-on-numeric-csv-column": "QuickJS's Date.parse accepts strings such as \"0.0\" (the year 2000) that V8 reads as an invalid date",
	"trail-mark-many-arcs":                  "QuickJS trigonometry differs from V8 in the last bit",
	// These draw the current time (now()), so no cached reference can match.
	"clock": "draws the current time",
	"watch": "draws the current time",
}

type specResult struct {
	set, name string
	outcome   outcome
	detail    string
	vegaMatch string // VL only: "equal", "differ", "error", ""
}

// TestCompareWithReference renders the corpus with the pure-Go engine and the
// reference (QuickJS) engine and compares the SVG structurally. It reports a
// scoreboard rather than failing, because it measures progress; regressions
// against a recorded baseline are what should fail once the engine is
// complete.
func TestCompareWithReference(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: renders the whole corpus with both engines")
	}
	ld := corpusLoader(t)
	pg, err := purego.New(purego.WithLoader(ld), purego.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	var ref *aster.Converter // created lazily: only needed on a cache miss
	refConverter := func() (*aster.Converter, error) {
		if ref != nil {
			return ref, nil
		}
		var err error
		ref, err = aster.New(aster.WithLoader(ld), aster.WithTimeout(120*time.Second))
		return ref, err
	}
	defer func() {
		if ref != nil {
			ref.Close()
		}
	}()

	wanted := map[string]bool{}
	for _, s := range strings.Split(*compareSets, ",") {
		wanted[strings.TrimSpace(s)] = true
	}
	var results []specResult
	for _, set := range corpusSets {
		if !wanted[set.name] {
			continue
		}
		files, err := filepath.Glob(set.glob)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(files)
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".json")
			name = strings.TrimSuffix(strings.TrimSuffix(name, ".vl"), ".vg")
			if *compareFilter != "" && !strings.Contains(name, *compareFilter) {
				continue
			}
			r := compareOne(t, pg, refConverter, set, f, name)
			if *compareVerbose {
				t.Logf("%s/%s: %s %s", set.name, name, outcomeNames[r.outcome], r.detail)
			}
			results = append(results, r)
		}
	}
	summarize(t, results)
}

func compareOne(t *testing.T, pg *purego.Converter, refConv func() (*aster.Converter, error), set corpusSet, file, name string) specResult {
	res := specResult{set: set.name, name: name}
	spec, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want, vegaWant, err := oracle(refConv, set, name, spec)
	if err != nil {
		res.outcome, res.detail = outOracleError, err.Error()
		return res
	}
	var got string
	if set.lite {
		got, err = pg.VegaLiteToSVG(spec)
		if vegaWant != nil {
			res.vegaMatch = compareVega(pg, spec, vegaWant)
		}
	} else {
		got, err = pg.VegaToSVG(spec)
	}
	if err != nil {
		res.outcome, res.detail = outPuregoError, firstLine(err.Error())
		return res
	}
	d, err := svgdiff.Compare([]byte(got), want, svgdiff.DefaultOptions)
	switch {
	case err != nil:
		res.outcome, res.detail = outDiffer, err.Error()
	case d.Identical:
		res.outcome = outIdentical
	case d.Equal:
		res.outcome = outEqual
	default:
		res.outcome, res.detail = outDiffer, fmt.Sprintf("%d diffs; first: %s", d.DiffCount, firstLine(d.Diff))
		if why, ok := referenceDiverges[name]; ok {
			res.outcome, res.detail = outRefDiverges, why
		}
	}
	return res
}

func compareVega(pg *purego.Converter, spec, want []byte) string {
	got, err := pg.VegaLiteToVega(spec)
	if err != nil {
		return "error"
	}
	g, err1 := jsval.ParseJSON(got)
	w, err2 := jsval.ParseJSON(want)
	if err1 != nil || err2 != nil {
		return "error"
	}
	if jsval.Equal(g, w) {
		return "equal"
	}
	return "differ"
}

// oracle returns the reference engine's SVG (and, for Vega-Lite, its compiled
// Vega) for spec, from the cache when the spec is unchanged.
func oracle(refConv func() (*aster.Converter, error), set corpusSet, name string, spec []byte) (svg, vega []byte, err error) {
	sum := sha256.Sum256(append([]byte(oracleEngineVersion+"\x00"), spec...))
	key := hex.EncodeToString(sum[:8])
	dir := filepath.Join(oracleDir, set.name)
	base := filepath.Join(dir, name+"."+key)
	if b, err := os.ReadFile(base + ".svg"); err == nil {
		v, _ := os.ReadFile(base + ".vg.json")
		return b, v, nil
	}
	if b, err := os.ReadFile(base + ".err"); err == nil {
		return nil, nil, errors.New(string(b))
	}
	ref, err := refConv()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	var s string
	if set.lite {
		s, err = ref.VegaLiteToSVG(spec)
		if err == nil {
			vega, _ = ref.VegaLiteToVega(spec)
		}
	} else {
		s, err = ref.VegaToSVG(spec)
	}
	if err != nil {
		_ = os.WriteFile(base+".err", []byte(firstLine(err.Error())), 0o644)
		return nil, nil, err
	}
	if err := os.WriteFile(base+".svg", []byte(s), 0o644); err != nil {
		return nil, nil, err
	}
	if vega != nil {
		_ = os.WriteFile(base+".vg.json", vega, 0o644)
	}
	return []byte(s), vega, nil
}

func summarize(t *testing.T, results []specResult) {
	type tally struct {
		n       int
		counts  [6]int
		vgEqual int
		vgTotal int
	}
	bySet := map[string]*tally{}
	var order []string
	for _, r := range results {
		tl := bySet[r.set]
		if tl == nil {
			tl = &tally{}
			bySet[r.set] = tl
			order = append(order, r.set)
		}
		tl.n++
		tl.counts[r.outcome]++
		if r.vegaMatch != "" {
			tl.vgTotal++
			if r.vegaMatch == "equal" {
				tl.vgEqual++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s %6s %9s %6s %6s %12s %12s %12s %10s\n", "set", "specs", "identical", "equal", "differ", "purego-error", "oracle-error", "ref-diverges", "VL→Vega=")
	for _, s := range order {
		tl := bySet[s]
		fmt.Fprintf(&b, "%-12s %6d %9d %6d %6d %12d %12d %12d %5d/%-4d\n", s, tl.n, tl.counts[0], tl.counts[1], tl.counts[2], tl.counts[3], tl.counts[4], tl.counts[5], tl.vgEqual, tl.vgTotal)
	}
	t.Log(b.String())
	if *compareReport != "" {
		var md strings.Builder
		md.WriteString("# purego vs reference engine\n\n```" + b.String() + "```\n\n| set | spec | outcome | VL→Vega | detail |\n|---|---|---|---|---|\n")
		for _, r := range results {
			fmt.Fprintf(&md, "| %s | %s | %s | %s | %s |\n", r.set, r.name, outcomeNames[r.outcome], r.vegaMatch, strings.ReplaceAll(r.detail, "|", "\\|"))
		}
		if err := os.WriteFile(*compareReport, []byte(md.String()), 0o644); err != nil {
			t.Error(err)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// corpusLoader serves the corpus datasets: the root package's vega-datasets
// copy, the extra datasets under testdata/data, and the CDN URLs the
// Vega-Lite examples use, redirected to a local server.
func corpusLoader(t testing.TB) purego.Loader {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("../testdata/vega-datasets")))
	t.Cleanup(srv.Close)
	httpLoader := &aster.HTTPLoader{Client: &http.Client{Transport: redirectTransport{target: srv.URL, next: srv.Client().Transport}}}
	return aster.NewFallbackLoader(
		&aster.FileLoader{BaseDir: "../testdata/vega-datasets"},
		&aster.FileLoader{BaseDir: "testdata/data"},
		httpLoader,
	)
}

// redirectTransport rewrites vega-datasets CDN/GitHub URLs to a local server.
type redirectTransport struct {
	target string
	next   http.RoundTripper
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	rewritten := ""
	switch req.URL.Hostname() {
	case "cdn.jsdelivr.net", "raw.githubusercontent.com", "vega.github.io":
		if i := strings.Index(path, "/data/"); i >= 0 {
			rewritten = path[i:]
		}
	}
	if rewritten == "" {
		return nil, fmt.Errorf("offline corpus: refusing %s", req.URL)
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	req.URL.Path = rewritten
	return rt.next.RoundTrip(req)
}
