package aster_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

const compareSetsDefault = "vg-fixtures,vl-fixtures,vl-examples,vl-convert,vg-gallery,regress-vg,regress-vl"

var (
	compareSets     = flag.String("compare.sets", compareSetsDefault, "comma-separated corpus sets to compare with the node oracle")
	compareFilter   = flag.String("compare.run", "", "only compare specs whose name contains this substring")
	compareReport   = flag.String("compare.report", "", "write a per-spec markdown report to this path")
	compareVerbose  = flag.Bool("compare.v", false, "log every spec's outcome")
	compareUpdate   = flag.Bool("compare.update", false, "rewrite "+expectFile+" with the observed statuses, keeping existing reasons")
	compareHarfBuzz = flag.Bool("compare.harfbuzz", false, "measure text with WithHarfBuzzTextMetrics")
)

// corpusSet is one directory of specs compared with the oracle.
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
	{"vl-examples", "testdata/vega-lite/v6.4.3/specs/*.vl.json", true},
	{"vl-convert", "testdata/vl-convert/*.vl.json", true},
}

// expectFile lists the specs whose status is not "ok", with the reason:
//
//	set/name<TAB>status<TAB>reason
//
// A status is "ok" or "+"-joined problems: differ (the SVG differs from
// node's beyond the tolerance), engine-error (only the engine fails),
// node-error (only upstream fails) and vega-differ (the compiled Vega is not
// equal to upstream's). Any other status than the listed one fails the test,
// a better one too, so the list stays exact; -compare.update rewrites it.
//
// A status may list "|"-separated alternatives for the few specs whose
// oracle answer depends on the platform node runs on (its text shaping, or
// V8's last-bit trigonometry on x86-64); the reason says which gives which.
const expectFile = "testdata/oracle-expect.txt"

// pinnedNow is the instant the oracle pins Date.now and new Date() to
// (testdata/oracle-node/oracle.mjs), so a chart that draws the current time
// compares like any other.
var pinnedNow = time.UnixMilli(1767225600000) // 2026-01-01T00:00:00Z

// svgTolerance absorbs the two text engines (node-canvas and forme) placing
// glyph advances differently in the last digits.
var svgTolerance = svgdiff.Options{Abs: 0.5, Rel: 1e-6}

// pangoVersion reads "pango 1.57.1" from the oracle's version line.
var pangoVersion = regexp.MustCompile(`pango (\d+)\.(\d+)`)

// pangoText is the engine's text model for the oracle's node-canvas: node-canvas
// measures with the Pango it was built against, which scales every glyph
// advance to 1/1024 px with the HarfBuzz it bundles. Pango 1.48 (the Linux
// build) floors, Pango 1.57 (the macOS build) rounds to nearest; the engine
// reproduces the oracle's widths bit for bit under either, so a layout that
// takes the ceiling of a width cannot land on the other side of an integer.
// Without an oracle the engine measures unrounded advances.
func pangoText(o *oracle.Oracle) []aster.Option {
	if o == nil {
		return nil
	}
	m := pangoVersion.FindStringSubmatch(o.Version())
	if m == nil {
		return nil
	}
	minor, _ := strconv.Atoi(m[2])
	return []aster.Option{aster.WithPangoTextForTest(m[1] == "1" && minor < 50)}
}

// oracleConverter is the engine configured as the oracle measures text:
// DejaVu Sans Mono for monospace and DejaVu Sans for every other family
// (the embedded Noto Emoji covers emoji on both sides), with the advances
// the oracle's Pango produces (see pangoText; o may be nil).
func oracleConverter(t testing.TB, o *oracle.Oracle, extra ...aster.Option) *aster.Converter {
	t.Helper()
	opts := []aster.Option{
		aster.WithFont("DejaVu Sans", dejavu.SansRegular), aster.WithFont("DejaVu Sans", dejavu.SansBold),
		aster.WithFont("DejaVu Sans", dejavu.SansOblique), aster.WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoRegular), aster.WithFont("DejaVu Sans Mono", dejavu.MonoBold),
		aster.WithFont("DejaVu Sans Mono", dejavu.MonoOblique), aster.WithFont("DejaVu Sans Mono", dejavu.MonoBoldOblique),
		aster.WithDefaultFontFamily("DejaVu Sans"), aster.WithDefaultMonospaceFamily("DejaVu Sans Mono"),
		aster.WithDefaultSerifFamily("DejaVu Sans"),
		aster.WithLoader(corpusLoader(t)), aster.WithTimeout(2 * time.Minute),
		aster.WithClockForTest(func() time.Time { return pinnedNow }),
	}
	if *compareHarfBuzz {
		opts = append(opts, aster.WithHarfBuzzTextMetrics())
	}
	opts = append(opts, pangoText(o)...)
	c, err := aster.New(append(opts, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

type specResult struct {
	id     string // set/name
	status string
	detail string
	svg    string // identical, equal, differ, engine-error, node-error, both-error
}

// TestCompareWithNode renders and compiles the corpus with the engine and
// with upstream Vega / Vega-Lite in node, compares the SVG within
// svgTolerance and the compiled Vega exactly, prints a scoreboard, and fails
// when a spec's status is not the one expectFile records.
func TestCompareWithNode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: renders the whole corpus")
	}
	o := oracle.For(t, oracle.VL6)
	c := oracleConverter(t, o)
	t.Logf("oracle: %s", o.Version())

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
			name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".json"), ".vl"), ".vg")
			if *compareFilter != "" && !strings.Contains(name, *compareFilter) {
				continue
			}
			spec, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			r := compareOne(t, o, c, set, spec)
			r.id = set.name + "/" + name
			if *compareVerbose {
				t.Logf("%s: %s (%s) %s", r.id, r.status, r.svg, r.detail)
			}
			results = append(results, r)
		}
	}
	scoreboard(t, results)
	// A floor, so a corpus directory that moved or emptied fails instead of
	// comparing less.
	const corpusFloor = 1394
	if *compareSets == compareSetsDefault && *compareFilter == "" && len(results) < corpusFloor {
		t.Errorf("compared %d specs, fewer than the corpus floor of %d", len(results), corpusFloor)
	}
	checkExpectations(t, expectFile, *compareUpdate, results)
}

