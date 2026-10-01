package aster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// The differential fuzzer. Skipped unless ASTER_FUZZ is set to a mutation
// count (and in -short mode):
//
//	ASTER_FUZZ=3000 ASTER_FUZZ_SEED=1 go test . -run TestFuzzDifferential -v -timeout 3h
//
// Every mutant is rendered by the engine (checked for panics and for a time
// budget) and by the node oracle (upstream Vega; see internal/oracle), then
// classified:
//
//	equal          both render, SVG equal within svgTolerance
//	both-error     both fail
//	engine-only    only the engine fails
//	node-only      only upstream fails
//	differ         both render, SVG differs
//	panic          the engine hit an internal error (a Go panic)
//	slow           the engine needed more than the budget or timed out
//
// Other variables: ASTER_FUZZ_START (first mutant number, to shard runs),
// ASTER_FUZZ_WORKERS, ASTER_FUZZ_ORACLE=0 (skip the oracle: crash and time
// checks only), ASTER_FUZZ_OUT (directory receiving one file per finding and
// an index.tsv), ASTER_FUZZ_BUDGET (seconds, default 4).
func TestFuzzDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("fuzzing is slow")
	}
	nEnv := os.Getenv("ASTER_FUZZ")
	if nEnv == "" {
		t.Skip("set ASTER_FUZZ=<mutations> to run the differential fuzzer")
	}
	count, _ := strconv.Atoi(nEnv)
	seed := envInt("ASTER_FUZZ_SEED", 1)
	start := envInt("ASTER_FUZZ_START", 0)
	workers := envInt("ASTER_FUZZ_WORKERS", 4)
	budget := time.Duration(envInt("ASTER_FUZZ_BUDGET", 4)) * time.Second
	var o *oracle.Oracle
	if os.Getenv("ASTER_FUZZ_ORACLE") != "0" {
		o = oracle.For(t, oracle.VL6)
	}
	out := os.Getenv("ASTER_FUZZ_OUT")
	if out == "" {
		out = t.TempDir()
	}
	pool := fuzzPool(t)
	t.Logf("%d base specs", len(pool))

	type job struct{ n int }
	jobs := make(chan job)
	var mu sync.Mutex
	var results []fuzzResult
	var wg sync.WaitGroup
	// One converter for every worker: a Converter is safe for concurrent use.
	c := oracleConverter(t, o, aster.WithTimeout(10*time.Second))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r := fuzzOne(c, o, pool, seed, j.n, budget)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < count; i++ {
		jobs <- job{start + i}
	}
	close(jobs)
	wg.Wait()

	// Time budgets are only meaningful without contention: re-time the slow
	// ones serially, on an otherwise idle engine.
	if len(results) > 0 {
		pg, err := aster.New(aster.WithLoader(corpusLoader(t)), aster.WithTimeout(30*time.Second))
		if err == nil {
			for i := range results {
				r := &results[i]
				if r.class != "slow" {
					continue
				}
				t0 := time.Now()
				_, rerr := renderGuarded(func() (string, error) {
					if r.lite {
						return pg.VegaLiteToSVG(r.spec)
					}
					return pg.VegaToSVG(r.spec)
				})
				r.dur = time.Since(t0)
				if r.dur <= budget && (rerr == nil || !strings.Contains(rerr.Error(), "timed out")) {
					r.class, r.detail = "slow-transient", "fast when re-timed alone: "+r.dur.String()
				}
			}
			pg.Close()
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].n < results[j].n })
	counts := map[string]int{}
	var slowest time.Duration
	var index strings.Builder
	for _, r := range results {
		counts[r.class]++
		if r.dur > slowest {
			slowest = r.dur
		}
		if r.vegaDiffers {
			counts["vl->vega-differs"]++
		}
		if r.class == "equal" || r.class == "both-error" {
			if !r.vegaDiffers {
				continue
			}
		}
		dir := filepath.Join(out, r.class)
		_ = os.MkdirAll(dir, 0o755)
		ext := ".vg.json"
		if r.lite {
			ext = ".vl.json"
		}
		if r.class == "equal" || r.class == "both-error" {
			dir = filepath.Join(out, "vega-differs")
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = os.WriteFile(filepath.Join(dir, r.id+ext), r.spec, 0o644)
		fmt.Fprintf(&index, "%s\t%s\t%s\t%s\t%s\t%s\n", r.class, r.id, r.base, strings.Join(r.muts, ";"), r.dur.Round(time.Millisecond), strings.ReplaceAll(r.detail, "\n", " "))
	}
	_ = os.WriteFile(filepath.Join(out, "index.tsv"), []byte(index.String()), 0o644)
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sum strings.Builder
	fmt.Fprintf(&sum, "\nfuzz seed=%d start=%d n=%d (findings in %s)\n", seed, start, count, out)
	for _, k := range keys {
		fmt.Fprintf(&sum, "  %-18s %6d\n", k, counts[k])
	}
	fmt.Fprintf(&sum, "  slowest render: %v\n", slowest.Round(time.Millisecond))
	t.Log(sum.String())
	if counts["panic"] > 0 {
		t.Errorf("%d mutants panicked the engine", counts["panic"])
	}
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

type fuzzResult struct {
	n           int
	id, base    string
	lite        bool
	muts        []string
	class       string
	detail      string
	spec        []byte
	dur         time.Duration
	vegaDiffers bool
}

type baseSpec struct {
	name string
	lite bool
	spec jsval.Value
}

var panicRE = regexp.MustCompile(`internal error|runtime error|goroutine \d+|nil pointer|index out of range|slice bounds`)

// fuzzOne mutates, renders and classifies mutant number n.
func fuzzOne(pg *aster.Converter, o *oracle.Oracle, pool []baseSpec, seed, n int, budget time.Duration) fuzzResult {
	rng := rand.New(rand.NewSource(int64(seed)*1_000_003 + int64(n)))
	b := pool[rng.Intn(len(pool))]
	mutant, log := mutate(b.spec, b.lite, rng)
	spec := jsval.AppendJSON(nil, mutant)
	r := fuzzResult{n: n, id: fmt.Sprintf("s%d-%05d", seed, n), base: b.name, lite: b.lite, muts: log, spec: spec}

	render := func(c interface {
		VegaLiteToSVG([]byte) (string, error)
		VegaToSVG([]byte) (string, error)
	}) (string, error) {
		if b.lite {
			return c.VegaLiteToSVG(spec)
		}
		return c.VegaToSVG(spec)
	}
	t0 := time.Now()
	got, gerr := renderGuarded(func() (string, error) { return render(pg) })
	r.dur = time.Since(t0)
	if gerr != nil && panicRE.MatchString(gerr.Error()) {
		r.class, r.detail = "panic", firstLine(gerr.Error())
		if strings.Contains(gerr.Error(), "vegalite: internal error") {
			// Compile recovers its own panics; still a bug, tallied apart.
			r.class = "panic-vegalite"
		}
		return r
	}
	if gerr != nil && strings.Contains(gerr.Error(), "timed out") {
		r.class, r.detail = "slow", "timeout: "+firstLine(gerr.Error())
		return r
	}
	if r.dur > budget {
		r.class, r.detail = "slow", "took "+r.dur.String()
		return r
	}
	if o == nil {
		if gerr != nil {
			r.class, r.detail = "engine-error", firstLine(gerr.Error())
		} else {
			r.class = "rendered"
		}
		return r
	}
	want, err := o.SVG(b.lite, spec)
	if err != nil {
		r.class, r.detail = "oracle-failure", err.Error()
		return r
	}
	switch {
	case gerr != nil && want.Err != "":
		r.class, r.detail = "both-error", "engine: "+firstLine(gerr.Error())+" | node: "+firstLine(want.Err)
	case gerr != nil:
		r.class, r.detail = "engine-only", firstLine(gerr.Error())
	case want.Err != "":
		r.class, r.detail = "node-only", firstLine(want.Err)
	default:
		d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
		switch {
		case err != nil:
			r.class, r.detail = "differ", err.Error()
		case d.Equal:
			r.class = "equal"
		default:
			r.class, r.detail = "differ", fmt.Sprintf("%d diffs; first: %s", d.DiffCount, firstLine(d.Diff))
		}
	}
	if b.lite && len(want.Vega) > 0 && gerr == nil {
		if vg, err := pg.VegaLiteToVega(spec); err == nil {
			g, e1 := jsval.ParseJSON(vg)
			w, e2 := jsval.ParseJSON(want.Vega)
			r.vegaDiffers = e1 == nil && e2 == nil && !jsval.Equal(g, w)
		}
	}
	return r
}

// renderGuarded runs f with a hard watchdog: a render that ignores its
// context cancellation must not stall the fuzzer.
func renderGuarded(f func() (string, error)) (string, error) {
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- res{"", fmt.Errorf("goroutine panic escaped the public API: %v", r)}
			}
		}()
		s, err := f()
		ch <- res{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-time.After(60 * time.Second):
		return "", fmt.Errorf("render timed out (watchdog): did not return in 60s")
	}
}

