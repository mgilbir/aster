package vega

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego/internal/expr"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/svg"
	"github.com/mgilbir/aster/purego/internal/svgdiff"
)

// TestCorpusSVG renders every recorded specification to SVG through package svg
// and compares it structurally with upstream's view.toSVG() (testdata/svg,
// recorded by gen_scenegraph.mjs with WITH_SVG=1).
func TestCorpusSVG(t *testing.T) {
	specs := specFiles()
	only := os.Getenv("VEGA_ONLY")
	pass, total := 0, 0
	var failed []string
	for _, spec := range specs {
		name := strings.TrimSuffix(filepath.Base(spec), ".vg.json")
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		want, err := readGz(filepath.Join("testdata", "svg", name+".svg.gz"))
		if err != nil {
			continue
		}
		total++
		b, _ := os.ReadFile(spec)
		v, err := jsval.ParseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Render(context.Background(), v, Options{
			Loader: newTestLoader(),
			Now:    func() time.Time { return time.UnixMilli(1700000000000) },
			Random: expr.NewLCG(12345),
		})
		var diff string
		if err != nil {
			diff = "error: " + err.Error()
		} else {
			got, rerr := svg.Render(context.Background(), res.Scenegraph, svg.Options{
				Width: res.Width, Height: res.Height, Origin: res.Origin, Background: res.Background,
			})
			if rerr != nil {
				diff = "svg error: " + rerr.Error()
			} else if r, cerr := svgdiff.Compare([]byte(got), want, svgdiff.DefaultOptions); cerr != nil {
				diff = cerr.Error()
			} else if !r.Equal {
				diff = r.Diff
			}
		}
		if diff == "" {
			pass++
		} else {
			failed = append(failed, name)
			if only != "" {
				t.Logf("%s: %s", name, diff)
			}
		}
	}
	t.Logf("svg scoreboard: %d/%d specs match", pass, total)
	if only == "" && len(failed) > 0 {
		t.Logf("failing: %s", strings.Join(failed, " "))
	}
}
