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

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// confusedValues are values of the wrong type for almost every slot of a
// specification.
var confusedValues = []string{
	"null", "true", "false", `""`, `"x"`, `"1e999"`, `"NaN"`, `"__proto__"`, `"constructor"`, `"datum.x"`,
	"0", "-1", "1e308", "[]", "{}", "[1,2]", `["a","b"]`, `{"a":1}`, "[[1]]", "[{}]",
	`{"signal":"1/0"}`, `{"signal":"datum"}`, `{"field":"nofield"}`, `{"value":null}`, `{"type":{}}`,
}

// nodePaths lists the path of every value in v (containers and scalars).
func nodePaths(v jsval.Value) [][]string {
	var out [][]string
	var walk func(v jsval.Value, path []string)
	walk = func(v jsval.Value, path []string) {
		if len(path) > 0 {
			out = append(out, path)
		}
		switch {
		case v.IsObj():
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				walk(o.ValueAt(i), append(append([]string(nil), path...), o.KeyAt(i)))
			}
		case v.IsArr():
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), append(append([]string(nil), path...), strconv.Itoa(i)))
			}
		}
	}
	walk(v, nil)
	return out
}

func setAtPath(root jsval.Value, path []string, nv jsval.Value) {
	v := root
	for _, p := range path[:len(path)-1] {
		if v.IsObj() {
			v = v.ObjValue().Lookup(p)
		} else {
			i, _ := strconv.Atoi(p)
			v = v.Index(i)
		}
	}
	last := path[len(path)-1]
	if v.IsObj() {
		v.ObjValue().Set(last, nv)
	} else {
		i, _ := strconv.Atoi(last)
		v.Items()[i] = nv
	}
}

// TestTypeConfusion replaces, one at a time, values of corpus specifications
// (every key, array element and container) by values of another type and checks
// that the compiler returns an error or a result but never panics: a panic
// surfaces from Compile as an "internal error" (its recover), which this test
// rejects. The mutants are sampled deterministically; VEGALITE_FUZZ_N raises
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
		paths := nodePaths(spec)
		if len(paths) == 0 {
			continue
		}
		path := paths[rng.Intn(len(paths))]
		val := confusedValues[rng.Intn(len(confusedValues))]
		setAtPath(spec, path, mustParse(t, val))
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
			_, err := Compile(mustParse(t, spec), Options{Version: version})
			if err == nil || strings.Contains(err.Error(), "internal error") {
				t.Errorf("%s (%s): want a descriptive error, got %v", name, version, err)
			}
		}
	}
}
