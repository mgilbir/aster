package aster_test

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The counts docs/testing.md states are pinned, as limits.md's numbers are:
// a count kept by hand drifts (C61, audit 2026-10-18: the fuzz targets, the
// recursion cycles and the upstream floors had all moved on). Each stated
// count is found by its sentence and compared with what the repository holds;
// a sentence reworded so that its pattern no longer matches fails too, so a
// count cannot leave the check unnoticed. The counts only a slow gate knows
// are pinned by that gate: the recursion cycles by TestRecursionsAreBounded,
// the sweeps' case counts by runSweep, the comparator's mutation kinds by
// internal/svgdiff's TestMutationsAreReported, and the deep-input shapes and
// the bounded-input harness's settings by TestTestingDocHarnessCounts.
func TestTestingDocCounts(t *testing.T) {
	doc := readDoc(t, "docs/testing.md")

	// The corpus TestCompareWithNode compares, set by set.
	sets := map[string]int{}
	total := 0
	for _, s := range corpusSets {
		files, err := filepath.Glob(s.glob)
		if err != nil {
			t.Fatal(err)
		}
		sets[s.name] = len(files)
		total += len(files)
	}
	docCountIs(t, doc, `corpus ([\d,]+) specs<br/>`, total)
	docCountIs(t, doc, `runs over ([\d,]+) specs:`, total)
	docCountIs(t, doc, `specs: ([\d,]+) Vega fixtures`, sets["vg-fixtures"])
	docCountIs(t, doc, `([\d,]+) Vega-Lite fixtures`, sets["vl-fixtures"])
	docCountIs(t, doc, `Vega's ([\d,]+) example specs`, sets["vg-gallery"])
	docCountIs(t, doc, `the ([\d,]+) Vega-Lite examples`, sets["vl-examples"])
	docCountIs(t, doc, `([\d,]+) vl-convert specs`, sets["vl-convert"])
	docCountIs(t, doc, `and ([\d,]+) regressions`, sets["regress-vg"]+sets["regress-vl"])

	// The entries of the corpus's expectation file, and those that depend on
	// the platform (their reason says so first).
	entries, platform := 0, 0
	for _, line := range strings.Split(readDoc(t, expectFile), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		entries++
		if f := strings.SplitN(line, "\t", 3); len(f) == 3 && strings.HasPrefix(f[2], "platform") {
			platform++
		}
	}
	docCountIs(t, doc, `(\w+) of its \w+ entries depend on the platform`, platform)
	docCountIs(t, doc, `\w+ of its (\w+) entries depend on the platform`, entries)

	// The sweeps' floors, as the tests set them.
	rows := regexp.MustCompile("(?m)^\\| `(Test\\w+)` \\| ([\\d,]+) \\| ([\\d,]+) \\|").FindAllStringSubmatch(doc, -1)
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r[1]] = true
		floor, ok := sweepFloors[r[1]]
		if !ok {
			t.Errorf("docs/testing.md: the sweep table lists %s, which sweepFloors does not", r[1])
			continue
		}
		if got := number(t, r[3]); got != floor {
			t.Errorf("docs/testing.md: %s's floor is %d, the test's %d", r[1], got, floor)
		}
	}
	for name := range sweepFloors {
		if !seen[name] {
			t.Errorf("docs/testing.md: the sweep table has no row for %s", name)
		}
	}

	// The type-confusion differential's default size.
	n, err := strconv.Atoi(flag.Lookup("typeconfusion.n").DefValue)
	if err != nil {
		t.Fatal(err)
	}
	docCountIs(t, doc, `mutants; ([\d,]+) by default`, n)

	// The coverage-guided fuzz targets.
	targets := 0
	for _, line := range strings.Split(readDoc(t, "scripts/fuzz-targets.txt"), "\n") {
		if line = strings.TrimSpace(line); line != "" && line[0] != '#' {
			targets++
		}
	}
	docCountIs(t, doc, `scripts/fuzz-targets.txt`+"`"+` \(([\d,]+) of them`, targets)

	// Upstream's own test suites: the packages the recording script lists,
	// the TestUpstream* functions, the packages they replay and their floors.
	script := readDoc(t, "scripts/record-upstream-vectors.sh")
	listed := 0
	for _, m := range regexp.MustCompile(`(?s)\n(?:VEGA|D3)_PACKAGES=\((.*?)\)`).FindAllStringSubmatch(script, -1) {
		listed += len(strings.Fields(m[1]))
	}
	if listed == 0 {
		t.Error("scripts/record-upstream-vectors.sh: no package lists found")
	}
	docCountIs(t, doc, `the Vega and d3 packages it lists \(([\d,]+)`, listed)
	if recorded, err := filepath.Glob("testdata/upstream-vectors-cache/*.json"); err == nil && len(recorded) > 0 {
		docCountIs(t, doc, `packages it lists \([\d,]+, of which ([\d,]+) record`, len(recorded))
	}
	tests, floors := 0, 0
	pkgs := map[string]bool{}
	start := regexp.MustCompile(`upstream\.Start\(t, "([^"]+)"\)`)
	done := regexp.MustCompile(`\br\.Done\((\d+)\)`)
	declared := regexp.MustCompile(`(?m)^func TestUpstream\w*\(`)
	for _, f := range trackedFiles(t, "internal/*_test.go") {
		src := readDoc(t, f)
		tests += len(declared.FindAllString(src, -1))
		for _, m := range start.FindAllStringSubmatch(src, -1) {
			pkgs[m[1]] = true
		}
		for _, m := range done.FindAllStringSubmatch(src, -1) {
			floors += number(t, m[1])
		}
	}
	docCountIs(t, doc, `The ([\d,]+) `+"`TestUpstream\\*`"+` tests`, tests)
	docCountIs(t, doc, `the vectors of ([\d,]+) of those packages`, len(pkgs))
	docCountIs(t, doc, `floors totalling ([\d,]+) calls`, floors)
}

// sweepFloors is the least number of cases each sweep's generator must
// produce, by test; runSweep enforces it, and docs/testing.md states it.
var sweepFloors = map[string]int{
	"TestSweepVegaSchema":     7549,
	"TestSweepVegaLiteSchema": 27513,
	"TestSweepValues":         500,
	"TestSweepSignals":        166,
	"TestSweepTimezones":      801,
	"TestSweepLocales":        372,
	"TestCorpusWild":          1981,
	"TestCorpusDeneb":         62,
}

// checkSweepDocCases compares the cases a sweep generated with the count
// docs/testing.md's table states for it.
func checkSweepDocCases(t *testing.T, cases int) {
	t.Helper()
	doc := readDoc(t, "docs/testing.md")
	docCountIs(t, doc, "(?m)^\\| `"+regexp.QuoteMeta(t.Name())+"` \\| ([\\d,]+) \\|", cases)
}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// docCountIs requires the one group of re, a count, to be want, where re
// matches doc exactly once.
func docCountIs(t *testing.T, doc, re string, want int) {
	t.Helper()
	ms := regexp.MustCompile(re).FindAllStringSubmatch(doc, -1)
	if len(ms) != 1 {
		t.Errorf("the doc states no count matching %q (%d matches); reword the test with the doc", re, len(ms))
		return
	}
	if got := number(t, ms[0][1]); got != want {
		t.Errorf("the doc states %q, the repository has %d", ms[0][0], want)
	}
}

// number reads a count written with thousands separators, or as a word.
func number(t *testing.T, s string) int {
	t.Helper()
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	for i, w := range words {
		if strings.EqualFold(s, w) {
			return i
		}
	}
	n, err := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	if err != nil {
		t.Errorf("%q is not a count", s)
	}
	return n
}
