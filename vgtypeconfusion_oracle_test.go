package aster_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

var (
	vgConfusionN    = flag.Int("vgconfusion.n", 1500, "mutants TestVegaTypeConfusionMatchesNode renders")
	vgConfusionSeed = flag.Int64("vgconfusion.seed", 1, "seed of TestVegaTypeConfusionMatchesNode's sample")
	vgConfusionDump = flag.String("vgconfusion.dump", "", "directory to write the first mutant of each difference to")
	vgConfusionSVGs = flag.String("vgconfusion.svgs", "", "directory TestVegaTypeConfusionRegressionsMatchNode writes each mutant's engine and node SVG to (NAME.engine.svg, NAME.node.svg), for diffing")
	// vgConfusionBudget bounds the run by time rather than by count, for the
	// scheduled search (.github/workflows/fuzz.yml): mutants are rendered
	// until n or the budget is reached, whichever comes first. It bounds
	// work, it is not a pass/fail condition.
	vgConfusionBudget = flag.Duration("vgconfusion.budget", 0, "stop after this long (0: no limit)")
	// vgConfusionReplay replays a -vgconfusion.dump directory's mutants in
	// TestVegaTypeConfusionRegressionsMatchNode instead of the recorded
	// regressions, to see which of a search's differences remain.
	// vgConfusionPaths restricts the mutations to the slots whose path
	// (indices written /N, as the differences are keyed) matches, for a
	// search focused on one part of the specification.
	vgConfusionPaths  = flag.String("vgconfusion.paths", "", "only mutate slots whose path matches this regular expression")
	vgConfusionReplay = flag.String("vgconfusion.replay", "", "replay the mutants of this dump directory instead of testdata/vgtypeconfusion")
)

// vgCorpusSets are the Vega corpora the differential mutates.
var vgCorpusSets = []string{"vg-fixtures", "vg-gallery", "regress-vg"}

// vgConfusedValues replace a value with one of (mostly) another type: the
// primitives, the empty and one-element containers, and the shapes Vega gives
// meaning to (a signal, a field, a value reference).
var vgConfusedValues = []string{
	"null", "true", "false", `""`, `"x"`, `"1e999"`, `"NaN"`, `"__proto__"`, `"constructor"`, `"toString"`, `"datum.x"`,
	"0", "-1", "0.5", "1e308", "[]", "{}", "[1,2]", `["a","b"]`, `{"a":1}`, "[[1]]", "[{}]", "[null]",
	`{"signal":"1/0"}`, `{"signal":"datum"}`, `{"signal":"null"}`, `{"field":"nofield"}`, `{"field":""}`, `{"value":null}`,
	`{"scale":"x"}`, `{"type":{}}`, `{"data":"nodata"}`,
}

// vgProtoValues are the strings a data value is also replaced with (half of
// the replacements inside /data/N/values): the names a plain object used as a
// map keyed by data (a facet's subflows, a data join, an aggregate's cells, a
// lookup's index, an impute's domain ...) finds as members of
// Object.prototype instead of finding no entry.
var vgProtoValues = []string{
	`"__proto__"`, `"constructor"`, `"toString"`, `"hasOwnProperty"`, `"valueOf"`, `"isPrototypeOf"`,
	`"propertyIsEnumerable"`, `"toLocaleString"`, `"__defineGetter__"`, `"__lookupSetter__"`,
}

// vgRenamedKeys are the names a key is renamed to: JavaScript's own (an
// inherited member, the undefined key's text) and the empty name; a key is
// also renamed to its case-mapped spellings.
var vgRenamedKeys = []string{"undefined", "", "__proto__", "constructor", "toString", "hasOwnProperty"}

// vgMutant is one mutation of one corpus specification.
type vgMutant struct {
	file  string // the specification mutated
	label string // the mutation, with the path's indices generalised
	spec  []byte
}

