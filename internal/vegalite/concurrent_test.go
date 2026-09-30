package vegalite

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestConcurrentVersions compiles the same specs as 6.4 and as 5.8 from many
// goroutines at once and checks each result against a sequential run. The
// version lives in the per-compilation state, so the versions neither block one
// another nor share anything (run with -race to check the latter).
func TestConcurrentVersions(t *testing.T) {
	refs := loadReferences(t)
	if len(refs) > 200 {
		refs = refs[:200]
	}
	versions := []string{Version64, Version58}
	specs := make([]jsval.Value, len(refs))
	want := make([][2]string, len(refs))
	for i, r := range refs {
		data, err := os.ReadFile(filepath.Join("testdata", "specs", r.name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		specs[i] = mustParse(t, string(data))
		for v, version := range versions {
			if out, err := Compile(specs[i], Options{Version: version}); err == nil {
				want[i][v] = string(jsval.AppendJSON(nil, out))
			}
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			v := g % 2 // half the goroutines per version, interleaved
			for i := range specs {
				j := (i + g*29) % len(specs)
				out, err := Compile(specs[j], Options{Version: versions[v]})
				got := ""
				if err == nil {
					got = string(jsval.AppendJSON(nil, out))
				}
				if got != want[j][v] {
					t.Errorf("%s (%s): concurrent result differs", refs[j].name, versions[v])
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
