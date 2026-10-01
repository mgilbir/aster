package aster_test

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

// The benchmark scenes: charts sized to show how each stage scales, rendered
// to SVG, then PNG and PDF, with every stage timed separately (StagesForTest
// in export_test.go). A chart that got slower is a weak observation; a chart
// whose text measurement got slower names the change that did it.
//
//	go test -run '^$' -bench BenchmarkScenes .        # ns/op per stage, allocations
//	ASTER_PERF=1 go test -run TestPerfReport -v .     # median, P90, P95 per stage
//
// The stages: json (parsing the specification), compile (Vega-Lite to Vega),
// parse (Vega to a dataflow), dataflow (data, transforms, encoding, layout;
// it includes text, the time spent measuring text), svg, png and pdf.

type scene struct {
	name string
	lite bool
	spec []byte
}

// lcg is a fixed pseudo-random sequence, so a scene's data is the same on
// every run.
type lcg uint32

func (l *lcg) next() float64 {
	*l = *l*1664525 + 1013904223
	return float64(*l) / (1 << 32)
}

func values(n int, row func(i int, r *lcg) string) string {
	r := lcg(42)
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(row(i, &r))
	}
	b.WriteByte(']')
	return b.String()
}

func benchScenes() []scene {
	bars := func(n int) scene {
		return scene{fmt.Sprintf("bars-%d", n), true, fmt.Appendf(nil, `{"width":600,"height":300,"data":{"values":%s},"mark":"bar","encoding":{"x":{"field":"c","type":"nominal","axis":{"labels":%t}},"y":{"field":"v","type":"quantitative"}}}`,
			values(n, func(i int, r *lcg) string { return fmt.Sprintf(`{"c":"c%04d","v":%.3f}`, i, r.next()*100) }), n <= 100)}
	}
	symbols := func(n int) scene {
		return scene{fmt.Sprintf("symbols-%d", n), true, fmt.Appendf(nil, `{"width":600,"height":400,"data":{"values":%s},"mark":"point","encoding":{"x":{"field":"x","type":"quantitative"},"y":{"field":"y","type":"quantitative"}}}`,
			values(n, func(_ int, r *lcg) string { return fmt.Sprintf(`{"x":%.4f,"y":%.4f}`, r.next(), r.next()) }))}
	}
	line := func(n int) scene {
		return scene{fmt.Sprintf("line-%d", n), true, fmt.Appendf(nil, `{"width":600,"height":300,"data":{"values":%s},"mark":"line","encoding":{"x":{"field":"i","type":"quantitative"},"y":{"field":"v","type":"quantitative"}}}`,
			values(n, func(i int, r *lcg) string { return fmt.Sprintf(`{"i":%d,"v":%.3f}`, i, r.next()) }))}
	}
	return []scene{
		bars(100), bars(1000),
		symbols(10000), symbols(100000),
		line(1000), line(10000), line(100000),
		{"series-20-with-legend", true, fmt.Appendf(nil, `{"width":600,"height":300,"data":{"values":%s},"mark":"line","encoding":{"x":{"field":"i","type":"quantitative"},"y":{"field":"v","type":"quantitative"},"color":{"field":"s","type":"nominal","scale":{"scheme":"category20"}}}}`,
			values(2000, func(i int, r *lcg) string {
				return fmt.Sprintf(`{"s":"series %02d","i":%d,"v":%.3f}`, i%20, i/20, r.next())
			}))},
		{"dense-labels-2000", true, fmt.Appendf(nil, `{"width":800,"height":600,"data":{"values":%s},"mark":"text","encoding":{"x":{"field":"x","type":"quantitative"},"y":{"field":"y","type":"quantitative"},"text":{"field":"t"}}}`,
			values(2000, func(i int, r *lcg) string {
				return fmt.Sprintf(`{"x":%.4f,"y":%.4f,"t":"label %d"}`, r.next(), r.next(), i)
			}))},
	}
}

var stageOrder = []string{"json", "compile", "parse", "dataflow", "text", "svg", "png", "pdf"}

func BenchmarkScenes(b *testing.B) {
	c, err := aster.New(aster.WithTimeout(5 * time.Minute))
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	for _, sc := range benchScenes() {
		b.Run(sc.name, func(b *testing.B) {
			if _, err := c.StagesForTest(sc.spec, sc.lite); err != nil { // warm up
				b.Fatal(err)
			}
			total := map[string]time.Duration{}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				st, err := c.StagesForTest(sc.spec, sc.lite)
				if err != nil {
					b.Fatal(err)
				}
				for k, d := range st {
					total[k] += d
				}
			}
			for _, k := range stageOrder {
				if d, ok := total[k]; ok {
					b.ReportMetric(float64(d.Nanoseconds())/float64(b.N), k+"-ns/op")
				}
			}
		})
	}
}

// TestPerfReport times every scene runs times and prints, per stage, the
// median, P90 and P95, with the allocations of a whole render. Opt-in, and
// meant for a quiet machine: numbers from a shared CI runner say little.
func TestPerfReport(t *testing.T) {
	if os.Getenv("ASTER_PERF") == "" {
		t.Skip("set ASTER_PERF=1 to time the benchmark scenes")
	}
	runs := 15
	c, err := aster.New(aster.WithTimeout(5 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s, %s/%s, GOMAXPROCS=%d, %d runs per scene (after one warm-up)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), runs)
	fmt.Fprintf(&b, "%-24s %-9s %10s %10s %10s\n", "scene", "stage", "median", "p90", "p95")
	for _, sc := range benchScenes() {
		if _, err := c.StagesForTest(sc.spec, sc.lite); err != nil {
			t.Fatalf("%s: %v", sc.name, err)
		}
		samples := map[string][]time.Duration{}
		var ms0, ms1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms0)
		for range runs {
			st, err := c.StagesForTest(sc.spec, sc.lite)
			if err != nil {
				t.Fatalf("%s: %v", sc.name, err)
			}
			var all time.Duration
			for k, d := range st {
				samples[k] = append(samples[k], d)
				if k != "text" { // text is inside dataflow
					all += d
				}
			}
			samples["total"] = append(samples["total"], all)
		}
		runtime.ReadMemStats(&ms1)
		for _, k := range append(append([]string{}, stageOrder...), "total") {
			s := samples[k]
			if len(s) == 0 {
				continue
			}
			sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
			q := func(p float64) time.Duration { return s[min(len(s)-1, int(p*float64(len(s))))] }
			fmt.Fprintf(&b, "%-24s %-9s %10s %10s %10s\n", sc.name, k, q(0.5).Round(time.Microsecond), q(0.9).Round(time.Microsecond), q(0.95).Round(time.Microsecond))
		}
		fmt.Fprintf(&b, "%-24s %-9s %10d allocations, %.1f MB per render\n", sc.name, "allocs", (ms1.Mallocs-ms0.Mallocs)/uint64(runs), float64(ms1.TotalAlloc-ms0.TotalAlloc)/float64(runs)/(1<<20))
	}
	t.Log(b.String())
}
