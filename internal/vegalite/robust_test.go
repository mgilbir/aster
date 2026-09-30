package vegalite

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

func mustParse(t *testing.T, s string) jsval.Value {
	t.Helper()
	v, err := jsval.ParseJSONString(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNestingIsBounded(t *testing.T) {
	// Layers nested far deeper than any real chart must produce an error, not a
	// stack overflow.
	inner := mkv("mark", "point", "encoding", mkv("x", mkv("field", "a", "type", "quantitative")))
	spec := inner
	for i := 0; i < 5000; i++ {
		spec = mkv("layer", arr(spec))
	}
	if _, err := Compile(spec, Options{}); err == nil {
		t.Fatal("expected a depth error")
	}
	spec = inner
	for i := 0; i < 5000; i++ {
		spec = mkv("vconcat", arr(spec))
	}
	if _, err := Compile(spec, Options{}); err == nil {
		t.Fatal("expected a depth error for concat")
	}
	spec = inner
	for i := 0; i < 5000; i++ {
		spec = mkv("facet", mkv("row", mkv("field", "a", "type", "nominal")), "spec", spec)
	}
	if _, err := Compile(spec, Options{}); err == nil {
		t.Fatal("expected a depth error for facet")
	}
}

func TestExpressionNestingIsBounded(t *testing.T) {
	for _, expr := range []string{
		strings.Repeat("(", 100000) + "1" + strings.Repeat(")", 100000),
		strings.Repeat("!", 100000) + "1",
		strings.Repeat("[", 100000) + strings.Repeat("]", 100000),
		strings.Repeat("f(", 100000) + strings.Repeat(")", 100000),
	} {
		spec := mkv("data", mkv("values", arr(mkv("a", 1))), "transform", arr(mkv("calculate", expr, "as", "b")),
			"mark", "point", "encoding", mkv("x", mkv("field", "b", "type", "quantitative")))
		if _, err := Compile(spec, Options{}); err == nil {
			t.Errorf("expected an error for a %d byte expression", len(expr))
		}
	}
}

func TestRepeatIsBounded(t *testing.T) {
	vals := make([]any, 200)
	for i := range vals {
		vals[i] = "f"
	}
	spec := mkv("repeat", mkv("row", vals, "column", vals), "spec", mkv("mark", "point",
		"encoding", mkv("x", mkv("field", mkv("repeat", "row"), "type", "quantitative"))))
	if _, err := Compile(spec, Options{}); err == nil {
		t.Fatal("expected a repeat limit error")
	}
}

func TestInvalidInputsDoNotPanic(t *testing.T) {
	for _, in := range []string{
		`null`, `1`, `"x"`, `[]`, `{}`, `{"mark": 3}`, `{"mark": null}`, `{"layer": 3}`, `{"layer": [null]}`,
		`{"mark": "bar", "encoding": 3}`, `{"mark": "bar", "encoding": {"x": null}}`, `{"mark": "bar", "encoding": {"x": []}}`,
		`{"mark": "bar", "encoding": {"x": {"field": 3, "type": {}}}}`, `{"hconcat": [1]}`, `{"repeat": 3, "spec": {}}`,
		`{"facet": 3, "spec": {"mark": "bar"}}`, `{"mark": "bar", "transform": [null, 3, "x", {"filter": 5}]}`,
		`{"mark": "bar", "config": 5}`, `{"mark": "bar", "config": {"axis": 3, "mark": []}}`,
		`{"mark": "bar", "params": [null, {"name": 3, "select": 4}]}`, `{"mark": "bar", "data": 5}`,
	} {
		v := mustParse(t, in)
		if out, err := Compile(v, Options{}); err != nil && out.Kind() != jsval.KindUndefined {
			t.Errorf("%s: result alongside error", in)
		}
	}
}

// TestRandomSpecsDoNotHang throws random JSON trees at the compiler; whatever the
// result, it must return promptly and never panic.
func TestRandomSpecsDoNotHang(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	keys := []string{"mark", "encoding", "x", "y", "color", "field", "type", "layer", "hconcat", "vconcat", "concat",
		"facet", "row", "column", "spec", "repeat", "transform", "filter", "calculate", "as", "aggregate", "bin",
		"timeUnit", "scale", "axis", "legend", "sort", "stack", "params", "select", "bind", "data", "values",
		"name", "config", "width", "height", "resolve", "condition", "test", "value", "datum", "detail", "order"}
	scalars := []jsval.Value{jsval.Str("bar"), jsval.Str("point"), jsval.Str("a"), jsval.Str("quantitative"),
		jsval.Str("nominal"), jsval.Str("temporal"), jsval.Str("datum.a > 1"), jsval.Int(3), jsval.True, jsval.Null,
		jsval.Str("count"), jsval.Str("month"), jsval.Str("click"), jsval.Str("interval")}
	var gen func(depth int) jsval.Value
	gen = func(depth int) jsval.Value {
		if depth <= 0 || rng.Intn(4) == 0 {
			return scalars[rng.Intn(len(scalars))]
		}
		if rng.Intn(5) == 0 {
			n := rng.Intn(3)
			items := make([]jsval.Value, n)
			for i := range items {
				items[i] = gen(depth - 1)
			}
			return jsval.Arr(items)
		}
		o := jsval.NewObject(4)
		for i, n := 0, 1+rng.Intn(4); i < n; i++ {
			o.Set(keys[rng.Intn(len(keys))], gen(depth-1))
		}
		return jsval.Obj(o)
	}
	start := time.Now()
	for i := 0; i < 3000; i++ {
		spec := gen(6)
		if _, err := Compile(spec, Options{}); err != nil && strings.Contains(err.Error(), "goroutine") {
			t.Fatalf("stack in error: %v", err)
		}
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("random specs took %v", d)
	}
}

// TestConcurrentCompile compiles the same specs from many goroutines and checks
// they agree with a sequential run (and, under -race, that no state is shared).
func TestConcurrentCompile(t *testing.T) {
	refs := loadReferences(t)
	if len(refs) > 300 {
		refs = refs[:300]
	}
	specs := make([]jsval.Value, len(refs))
	want := make([]string, len(refs))
	for i, r := range refs {
		data, err := os.ReadFile(filepath.Join("testdata", "specs", r.name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		specs[i] = mustParse(t, string(data))
		out, err := Compile(specs[i], Options{})
		if err == nil {
			want[i] = string(jsval.AppendJSON(nil, out))
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range specs {
				j := (i + g*37) % len(specs)
				out, err := Compile(specs[j], Options{})
				got := ""
				if err == nil {
					got = string(jsval.AppendJSON(nil, out))
				}
				if got != want[j] {
					t.Errorf("%s: concurrent result differs", refs[j].name)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
