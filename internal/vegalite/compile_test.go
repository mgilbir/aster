package vegalite

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

// corpusFloor is the number of corpus specifications that must compile to
// exactly upstream's Vega (key order included). Raise it as the compiler
// improves; the test fails if the score drops below it.
const corpusFloor = 1924

type refEntry struct {
	name   string
	theme  string
	vega   jsval.Value
	errMsg string
}

func loadReferences(t testing.TB) []refEntry {
	return loadGzipReferences(t, "testdata/reference.jsonl.gz")
}

func loadGzipReferences(t testing.TB, path string) []refEntry {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var out []refEntry
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		v, err := jsval.ParseJSON(line)
		if err != nil {
			t.Fatalf("bad reference line: %v", err)
		}
		e := refEntry{name: v.Get("name").StrValue(), vega: v.Get("vega"), theme: v.Get("theme").StrValue()}
		if v.Get("error").IsStr() {
			e.errMsg = v.Get("error").StrValue()
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// firstDiff describes the first difference between two values ("" if equal).
func firstDiff(path string, a, b jsval.Value) string {
	if a.Kind() != b.Kind() {
		return fmt.Sprintf("%s: kind %v vs %v (%s vs %s)", path, a.Kind(), b.Kind(), clip(a), clip(b))
	}
	switch a.Kind() {
	case jsval.KindArr:
		x, y := a.Items(), b.Items()
		for i := 0; i < len(x) && i < len(y); i++ {
			if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), x[i], y[i]); d != "" {
				return d
			}
		}
		if len(x) != len(y) {
			return fmt.Sprintf("%s: length %d vs %d", path, len(x), len(y))
		}
	case jsval.KindObj:
		x, y := a.ObjValue(), b.ObjValue()
		ka, kb := definedKeys(x), definedKeys(y)
		for i := 0; i < len(ka) && i < len(kb); i++ {
			if ka[i] != kb[i] {
				return fmt.Sprintf("%s: key order/name %q vs %q (ours %v, want %v)", path, ka[i], kb[i], ka, kb)
			}
			if d := firstDiff(path+"."+ka[i], x.Lookup(ka[i]), y.Lookup(kb[i])); d != "" {
				return d
			}
		}
		if len(ka) != len(kb) {
			return fmt.Sprintf("%s: keys %v vs %v", path, ka, kb)
		}
	default:
		if !jsval.Equal(a, b) {
			return fmt.Sprintf("%s: %s vs %s", path, clip(a), clip(b))
		}
	}
	return ""
}

func definedKeys(o *jsval.Object) []string {
	var ks []string
	for i := 0; i < o.Len(); i++ {
		if !o.ValueAt(i).IsUndefined() {
			ks = append(ks, o.KeyAt(i))
		}
	}
	return ks
}

