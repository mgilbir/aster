package aster

// Does WithMemoryLimit hold against the real heap? The option is not a heap
// cap: limits() and rasterLimits() turn the byte limit into budgets (rows at
// 256 bytes, scene items at 512, loaded bytes, large strings, canvas pixels, SVG
// size), and the engine charges them as it works. This file renders
// adversarial specifications under several limits and records the peak heap of
// each render next to the limit, so the per-row and per-item estimates are
// checked against what the render really holds.
//
// Method. Every render runs in its own child process (the pattern of
// harness_security_test.go), so heaps do not mix. The child builds the
// Converter, renders a trivial chart of the same format (this loads the fonts,
// which are shared, one-off state and not the render's memory), forces a GC and
// takes the heap as its baseline. A sampler goroutine then reads, every 50
// microseconds, runtime/metrics "/memory/classes/heap/objects:bytes" (live
// objects plus garbage not yet swept: the figure ReadMemStats calls HeapAlloc,
// but read without stopping the world, so sampling does not slow the render)
// and "/gc/heap/live:bytes" (the heap the last GC found reachable). Peaks are
// reported net of the baseline.
//
// A budget should bound what the render keeps, so the peak that matters is the
// live one; with GOGC=100 the in-use peak can be twice that, and the live
// figure is only refreshed at the end of a GC. So each (case, limit, scale) runs
// twice: at GOGC=20, where collections are frequent and the live figure tracks
// the peak to within 20%, and at GOGC=100, the Go default, which is what a host
// process sees as in-use. The table reports live from the first and in-use from
// the second.
//
// Each case is sized in units of the budget it targets (the rows and items
// limits() derives from the limit, the loaded bytes, ...) and run at two scales: 0.9 of the budget, which the render
// should complete or be refused by another budget, and 2, which should end in an
// error wrapping ErrLimit. A (case, limit) holds if the render fails with
// ErrLimit, or succeeds with a peak live heap of at most memcheckK times the
// limit.
//
// The full matrix is heavy and opt in:
//
//	ASTER_MEMCHECK=1 go test . -run TestMemoryLimitHeap -v -timeout 60m
//	ASTER_MEMCHECK_OUT=memcheck.md ...                      also write the table as markdown
//	ASTER_MEMCHECK_LIMITS=16,64 ASTER_MEMCHECK_CASES=rows ... narrow the matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const memChildEnv = "ASTER_MEM_CHILD"

// memcheckK is how many times the limit the peak live heap may reach. The
// budgets charge what a render holds as data (rows and items at the estimated
// cost, loaded bytes, strings) and not the engine's working set: parsed
// specification, operator graph, text measurement caches, the SVG being built
// (up to limit/4) and its copy as a string. A render that sits at the limit on
// every budget at once therefore holds a small multiple of it; 2 is the most a
// bound of "about the limit" can allow.
const memcheckK = 2.0

type memJob struct {
	Format   string  `json:"format"` // svg, png, pdf
	Kind     string  `json:"kind"`   // vega, vl
	Spec     string  `json:"spec"`
	Limit    uint64  `json:"limit"`
	GOGC     int     `json:"gogc"`
	LoadMB   float64 `json:"loadMB"`   // size of the payload the in-memory loader serves
	LoadKind string  `json:"loadKind"` // json-tiny, json-wide, csv
	CapMB    uint64  `json:"capMB"`    // child exits above this heap
}

type memResult struct {
	Err       string  `json:"err"`
	IsLimit   bool    `json:"isLimit"`
	Panic     string  `json:"panic"`
	Baseline  float64 `json:"baselineMiB"`
	PeakInUse float64 `json:"peakInUseMiB"` // net of the baseline
	PeakLive  float64 `json:"peakLiveMiB"`  // net of the baseline
	AllocMiB  float64 `json:"allocMiB"`     // bytes allocated over the render, garbage included
	OutMiB    float64 `json:"outMiB"`
	Seconds   float64 `json:"seconds"`
	Capped    bool    `json:"capped"`
}

