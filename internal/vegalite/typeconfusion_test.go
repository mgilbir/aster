package vegalite

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fuzzutil/confusion"
	"github.com/mgilbir/aster/internal/jsval"
)

// TestTypeConfusion replaces, one at a time, values of corpus specifications
// (every key, array element and container) by values of another type, or by
// values derived from them, or renames a key (confusion.Confuse), and checks
// that the compiler returns an error or a result but never panics: a panic
// surfaces from Compile as an "internal error" (its recover), which this test
// rejects. Whether it fails exactly where upstream throws is checked against
// node by the root package's TestVegaLiteTypeConfusionMatchesNode (ADR 0013). The mutants are sampled deterministically; VEGALITE_FUZZ_N raises
// the count (and VEGALITE_FUZZ_SEED changes the sample).
func TestTypeConfusion(t *testing.T) {
	n := 1500
	if s := os.Getenv("VEGALITE_FUZZ_N"); s != "" {
		n, _ = strconv.Atoi(s)
	} else if testing.Short() {
		n = 300
	}
	seed := int64(1)
	if s := os.Getenv("VEGALITE_FUZZ_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	files, _ := filepath.Glob("testdata/specs/*.json")
	more, _ := filepath.Glob("testdata/specs/*/*.json")
	files = append(files, more...)
	if len(files) == 0 {
		t.Skip("no corpus")
	}
	sort.Strings(files)
	rng := rand.New(rand.NewSource(seed))
	bad := map[string]string{}
	var lastStack string
	panicHook = func(st []byte) { lastStack = string(st) }
	defer func() { panicHook = nil }()
	for k := 0; k < n; k++ {
		f := files[rng.Intn(len(files))]
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			continue
		}
		slot, val, ok := confusion.Confuse(rng, spec)
		if !ok {
			continue
		}
		path := slot.Path
		version := Version64
		if k%2 == 1 {
			version = Version58
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, cerr := Compile(spec, Options{Version: version, Location: time.UTC, Context: ctx})
		cancel()
		if cerr != nil && (strings.Contains(cerr.Error(), "internal error") || cerr == context.DeadlineExceeded) {
			key := panicSite(lastStack)
			if _, dup := bad[key]; !dup {
				bad[key] = fmt.Sprintf("%s [%s=%s] (%s): %v", filepath.Base(f), strings.Join(path, "/"), val, version, cerr)
			}
		}
	}
	keys := make([]string, 0, len(bad))
	for k := range bad {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%s\n\tat %s", bad[k], k)
	}
}

// panicSite names where a panic happened: the first stack frame inside this
// package that is not the recovery itself.
func panicSite(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, l := range lines {
		if strings.Contains(l, "/vegalite.") && !strings.Contains(l, "Compile.func") && !strings.Contains(l, "runtime/") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	return "unknown"
}

func TestInvalidSpecsReturnErrors(t *testing.T) {
	data := `"data":{"values":[{"a":1}]}`
	enc := `"encoding":{"x":{"field":"a","type":"quantitative"}}`
	for name, spec := range map[string]string{
		"unknown mark":     `{` + data + `,"mark":"nope",` + enc + `}`,
		"mark array":       `{` + data + `,"mark":[1,2],` + enc + `}`,
		"mark type object": `{` + data + `,"mark":{"type":{}},` + enc + `}`,
		"mark null":        `{` + data + `,"mark":null,` + enc + `}`,
		"translate":        `{` + data + `,"params":[{"name":"p","select":{"type":"interval","translate":"__proto__"}}],"mark":"point",` + enc + `}`,
		"on numbers":       `{` + data + `,"params":[{"name":"p","select":{"type":"point","on":[1,2]}}],"mark":"point",` + enc + `}`,
		"lookup from":      `{` + data + `,"transform":[{"lookup":"a","from":{"signal":"datum"}}],"mark":"point",` + enc + `}`,
	} {
		for _, version := range []string{Version64, Version58} {
			if name == "on numbers" && version == Version58 {
				continue // 5.8 has no timer selections, so nothing reads the events as objects; node compiles it
			}
			_, err := Compile(mustParse(t, spec), Options{Version: version})
			if err == nil || strings.Contains(err.Error(), "internal error") {
				t.Errorf("%s (%s): want a descriptive error, got %v", name, version, err)
			}
		}
	}
}

// TestConversionTypeErrorMessage: String() of an object with no conversion
// to text is reported as every TypeError the compiler mirrors is (throw):
// JavaScript's message alone, whichever of the two paths raises it (an own
// toString of a plain object, a null-prototype object in the compile's
// conversion scope).
func TestConversionTypeErrorMessage(t *testing.T) {
	const want = "Cannot convert object to primitive value"
	for _, c := range []struct{ name, version, spec string }{
		{"own toString, field", "6.4", `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":{"toString":"x"},"type":"quantitative"}}}`},
		{"own toString, title", "6.4", `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","title":{"toString":1}}}}`},
		{"null prototype, title", "5.8", `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","title":{"__proto__":null}}}}`},
	} {
		_, err := Compile(mustParse(t, c.spec), Options{Version: c.version})
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want exactly %q", c.name, err, want)
		}
	}
	// The path of an object outside the compile's conversion scope (its
	// own toString) raises a *jsval.Thrown, reported the same way.
	func() {
		defer func() {
			if got := panicError(recover()); got.Error() != want {
				t.Errorf("unscoped object with a toString of its own: err = %q, want exactly %q", got, want)
			}
		}()
		_ = mkv("toString", 1).AsString()
	}()
}
