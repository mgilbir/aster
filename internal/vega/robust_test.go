package vega

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

func renderJSON(t testing.TB, ctx context.Context, spec string) (*Result, error) {
	t.Helper()
	v, err := jsval.ParseJSONString(spec)
	if err != nil {
		t.Fatal(err)
	}
	return Render(ctx, v, Options{Loader: newTestLoader()})
}

// Specifications are untrusted: none of these may panic or hang.
func TestHostileSpecs(t *testing.T) {
	deep := `{"marks":[`
	for i := 0; i < 500; i++ {
		deep += `{"type":"group","marks":[`
	}
	deep += `{"type":"rect"}`
	for i := 0; i < 500; i++ {
		deep += `]}`
	}
	deep += `]}`

	cases := map[string]string{
		"signal cycle":   `{"signals":[{"name":"a","update":"b+1"},{"name":"b","update":"a+1"}]}`,
		"self reference": `{"signals":[{"name":"a","update":"a+1"}]}`,
		"unknown signal": `{"signals":[{"name":"a","update":"nope"}]}`,
		"deep groups":    deep,
		"huge sequence":  `{"data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":1e15}]}]}`,
		"bad mark":       `{"marks":[{"type":"nothing"}]}`,
		"bad scale":      `{"scales":[{"name":"x","type":"nothing"}]}`,
		"dup signal":     `{"signals":[{"name":"a","value":1},{"name":"a","value":2}]}`,
		"bad data ref":   `{"marks":[{"type":"rect","from":{"data":"missing"}}]}`,
		"layout loop": `{"width":100,"height":100,"autosize":"fit","signals":[{"name":"width","update":"width + 1"}],
			"marks":[{"type":"rect","encode":{"update":{"width":{"signal":"width"}}}}]}`,
		"not an object": `[]`,
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("panic: %v", r)
					}
				}()
				v, err := jsval.ParseJSONString(spec)
				if err != nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_, err = Render(ctx, v, Options{})
				t.Logf("%s: %v", name, err)
			}()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("hang")
			}
		})
	}
}

// A cancelled context stops a render with its error.
func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := `{"data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":100000}]}],
	  "marks":[{"type":"rect","from":{"data":"d"}}]}`
	if _, err := renderJSON(t, ctx, spec); err == nil {
		t.Fatal("expected an error")
	}
}

// Rendering the corpus with an already-cancelled context must not panic.
func TestCorpusCancelled(t *testing.T) {
	specs, _ := filepath.Glob(filepath.Join(repoRoot, "testdata", "corpus", "vega", "*.vg.json"))
	sort.Strings(specs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, f := range specs {
		b, _ := os.ReadFile(f)
		v, _ := jsval.ParseJSON(b)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v", filepath.Base(f), r)
				}
			}()
			_, _ = Render(ctx, v, Options{Loader: newTestLoader()})
		}()
	}
}

func TestUnknownLoaderIsDenied(t *testing.T) {
	spec := `{"data":[{"name":"d","url":"https://example.com/x.json"}],"marks":[{"type":"rect","from":{"data":"d"}}]}`
	v, _ := jsval.ParseJSONString(spec)
	res, err := Render(context.Background(), v, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "Loading failed") {
		t.Fatalf("expected a load warning, got %v", res.Warnings)
	}
}

// mutate returns a copy of v with one randomly chosen node damaged: removed,
// replaced by another type, or a number changed to an extreme.
func mutate(rng *rand.Rand, v jsval.Value) jsval.Value {
	// collect the paths of all nodes
	type path []any
	var paths []path
	var walk func(v jsval.Value, p path)
	walk = func(v jsval.Value, p path) {
		paths = append(paths, append(path(nil), p...))
		switch v.Kind() {
		case jsval.KindObj:
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				walk(o.ValueAt(i), append(p, o.KeyAt(i)))
			}
		case jsval.KindArr:
			for i, it := range v.Items() {
				walk(it, append(p, i))
			}
		}
	}
	walk(v, nil)
	target := paths[rng.Intn(len(paths))]
	replacements := []jsval.Value{
		jsval.Null, jsval.Undefined, jsval.Num(0), jsval.Num(-1), jsval.Num(1e300), jsval.Num(math.NaN()),
		jsval.Str(""), jsval.Str("x"), jsval.True, jsval.Arr(nil), jsval.Obj(jsval.NewObject(0)),
		jsval.ArrOf(jsval.Num(1), jsval.Str("a")),
	}
	var rebuild func(v jsval.Value, p path) jsval.Value
	rebuild = func(v jsval.Value, p path) jsval.Value {
		if len(p) == 0 {
			return replacements[rng.Intn(len(replacements))]
		}
		switch k := p[0].(type) {
		case string:
			o := v.ObjValue().Clone()
			if len(p) == 1 && rng.Intn(3) == 0 {
				o.Delete(k)
			} else {
				o.Set(k, rebuild(o.Lookup(k), p[1:]))
			}
			return jsval.Obj(o)
		case int:
			items := append([]jsval.Value(nil), v.Items()...)
			items[k] = rebuild(items[k], p[1:])
			return jsval.Arr(items)
		}
		return v
	}
	return rebuild(v, target)
}

// Damaged specifications must fail cleanly: no panic, no hang.
func TestMutatedSpecs(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	var pool []jsval.Value
	for _, f := range specFiles() {
		if b, err := os.ReadFile(f); err == nil {
			if v, err := jsval.ParseJSON(b); err == nil {
				pool = append(pool, v)
			}
		}
	}
	// compiled Vega-Lite charts are the richest specifications
	vls, _ := filepath.Glob(filepath.Join("testdata", "vl", "*.json.gz"))
	for i, f := range vls {
		if i%3 != 0 {
			continue
		}
		if b, err := readGz(f); err == nil {
			if rec, err := jsval.ParseJSON(b); err == nil {
				pool = append(pool, rec.Get("vega"))
			}
		}
	}
	rng := rand.New(rand.NewSource(1))
	n := 1500
	if v, err := strconv.Atoi(os.Getenv("VEGA_FUZZ_N")); err == nil {
		n = v
	}
	rng.Seed(1 + int64(n))
	for i := 0; i < n; i++ {
		idx := rng.Intn(len(pool))
		f := fmt.Sprintf("spec#%d", idx)
		m := mutate(rng, pool[idx])
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s (mutation %d): panic: %v\n%s", f, i, r, string(jsval.AppendJSON(nil, m)))
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := Render(ctx, m, Options{Loader: newTestLoader(), Limits: Limits{MaxRows: 200000, MaxItems: 200000}}); err != nil && ctx.Err() != nil {
				t.Errorf("%s (mutation %d): render did not finish: %v", f, i, err)
			}
		}()
	}
}
