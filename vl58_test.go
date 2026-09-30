package aster_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
)

// TestVegaLite58MatchesNode compiles the vl-convert specs (written for
// Vega-Lite 5) with WithVegaLiteVersion("5.8"): the Vega must equal what
// upstream vega-lite 5.8.0 compiles, and the engine must render it. Upstream
// renders 5.8 output with Vega 5 while the engine uses its Vega 6.4 runtime,
// so the SVG is not compared.
func TestVegaLite58MatchesNode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs the node oracle")
	}
	o := oracle.For(t, oracle.VL5)
	c, err := aster.New(aster.WithVegaLiteVersion("5.8"), aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	files, _ := filepath.Glob("testdata/vl-convert/*.vl.json")
	if len(files) == 0 {
		t.Fatal("no vl-convert specs")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".vl.json")
		spec, _ := os.ReadFile(f)
		want, err := o.Compile(spec)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		if want.Err != "" {
			t.Logf("%s: vega-lite 5.8.0: %s", name, firstLine(want.Err))
			continue
		}
		got, err := c.VegaLiteToVega(spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		g, _ := jsval.ParseJSON(got)
		w, _ := jsval.ParseJSON(want.Vega)
		if !jsval.Equal(g, w) {
			t.Errorf("%s: compiled Vega differs from vega-lite 5.8.0's", name)
		}
		if _, err := c.VegaLiteToSVG(spec); err != nil {
			t.Errorf("%s: render: %v", name, err)
		}
	}
}