func compareOne(t *testing.T, o *oracle.Oracle, c *aster.Converter, set corpusSet, spec []byte) specResult {
	var r specResult
	want, err := o.SVG(set.lite, spec)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	var got string
	var gerr error
	if set.lite {
		got, gerr = c.VegaLiteToSVG(spec)
	} else {
		got, gerr = c.VegaToSVG(spec)
	}
	var problems []string
	switch {
	case gerr != nil && want.Err != "":
		r.svg, r.detail = "both-error", "engine: "+firstLine(gerr.Error())+" | node: "+firstLine(want.Err)
	case gerr != nil:
		r.svg, r.detail = "engine-error", firstLine(gerr.Error())
		problems = append(problems, "engine-error")
	case want.Err != "":
		r.svg, r.detail = "node-error", firstLine(want.Err)
		problems = append(problems, "node-error")
	default:
		d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
		switch {
		case err != nil:
			r.svg, r.detail = "differ", err.Error()
		case d.Identical:
			r.svg = "identical"
		case d.Equal:
			r.svg = "equal"
		default:
			r.svg, r.detail = "differ", fmt.Sprintf("%d diffs; first: %s", d.DiffCount, firstLine(d.Diff))
		}
		if r.svg == "differ" {
			problems = append(problems, "differ")
		}
	}
	if set.lite && len(want.Vega) > 0 {
		if vg, err := c.VegaLiteToVega(spec); err == nil {
			g, err1 := jsval.ParseJSON(vg)
			w, err2 := jsval.ParseJSON(want.Vega)
			if err1 != nil || err2 != nil || !jsval.Equal(g, w) {
				problems = append(problems, "vega-differ")
			}
		}
	}
	r.status = "ok"
	if len(problems) > 0 {
		r.status = strings.Join(problems, "+")
	}
	return r
}

func scoreboard(t *testing.T, results []specResult) {
	cols := []string{"identical", "equal", "differ", "engine-error", "node-error", "both-error"}
	type tally struct {
		n, vegaDiffer int
		by            map[string]int
	}
	bySet := map[string]*tally{}
	var order []string
	for _, r := range results {
		set, _, _ := strings.Cut(r.id, "/")
		tl := bySet[set]
		if tl == nil {
			tl = &tally{by: map[string]int{}}
			bySet[set] = tl
			order = append(order, set)
		}
		tl.n++
		tl.by[r.svg]++
		if strings.Contains(r.status, "vega-differ") {
			tl.vegaDiffer++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s %6s", "set", "specs")
	for _, c := range cols {
		fmt.Fprintf(&b, " %12s", c)
	}
	fmt.Fprintf(&b, " %12s\n", "vega-differ")
	for _, s := range order {
		tl := bySet[s]
		fmt.Fprintf(&b, "%-12s %6d", s, tl.n)
		for _, c := range cols {
			fmt.Fprintf(&b, " %12d", tl.by[c])
		}
		fmt.Fprintf(&b, " %12d\n", tl.vegaDiffer)
	}
	t.Log(b.String())
	if *compareReport != "" {
		var md strings.Builder
		md.WriteString("# Engine vs upstream (node)\n\n```" + b.String() + "```\n\n| spec | status | SVG | detail |\n|---|---|---|---|\n")
		for _, r := range results {
			fmt.Fprintf(&md, "| %s | %s | %s | %s |\n", r.id, r.status, r.svg, strings.ReplaceAll(r.detail, "|", "\\|"))
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

// corpusLoader serves the corpus datasets: the vega-datasets copy, the extra
// datasets under testdata/data, and the CDN URLs the Vega-Lite examples use,
// redirected to a local server.
func corpusLoader(t testing.TB) aster.Loader {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/vega-datasets")))
	t.Cleanup(srv.Close)
	httpLoader := &aster.HTTPLoader{Client: &http.Client{Transport: redirectTransport{target: srv.URL, next: srv.Client().Transport}}}
	return aster.NewFallbackLoader(
		&aster.FileLoader{BaseDir: "testdata/vega-datasets"},
		&aster.FileLoader{BaseDir: "testdata/data"},
		httpLoader,
	)
}

// datasetURLs are the URL patterns that serve copies of vega-datasets,
// shared with the oracle (testdata/oracle-node/dataset-urls.json), so both
// sides map exactly the same URLs to the local copy and refuse the rest.
var datasetURLs = sync.OnceValue(func() []*regexp.Regexp {
	b, err := os.ReadFile("testdata/oracle-node/dataset-urls.json")
	if err != nil {
		panic(err)
	}
	var f struct{ Patterns []string }
	if err := json.Unmarshal(b, &f); err != nil {
		panic(err)
	}
	res := make([]*regexp.Regexp, len(f.Patterns))
	for i, p := range f.Patterns {
		res[i] = regexp.MustCompile(p)
	}
	return res
})

// redirectTransport serves the URLs datasetURLs maps from a local server.
type redirectTransport struct {
	target string
	next   http.RoundTripper
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rewritten := ""
	for _, re := range datasetURLs() {
		if m := re.FindStringSubmatch(req.URL.String()); m != nil {
			rewritten = "/" + m[1]
			break
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
