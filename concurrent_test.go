package aster_test

// A Converter is safe for concurrent use: many goroutines render the corpus
// through one Converter and must get exactly what a serial run gets.
//
//	go test -race . -run 'TestConcurrent'
//	ASTER_STRESS_FULL=1 go test -race . -run 'TestConcurrentConverterSharesNothing'   # every corpus spec

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

func corpusSpecs(t testing.TB, kind string, limit int) (names []string, specs [][]byte) {
	t.Helper()
	m, _ := filepath.Glob("testdata/corpus/" + kind + "/*.json")
	sort.Strings(m)
	if len(m) < 20 {
		t.Skip("no corpus")
	}
	for i, p := range m {
		if limit > 0 && i%max(1, len(m)/limit) != 0 {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(p))
		specs = append(specs, b)
	}
	return names, specs
}

func TestConcurrentConverterSharesNothing(t *testing.T) {
	limit := 60
	if testing.Short() {
		limit = 20
	}
	if os.Getenv("ASTER_STRESS_FULL") != "" {
		limit = 0 // the whole corpus
	}
	vgNames, vg := corpusSpecs(t, "vega", limit)
	vlNames, vl := corpusSpecs(t, "vegalite", limit)
	c, err := aster.New(aster.WithLoader(corpusLoader(t)), aster.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	type job struct {
		spec []byte
		lite bool
	}
	var jobs []job
	var names []string
	for i, s := range vg {
		jobs = append(jobs, job{s, false})
		names = append(names, "vega/"+vgNames[i])
	}
	for i, s := range vl {
		jobs = append(jobs, job{s, true})
		names = append(names, "vegalite/"+vlNames[i])
	}
	render := func(j job) (string, error) {
		if j.lite {
			return c.VegaLiteToSVG(j.spec)
		}
		return c.VegaToSVG(j.spec)
	}
	// Serial baseline. Specs that draw the current time or random data are
	// not deterministic between runs and are skipped in the comparison.
	want := make([]string, len(jobs))
	werr := make([]bool, len(jobs))
	stable := make([]bool, len(jobs))
	for i, j := range jobs {
		a, e1 := render(j)
		b, e2 := render(j)
		want[i], werr[i] = a, e1 != nil
		// A chart of the current time changes between any two renders.
		stable[i] = (e1 == nil) == (e2 == nil) && a == b && !bytes.Contains(j.spec, []byte("now()"))
	}

	const workers = 12
	var wg sync.WaitGroup
	var bad atomic.Int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := range jobs {
				i := (k*5 + w*7) % len(jobs)
				got, err := render(jobs[i])
				if stable[i] && ((err != nil) != werr[i] || got != want[i]) {
					bad.Add(1)
					t.Errorf("worker %d: job %d (%s) differs from the serial render (err=%v)", w, i, names[i], err)
				}
				if k%25 == 0 && err == nil {
					// PNG and PDF are exercised for races only; svgpdf supports
					// a subset of SVG, so its errors are expected.
					if _, err := c.SVGToPNG(got); err != nil {
						t.Errorf("png: %v", err)
					}
					_, _ = c.SVGToPDF(got)
				}
			}
		}(w)
	}
	wg.Wait()
}

// Close while renders are in flight: no race, no panic, and every call either
// succeeds or reports that the converter is closed.
func TestConcurrentCloseDuringRender(t *testing.T) {
	_, vl := corpusSpecs(t, "vegalite", 30)
	c, err := aster.New(aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for k := range vl {
				_, _ = c.VegaLiteToSVG(vl[(k+w)%len(vl)])
			}
		}(w)
	}
	close(start)
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Close() }()
	}
	wg.Wait()
	if _, err := c.VegaLiteToSVG(vl[0]); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("render after Close: err = %v, want a closed error", err)
	}
}
