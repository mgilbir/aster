// Command enginebench times the Vega / Vega-Lite engines on the same specs:
// the root package (QuickJS and resvg on andsifr), purego, and upstream Vega
// in node (bench.mjs). Each engine runs in its own process so startup and peak
// memory are its own; every process writes the same JSON result format, and
// -report merges them into a comparison table. See README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/purego"
)

// engine is the API the root package and purego share.
type engine interface {
	VegaToSVG(spec []byte) (string, error)
	VegaLiteToSVG(spec []byte) (string, error)
	VegaLiteToVega(spec []byte) ([]byte, error)
	Close() error
}

// pngEngine adapts the two packages' PNG methods, whose option types differ.
type pngEngine interface {
	vegaPNG(spec []byte) ([]byte, error)
	vegaLitePNG(spec []byte) ([]byte, error)
}

type asterEngine struct{ *aster.Converter }

func (e asterEngine) vegaPNG(s []byte) ([]byte, error)     { return e.VegaToPNG(s) }
func (e asterEngine) vegaLitePNG(s []byte) ([]byte, error) { return e.VegaLiteToPNG(s) }

type puregoEngine struct{ *purego.Converter }

func (e puregoEngine) vegaPNG(s []byte) ([]byte, error)     { return e.VegaToPNG(s) }
func (e puregoEngine) vegaLitePNG(s []byte) ([]byte, error) { return e.VegaLiteToPNG(s) }

// Result is one (spec, stage) measurement. The JSON form is shared with
// bench.mjs.
type Result struct {
	Suite  string  `json:"suite"`
	Name   string  `json:"name"`
	Stage  string  `json:"stage"`
	Err    string  `json:"err,omitempty"`
	Runs   int     `json:"runs,omitempty"`
	Median float64 `json:"median_ms,omitempty"`
	Min    float64 `json:"min_ms,omitempty"`
	// AllocMB is Go heap allocated per run; the root engine's WASM linear
	// memory is not included. Node reports nothing here.
	AllocMB float64 `json:"alloc_mb,omitempty"`
}

// Report is one engine's output file.
type Report struct {
	Engine  string   `json:"engine"`
	Version string   `json:"version"`
	Env     string   `json:"env"`
	InitMS  float64  `json:"init_ms"`      // create the engine (node: import the modules)
	FirstMS float64  `json:"first_svg_ms"` // first VL→SVG render of a bar chart, cold
	RSSMB   float64  `json:"peak_rss_mb"`  // peak resident set of the process
	Budget  float64  `json:"budget_ms"`    // timed-run budget per (spec, stage)
	Results []Result `json:"results"`
}

type spec struct {
	suite, name string
	lite        bool
	data        []byte
}

var stageNames = []string{"vl2vg", "svg", "png"}

