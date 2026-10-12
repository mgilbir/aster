package aster_test

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
)

var (
	typeConfusionN    = flag.Int("typeconfusion.n", 1500, "mutants TestVegaLiteTypeConfusionMatchesNode compiles")
	typeConfusionSeed = flag.Int64("typeconfusion.seed", 1, "seed of TestVegaLiteTypeConfusionMatchesNode's sample")
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
// in node, with Vega-Lite 6.4.3 and 5.8.0. Where node throws, the engine must
// return an error too (ADR 0013: what upstream throws on, aster refuses), not
// compile the mutant to a chart; the mutants where only the engine fails, and
// where both compile, are not judged here.
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
	reverse := map[string][]string{} // the engine errors, node compiles: logged
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
		_, gerr := conv[version].VegaLiteToVega(mutant)
		if want.Err == "" && gerr != nil {
			rkey := fmt.Sprintf("%s %s=%s: engine %q", version, digits.ReplaceAllString("/"+s.pathString(), "/N"), val, firstLine(gerr.Error()))
			reverse[rkey] = append(reverse[rkey], filepath.Base(f))
		}
		if want.Err == "" || gerr != nil {
			continue
		}
		key := fmt.Sprintf("%s %s=%s: node %q", version, digits.ReplaceAllString("/"+s.pathString(), "/N"), val, firstLine(want.Err))
		groups[key] = append(groups[key], filepath.Base(f))
	}
	rkeys := make([]string, 0, len(reverse))
	for k := range reverse {
		rkeys = append(rkeys, k)
	}
	sort.Strings(rkeys)
	for _, k := range rkeys {
		t.Logf("the engine errors, node compiles: %s (%s)", k, strings.Join(reverse[k], ", "))
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if reason := notMirrored(k); reason != "" {
			t.Logf("not mirrored (%s): %s", reason, k)
			continue
		}
		t.Errorf("node errors, the engine compiles: %s (%s)", k, strings.Join(groups[k], ", ")+" ×"+strconv.Itoa(len(groups[k])))
	}
}

// notMirroredThrows are the places upstream throws on a value of the wrong
// type that the engine does not reproduce: each is a TypeError deep in one
// subsystem, past the structural slots (composition lists, transforms,
// params, repeat values, field types, channel definitions, data, selection
// definitions and names, resolve) that the engine checks as upstream does.
// A key is "<version> <path>=<value>: node \"<error>\"".
var notMirroredThrows = []struct {
	key    *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(` /config/`), "config properties of the wrong type, read deep in theme merging"},
	{regexp.MustCompile(`/axis=.*reading 'forEach'`), "an axis that is a signal or a number"},
	{regexp.MustCompile(`/scale=false: .*reading 'get'`), "a scale of false"},
	{regexp.MustCompile(`/scale/domain=.*domain\.map`), "a scale domain that is a number"},
	{regexp.MustCompile(`labelExpr=.*string\.replace`), "a labelExpr that is not a string"},
	{regexp.MustCompile(`Access path missing closing bracket: \[object Object\]|replaceAll`), "a field that is an object or false, read as an access path"},
	{regexp.MustCompile(`/field=null`), "a field of null"},
	{regexp.MustCompile(`could not be cloned`), "structuredClone of a function (a repeat or data value that names an Object.prototype member)"},
	{regexp.MustCompile(`Set\.prototype\.values called on incompatible receiver|\.add is not a function`), "an aggregate op named __proto__"},
	{regexp.MustCompile(`/tooltip/aggregate=`), "a composite mark's tooltip aggregate of the wrong type"},
	{regexp.MustCompile(`/params/N/name="": `), "an empty selection name"},
	{regexp.MustCompile(`/select/on=.*reading '0'|selector\.trim`), "selection events that are objects or arrays of the wrong shape"},
	{regexp.MustCompile(`/fields/N=null`), "a selection field of null"},
	{regexp.MustCompile(` .*/selection(/[^=]*)?=`), "a legacy (pre-5.0) selection definition of the wrong type"},
	{regexp.MustCompile(` /facet/`), "a facet mapping of the wrong type"},
	{regexp.MustCompile(`^5\.8 .*/encoding/(row|column|facet)/type=`), "5.8: an invalid type on a facet channel"},
	{regexp.MustCompile(`^5\.8 .*/encoding/[a-z0-9]+=\[`), "5.8: a channel definition that is an array of primitives"},
	{regexp.MustCompile(`^5\.8 .*/transform/N/(groupby|from/fields)`), "5.8: transform lists of the wrong type, iterated deep in the dataflow"},
	{regexp.MustCompile(`/transform/N/as=null`), "a transform's as of null"},
	{regexp.MustCompile(`/datum/repeat=`), "a datum repeat reference of the wrong type"},
	{regexp.MustCompile(`/encoding=null`), "an encoding of null"},
	{regexp.MustCompile(`/encoding/[a-zA-Z0-9]+=false: .*replaceAll`), "a channel of false"},
	{regexp.MustCompile(`^5\.8 .*/(width|height)/step=`), "5.8: a step size of 0, which 5.8 asserts against"},
	{regexp.MustCompile(`/range/N/(date|year|month|day|hours|minutes|seconds|milliseconds|quarter)=null: node "Unexpected token ,"`), "a null datetime part, which upstream prints into an expression it cannot parse"},
}

func notMirrored(key string) string {
	for _, m := range notMirroredThrows {
		if m.key.MatchString(key) {
			return m.reason
		}
	}
	return ""
}
