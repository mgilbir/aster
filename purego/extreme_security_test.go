package purego_test

// Extreme-value mutation sweep. Every numeric literal of a corpus spec is
// replaced, one at a time, by a hostile value (huge, negative, fractional,
// non-finite-ish). A mutant must neither panic (an "internal error" result
// means a layer panicked and only the top-level recover saved the host) nor
// run much longer than the timeout.
//
// Run:  go test ./purego -run TestExtremeValues -v -args -sec.n=300
// The default is small so that it can run in CI.

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

var (
	secN    = flag.Int("sec.n", 150, "extreme-value mutants per run")
	secSeed = flag.Int64("sec.seed", 1, "seed")
)

var confusedValues = []string{"null", "true", "false", `""`, `"x"`, `"1e999"`, `"NaN"`, `"__proto__"`, `"constructor"`, "[]", "{}", "[1,2]", `["a","b"]`, `{"a":1}`, `{"signal":"1/0"}`, `{"signal":"datum"}`, `{"field":"nofield"}`, `{"value":null}`, "[[1]]", `[{}]`, `"datum.x"`, `{"signal":"pad('x',1e9)"}`}

var extremeNumbers = []string{"1e308", "-1e308", "1e15", "-1e15", "2147483648", "4294967296", "-1", "0", "1e-300", "0.5", "1e9", "65536", "100000", "-0", "1e21"}

func leafSlots(root jsval.Value, nums bool) [][]string {
	var out [][]string
	var walk func(v jsval.Value, path []string)
	walk = func(v jsval.Value, path []string) {
		switch {
		case v.IsObj():
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				walk(o.ValueAt(i), append(append([]string(nil), path...), o.KeyAt(i)))
			}
		case v.IsArr():
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), append(append([]string(nil), path...), fmt.Sprint(i)))
			}
		case v.IsNum() || (!nums && (v.IsStr() || v.IsBool() || v.IsNull())):
			out = append(out, path)
		}
	}
	walk(root, nil)
	return out
}

func setPath(root jsval.Value, path []string, nv jsval.Value) {
	v := root
	for _, p := range path[:len(path)-1] {
		if v.IsObj() {
			v = v.ObjValue().Lookup(p)
		} else {
			var i int
			fmt.Sscan(p, &i)
			v = v.Index(i)
		}
	}
	last := path[len(path)-1]
	if v.IsObj() {
		v.ObjValue().Set(last, nv)
	} else {
		var i int
		fmt.Sscan(last, &i)
		v.Items()[i] = nv
	}
}

func TestExtremeValues(t *testing.T) { runMutations(t, "numbers") }

// TestTypeConfusion replaces scalar values with values of another type.
func TestTypeConfusion(t *testing.T) { runMutations(t, "types") }

func runMutations(t *testing.T, mode string) {
	var files []string
	for _, d := range []string{"testdata/corpus/vega", "testdata/corpus/vegalite", "testdata/corpus/vg-gallery"} {
		m, _ := filepath.Glob(d + "/*.json")
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Skip("no corpus")
	}
	sort.Strings(files)
	rng := rand.New(rand.NewSource(*secSeed))
	ld := corpusLoader(t)

	type job struct {
		file, desc, spec string
		lite             bool
	}
	var jobs []job
	for len(jobs) < *secN {
		f := files[rng.Intn(len(files))]
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := jsval.ParseJSON(b)
		if err != nil {
			continue
		}
		slots := leafSlots(v, mode == "numbers")
		if len(slots) == 0 {
			continue
		}
		s := slots[rng.Intn(len(slots))]
		pool := extremeNumbers
		if mode == "types" {
			pool = confusedValues
		}
		x := pool[rng.Intn(len(pool))]
		setPath(v, s, mustJSON(x))
		jobs = append(jobs, job{f, strings.Join(s, "/") + "=" + x, string(jsval.AppendJSON(nil, v)), strings.Contains(f, "vegalite")})
	}

	var mu sync.Mutex
	var bad []string
	var wg sync.WaitGroup
	ch := make(chan job)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				c, err := purego.New(purego.WithLoader(ld), purego.WithTimeout(3*time.Second), purego.WithMemoryLimit(256<<20))
				if err != nil {
					t.Error(err)
					return
				}
				done := make(chan string, 1)
				start := time.Now()
				go func() {
					var err error
					if j.lite {
						_, err = c.VegaLiteToSVG([]byte(j.spec))
					} else {
						_, err = c.VegaToSVG([]byte(j.spec))
					}
					if err != nil && strings.Contains(err.Error(), "internal error") {
						done <- "PANIC " + err.Error()
						return
					}
					done <- ""
				}()
				var msg string
				select {
				case msg = <-done:
				case <-time.After(20 * time.Second):
					msg = "HANG(>20s with 3s timeout)"
				}
				if el := time.Since(start); msg == "" && el > 8*time.Second {
					msg = fmt.Sprintf("SLOW %v with 3s timeout", el)
				}
				if msg != "" {
					mu.Lock()
					bad = append(bad, fmt.Sprintf("%s [%s]: %s", filepath.Base(j.file), j.desc, msg))
					mu.Unlock()
				}
				_ = context.Background
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
	t.Logf("%d mutants, %d bad", len(jobs), len(bad))
}

func mustJSON(s string) jsval.Value {
	v, err := jsval.ParseJSONString(s)
	if err != nil {
		panic(err)
	}
	return v
}