// mutateVega applies one mutation, drawn from rng, to a parsed specification
// and describes it ("" when the specification offers no slot for it).
func mutateVega(rng *rand.Rand, spec jsval.Value, paths *regexp.Regexp) string {
	slots := collect(spec)
	if paths != nil {
		kept := slots[:0]
		for _, s := range slots {
			if paths.MatchString(vgDigits.ReplaceAllString("/"+s.pathString(), "/N")) {
				kept = append(kept, s)
			}
		}
		slots = kept
	}
	if len(slots) == 0 {
		return ""
	}
	s := slots[rng.Intn(len(slots))]
	path := vgDigits.ReplaceAllString("/"+s.pathString(), "/N")
	orig := s.get()
	switch r := rng.Intn(10); {
	case r < 4: // a value of another type
		val := vgConfusedValues[rng.Intn(len(vgConfusedValues))]
		proto := strings.HasPrefix(path, "/data/N/values") && rng.Intn(2) == 0
		if proto {
			val = vgProtoValues[rng.Intn(len(vgProtoValues))]
		}
		for i := 0; i < 8 && !proto; i++ {
			nv := parseSnippet(val)
			if nv.Kind() != orig.Kind() {
				break
			}
			val = vgConfusedValues[rng.Intn(len(vgConfusedValues))]
		}
		s.set(parseSnippet(val))
		return path + "=" + val
	case r < 8: // a value derived from the one replaced
		switch rng.Intn(5) {
		case 0:
			s.set(jsval.ArrOf(orig))
			return path + "=[orig]"
		case 1:
			s.set(jsval.ArrOf(jsval.ArrOf(orig)))
			return path + "=[[orig]]"
		case 2:
			if orig.IsStr() {
				break
			}
			s.set(jsval.Str(orig.AsString()))
			return path + "=String(orig)"
		case 3:
			// JSON holds no NaN or infinity: such a Number(orig) is not a
			// mutant a specification can spell.
			if n := orig.AsDouble(); !orig.IsNum() && !math.IsNaN(n) && !math.IsInf(n, 0) {
				s.set(jsval.Num(n))
				return path + "=Number(orig)"
			}
		case 4:
			if orig.IsArr() {
				s.set(jsval.Arr(append(append([]jsval.Value(nil), orig.Items()...), jsval.Null)))
				return path + "=[...orig,null]"
			}
		}
		return ""
	default: // the key renamed
		if s.obj == nil {
			return ""
		}
		names := append([]string(nil), vgRenamedKeys...)
		for _, n := range []string{strings.ToUpper(s.key), strings.ToLower(s.key), titleCase(s.key)} {
			if n != s.key {
				names = append(names, n)
			}
		}
		to := names[rng.Intn(len(names))]
		if to == s.key || s.obj.Has(to) {
			return ""
		}
		renameKey(s.obj, s.key, to)
		label := to
		if to != "" && strings.EqualFold(to, s.key) && !slices.Contains(vgRenamedKeys, to) {
			label = "<case-mapped>"
		}
		return path + " renamed " + fmt.Sprintf("%q", label)
	}
}

var vgDigits = regexp.MustCompile(`/\d+`)

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// renameKey renames from to to in o, in place, keeping the other keys' order
// (the renamed key moves to where JSON.parse would put it: last, unless it is
// an array index).
func renameKey(o *jsval.Object, from, to string) {
	v := o.Lookup(from)
	o.Delete(from)
	o.Set(to, v)
}

// vgConfusionCorpus lists the Vega specifications the differential mutates:
// the Vega corpora's specifications whose unmutated comparison is "ok", so
// that a difference is the mutation's.
func vgConfusionCorpus(t *testing.T) []string {
	expect := readExpectations(t, expectFile)
	var files []string
	for _, set := range corpusSets {
		if !slices.Contains(vgCorpusSets, set.name) {
			continue
		}
		fs, _ := filepath.Glob(set.glob)
		for _, f := range fs {
			id := set.name + "/" + strings.TrimSuffix(filepath.Base(f), ".vg.json")
			if _, listed := expect[id]; listed {
				continue
			}
			files = append(files, f)
		}
	}
	sort.Strings(files)
	if len(files) < 400 {
		t.Fatalf("Vega corpus: %d specifications, expected the three sets' 400 and more", len(files))
	}
	return files
}