// fuzzPool selects the base specs: corpus specs the engine renders quickly.
// The selection is cached (testdata/oracle-cache/fuzz-pool.txt).
func fuzzPool(t *testing.T) []baseSpec {
	cache := filepath.Join("testdata", "oracle-cache", "fuzz-pool.txt")
	var files []string
	if b, err := os.ReadFile(cache); err == nil {
		files = strings.Fields(string(b))
	} else {
		pg, err := aster.New(aster.WithLoader(corpusLoader(t)), aster.WithTimeout(10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		for _, set := range corpusSets {
			fs, _ := filepath.Glob(set.glob)
			sort.Strings(fs)
			for _, f := range fs {
				info, err := os.Stat(f)
				if err != nil || info.Size() > 40_000 {
					continue
				}
				spec, _ := os.ReadFile(f)
				if strings.Contains(string(spec), "now()") || strings.Contains(string(spec), "Date()") {
					continue
				}
				t0 := time.Now()
				var rerr error
				if set.lite {
					_, rerr = pg.VegaLiteToSVG(spec)
				} else {
					_, rerr = pg.VegaToSVG(spec)
				}
				if rerr != nil || time.Since(t0) > 250*time.Millisecond {
					continue
				}
				files = append(files, f)
			}
		}
		_ = os.MkdirAll(filepath.Dir(cache), 0o755)
		_ = os.WriteFile(cache, []byte(strings.Join(files, "\n")), 0o644)
	}
	var pool []baseSpec
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := jsval.ParseJSON(b)
		if err != nil || !v.IsObj() {
			continue
		}
		pool = append(pool, baseSpec{name: filepath.Base(f), lite: strings.HasSuffix(f, ".vl.json"), spec: v})
	}
	if len(pool) == 0 {
		t.Fatal("no base specs")
	}
	return pool
}
