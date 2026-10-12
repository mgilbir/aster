package aster_test

import (
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

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
)

var (
	typeConfusionN    = flag.Int("typeconfusion.n", 1500, "mutants TestVegaLiteTypeConfusionMatchesNode compiles")
	typeConfusionSeed = flag.Int64("typeconfusion.seed", 1, "seed of TestVegaLiteTypeConfusionMatchesNode's sample")
	typeConfusionDump = flag.String("typeconfusion.dump", "", "directory to write the first mutant of each difference to")
)

// vlConfusedValues are values of the wrong type for almost every slot of a
// specification (internal/vegalite's TestTypeConfusion uses the same).
var vlConfusedValues = []string{
	"null", "true", "false", `""`, `"x"`, `"1e999"`, `"NaN"`, `"__proto__"`, `"constructor"`, `"datum.x"`,
	"0", "-1", "1e308", "[]", "{}", "[1,2]", `["a","b"]`, `{"a":1}`, "[[1]]", "[{}]",
	`{"signal":"1/0"}`, `{"signal":"datum"}`, `{"field":"nofield"}`, `{"value":null}`, `{"type":{}}`,
}

// TestVegaLiteTypeConfusionMatchesNode is the differential form of
// internal/vegalite's TestTypeConfusion: it replaces one value of a corpus
// specification by a value of another type and compiles the mutant here and
// in node, with Vega-Lite 6.4.3 and 5.8.0. Where node throws the engine must
// return an error, where node compiles the engine must compile (ADR 0013), and
// where both compile the compiler's warnings must be node's, in order and
// count (C38).
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
	for k := 0; k < *typeConfusionN; k++ {
		f := files[rng.Intn(len(files))]
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			continue
		}
		slots := collect(spec)
		if len(slots) == 0 {
			continue
		}
		s := slots[rng.Intn(len(slots))]
		val := vlConfusedValues[rng.Intn(len(vlConfusedValues))]
		nv, _ := jsval.ParseJSONString(val)
		s.set(nv)
		version := "6.4"
		if k%2 == 1 {
			version = "5.8"
		}
		mutant := jsval.AppendJSON(nil, spec)
		want, err := ovl[version].Compile(mutant)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		gotWarn, gerr := compileWarnings(conv[version], mutant)
		pathKey := digits.ReplaceAllString("/"+s.pathString(), "/N")
		var key string
		switch {
		case want.Err == "" && gerr != nil:
			key = fmt.Sprintf("%s %s=%s: the engine errors (%q), node compiles", version, pathKey, val, firstLine(gerr.Error()))
		case want.Err != "" && gerr == nil:
			key = fmt.Sprintf("%s %s=%s: node errors (%q), the engine compiles", version, pathKey, val, firstLine(want.Err))
		case want.Err == "" && !slices.Equal(gotWarn, want.Warnings):
			// Both compile: the warnings must be upstream's, in order and
			// count.
			key = fmt.Sprintf("%s %s=%s: warnings differ\n  engine: %s\n  node:   %s", version, pathKey, val,
				strings.Join(gotWarn, " | "), strings.Join(want.Warnings, " | "))
		default:
			continue
		}
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