// TestVegaTypeConfusionMatchesNode is the Vega form of
// TestVegaLiteTypeConfusionMatchesNode (ADR 0013): it mutates one value of a
// corpus specification, replacing it by a value of another type or by one
// derived from it ([v], [[v]], String(v), Number(v), [...v, null]), or
// renames a key (to JavaScript's own names, the empty name or its
// case-mapped spellings), and renders the mutant here and in node. Where node
// throws the engine must fail, where node renders the engine must render,
// and the SVGs must be equal as the corpus comparison holds them
// (svgTolerance). An engine panic is never agreement. A render the engine
// stops at one of its documented bounds (ErrLimit, the timeout) is counted
// and logged, not compared: the bound is the engine's policy (ADR 0005).
func TestVegaTypeConfusionMatchesNode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: renders 1,500 mutants in node")
	}
	files := vgConfusionCorpus(t)
	o := oracle.For(t, oracle.VL6)
	c := oracleConverter(t, o)
	src := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src[f] = b
	}
	var paths *regexp.Regexp
	if *vgConfusionPaths != "" {
		paths = regexp.MustCompile(*vgConfusionPaths)
	}
	rng := rand.New(rand.NewSource(*vgConfusionSeed))
	next := func() (vgMutant, bool) {
		for range 5000 {
			f := files[rng.Intn(len(files))]
			spec, err := jsval.ParseJSON(src[f])
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if label := mutateVega(rng, spec, paths); label != "" {
				return vgMutant{file: f, label: label, spec: jsval.AppendJSON(nil, spec)}, true
			}
		}
		return vgMutant{}, false
	}

	type outcome struct {
		m       vgMutant
		diff    string // "" when the engine agrees
		limited bool   // the engine stopped at a bound
		noAns   bool   // node did not answer in time
	}
	jobs := make(chan vgMutant)
	results := make(chan outcome)
	var wg sync.WaitGroup
	for range vgConfusionWorkers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				want, err := o.SVG(false, m.spec)
				// No answer in time, or node died answering (upstream
				// exhausted its heap or stack): not an answer to compare.
				if errors.Is(err, oracle.ErrNoAnswer) || err != nil && strings.Contains(err.Error(), "node exited") {
					results <- outcome{m: m, noAns: true}
					continue
				}
				if err != nil {
					results <- outcome{m: m, diff: "oracle: " + err.Error()}
					continue
				}
				d, limited := vegaMutantDiffers(c, want, m.spec)
				results <- outcome{m: m, diff: d, limited: limited}
			}
		}()
	}
	start := time.Now()
	go func() {
		defer close(jobs)
		for k := 0; k < *vgConfusionN; k++ {
			if *vgConfusionBudget > 0 && time.Since(start) > *vgConfusionBudget {
				return
			}
			m, ok := next()
			if !ok {
				return
			}
			jobs <- m
		}
	}()
	go func() { wg.Wait(); close(results) }()

	groups := map[string][]outcome{}
	compared, limited, noAnswer := 0, 0, 0
	for r := range results {
		switch {
		case r.noAns:
			noAnswer++
			continue
		case r.limited:
			limited++
			continue
		}
		compared++
		if r.diff == "" {
			continue
		}
		key := r.m.label + ": " + diffClass(r.diff)
		groups[key] = append(groups[key], r)
	}
	t.Logf("seed %d: %d mutants compared, %d stopped at an engine bound, %d unanswered by node (timed out or died)", *vgConfusionSeed, compared, limited, noAnswer)
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		g := groups[k]
		sort.Slice(g, func(a, b int) bool { return string(g[a].m.spec) < string(g[b].m.spec) })
		if *vgConfusionDump != "" {
			base := filepath.Join(*vgConfusionDump, fmt.Sprintf("%04d", i))
			_ = os.MkdirAll(*vgConfusionDump, 0o755)
			_ = os.WriteFile(base+".vg.json", g[0].m.spec, 0o644)
			_ = os.WriteFile(base+".key", []byte(k+"\n"+g[0].m.file+"\n"+g[0].diff+"\n"), 0o644)
		}
		var names []string
		for _, r := range g {
			names = append(names, filepath.Base(r.m.file))
		}
		t.Errorf("%s (%s ×%d)\n  first: %s", k, strings.Join(names, ", "), len(g), g[0].diff)
	}
}

