package vega

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func TestMergeConfig(t *testing.T) {
	base := mustJSON(t, `{"axis":{"domain":true,"tickSize":5},"style":{"a":{"x":1,"y":2}},
		"legend":{"orient":"right","layout":{"offset":18,"left":{"direction":"vertical"}}},
		"signals":[{"name":"s","value":1},{"name":"t","value":2}]}`)
	over := mustJSON(t, `{"axis":{"tickSize":7},"style":{"a":{"y":3},"b":{"z":4}},
		"legend":{"layout":{"offset":20}},
		"signals":[{"name":"s","value":9}]}`)
	got := mergeConfig(base, over)
	if v := got.Get("axis").Get("domain"); !v.IsTruthy() {
		t.Error("axis.domain lost: object keys merge one level deep")
	}
	if v := got.Get("axis").Get("tickSize").NumValue(); v != 7 {
		t.Errorf("tickSize = %v", v)
	}
	if v := got.Get("style").Get("a").Get("x").NumValue(); v != 1 {
		t.Errorf("style merges recursively, x = %v", v)
	}
	if v := got.Get("legend").Get("orient").AsString(); v != "right" {
		t.Errorf("orient = %v", v)
	}
	if got.Get("legend").Get("layout").Get("offset").NumValue() != 20 {
		t.Error("legend.layout.offset not overridden")
	}
	sigs := got.Get("signals").Items()
	if len(sigs) != 2 || sigs[0].Get("value").NumValue() != 9 {
		t.Errorf("signals merge by name with later winning, got %v", got.Get("signals"))
	}
	if defaultConfig().Get("axis").Get("tickSize").NumValue() != 5 {
		t.Error("defaults mutated")
	}
}

func mustJSON(t testing.TB, s string) jsval.Value {
	t.Helper()
	v, err := jsval.ParseJSONString(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseDSV(t *testing.T) {
	rows, cols := parseDSV("a,b,c\n1,\"x,y\",3\r\n4,\"q\"\"r\"\n\n", ',')
	if len(cols) != 3 || cols[2] != "c" {
		t.Fatalf("columns %v", cols)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	if got := rows[0].Get("b").StrValue(); got != "x,y" {
		t.Errorf("quoted comma: %q", got)
	}
	if got := rows[1].Get("b").StrValue(); got != `q"r` {
		t.Errorf("escaped quote: %q", got)
	}
	if v := rows[1].Get("c"); !v.IsStr() || v.StrValue() != "" {
		t.Errorf("a missing cell is the empty string, got %v", v)
	}
	if r, c := parseDSV("", ','); len(r) != 0 || len(c) != 0 {
		t.Errorf("empty input: %v %v", r, c)
	}
}

func TestInferType(t *testing.T) {
	data := func(vals ...string) []jsval.Value {
		out := make([]jsval.Value, len(vals))
		for i, v := range vals {
			out[i] = jsval.Obj(jsval.ObjectOf("f", jsval.Str(v)))
		}
		return out
	}
	cases := []struct {
		vals []string
		want string
	}{
		{[]string{"1", "2", "3"}, "integer"},
		{[]string{"1", "2.5"}, "number"},
		{[]string{"true", "false"}, "boolean"},
		{[]string{"2020-01-01", "2021-05-06"}, "date"},
		{[]string{"a", "1"}, "string"},
	}
	for _, c := range cases {
		if got := inferType(data(c.vals...), "f"); got != c.want {
			t.Errorf("%v: got %s want %s", c.vals, got, c.want)
		}
	}
}

// Renders share no mutable state: concurrent calls (run with -race) must agree.
func TestConcurrentRender(t *testing.T) {
	specs, _ := filepath.Glob(filepath.Join(repoRoot, "purego", "testdata", "corpus", "vega", "bar*.vg.json"))
	if len(specs) == 0 {
		t.Skip("no corpus")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := specs[i%len(specs)]
			b, _ := os.ReadFile(f)
			v, _ := jsval.ParseJSON(b)
			for j := 0; j < 3; j++ {
				if _, err := Render(context.Background(), v, Options{Loader: newTestLoader()}); err != nil {
					t.Errorf("%s: %v", filepath.Base(f), err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// The same specification value can be rendered repeatedly: rendering never
// writes into the specification (transforms annotate copies of the data).
func TestSpecNotMutated(t *testing.T) {
	spec := mustJSON(t, `{"data":[{"name":"t","values":[{"a":1},{"a":2}],
		"transform":[{"type":"formula","expr":"datum.a*2","as":"b"}]}],
		"marks":[{"type":"symbol","from":{"data":"t"}}]}`)
	before := spec.String()
	for i := 0; i < 2; i++ {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	if spec.String() != before {
		t.Errorf("specification mutated:\n%s\n%s", before, spec.String())
	}
}

func TestSignalUpdatesAndInit(t *testing.T) {
	spec := mustJSON(t, `{"signals":[
		{"name":"a","value":2},
		{"name":"b","update":"a*3"},
		{"name":"c","init":"a+10"},
		{"name":"d","value":0,"on":[{"events":{"signal":"a"},"update":"a+100"}]}
	]}`)
	v := debugView(t, spec)
	get := func(n string) float64 { return v.root.signal(n).sig().NumValue() }
	if get("b") != 6 || get("c") != 12 {
		t.Errorf("b=%v c=%v", get("b"), get("c"))
	}
	// signal-sourced event handlers do not fire on the initial run
	if get("d") != 0 {
		t.Errorf("d=%v", get("d"))
	}
}

func TestParseDSVLimitFailsBeforeAllocating(t *testing.T) {
	text := "a\n" + strings.Repeat("1\n", 10000)
	if _, _, err := parseDSVLimit(context.Background(), text, ',', 100); err == nil {
		t.Fatal("10000 rows against a limit of 100 must fail")
	}
	rows, _, err := parseDSVLimit(context.Background(), text, ',', 20000)
	if err != nil || len(rows) != 10000 {
		t.Fatalf("within the limit: %d rows, %v", len(rows), err)
	}
	wide := strings.Repeat("c,", 99) + "c\n" + strings.Repeat("1\n", 2000)
	if _, _, err := parseDSVLimit(context.Background(), wide, ',', 100); err == nil {
		t.Fatal("wide rows must be bounded by cells")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := parseDSVLimit(ctx, text, ',', 1<<30); err == nil {
		t.Fatal("cancelled context must stop parsing")
	}
}