func clip(v jsval.Value) string {
	s := string(jsval.AppendJSON(nil, v))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func TestCorpus(t *testing.T) {
	refs := loadReferences(t)
	exact := 0
	type fail struct{ name, why string }
	var fails []fail
	for _, r := range refs {
		specPath := filepath.Join("testdata", "specs", r.name+".json")
		data, err := os.ReadFile(specPath)
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		got, cerr := Compile(spec, Options{Location: goldenZone})
		if r.errMsg != "" {
			if cerr == nil {
				fails = append(fails, fail{r.name, "expected error: " + r.errMsg})
			} else {
				exact++
			}
			continue
		}
		if cerr != nil {
			msg := cerr.Error()
			if i := strings.Index(msg, "\n"); i > 0 {
				msg = msg[:i]
			}
			fails = append(fails, fail{r.name, "error: " + msg})
			continue
		}
		want := string(jsval.AppendJSON(nil, r.vega))
		have := string(jsval.AppendJSON(nil, got))
		if want == have {
			exact++
			continue
		}
		fails = append(fails, fail{r.name, firstDiff("$", got, r.vega)})
	}
	t.Logf("corpus: %d/%d compile to exactly upstream's Vega", exact, len(refs))
	if path := os.Getenv("VEGALITE_REPORT"); path != "" {
		sort.Slice(fails, func(i, j int) bool { return fails[i].name < fails[j].name })
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d/%d exact\n", exact, len(refs))
		for _, f := range fails {
			fmt.Fprintf(&sb, "%s\t%s\n", f.name, f.why)
		}
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
	if exact < corpusFloor {
		t.Errorf("corpus score %d fell below the floor %d", exact, corpusFloor)
	}
}

// TestExternalCorpus compares against a larger, externally generated corpus:
//
//	VEGALITE_EXT_SPECS=<dir of *.json specs> VEGALITE_EXT_REF=<reference jsonl>
//
// (same format as testdata/reference.jsonl, names being file names). It is
// skipped when the variables are unset; VEGALITE_REPORT writes the failures.
func TestExternalCorpus(t *testing.T) {
	dir, refFile := os.Getenv("VEGALITE_EXT_SPECS"), os.Getenv("VEGALITE_EXT_REF")
	if dir == "" || refFile == "" {
		t.Skip("VEGALITE_EXT_SPECS / VEGALITE_EXT_REF not set")
	}
	f, err := os.Open(refFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	exact, total := 0, 0
	var report strings.Builder
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		v, err := jsval.ParseJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		name := v.Get("name").StrValue()
		total++
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		got, cerr := Compile(spec, Options{Config: v.Get("config"), Location: goldenZone})
		if v.Get("error").IsStr() {
			if cerr != nil {
				exact++
			} else {
				fmt.Fprintf(&report, "%s\texpected error: %s\n", name, v.Get("error").StrValue())
			}
			continue
		}
		if cerr != nil {
			msg := cerr.Error()
			if i := strings.Index(msg, "\n"); i > 0 {
				msg = msg[:i]
			}
			fmt.Fprintf(&report, "%s\terror: %s\n", name, msg)
			continue
		}
		want := v.Get("vega")
		if string(jsval.AppendJSON(nil, want)) == string(jsval.AppendJSON(nil, got)) {
			exact++
			continue
		}
		fmt.Fprintf(&report, "%s\t%s\n", name, firstDiff("$", got, want))
	}
	t.Logf("external corpus: %d/%d exact", exact, total)
	if path := os.Getenv("VEGALITE_REPORT"); path != "" {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("%d/%d exact\n%s", exact, total, report.String())), 0o644)
	}
}

// TestDifferentialFuzz compares against upstream on mutated specifications:
//
//	VEGALITE_FUZZ=<jsonl of {name, spec, vega|error}>
//
// Upstream errors must be errors here and vice versa; results must match
// exactly. Skipped when the variable is unset. VEGALITE_VERSION selects the
// Vega-Lite version the recording was made with ("5.8"; default 6.4).
func TestDifferentialFuzz(t *testing.T) {
	file := os.Getenv("VEGALITE_FUZZ")
	if file == "" {
		t.Skip("VEGALITE_FUZZ not set")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	var okBoth, errBoth, weErr, theyErr, diff, internal int
	var report strings.Builder
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		v, err := jsval.ParseJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		name := v.Get("name").StrValue()
		got, cerr := Compile(v.Get("spec"), Options{Location: goldenZone, Version: os.Getenv("VEGALITE_VERSION")})
		wantErr := v.Get("error").IsStr()
		switch {
		case wantErr && cerr != nil:
			errBoth++
			if strings.HasPrefix(cerr.Error(), "vegalite: internal error") {
				internal++
				fmt.Fprintf(&report, "%s\tINTERNAL\t%s\n", name, strings.SplitN(cerr.Error(), "\n", 2)[0])
			}
		case wantErr:
			theyErr++
			fmt.Fprintf(&report, "%s\tUPSTREAM-ERROR-ONLY\t%s\n", name, v.Get("error").StrValue())
		case cerr != nil:
			weErr++
			msg := cerr.Error()
			if i := strings.Index(msg, "\n"); i > 0 {
				msg = msg[:i]
			}
			fmt.Fprintf(&report, "%s\tOUR-ERROR-ONLY\t%s\n", name, msg)
			if strings.HasPrefix(msg, "vegalite: internal error") {
				internal++
			}
		default:
			if string(jsval.AppendJSON(nil, v.Get("vega"))) == string(jsval.AppendJSON(nil, got)) {
				okBoth++
			} else {
				diff++
				fmt.Fprintf(&report, "%s\tDIFF\t%s\n", name, firstDiff("$", got, v.Get("vega")))
			}
		}
	}
	t.Logf("fuzz: both ok exact %d, both error %d, we error only %d, upstream error only %d, differ %d, internal errors %d", okBoth, errBoth, weErr, theyErr, diff, internal)
	if path := os.Getenv("VEGALITE_REPORT"); path != "" {
		_ = os.WriteFile(path, []byte(report.String()), 0o644)
	}
}