// diffClass is the kind of a difference, for grouping: what each side did,
// without the details that vary between instances of one class.
func diffClass(d string) string {
	if i := strings.Index(d, " ("); i > 0 {
		return d[:i]
	}
	if i := strings.Index(d, ":"); i > 0 {
		return d[:i]
	}
	return d
}

// vgConfusionWorkers is how many mutants are rendered at once: the oracle's
// pool's size (ASTER_ORACLE_PROCS), else 4.
func vgConfusionWorkers() int {
	if n := os.Getenv("ASTER_ORACLE_PROCS"); n != "" {
		var k int
		if _, err := fmt.Sscan(n, &k); err == nil && k > 0 {
			return k
		}
	}
	return 4
}

// vegaMutantDiffers renders mutant with the engine and compares it with node's
// answer want, describing the first difference ("" when none). limited
// reports a render the engine stopped at one of its bounds, which is not
// compared.
func vegaMutantDiffers(c *aster.Converter, want oracle.Result, mutant []byte) (diff string, limited bool) {
	got, gerr := c.VegaToSVG(mutant, asUpstream)
	if gerr != nil && !isEngineBug(gerr) && (errors.Is(gerr, aster.ErrLimit) || errors.Is(gerr, context.DeadlineExceeded)) {
		return "", true
	}
	switch errorOutcome(gerr, want.Err) {
	case statusInternalError:
		return fmt.Sprintf("the engine panics (%q)", firstLine(gerr.Error())), false
	case "engine-error":
		return fmt.Sprintf("the engine errors, node renders (%q)", firstLine(gerr.Error())), false
	case "node-error":
		return fmt.Sprintf("node errors, the engine renders (%q)", firstLine(want.Err)), false
	case "both-error":
		return "", false
	}
	d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
	switch {
	case err != nil:
		return "the SVG differs: " + firstLine(err.Error()), false
	case !d.Equal:
		return fmt.Sprintf("the SVG differs: %d diffs; first: %s", d.DiffCount, firstLine(d.Diff)), false
	}
	return "", false
}

// TestVegaTypeConfusionRegressionsMatchNode replays the Vega mutants the
// differential found (testdata/vgtypeconfusion/NAME.vg.json), so each stays
// fixed whatever the sampled corpus becomes. See docs/testing.md for how a
// found divergence becomes one.
func TestVegaTypeConfusionRegressionsMatchNode(t *testing.T) {
	dir := "testdata/vgtypeconfusion"
	if *vgConfusionReplay != "" {
		dir = *vgConfusionReplay
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.vg.json"))
	if len(files) == 0 {
		t.Fatal("no regression mutants")
	}
	o := oracle.For(t, oracle.VL6)
	c := oracleConverter(t, o)
	for _, f := range files {
		mutant, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want, err := o.SVG(false, mutant)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		d, limited := vegaMutantDiffers(c, want, mutant)
		if limited {
			d = "the engine stops at a bound"
		}
		if d != "" && *vgConfusionSVGs != "" {
			name := strings.TrimSuffix(filepath.Base(f), ".vg.json")
			got, _ := c.VegaToSVG(mutant, asUpstream)
			_ = os.MkdirAll(*vgConfusionSVGs, 0o755)
			_ = os.WriteFile(filepath.Join(*vgConfusionSVGs, name+".engine.svg"), []byte(got), 0o644)
			_ = os.WriteFile(filepath.Join(*vgConfusionSVGs, name+".node.svg"), []byte(want.SVG), 0o644)
		}
		if d != "" {
			t.Errorf("%s: %s", strings.TrimSuffix(filepath.Base(f), ".vg.json"), d)
		}
	}
}
