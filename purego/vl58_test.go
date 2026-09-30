package purego_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/purego"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// TestVegaLite58MatchesReference compiles the vl-convert specs (written for
// Vega-Lite 5) with WithVegaLiteVersion("5.8") in both engines: the Vega they
// emit must be identical, and purego must render it. The reference renders
// 5.8 with Vega 5.25 while purego uses its Vega 6.4 runtime, so the SVG is
// not compared.
func TestVegaLite58MatchesReference(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs the reference engine")
	}
	ref, err := aster.New(aster.WithVegaLiteVersion("5.8"), aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	pg, err := purego.New(purego.WithVegaLiteVersion("5.8"), purego.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	files, _ := filepath.Glob("../testdata/vl-convert/*.vl.json")
	if len(files) == 0 {
		t.Fatal("no vl-convert specs")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".vl.json")
		spec, _ := os.ReadFile(f)
		want, err := ref.VegaLiteToVega(spec)
		if err != nil {
			t.Logf("%s: reference: %v", name, firstLine(err.Error()))
			continue
		}
		got, err := pg.VegaLiteToVega(spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		g, _ := jsval.ParseJSON(got)
		w, _ := jsval.ParseJSON(want)
		if !jsval.Equal(g, w) {
			t.Errorf("%s: compiled Vega differs from vega-lite 5.8.0's", name)
		}
		if _, err := pg.VegaLiteToSVG(spec); err != nil {
			t.Errorf("%s: render: %v", name, err)
		}
	}
}