// memLoader serves a generated payload for every Load: the data a host's
// loader would hand to the render.
type memLoader struct {
	mb   float64
	kind string
}

func (l memLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }
func (l memLoader) Load(_ context.Context, _ string) ([]byte, error) {
	n := int(l.mb * (1 << 20))
	b := make([]byte, 0, n+64)
	switch l.kind {
	case "csv":
		b = append(b, "a,b,c,d,e\n"...)
		for len(b) < n {
			b = append(b, "12,abc,3.5,def,x\n"...)
		}
	case "json-wide":
		b = append(b, '[')
		for i := 0; len(b) < n; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, '{')
			for f := 0; f < 40; f++ {
				if f > 0 {
					b = append(b, ',')
				}
				b = append(b, `"f`...)
				b = strconv.AppendInt(b, int64(f), 10)
				b = append(b, `":`...)
				b = strconv.AppendFloat(b, float64(i%1000)+0.5, 'f', 1, 64)
			}
			b = append(b, '}')
		}
		b = append(b, ']')
	default: // json-tiny
		b = append(b, '[')
		for len(b) < n {
			b = append(b, `{"a":1},`...)
		}
		b = append(b, `{"a":1}]`...)
	}
	return b, nil
}

func init() {
	if os.Getenv(memChildEnv) == "" {
		return
	}
	var job memJob
	if err := json.NewDecoder(os.Stdin).Decode(&job); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(runMemChild(job))
}

const memWarmup = `{"width":50,"height":50,"marks":[{"type":"text","encode":{"update":{"text":{"value":"a"},"x":{"value":5},"y":{"value":20}}}}]}`

func memRender(c *Converter, job memJob, spec string) ([]byte, error) {
	b := []byte(spec)
	switch job.Format + "/" + job.Kind {
	case "svg/vega":
		s, err := c.VegaToSVG(b)
		return []byte(s), err
	case "svg/vl":
		s, err := c.VegaLiteToSVG(b)
		return []byte(s), err
	case "png/vega":
		return c.VegaToPNG(b)
	case "png/vl":
		return c.VegaLiteToPNG(b)
	case "pdf/vega":
		return c.VegaToPDF(b)
	default:
		return c.VegaLiteToPDF(b)
	}
}

func runMemChild(job memJob) int {
	debug.SetGCPercent(job.GOGC)
	opts := []Option{WithMemoryLimit(job.Limit), WithTimeout(5 * time.Minute)}
	if job.LoadMB > 0 {
		opts = append(opts, WithLoader(memLoader{job.LoadMB, job.LoadKind}))
	}
	c, err := New(opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer c.Close()
	// The fonts, the shaper and the rasterizer's tables load on first use:
	// a trivial chart of the same format loads them before the baseline.
	warm := memWarmup
	if job.Kind == "vl" {
		warm = `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`
	}
	if _, err := memRender(c, job, warm); err != nil {
		fmt.Fprintln(os.Stderr, "warm-up:", err)
		return 2
	}
	runtime.GC()
	runtime.GC()

	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/gc/heap/live:bytes"},
		{Name: "/gc/heap/allocs:bytes"},
	}
	metrics.Read(samples)
	baseUse, baseLive, baseAlloc := samples[0].Value.Uint64(), samples[1].Value.Uint64(), samples[2].Value.Uint64()

	var (
		mu               sync.Mutex
		peakUse, peakLiv uint64
	)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		s := []metrics.Sample{samples[0], samples[1]}
		for {
			metrics.Read(s)
			use := s[0].Value.Uint64()
			mu.Lock()
			peakUse = max(peakUse, use)
			peakLiv = max(peakLiv, s[1].Value.Uint64())
			mu.Unlock()
			if job.CapMB > 0 && use > job.CapMB<<20 {
				b, _ := json.Marshal(memResult{Err: "heap cap exceeded", Capped: true, PeakInUse: float64(use) / (1 << 20)})
				fmt.Println(string(b))
				os.Exit(3)
			}
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Microsecond):
			}
		}
	}()

	var res memResult
	t0 := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				res.Panic = fmt.Sprint(r)
			}
		}()
		out, err := memRender(c, job, job.Spec)
		if err != nil {
			res.Err = err.Error()
			res.IsLimit = errors.Is(err, ErrLimit)
		}
		res.OutMiB = float64(len(out)) / (1 << 20)
	}()
	res.Seconds = time.Since(t0).Seconds()
	close(stop)
	<-done
	metrics.Read(samples)
	mu.Lock()
	defer mu.Unlock()
	peakUse = max(peakUse, samples[0].Value.Uint64())
	peakLiv = max(peakLiv, samples[1].Value.Uint64())
	net := func(v, base uint64) float64 { return float64(max(v, base)-base) / (1 << 20) }
	res.Baseline = float64(baseUse) / (1 << 20)
	res.PeakInUse = net(peakUse, baseUse)
	res.PeakLive = net(peakLiv, baseLive)
	res.AllocMiB = net(samples[2].Value.Uint64(), baseAlloc)
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
	return 0
}