func main() {
	var (
		engineName = flag.String("engine", "", "engine to time: aster or purego")
		out        = flag.String("out", "", "write the JSON result here (default stdout)")
		budget     = flag.Duration("budget", 300*time.Millisecond, "time budget for the timed runs of one (spec, stage)")
		minRuns    = flag.Int("min", 3, "minimum timed runs (a single run over 2s is not repeated)")
		maxRuns    = flag.Int("max", 200, "maximum timed runs")
		stages     = flag.String("stages", "vl2vg,svg,png", "stages to time: vl2vg (VL→Vega JSON), svg, png")
		filter     = flag.String("filter", "", "only specs whose suite/name matches this regexp")
		report     = flag.Bool("report", false, "merge the JSON files given as arguments into a markdown report")
	)
	flag.Parse()
	if *report {
		if err := writeReport(os.Stdout, flag.Args()); err != nil {
			fatal(err)
		}
		return
	}
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	specs, err := loadSpecs(root, *filter)
	if err != nil {
		fatal(err)
	}
	want := strings.Split(*stages, ",")
	for _, s := range want {
		if !slices.Contains(stageNames, s) {
			fatal(fmt.Errorf("unknown stage %q", s))
		}
	}
	loader := newMemLoader(root)

	bar, err := os.ReadFile(filepath.Join(root, "testdata/vega-lite/v6.4.3/specs/bar.vl.json"))
	if err != nil {
		fatal(err)
	}
	rep := Report{Engine: *engineName, Env: env(), Budget: float64(*budget) / 1e6}
	t0 := time.Now()
	var e engine
	switch *engineName {
	case "aster":
		c, err := aster.New(aster.WithLoader(loader), aster.WithTimeout(2*time.Minute))
		if err != nil {
			fatal(err)
		}
		e = asterEngine{c}
		rep.Version = versionOf(aster.AvailableVersions) + ", QuickJS + resvg (WASM, andsifr)"
	case "purego":
		c, err := purego.New(purego.WithLoader(loader), purego.WithTimeout(2*time.Minute))
		if err != nil {
			fatal(err)
		}
		e = puregoEngine{c}
		rep.Version = versionOf(purego.AvailableVersions) + ", pure Go"
	default:
		fatal(errors.New("-engine must be aster or purego"))
	}
	defer e.Close()
	rep.InitMS = ms(time.Since(t0))
	t0 = time.Now()
	if _, err := e.VegaLiteToSVG(bar); err != nil {
		fatal(err)
	}
	rep.FirstMS = ms(time.Since(t0))

	// Warm pass: every (spec, stage) once, so lazy initialisation (resvg on
	// the first PNG, font loading, caches) is out of the timed runs, and
	// failing cases are recorded rather than timed.
	type job struct {
		s     spec
		stage string
		run   func() error
	}
	var jobs []job
	for _, s := range specs {
		for _, st := range want {
			if st == "vl2vg" && !s.lite {
				continue
			}
			run := runner(e, s, st)
			if err := run(); err != nil {
				rep.Results = append(rep.Results, Result{Suite: s.suite, Name: s.name, Stage: st, Err: firstLine(err)})
				continue
			}
			jobs = append(jobs, job{s, st, run})
		}
	}
	fmt.Fprintf(os.Stderr, "%s: %d timed cases, %d failed in the warm pass\n", *engineName, len(jobs), len(rep.Results))
	for i, j := range jobs {
		r := measure(j.run, *budget, *minRuns, *maxRuns)
		r.Suite, r.Name, r.Stage = j.s.suite, j.s.name, j.stage
		rep.Results = append(rep.Results, r)
		if (i+1)%100 == 0 {
			fmt.Fprintf(os.Stderr, "%s: %d/%d\n", *engineName, i+1, len(jobs))
		}
	}
	rep.RSSMB = peakRSSMB()

	b, err := json.MarshalIndent(rep, "", " ")
	if err != nil {
		fatal(err)
	}
	if *out == "" {
		os.Stdout.Write(append(b, '\n'))
		return
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fatal(err)
	}
}

func runner(e engine, s spec, stage string) func() error {
	p := e.(pngEngine)
	switch {
	case stage == "vl2vg":
		return func() error { _, err := e.VegaLiteToVega(s.data); return err }
	case stage == "svg" && s.lite:
		return func() error { _, err := e.VegaLiteToSVG(s.data); return err }
	case stage == "svg":
		return func() error { _, err := e.VegaToSVG(s.data); return err }
	case s.lite:
		return func() error { _, err := p.vegaLitePNG(s.data); return err }
	default:
		return func() error { _, err := p.vegaPNG(s.data); return err }
	}
}

