package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// highlights are shown individually: they stress different hot paths
// (plain marks, facets, many views, geo, contours, force layout, hierarchies).
var highlights = []string{
	"vl-examples/bar", "vl-examples/trellis_bar", "vl-examples/repeat_splom", "vl-examples/geo_choropleth",
	"vg-gallery/scatter-plot", "vg-gallery/stacked-area-chart", "vg-gallery/treemap",
	"vg-gallery/county-unemployment", "vg-gallery/force-directed-layout", "vg-gallery/beeswarm-plot",
	"vg-gallery/contour-plot", "vg-gallery/world-map",
}

type key struct{ suite, name, stage string }

func writeReport(w io.Writer, files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("-report needs the JSON result files as arguments")
	}
	var reps []Report
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r Report
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		reps = append(reps, r)
	}
	// The baseline for ratios is the first node file when present, else the
	// first file.
	base := 0
	for i := len(reps) - 1; i >= 0; i-- {
		if strings.HasPrefix(reps[i].Engine, "node") {
			base = i
		}
	}
	res := make([]map[key]Result, len(reps))
	for i, r := range reps {
		res[i] = map[key]Result{}
		for _, x := range r.Results {
			res[i][key{x.Suite, x.Name, x.Stage}] = x
		}
	}

	// pr writes to w until a write fails; writeReport returns that error.
	var werr error
	pr := func(format string, a ...any) {
		if werr == nil {
			_, werr = fmt.Fprintf(w, format, a...)
		}
	}
	pr("## Engines\n\n| engine | version | environment | init | first VL→SVG | peak RSS | failed cases |\n|---|---|---|---:|---:|---:|---:|\n")
	for _, r := range reps {
		failed := 0
		for _, x := range r.Results {
			if x.Err != "" {
				failed++
			}
		}
		pr("| %s | %s | %s | %s | %s | %.0f MB | %d |\n", r.Engine, r.Version, r.Env, fmtMS(r.InitMS), fmtMS(r.FirstMS), r.RSSMB, failed)
	}
	pr("\nTimes are the median of repeated runs on a warm engine (budget %.0f ms per case, at least 3 runs unless one run exceeds 2 s). Only cases every engine completed are compared.\n", reps[0].Budget)

	for _, suite := range []string{"vl-examples", "vg-gallery"} {
		for _, stage := range stageNames {
			keys := common(res, suite, stage)
			if len(keys) == 0 {
				continue
			}
			pr("\n## %s, %s (%d specs)\n\n", suite, stageTitle(stage), len(keys))
			pr("| engine | geomean | median | p90 | max | total | speedup vs %s (geomean) | faster than %s |\n|---|---:|---:|---:|---:|---:|---:|---:|\n", reps[base].Engine, reps[base].Engine)
			for i, r := range reps {
				var t, ratio []float64
				faster := 0
				for _, k := range keys {
					t = append(t, res[i][k].Median)
					ratio = append(ratio, res[base][k].Median/res[i][k].Median)
					if res[i][k].Median < res[base][k].Median {
						faster++
					}
				}
				sp, fs := fmt.Sprintf("%.2f×", geomean(ratio)), fmt.Sprintf("%d/%d", faster, len(keys))
				if i == base {
					sp, fs = "—", "—"
				}
				pr("| %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Engine,
					fmtMS(geomean(t)), fmtMS(pct(t, 0.5)), fmtMS(pct(t, 0.9)), fmtMS(pct(t, 1)), fmtMS(sum(t)), sp, fs)
			}
		}
	}

	pr("\n## Highlights (end-to-end SVG and PNG)\n\n| spec | stage |")
	for _, r := range reps {
		pr(" %s |", r.Engine)
	}
	pr("\n|---|---|%s\n", strings.Repeat("---:|", len(reps)))
	for _, h := range highlights {
		suite, name, _ := strings.Cut(h, "/")
		for _, stage := range []string{"svg", "png"} {
			pr("| %s | %s |", h, stage)
			for i := range reps {
				pr(" %s |", cell(res[i], key{suite, name, stage}))
			}
			pr("\n")
		}
	}

	for _, stage := range []string{"svg", "png"} {
		type row struct {
			k key
			t float64
		}
		var rows []row
		for _, suite := range []string{"vl-examples", "vg-gallery"} {
			for _, k := range common(res, suite, stage) {
				worst := 0.0
				for i := range reps {
					worst = math.Max(worst, res[i][k].Median)
				}
				rows = append(rows, row{k, worst})
			}
		}
		sort.Slice(rows, func(a, b int) bool { return rows[a].t > rows[b].t })
		pr("\n## Slowest %s cases (by the slowest engine)\n\n| spec |", stage)
		for _, r := range reps {
			pr(" %s |", r.Engine)
		}
		pr("\n|---|%s\n", strings.Repeat("---:|", len(reps)))
		for _, x := range rows[:min(10, len(rows))] {
			pr("| %s/%s |", x.k.suite, x.k.name)
			for i := range reps {
				pr(" %s |", cell(res[i], x.k))
			}
			pr("\n")
		}
	}

	pr("\n## Failures\n\n")
	for _, r := range reps {
		var errs []string
		for _, x := range r.Results {
			if x.Err != "" {
				errs = append(errs, fmt.Sprintf("%s/%s %s: %s", x.Suite, x.Name, x.Stage, x.Err))
			}
		}
		pr("- **%s**: %d", r.Engine, len(errs))
		for _, e := range errs[:min(8, len(errs))] {
			pr("\n  - %s", e)
		}
		if len(errs) > 8 {
			pr("\n  - … %d more", len(errs)-8)
		}
		pr("\n")
	}
	return werr
}

// common lists the (suite, stage) cases every report timed successfully.
func common(res []map[key]Result, suite, stage string) []key {
	var keys []key
	for k, x := range res[0] {
		if k.suite != suite || k.stage != stage || x.Err != "" || x.Runs == 0 {
			continue
		}
		ok := true
		for _, m := range res[1:] {
			if y, found := m[k]; !found || y.Err != "" || y.Runs == 0 {
				ok = false
				break
			}
		}
		if ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].name < keys[j].name })
	return keys
}

func cell(m map[key]Result, k key) string {
	x, ok := m[k]
	switch {
	case !ok:
		return "–"
	case x.Err != "":
		return "error"
	}
	return fmtMS(x.Median)
}

func stageTitle(s string) string {
	switch s {
	case "vl2vg":
		return "Vega-Lite → Vega JSON"
	case "svg":
		return "spec → SVG"
	}
	return "spec → PNG"
}

func geomean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += math.Log(math.Max(v, 1e-6))
	}
	return math.Exp(s / float64(len(x)))
}

func pct(x []float64, p float64) float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[min(len(s)-1, int(p*float64(len(s))))]
}

func sum(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s
}

func fmtMS(v float64) string {
	switch {
	case v >= 10000:
		return fmt.Sprintf("%.1f s", v/1000)
	case v >= 100:
		return fmt.Sprintf("%.0f ms", v)
	case v >= 10:
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.2f ms", v)
}
