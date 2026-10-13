package aster_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/raster"
)

// The rasterizer's default work budget (128 Mi pixel operations at scale 1,
// times the scale squared above it, at least 16 per output pixel) keeps at
// least twice what every corpus chart spends: each of its renders, the PNG
// at scales 1 and 3 and the bitmaps the label transform draws, spends at
// most half its budget, counted by the renders themselves; the label's
// bitmaps have the budget at scale 1. Scale 1 is where
// the margin is least (the work per output pixel falls a little as the
// scale grows); scale 3 catches a budget that does not grow with the
// scale's square. The heaviest chart is in the wild corpus (vl_0123, about
// 140 operations an output pixel, a 5.5x margin); the in-repo corpus's is
// vg-fixtures/volcano-contours (11.5x). The external corpora are skipped
// when they have not been fetched, as their sweeps are.
func TestPixelOpsMarginOverCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: renders every corpus chart to PNG at two scales")
	}
	t.Run("in-repo", func(t *testing.T) {
		var charts []marginChart
		for _, s := range corpusSets {
			files, err := filepath.Glob(s.glob)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				charts = append(charts, marginChart{s.name + "/" + strings.TrimSuffix(filepath.Base(f), ".json"), b, s.lite})
			}
		}
		pixelOpsMargin(t, oracleConverter(t, nil), charts)
	})
	for _, name := range []string{"wild", "deneb"} {
		t.Run(name, func(t *testing.T) {
			requireCorpus(t, name)
			o := oracle.For(t, oracle.VL6)
			gen, err := o.Generate(name)
			if err != nil {
				t.Fatalf("generating %s: %v", name, err)
			}
			if gen.Err != "" {
				t.Fatalf("generating %s: %s", name, gen.Err)
			}
			var out struct{ Cases []sweepCase }
			if err := json.Unmarshal(gen.Data, &out); err != nil {
				t.Fatal(err)
			}
			var charts []marginChart
			for _, c := range out.Cases {
				spec := []byte(c.Spec)
				if c.SpecText != "" {
					spec = []byte(c.SpecText)
				}
				charts = append(charts, marginChart{name + "/" + c.Name, spec, c.Lite})
			}
			pixelOpsMargin(t, oracleConverter(t, o), charts)
		})
	}
}

type marginChart struct {
	name string
	spec []byte
	lite bool
}

// pixelOpsMargin renders each chart and fails for every render that spends
// more than half its work budget.
func pixelOpsMargin(t *testing.T, c *aster.Converter, charts []marginChart) {
	t.Helper()
	type worst struct {
		margin float64
		chart  string
		w      raster.Work
	}
	var mu sync.Mutex
	worstAt := map[float64]*worst{}
	renders := 0
	check := func(chart, stage string, ws []raster.Work) {
		mu.Lock()
		defer mu.Unlock()
		for _, w := range ws {
			renders++
			// The label transform's bitmaps have the budget at scale 1 for
			// their pixels, however much the transform supersamples.
			if want := max(128<<20, 16*w.Width*w.Height); stage == "label" && w.Budget != want {
				t.Errorf("%s (label, scale %v, %dx%d): budget %d, want %d", chart, w.Scale, w.Width, w.Height, w.Budget, want)
			}
			if w.Ops > w.Budget/2 {
				t.Errorf("%s (%s, scale %v, %dx%d): %d pixel operations of a budget of %d, less than a 2x margin",
					chart, stage, w.Scale, w.Width, w.Height, w.Ops, w.Budget)
			}
			if stage != "PNG" || w.Ops == 0 {
				continue
			}
			m := float64(w.Budget) / float64(w.Ops)
			if b := worstAt[w.Scale]; b == nil || m < b.margin {
				worstAt[w.Scale] = &worst{m, chart, w}
			}
		}
	}
	next := make(chan marginChart)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range next {
				var mw sync.Mutex
				var ws []raster.Work
				ctx := raster.WithWorkReport(context.Background(), func(w raster.Work) {
					mw.Lock()
					ws = append(ws, w)
					mw.Unlock()
				})
				var svg string
				var err error
				if ch.lite {
					svg, err = c.VegaLiteToSVG(ch.spec, asUpstream, aster.WithContext(ctx))
				} else {
					svg, err = c.VegaToSVG(ch.spec, asUpstream, aster.WithContext(ctx))
				}
				check(ch.name, "label", ws)
				if err != nil {
					continue // the comparisons hold what each chart's SVG is
				}
				for _, s := range []float64{1, 3} {
					ws = nil
					// A PNG that fails for its size (an output past
					// MaxPixels) does no work; one that runs out of work
					// fails the margin first.
					_, _ = c.SVGToPNG(svg, asUpstream, aster.WithScale(s), aster.WithContext(ctx))
					check(ch.name, "PNG", ws)
				}
			}
		}()
	}
	for _, ch := range charts {
		next <- ch
	}
	close(next)
	wg.Wait()
	for _, s := range []float64{1, 3} {
		if b := worstAt[s]; b != nil {
			t.Logf("scale %v: least margin %.2fx, %s (%d of %d pixel operations, %dx%d)", s, b.margin, b.chart, b.w.Ops, b.w.Budget, b.w.Width, b.w.Height)
		}
	}
	t.Logf("%d charts, %d renders", len(charts), renders)
}