// measure times run until the budget is spent and at least min runs are
// done (or max runs), and reports the median. A run slower than 2s is not
// repeated past the first.
func measure(run func() error, budget time.Duration, min, max int) Result {
	var times []time.Duration
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	start := time.Now()
	for len(times) < max {
		t0 := time.Now()
		if err := run(); err != nil {
			return Result{Err: firstLine(err)}
		}
		d := time.Since(t0)
		times = append(times, d)
		if d > 2*time.Second || (len(times) >= min && time.Since(start) >= budget) {
			break
		}
	}
	runtime.ReadMemStats(&ms1)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return Result{
		Runs:    len(times),
		Median:  ms(times[len(times)/2]),
		Min:     ms(times[0]),
		AllocMB: float64(ms1.TotalAlloc-ms0.TotalAlloc) / float64(len(times)) / (1 << 20),
	}
}

// loadSpecs reads the Vega-Lite examples and the Vega gallery, sorted.
func loadSpecs(root, filter string) ([]spec, error) {
	var re *regexp.Regexp
	if filter != "" {
		var err error
		if re, err = regexp.Compile(filter); err != nil {
			return nil, err
		}
	}
	var out []spec
	for _, d := range []struct{ suite, dir, ext string }{
		{"vl-examples", "testdata/vega-lite/v6.4.3/specs", ".vl.json"},
		{"vg-gallery", "purego/testdata/corpus/vg-gallery", ".vg.json"},
	} {
		files, err := filepath.Glob(filepath.Join(root, d.dir, "*"+d.ext))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), d.ext)
			if re != nil && !re.MatchString(d.suite+"/"+name) {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			out = append(out, spec{d.suite, name, d.ext == ".vl.json", b})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no specs selected")
	}
	return out, nil
}

// memLoader serves the vega-datasets checkout (and purego's extra test data)
// from memory, mapping the CDN URLs examples use onto the local copy, so the
// timings measure the engines and not the file system. bench.mjs does the
// same.
type memLoader struct {
	dirs []string
	mu   sync.Mutex
	data map[string][]byte
}

func newMemLoader(root string) *memLoader {
	return &memLoader{
		dirs: []string{filepath.Join(root, "testdata/vega-datasets"), filepath.Join(root, "purego/testdata/data")},
		data: map[string][]byte{},
	}
}

var cdn = regexp.MustCompile(`^https?://(?:cdn\.jsdelivr\.net/npm/vega-datasets@[^/]+|raw\.githubusercontent\.com/vega/vega-datasets/[^/]+|vega\.github\.io/vega-datasets)/(data/.+)$`)

// Sanitize also serves hyperlink hrefs, so only Load rejects non-local URIs.
func (m *memLoader) Sanitize(_ context.Context, uri string) (string, error) {
	if s := cdn.FindStringSubmatch(uri); s != nil {
		uri = s[1]
	}
	return uri, nil
}

func (m *memLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	uri, _ = m.Sanitize(ctx, uri)
	if strings.Contains(uri, "://") || strings.Contains(uri, "..") || filepath.IsAbs(uri) {
		return nil, fmt.Errorf("enginebench: not a local dataset: %s", uri)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.data[uri]; ok {
		return b, nil
	}
	for _, d := range m.dirs {
		if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(uri))); err == nil {
			m.data[uri] = b
			return b, nil
		}
	}
	return nil, fmt.Errorf("enginebench: no such dataset: %s", uri)
}

// versionOf describes the default (Vega-Lite 6.4) engine of either package.
func versionOf[V aster.VersionInfo | purego.VersionInfo](available func() ([]V, error)) string {
	vs, err := available()
	if err != nil {
		return "?"
	}
	for _, v := range vs {
		if x := aster.VersionInfo(v); x.Key == "vl6_4" {
			return "Vega " + x.VegaVersion + " / Vega-Lite " + x.VegaLiteVersion
		}
	}
	return "?"
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "purego", "purego.go")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run inside the aster repository")
		}
		dir = parent
	}
}

func peakRSSMB() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20) // bytes
	}
	return float64(ru.Maxrss) / (1 << 10) // kilobytes
}

func env() string {
	return fmt.Sprintf("%s %s/%s, GOMAXPROCS=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0))
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "enginebench:", err)
	os.Exit(1)
}
