package vegalite

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// corpusFloorVL58 is the number of corpus specifications that must compile to
// exactly what vega-lite@5.8.0 emits (key order included). It is the whole
// corpus: testdata/reference_vl5.jsonl.gz is recorded by gen_reference.mjs
// against testdata/oracle-node-vl5, which pins vega-lite 5.8.0 on the module
// versions of its Vega 5.25 build.
const corpusFloorVL58 = 1924

// TestCorpusVL58 compiles the corpus under Version "5.8" and compares it with
// the references recorded by vega-lite 5.8.0. VEGALITE_REPORT writes the
// failures.
func TestCorpusVL58(t *testing.T) {
	refs := loadGzipReferences(t, "testdata/reference_vl5.jsonl.gz")
	exact := 0
	type fail struct{ name, why string }
	var fails []fail
	for _, r := range refs {
		data, err := os.ReadFile(filepath.Join("testdata", "specs", r.name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		got, cerr := Compile(spec, Options{Location: goldenZone, Version: Version58})
		switch {
		case r.errMsg != "":
			if cerr == nil {
				fails = append(fails, fail{r.name, "expected error: " + r.errMsg})
			} else {
				exact++
			}
		case cerr != nil:
			fails = append(fails, fail{r.name, "error: " + strings.SplitN(cerr.Error(), "\n", 2)[0]})
		case string(jsval.AppendJSON(nil, got)) == string(jsval.AppendJSON(nil, r.vega)):
			exact++
		default:
			fails = append(fails, fail{r.name, firstDiff("$", got, r.vega)})
		}
	}
	t.Logf("vega-lite 5.8 corpus: %d/%d compile to exactly upstream's Vega", exact, len(refs))
	if path := os.Getenv("VEGALITE_REPORT"); path != "" {
		sort.Slice(fails, func(i, j int) bool { return fails[i].name < fails[j].name })
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d/%d exact\n", exact, len(refs))
		for _, f := range fails {
			fmt.Fprintf(&sb, "%s\t%s\n", f.name, f.why)
		}
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
	if exact < corpusFloorVL58 {
		t.Errorf("vega-lite 5.8 corpus score %d fell below the floor %d", exact, corpusFloorVL58)
	}
}

// TestThemesVL58 compiles the themed corpus (gallery and fixture specs under
// each vega-themes config) under Version "5.8".
func TestThemesVL58(t *testing.T) {
	data, err := os.ReadFile("testdata/themes.json")
	if err != nil {
		t.Fatal(err)
	}
	themes, err := jsval.ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	refs := loadGzipReferences(t, "testdata/reference_themes_vl5.jsonl.gz")
	exact := 0
	for _, r := range refs {
		specData, err := os.ReadFile(filepath.Join("testdata", "specs", r.name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(specData)
		if err != nil {
			t.Fatal(err)
		}
		got, cerr := Compile(spec, Options{Config: themes.Get(r.theme), Location: goldenZone, Version: Version58})
		switch {
		case r.errMsg != "":
			if cerr == nil {
				t.Errorf("%s (%s): expected an error", r.name, r.theme)
			} else {
				exact++
			}
		case cerr != nil:
			t.Errorf("%s (%s): %v", r.name, r.theme, cerr)
		case string(jsval.AppendJSON(nil, got)) != string(jsval.AppendJSON(nil, r.vega)):
			t.Errorf("%s (%s): %s", r.name, r.theme, firstDiff("$", got, r.vega))
		default:
			exact++
		}
	}
	t.Logf("vega-lite 5.8 themed corpus: %d/%d exact", exact, len(refs))
}

func TestUnsupportedVersion(t *testing.T) {
	spec := jsval.Obj(mk("mark", "point"))
	if _, err := Compile(spec, Options{Version: "4.17"}); err == nil {
		t.Fatal("expected an error for an unsupported version")
	}
	for _, v := range []string{"", Version64, Version58} {
		if _, err := Compile(spec, Options{Version: v}); err != nil {
			t.Errorf("version %q: %v", v, err)
		}
	}
}

// TestVersionsInterleaved compiles the same specs under both versions from many
// goroutines at once (run with -race): each result must equal the serial one,
// so no compilation sees another's version.
func TestVersionsInterleaved(t *testing.T) {
	names := []string{"gallery/bar.vl", "gallery/line.vl", "gallery/point_2d.vl", "convert/circle_binned.vl", "fixtures/animated-frames.vl"}
	var specs []jsval.Value
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join("testdata", "specs", n+".json"))
		if err != nil {
			continue
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		t.Skip("no specs found")
	}
	compile := func(spec jsval.Value, version string) string {
		out, err := Compile(spec, Options{Location: goldenZone, Version: version})
		if err != nil {
			return "error: " + err.Error()
		}
		return string(jsval.AppendJSON(nil, out))
	}
	want := map[string][]string{}
	for _, v := range []string{Version64, Version58} {
		for _, s := range specs {
			want[v] = append(want[v], compile(s, v))
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				v := Version64
				if (g+i)%2 == 1 {
					v = Version58
				}
				for j, s := range specs {
					if got := compile(s, v); got != want[v][j] {
						t.Errorf("goroutine %d, version %s, spec %d: result differs from the serial compilation", g, v, j)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