func runMem(job memJob) memResult {
	if job.CapMB == 0 {
		job.CapMB = 6144
	}
	in, _ := json.Marshal(job)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), memChildEnv+"=1")
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return memResult{Err: "killed on wall-clock time", Capped: true}
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var res memResult
	if jerr := json.Unmarshal([]byte(lines[len(lines)-1]), &res); jerr != nil {
		return memResult{Err: fmt.Sprintf("child failed: %v: %.200s %.200s", err, out.String(), errb.String())}
	}
	return res
}

// --- adversarial specifications ----------------------------------------------

// memSpec is what a case generates for a limit and a scale.
type memSpec struct {
	spec     string
	loadMB   float64
	loadKind string
}

type memCase struct {
	name    string
	kind    string   // vega, vl
	formats []string // default svg
	// gen builds the specification. rows and items are the row and item
	// budgets of the limit, already scaled; l is the limit and s the scale.
	gen func(rows, items int, l uint64, s float64) memSpec
}

func memSeq(name string, n int, more ...string) string {
	tr := fmt.Sprintf(`{"type":"sequence","start":0,"stop":%d,"as":"x"}`, n)
	for _, m := range more {
		tr += "," + m
	}
	return fmt.Sprintf(`{"name":%q,"transform":[%s]}`, name, tr)
}

func memFormula(as, expr string) string {
	return fmt.Sprintf(`{"type":"formula","as":%q,"expr":%q}`, as, expr)
}

func memVega(data string, rest string) memSpec {
	if rest != "" {
		rest = "," + rest
	}
	return memSpec{spec: `{"width":400,"height":300,"data":[` + data + `]` + rest + `}`}
}

func memSqrt(n int) int { return max(int(math.Sqrt(float64(n))), 1) }

func memVL(spec string) memSpec { return memSpec{spec: spec} }

const memRectMark = `"marks":[{"type":"rect","from":{"data":"t"},"encode":{"update":{"x":{"signal":"datum.x % 400"},"y":{"signal":"datum.x % 300"},"width":{"value":3},"height":{"value":3}}}}]`

