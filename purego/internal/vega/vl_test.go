package vega

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego/internal/expr"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/vegalite"
)

// TestVegaLiteRuns compiles the Vega-Lite corpora with package vegalite and
// renders the result, reporting the specifications that fail to render. It
// checks that the engine copes with what real compiled charts contain; the
// output itself is compared by the public package's SVG comparison.
func TestVegaLiteRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every Vega-Lite example")
	}
	var files []string
	for _, g := range []string{
		filepath.Join(repoRoot, "testdata", "vega-lite", "v6.4.0", "specs", "*.vl.json"),
		filepath.Join(repoRoot, "purego", "testdata", "corpus", "vegalite", "*.vl.json"),
	} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	sort.Strings(files)
	only := os.Getenv("VEGA_ONLY")
	ok, total := 0, 0
	byError := map[string][]string{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".vl.json")
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		spec, err := jsval.ParseJSON(b)
		if err != nil {
			continue
		}
		vg, err := vegalite.Compile(spec, vegalite.Options{})
		if err != nil {
			continue // a compile error belongs to the compiler's own tests
		}
		total++
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, rerr := Render(ctx, vg, Options{
			Loader: newTestLoader(),
			Now:    func() time.Time { return time.UnixMilli(1700000000000) },
			Random: expr.NewLCG(12345),
		})
		cancel()
		if rerr == nil {
			ok++
			if d := time.Since(start); d > 5*time.Second {
				t.Logf("%s: slow (%v)", name, d)
			}
			continue
		}
		msg := firstLine(rerr.Error())
		byError[msg] = append(byError[msg], name)
	}
	t.Logf("vega-lite render: %d/%d render without error", ok, total)
	keys := make([]string, 0, len(byError))
	for k := range byError {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(byError[keys[i]]) > len(byError[keys[j]]) })
	for _, k := range keys {
		names := byError[k]
		if len(names) > 4 {
			names = append(names[:4:4], fmt.Sprintf("... %d more", len(byError[k])-4))
		}
		t.Logf("%3d  %s  [%s]", len(byError[k]), trunc(k), strings.Join(names, " "))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestVegaLiteScenes renders Vega specifications that upstream's Vega-Lite
// compiler produced for the Vega-Lite examples and compares the scenegraph with
// the one upstream Vega rendered from the same specification (testdata/vl,
// recorded by gen_vl_scenegraph.mjs). VEGA_VL_DIR selects another directory of
// uncompressed recordings.
func TestVegaLiteScenes(t *testing.T) {
	dir := os.Getenv("VEGA_VL_DIR")
	var files []string
	if dir != "" {
		files, _ = filepath.Glob(filepath.Join(dir, "*.json"))
	} else {
		files, _ = filepath.Glob(filepath.Join("testdata", "vl", "*.json.gz"))
	}
	sort.Strings(files)
	only := os.Getenv("VEGA_ONLY")
	pass, total := 0, 0
	var failed []string
	for _, f := range files {
		name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".gz"), ".json")
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		var b []byte
		var err error
		if dir != "" {
			b, err = os.ReadFile(f)
		} else {
			b, err = readGz(f)
		}
		if err != nil {
			t.Fatal(err)
		}
		rec, err := jsval.ParseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Get("error").IsStr() {
			continue
		}
		total++
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		res, rerr := Render(ctx, rec.Get("vega"), Options{
			Loader: newTestLoader(),
			Now:    func() time.Time { return time.UnixMilli(1700000000000) },
			Random: expr.NewLCG(12345),
		})
		cancel()
		var diffs []string
		if rerr != nil {
			diffs = append(diffs, "error: "+firstLine(rerr.Error()))
		} else {
			diffScene("", markJSON(res.Scenegraph.Root), rec.Get("scene"), 1e-9, &diffs, 12)
			diffScene("width", jsval.Num(res.Width), rec.Get("width"), 1e-9, &diffs, 6)
			diffScene("height", jsval.Num(res.Height), rec.Get("height"), 1e-9, &diffs, 6)
		}
		if len(diffs) == 0 {
			pass++
			continue
		}
		failed = append(failed, name)
		if os.Getenv("VEGA_SUMMARY") != "" {
			t.Logf("FAIL %s: %s", name, trunc(strings.Join(diffs[:min(len(diffs), 2)], " | ")))
		} else if only != "" {
			t.Logf("FAIL %s:\n  %s", name, strings.Join(diffs, "\n  "))
			if os.Getenv("VEGA_TREE") != "" && rerr == nil {
				var a, w strings.Builder
				structure(markJSON(res.Scenegraph.Root), 0, "", &a)
				structure(rec.Get("scene"), 0, "", &w)
				t.Logf("got:\n%s\nwant:\n%s", a.String(), w.String())
			}
		}
	}
	t.Logf("vega-lite scoreboard: %d/%d recorded scenes match", pass, total)
	if only == "" && len(failed) > 0 && os.Getenv("VEGA_SUMMARY") == "" {
		t.Logf("failing: %s", strings.Join(failed, " "))
	}
}