// TestDumpCase writes the specification, our output and upstream's output of one
// case of a JSONL corpus for inspection:
//
//	VEGALITE_DUMP=<jsonl>:<name>:<outdir>
//
// VEGALITE_VERSION selects the Vega-Lite version ("5.8"; default 6.4).
func TestDumpCase(t *testing.T) {
	arg := os.Getenv("VEGALITE_DUMP")
	if arg == "" {
		t.Skip("VEGALITE_DUMP not set")
	}
	parts := strings.Split(arg, ":")
	f, err := os.Open(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for sc.Scan() {
		v, err := jsval.ParseJSON(sc.Bytes())
		if err != nil || v.Get("name").StrValue() != parts[1] {
			continue
		}
		got, cerr := Compile(v.Get("spec"), Options{Location: goldenZone, Version: os.Getenv("VEGALITE_VERSION")})
		_ = os.WriteFile(filepath.Join(parts[2], "spec.json"), jsval.AppendJSONIndent(nil, v.Get("spec"), "  "), 0o644)
		_ = os.WriteFile(filepath.Join(parts[2], "want.json"), jsval.AppendJSONIndent(nil, v.Get("vega"), "  "), 0o644)
		if cerr != nil {
			_ = os.WriteFile(filepath.Join(parts[2], "ours.json"), []byte(cerr.Error()), 0o644)
		} else {
			_ = os.WriteFile(filepath.Join(parts[2], "ours.json"), jsval.AppendJSONIndent(nil, got, "  "), 0o644)
		}
		return
	}
	t.Fatal("case not found")
}

// TestCompileFile compiles VEGALITE_FILE and writes VEGALITE_FILE+".ours.json".
// VEGALITE_VERSION selects the Vega-Lite version ("5.8"; default 6.4).
func TestCompileFile(t *testing.T) {
	file := os.Getenv("VEGALITE_FILE")
	if file == "" {
		t.Skip("VEGALITE_FILE not set")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := jsval.ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	out, cerr := Compile(spec, Options{Location: goldenZone, Version: os.Getenv("VEGALITE_VERSION")})
	if cerr != nil {
		_ = os.WriteFile(file+".ours.json", []byte("ERROR: "+cerr.Error()), 0o644)
		return
	}
	_ = os.WriteFile(file+".ours.json", jsval.AppendJSONIndent(nil, out, "  "), 0o644)
}

// TestThemes compiles corpus specs under the named vega-themes configs
// (testdata/themes.json), passed as Options.Config, as aster's WithTheme does.
func TestThemes(t *testing.T) {
	data, err := os.ReadFile("testdata/themes.json")
	if err != nil {
		t.Fatal(err)
	}
	themes, err := jsval.ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	refs := loadGzipReferences(t, "testdata/reference_themes.jsonl.gz")
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
		got, cerr := Compile(spec, Options{Config: themes.Get(r.theme), Location: goldenZone})
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
	t.Logf("themed corpus: %d/%d exact", exact, len(refs))
}

// goldenZone is the time zone the references were recorded in: vega-lite
// reads datetime objects without `utc` in node's local time. Record new
// references with TZ=Europe/Amsterdam, or re-record them all under another
// zone and change this.
var goldenZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		panic(err)
	}
	return loc
}()