var memCases = []memCase{
	// Many rows.
	{name: "rows-sequence", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", rows), "")
	}},
	{name: "rows-inline", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		var sb strings.Builder
		sb.WriteString(`{"name":"t","values":[`)
		for i := range rows {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"a":%d}`, i)
		}
		sb.WriteString(`]}`)
		return memVega(sb.String(), "")
	}},
	// Wide rows: sixteen more fields on every row.
	{name: "rows-wide", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		var f []string
		for i := range 16 {
			f = append(f, memFormula(fmt.Sprintf("f%d", i), fmt.Sprintf("datum.x * %d", i+1)))
		}
		return memVega(memSeq("t", rows, f...), "")
	}},
	// Long strings: a 1000 character string per row, built by an expression.
	{name: "rows-longstring", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", rows, memFormula("s", "pad('' + datum.x, 1000, 'z', 'left')")), "")
	}},
	// Row blow-ups.
	{name: "fold", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		f := []string{memFormula("a", "datum.x"), memFormula("b", "datum.x"), memFormula("c", "datum.x"), memFormula("d", "datum.x"),
			`{"type":"fold","fields":["a","b","c","d"]}`}
		return memVega(memSeq("t", rows/4, f...), "")
	}},
	{name: "cross", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", memSqrt(rows), `{"type":"cross"}`), "")
	}},
	{name: "flatten", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", rows/100, memFormula("arr", "sequence(0, 100)"), `{"type":"flatten","fields":["arr"]}`), "")
	}},
	{name: "impute", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		m := memSqrt(rows)
		return memVega(memSeq("t", m, memFormula("k", "datum.x"), memFormula("g", "datum.x"),
			`{"type":"impute","field":"x","key":"k","groupby":["g"],"value":0}`), "")
	}},
	{name: "kde-steps", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		g := max(rows/1000, 1)
		return memVega(memSeq("t", 2*g, memFormula("g", "floor(datum.x / 2)"), memFormula("v", "datum.x % 2"),
			`{"type":"kde","groupby":["g"],"field":"v","steps":1000,"bandwidth":1}`), "")
	}},
	{name: "pivot-wide-row", kind: "vega", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", rows, memFormula("k", "'k' + datum.x"), `{"type":"pivot","field":"k","value":"x"}`), "")
	}},
	// Many scene items.
	{name: "items-rect", kind: "vega", formats: []string{"svg", "png", "pdf"}, gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items), memRectMark)
	}},
	{name: "items-text-long", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items, memFormula("s", "pad('' + datum.x, 200, 'z', 'left')")),
			`"marks":[{"type":"text","from":{"data":"t"},"encode":{"update":{"text":{"field":"s"},"x":{"value":0},"y":{"field":"x"}}}}]`)
	}},
	{name: "items-rule", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items),
			`"marks":[{"type":"rule","from":{"data":"t"},"encode":{"update":{"x":{"field":"x"},"y":{"value":0},"y2":{"value":100}}}}]`)
	}},
	{name: "items-line", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items),
			`"marks":[{"type":"line","from":{"data":"t"},"encode":{"update":{"x":{"field":"x"},"y":{"field":"x"}}}}]`)
	}},
	{name: "items-area", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items),
			`"marks":[{"type":"area","from":{"data":"t"},"encode":{"update":{"x":{"field":"x"},"y":{"field":"x"},"y2":{"value":0}}}}]`)
	}},
	{name: "path-long-strings", kind: "vega", gen: func(_, _ int, l uint64, s float64) memSpec {
		n := max(int(s*float64(l/4)/8000), 1)
		return memVega(memSeq("t", n, memFormula("p", "'M0,0' + pad('', 8000, 'L1,1', 'right')")),
			`"marks":[{"type":"path","from":{"data":"t"},"encode":{"update":{"path":{"field":"p"}}}}]`)
	}},
	{name: "groups-siblings", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", items/2),
			`"marks":[{"type":"group","from":{"data":"t"},"marks":[{"type":"rect","encode":{"update":{"width":{"value":3},"height":{"value":3}}}}]}]`)
	}},
	{name: "facets", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVega(memSeq("t", min(items/2, 25000)),
			`"marks":[{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"x"}},"marks":[{"type":"rect","from":{"data":"f"},"encode":{"update":{"width":{"value":3},"height":{"value":3}}}}]}]`)
	}},
	{name: "scales-axes", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		n := max(items/14, 1)
		var sc, ax []string
		for i := range n {
			sc = append(sc, fmt.Sprintf(`{"name":"s%d","type":"linear","domain":[0,1],"range":[0,100]}`, i))
			ax = append(ax, fmt.Sprintf(`{"scale":"s%d","orient":"bottom"}`, i))
		}
		return memSpec{spec: `{"width":100,"height":100,"scales":[` + strings.Join(sc, ",") + `],"axes":[` + strings.Join(ax, ",") + `]}`}
	}},
	{name: "legends", kind: "vega", gen: func(_, items int, _ uint64, _ float64) memSpec {
		n := max(items/12, 1)
		var sc, lg []string
		for i := range n {
			sc = append(sc, fmt.Sprintf(`{"name":"s%d","type":"ordinal","domain":["a","b","c","d","e"],"range":"category"}`, i))
			lg = append(lg, fmt.Sprintf(`{"fill":"s%d"}`, i))
		}
		return memSpec{spec: `{"width":100,"height":100,"scales":[` + strings.Join(sc, ",") + `],"legends":[` + strings.Join(lg, ",") + `]}`}
	}},
	// A large canvas: width*height*4 is the canvas budget; the SVG is tiny.
	{name: "canvas-big", kind: "vega", formats: []string{"png"}, gen: func(_, _ int, l uint64, s float64) memSpec {
		side := int(math.Sqrt(s * float64(l) / 8))
		return memSpec{spec: fmt.Sprintf(`{"width":%d,"height":%d,"padding":0,"background":"white","marks":[{"type":"rect","encode":{"update":{"x":{"value":0},"y":{"value":0},"width":{"value":%d},"height":{"value":%d},"fill":{"value":"#369"}}}}]}`, side, side, side, side)}
	}},
	// Large loaded data, from an in-memory loader.
	{name: "load-json-tiny", kind: "vega", gen: func(_, _ int, l uint64, s float64) memSpec {
		return memSpec{spec: `{"data":[{"name":"t","url":"x.json","format":{"type":"json"}}]}`, loadMB: s * float64(l) / 2 / (1 << 20), loadKind: "json-tiny"}
	}},
	{name: "load-json-wide", kind: "vega", gen: func(_, _ int, l uint64, s float64) memSpec {
		return memSpec{spec: `{"data":[{"name":"t","url":"x.json","format":{"type":"json"}}]}`, loadMB: s * float64(l) / 2 / (1 << 20), loadKind: "json-wide"}
	}},
	{name: "load-csv", kind: "vega", gen: func(_, _ int, l uint64, s float64) memSpec {
		return memSpec{spec: `{"data":[{"name":"t","url":"x.csv","format":{"type":"csv","parse":"auto"}}]}`, loadMB: s * float64(l) / 2 / (1 << 20), loadKind: "csv"}
	}},
	// Vega-Lite.
	{name: "vl-points", kind: "vl", formats: []string{"svg", "png"}, gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"mark":"point","encoding":{"x":{"field":"x","type":"quantitative"},"y":{"field":"x","type":"quantitative"}}}`, items))
	}},
	{name: "vl-bars-nominal", kind: "vl", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"mark":"bar","encoding":{"x":{"field":"x","type":"nominal"},"y":{"field":"x","type":"quantitative"}}}`, items/3))
	}},
	{name: "vl-text-long", kind: "vl", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"transform":[{"calculate":"pad('' + datum.x, 200, 'z', 'left')","as":"s"}],"mark":"text","encoding":{"y":{"field":"x","type":"quantitative"},"text":{"field":"s","type":"nominal"}}}`, items))
	}},
	{name: "vl-geoshape", kind: "vl", gen: func(_, _ int, l uint64, s float64) memSpec {
		n := max(int(s*float64(l)/32), 4)
		var sb strings.Builder
		sb.WriteString(`{"width":300,"height":300,"data":{"values":[{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[`)
		for i := range n {
			if i > 0 {
				sb.WriteByte(',')
			}
			a := 2 * math.Pi * float64(i) / float64(n)
			fmt.Fprintf(&sb, "[%.4f,%.4f]", 60*math.Cos(a), 40*math.Sin(a))
		}
		sb.WriteString(`,[60,0]]]}}]},"projection":{"type":"mercator"},"mark":"geoshape"}`)
		return memVL(sb.String())
	}},
	{name: "vl-density", kind: "vl", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		g := max(rows/1000, 1)
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"transform":[{"calculate":"floor(datum.x / 2)","as":"g"},{"calculate":"datum.x %% 2","as":"v"},{"density":"v","groupby":["g"],"steps":1000,"bandwidth":1}],"mark":"line","encoding":{"x":{"field":"value","type":"quantitative"},"y":{"field":"density","type":"quantitative"},"detail":{"field":"g"}}}`, 2*g))
	}},
	{name: "vl-facet-columns", kind: "vl", gen: func(_, items int, _ uint64, _ float64) memSpec {
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"mark":"point","encoding":{"column":{"field":"x","type":"nominal"},"y":{"field":"x","type":"quantitative"}}}`, min(items/8, 4000)))
	}},
	{name: "vl-histogram", kind: "vl", gen: func(rows, _ int, _ uint64, _ float64) memSpec {
		return memVL(fmt.Sprintf(`{"data":{"sequence":{"start":0,"stop":%d,"as":"x"}},"mark":"bar","encoding":{"x":{"bin":{"maxbins":20},"field":"x"},"y":{"aggregate":"count"}}}`, rows))
	}},
}

