package vega

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestCorpus renders every recorded specification and compares the scenegraph
// with the one upstream Vega produced (testdata/scene, recorded by
// gen_scenegraph.mjs). It reports how many match; set VEGA_ONLY to a name
// substring to print the differences of matching specs.
func TestCorpus(t *testing.T) {
	specs := specFiles()
	only := os.Getenv("VEGA_ONLY")
	pass, total, skipped := 0, 0, 0
	var failed []string
	for _, spec := range specs {
		name := strings.TrimSuffix(filepath.Base(spec), ".vg.json")
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		gp := filepath.Join("testdata", "scene", name+".json.gz")
		gb, err := readGz(gp)
		if err != nil {
			skipped++
			continue
		}
		golden, err := jsval.ParseJSON(gb)
		if err != nil {
			t.Fatalf("%s: %v", gp, err)
		}
		total++
		start := time.Now()
		res, got, rerr := renderSpecFile(t, spec)
		var diffs []string
		if e := golden.Get("error"); e.IsStr() {
			if rerr == nil {
				diffs = append(diffs, "upstream fails ("+trunc(e.StrValue())+") but we rendered")
			}
		} else if rerr != nil {
			diffs = append(diffs, "error: "+rerr.Error())
		} else {
			diffScene("", got, golden.Get("scene"), 1e-9, &diffs, 12)
			for _, k := range []string{"width", "height"} {
				diffScene(k, jsval.Num(map[string]float64{"width": res.Width, "height": res.Height}[k]), golden.Get(k), 1e-9, &diffs, 12)
			}
			diffScene("origin", jsval.ArrOf(jsval.Num(res.Origin[0]), jsval.Num(res.Origin[1])), golden.Get("origin"), 1e-9, &diffs, 12)
		}
		if len(diffs) == 0 {
			pass++
		} else {
			failed = append(failed, name)
			if only == "" && os.Getenv("VEGA_SUMMARY") != "" {
				t.Logf("FAIL %s: %s", name, trunc(diffs[0]))
			}
			if only != "" {
				t.Logf("%s (%v):\n  %s", name, time.Since(start), strings.Join(diffs, "\n  "))
				if os.Getenv("VEGA_TREE") != "" && rerr == nil {
					var a, w strings.Builder
					structure(got, 0, "", &a)
					structure(golden.Get("scene"), 0, "", &w)
					t.Logf("got:\n%s\nwant:\n%s", a.String(), w.String())
				}
			}
		}
	}
	t.Logf("scoreboard: %d/%d specs match (%d without a recording)", pass, total, skipped)
	if only == "" && len(failed) > 0 {
		t.Logf("failing: %s", strings.Join(failed, " "))
	}
	_ = fmt.Sprint
}

// structure summarises a scene JSON tree: mark types, roles and item counts.
func structure(v jsval.Value, depth int, indent string, b *strings.Builder) {
	if depth > 5 || !v.IsObj() {
		return
	}
	fmt.Fprintf(b, "%s%s role=%s items=%d bounds=%s\n", indent, v.Get("marktype").AsString(), v.Get("role").AsString(), v.Get("items").Len(), v.Get("bounds").String())
	for _, it := range v.Get("items").Items() {
		for _, m := range it.Get("items").Items() {
			structure(m, depth+1, indent+"  ", b)
		}
		break
	}
}
