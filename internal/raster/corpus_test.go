package raster

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// corpusDir holds the Vega-Lite examples; upstream renders them to the SVG
// the corpus test rasterizes (corpusSVG).
const corpusDir = "../../testdata/vega-lite/v6.4.3/specs"

func corpusFiles(t testing.TB) []string {
	files, _ := filepath.Glob(filepath.Join(corpusDir, "*.vl.json"))
	if len(files) == 0 {
		t.Skip("corpus not found")
	}
	sort.Strings(files)
	return files
}

// TestCorpus rasterizes upstream's SVG for the Vega-Lite examples and
// compares with resvg. RASTER_CORPUS_N limits the sample (evenly strided);
// RASTER_DUMP=dir writes side-by-side diff images.
func TestCorpus(t *testing.T) {
	files := corpusFiles(t)
	n := 40
	if testing.Short() {
		n = 12
	}
	if s := os.Getenv("RASTER_CORPUS_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	if n <= 0 || n > len(files) {
		n = len(files)
	}
	step := float64(len(files)) / float64(n)
	var sel []string
	for i := 0; i < n; i++ {
		sel = append(sel, files[int(float64(i)*step)])
	}
	dump := os.Getenv("RASTER_DUMP")
	if dump != "" {
		_ = os.MkdirAll(dump, 0o755)
	}
	var totMAE, totOver float64
	var cnt int
	type row struct {
		name string
		s    diffStats
	}
	var rows []row
	for _, f := range sel {
		name := strings.TrimSuffix(filepath.Base(f), ".vl.json")
		svg := corpusSVG(t, name)
		ours, err := Render(svg, Options{})
		ref := refPNG(t, svg, 1)
		if ref == nil {
			if err == nil {
				t.Logf("%s: resvg failed but raster rendered", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s := compareImages(ours, ref, 24)
		rows = append(rows, row{name, s})
		totMAE += s.MAE
		totOver += s.PctOver
		cnt++
		if dump != "" {
			writeDiff(filepath.Join(dump, name+".png"), ours, ref)
		}
		if s.SizeDiff {
			t.Errorf("%s: size mismatch %v vs %v", name, ours.Rect, ref.Rect)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].s.MAE > rows[j].s.MAE })
	for i, r := range rows {
		if i >= 15 && !testing.Verbose() {
			break
		}
		t.Logf("%-60s MAE %.3f  >24: %.3f%%  >64: %.3f%%  PSNR %.1f max %d", r.name, r.s.MAE, r.s.PctOver, r.s.PctBig, r.s.PSNR, r.s.Max)
	}
	// Regression gates. The full corpus (624 images) measures a mean MAE of
	// about 0.074 and a worst image of about 1.5 (thin geo outlines).
	for _, r := range rows {
		if r.s.MAE > 2.5 || r.s.PctOver > 4 {
			t.Errorf("%s: MAE %.3f / %.3f%% >24 exceeds the per-image limit", r.name, r.s.MAE, r.s.PctOver)
		}
	}
	// The mean gate is calibrated for the default sample; the 12-image -short
	// sample is dominated by a few thin-outline geo charts.
	if !testing.Short() && cnt > 0 && (totMAE/float64(cnt) > 0.15 || totOver/float64(cnt) > 0.1) {
		t.Errorf("corpus mean MAE %.4f / %%>24 %.4f exceeds the limit", totMAE/float64(cnt), totOver/float64(cnt))
	}
	if cnt > 0 {
		t.Logf("corpus: %d images, mean MAE %.4f, mean %%>24 %.4f", cnt, totMAE/float64(cnt), totOver/float64(cnt))
		fmt.Printf("CORPUS n=%d meanMAE=%.4f mean%%over=%.4f\n", cnt, totMAE/float64(cnt), totOver/float64(cnt))
	}
}