// --- running the matrix ------------------------------------------------------

// memRow is one (case, format, limit, scale) of the table.
type memRow struct {
	name, format string
	limit        uint64
	scale        float64
	live, inUse  memResult // the GOGC=20 and the GOGC=100 runs
}

func (r memRow) outcome() string {
	switch {
	case r.inUse.Capped || r.live.Capped:
		return "HEAPCAP"
	case r.inUse.Panic != "":
		return "PANIC"
	case r.inUse.IsLimit:
		return "limit"
	case r.inUse.Err != "":
		return "error"
	}
	return "ok"
}

func (r memRow) liveRatio() float64  { return r.live.PeakLive / float64(r.limit>>20) }
func (r memRow) inUseRatio() float64 { return r.inUse.PeakInUse / float64(r.limit>>20) }

// holds reports whether the row meets the contract: refused with ErrLimit, or
// within memcheckK times the limit. An error of another kind has no claim on
// the budget but is shown, and its peak is held to the same bound.
func (r memRow) holds() bool {
	switch r.outcome() {
	case "HEAPCAP", "PANIC":
		return false
	}
	return r.liveRatio() <= memcheckK
}

func memRun(c memCase, format string, limit uint64, scale float64) memRow {
	lim := (&Converter{cfg: &config{memoryLimit: limit}}).limits()
	rows := int(scale * float64(lim.MaxRows))
	items := int(scale * float64(lim.MaxItems))
	ms := c.gen(rows, items, limit, scale)
	job := memJob{Format: format, Kind: c.kind, Spec: ms.spec, Limit: limit, LoadMB: ms.loadMB, LoadKind: ms.loadKind}
	row := memRow{name: c.name, format: format, limit: limit, scale: scale}
	job.GOGC = 20
	row.live = runMem(job)
	job.GOGC = 100
	row.inUse = runMem(job)
	return row
}

