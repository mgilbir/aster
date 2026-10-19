package aster_test

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/fuzzutil/confusion"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
)

var (
	typeConfusionN    = flag.Int("typeconfusion.n", 1500, "mutants TestVegaLiteTypeConfusionMatchesNode compiles")
	typeConfusionSeed = flag.Int64("typeconfusion.seed", 1, "seed of TestVegaLiteTypeConfusionMatchesNode's sample")
	typeConfusionDump = flag.String("typeconfusion.dump", "", "directory to write the first mutant of each difference to")
	// typeConfusionBudget bounds the run by time rather than by count, for
	// the scheduled search (.github/workflows/fuzz.yml): mutants are
	// compiled until n or the budget is reached, whichever comes first. It
	// bounds work, it is not a pass/fail condition.
	typeConfusionBudget = flag.Duration("typeconfusion.budget", 0, "stop after this long (0: no limit)")
)

// TestVegaLiteTypeConfusionMatchesNode is the differential form of
// internal/vegalite's TestTypeConfusion: it replaces one value of a corpus
// specification by a value of another type, or by one derived from it ([v],
// String(v), a case-mapped spelling), or renames a key (confusion.Confuse,
// the vocabulary both tests share), and compiles the mutant here and in node, with Vega-Lite 6.4.3 and 5.8.0. Where node throws the engine must
// return an error, where node compiles the engine must compile (ADR 0013), and
// where both compile the compiler's warnings must be node's, in order and
// count (C38), and the compiled Vega must be node's, exactly. The oracle
// answers each mutant as a fresh node process would (upstream can change
// JavaScript's built-ins, see internal/oracle).
func TestVegaLiteTypeConfusionMatchesNode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles 1,500 mutants in node")
	}
	files, _ := filepath.Glob("internal/vegalite/testdata/specs/*/*.json")
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no corpus")
	}
	ovl := map[string]*oracle.Oracle{"6.4": oracle.For(t, oracle.VL6), "5.8": oracle.For(t, oracle.VL5)}
	conv := map[string]*aster.Converter{}
	for v := range ovl {
		c, err := aster.New(aster.WithVegaLiteVersion(v))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conv[v] = c
	}
	rng := rand.New(rand.NewSource(*typeConfusionSeed))
	digits := regexp.MustCompile(`/\d+`)
	groups := map[string][]string{}
	start := time.Now()
	compared := 0
	defer func() { t.Logf("seed %d: %d mutants compared", *typeConfusionSeed, compared) }()
	for k := 0; k < *typeConfusionN; k++ {
		if *typeConfusionBudget > 0 && time.Since(start) > *typeConfusionBudget {
			break
		}
		f := files[rng.Intn(len(files))]
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			continue
		}
		s, val, ok := confusion.Confuse(rng, spec)
		if !ok {
			continue
		}
		version := "6.4"
		if k%2 == 1 {
			version = "5.8"
		}
		mutant := jsval.AppendJSON(nil, spec)
		want, err := ovl[version].Compile(mutant)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		compared++
		pathKey := digits.ReplaceAllString("/"+strings.Join(s.Path, "/"), "/N")
		diff := mutantDiffers(t, conv[version], want, mutant)
		if diff == "" {
			continue
		}
		key := fmt.Sprintf("%s %s=%s: %s", version, pathKey, val, diff)
		if len(groups[key]) == 0 && *typeConfusionDump != "" {
			base := filepath.Join(*typeConfusionDump, fmt.Sprintf("%04d-%s", len(groups), version))
			_ = os.WriteFile(base+".json", mutant, 0o644)
			_ = os.WriteFile(base+".key", []byte(key+"\n"), 0o644)
		}
		groups[key] = append(groups[key], filepath.Base(f))
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%s (%s ×%d)", k, strings.Join(groups[k], ", "), len(groups[k]))
	}
}

// mutantDiffers compares the engine's compilation of mutant with node's
// answer want and describes the first difference ("" when none): an error
// on one side only, the warnings (in order and count), or the compiled Vega,
// exactly (keys in node's order; upstream's compiler is deterministic under
// the oracle's pinned clock, so nothing is normalised beyond JSON text).
func mutantDiffers(t *testing.T, conv *aster.Converter, want oracle.Result, mutant []byte) string {
	t.Helper()
	var gotWarn []string
	got, gerr := conv.VegaLiteToVega(mutant, aster.WithErrorHandler(func(err error) error {
		var w aster.Warning
		if errors.As(err, &w) {
			gotWarn = append(gotWarn, string(w))
			return nil
		}
		return err
	}))
	switch {
	case want.Err == "" && gerr != nil:
		return fmt.Sprintf("the engine errors (%q), node compiles", firstLine(gerr.Error()))
	case want.Err != "" && gerr == nil:
		return fmt.Sprintf("node errors (%q), the engine compiles", firstLine(want.Err))
	case want.Err != "":
		return ""
	case !slices.Equal(gotWarn, want.Warnings):
		return fmt.Sprintf("warnings differ\n  engine: %s\n  node:   %s", strings.Join(gotWarn, " | "), strings.Join(want.Warnings, " | "))
	}
	g, err1 := jsval.ParseJSON(got)
	w, err2 := jsval.ParseJSON(want.Vega)
	if err1 != nil || err2 != nil {
		t.Fatalf("unparsable output: %v / %v", err1, err2)
	}
	if string(jsval.AppendJSON(nil, g)) == string(jsval.AppendJSON(nil, w)) {
		return ""
	}
	return "compiled Vega differs: " + firstJSONDiff(g, w, "")
}

// TestTypeConfusionRegressionsMatchNode replays the mutants the
// differential found (testdata/typeconfusion/NAME-VERSION.json, VERSION 6.4
// or 5.8), so each stays fixed whatever the sampled corpus becomes. See
// docs/testing.md for how a found divergence becomes one.
func TestTypeConfusionRegressionsMatchNode(t *testing.T) {
	files, _ := filepath.Glob("testdata/typeconfusion/*.json")
	if len(files) == 0 {
		t.Fatal("no regression mutants")
	}
	ovl := map[string]*oracle.Oracle{"6.4": oracle.For(t, oracle.VL6), "5.8": oracle.For(t, oracle.VL5)}
	conv := map[string]*aster.Converter{}
	for v := range ovl {
		c, err := aster.New(aster.WithVegaLiteVersion(v))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conv[v] = c
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		version := name[strings.LastIndexByte(name, '-')+1:]
		if ovl[version] == nil {
			t.Fatalf("%s: the name must end in -6.4 or -5.8", f)
		}
		mutant, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ovl[version].Compile(mutant)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		if d := mutantDiffers(t, conv[version], want, mutant); d != "" {
			t.Errorf("%s: %s", name, d)
		}
	}
}