func memTable(rows []memRow) string {
	var sb strings.Builder
	sb.WriteString("| case | format | limit MiB | scale | outcome | peak live MiB | live/limit | peak in-use MiB | in-use/limit | allocated MiB | out MiB | s | holds |\n")
	sb.WriteString("|---|---|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, r := range rows {
		ok := "yes"
		if !r.holds() {
			ok = "NO"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d | %.1f | %s | %.0f | %.2f | %.0f | %.2f | %.0f | %.1f | %.1f | %s |\n",
			r.name, r.format, r.limit>>20, r.scale, r.outcome(), r.live.PeakLive, r.liveRatio(),
			r.inUse.PeakInUse, r.inUseRatio(), r.inUse.AllocMiB, r.inUse.OutMiB, r.inUse.Seconds, ok)
	}
	return sb.String()
}

func memEnvList(name string, def []string) []string {
	if v := os.Getenv(name); v != "" {
		return strings.Split(v, ",")
	}
	return def
}

func TestMemoryLimitHeap(t *testing.T) {
	if os.Getenv("ASTER_MEMCHECK") == "" {
		t.Skip("set ASTER_MEMCHECK=1 to measure the heap of adversarial renders under WithMemoryLimit")
	}
	var limits []uint64
	for _, s := range memEnvList("ASTER_MEMCHECK_LIMITS", []string{"16", "64", "256"}) {
		mb, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("ASTER_MEMCHECK_LIMITS: %v", err)
		}
		limits = append(limits, uint64(mb)<<20)
	}
	sel := memEnvList("ASTER_MEMCHECK_CASES", nil)
	type job struct {
		c      memCase
		format string
		limit  uint64
		scale  float64
	}
	var jobs []job
	for _, c := range memCases {
		if sel != nil {
			match := false
			for _, s := range sel {
				match = match || strings.Contains(c.name, s)
			}
			if !match {
				continue
			}
		}
		formats := c.formats
		if len(formats) == 0 {
			formats = []string{"svg"}
		}
		for _, f := range formats {
			for _, l := range limits {
				for _, s := range []float64{0.9, 2} {
					jobs = append(jobs, job{c, f, l, s})
				}
			}
		}
	}
	rows := make([]memRow, len(jobs))
	work := make(chan int)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				j := jobs[i]
				rows[i] = memRun(j.c, j.format, j.limit, j.scale)
			}
		}()
	}
	for i := range jobs {
		work <- i
	}
	close(work)
	wg.Wait()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].limit < rows[j].limit })
	table := memTable(rows)
	t.Logf("k = %.1f\n%s", memcheckK, table)
	if out := os.Getenv("ASTER_MEMCHECK_OUT"); out != "" {
		hdr := fmt.Sprintf("Peak heap of adversarial renders under WithMemoryLimit (k = %.1f, net of the post-warm-up baseline)\n\n", memcheckK)
		if err := os.WriteFile(out, []byte(hdr+table), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range rows {
		if !r.holds() {
			t.Errorf("%s/%s limit %d MiB scale %.1f: %s, live %.0f MiB (%.2fx), in-use %.0f MiB (%.2fx): %.150s",
				r.name, r.format, r.limit>>20, r.scale, r.outcome(), r.live.PeakLive, r.liveRatio(), r.inUse.PeakInUse, r.inUseRatio(), r.inUse.Err)
		}
	}
}

// TestMemoryLimitHeapSmoke is the cheap version of the matrix for the normal
// suite: three cases at 16 MiB, each at the budget and at twice it. A render's
// allocations depend on the specification, not on timing, so the peaks are
// stable to about 10%; the bound is set well above what the table shows (see
// memcheckK for the honest one) so that it catches a charge that stops working,
// not a drift in the estimates.
func TestMemoryLimitHeapSmoke(t *testing.T) {
	const limit = 16 << 20
	const bound = 6.0
	for _, name := range []string{"rows-sequence", "items-rect", "canvas-big"} {
		for _, c := range memCases {
			if c.name != name {
				continue
			}
			format := "svg"
			if c.formats != nil {
				format = c.formats[0]
			}
			for _, scale := range []float64{0.9, 2} {
				t.Run(fmt.Sprintf("%s/%.1f", name, scale), func(t *testing.T) {
					t.Parallel()
					r := memRun(c, format, limit, scale)
					t.Logf("%s: live %.0f MiB (%.2fx), in-use %.0f MiB (%.2fx)", r.outcome(), r.live.PeakLive, r.liveRatio(), r.inUse.PeakInUse, r.inUseRatio())
					if r.outcome() == "HEAPCAP" || r.outcome() == "PANIC" || r.liveRatio() > bound {
						t.Errorf("%s, live heap %.2fx the limit (want at most %.0fx): %.150s", r.outcome(), r.liveRatio(), bound, r.inUse.Err)
					}
					if scale >= 1 && r.outcome() != "limit" {
						t.Errorf("twice the budget ended %s, want an error wrapping ErrLimit: %.150s", r.outcome(), r.inUse.Err)
					}
				})
			}
		}
	}
}
