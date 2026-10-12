package jsval

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/testutil"
	ecma262 "github.com/mgilbir/goecma262"
	ecmacompiler "github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/vm"
)

// programLen is the length and capacity of re's program, read as codeSlots
// reads it; the test fails should goecma262 stop exposing it that way.
func programLen(t testing.TB, re *ecma262.Regexp) (n, slots int64) {
	t.Helper()
	f := reflect.ValueOf(re).Elem().FieldByName("code")
	if !f.IsValid() || f.Kind() != reflect.Slice {
		t.Fatal("goecma262's Regexp no longer has its program in a field named code")
	}
	return int64(f.Len()), int64(f.Cap())
}

// patternCase is a source and flags.
type patternCase struct{ src, flags string }

// checkCost compiles c with goecma262 and checks EstimatePattern against
// what it builds: the same error for a source that does not compile, the
// cap refusal exactly when goecma262 refuses for the cap, and otherwise an
// instruction count at or above the program's length (equal unless a v-mode
// class holds strings, the one place the bound is not exact), with the
// program's capacity within the bound's. It returns bound/len, 0 when the
// pattern does not compile.
func checkCost(t testing.TB, c patternCase) float64 {
	t.Helper()
	cost, estErr := EstimatePattern(c.src, c.flags)
	re, err := ecma262.CompileFlags(c.src, c.flags)
	if err != nil {
		want := patternError(c.src, c.flags, err)
		switch {
		case estErr != nil:
			if estErr.Error() != want.Error() {
				t.Errorf("/%s/%s: EstimatePattern's error %q, goecma262's %q", c.src, c.flags, estErr, want)
			}
		case cost.TooLarge:
			if got := tooLargeError(c.src, c.flags); got.Error() != want.Error() {
				t.Errorf("/%s/%s: refused as %q, goecma262 fails with %q", c.src, c.flags, got, want)
			}
		case !cost.fails:
			if !strings.Contains(err.Error(), "compiled program too large") {
				t.Errorf("/%s/%s: estimated to compile, goecma262 fails: %v", c.src, c.flags, err)
			}
		}
		return 0
	}
	if estErr != nil || cost.TooLarge || cost.fails {
		t.Errorf("/%s/%s: compiles, estimated as error %v, too large %v, failing %v", c.src, c.flags, estErr, cost.TooLarge, cost.fails)
		return 0
	}
	n, slots := programLen(t, re)
	if cost.Instructions < n {
		t.Errorf("/%s/%s: bound of %d instructions under the program's %d", c.src, c.flags, cost.Instructions, n)
	}
	if slots > programCap(cost.Instructions) {
		t.Errorf("/%s/%s: program capacity %d past the bound's %d", c.src, c.flags, slots, programCap(cost.Instructions))
	}
	if cost.Instructions != n && !strings.Contains(c.flags, "v") {
		t.Errorf("/%s/%s: bound of %d instructions, program of %d: the bound is exact outside v-mode", c.src, c.flags, cost.Instructions, n)
	}
	return float64(cost.Instructions) / float64(n)
}

// corpusPatterns are the patterns of the repository's specifications and
// expression vectors: every regular expression literal and string argument
// of test, regexp and replace, every countpattern pattern, and every short
// string value as a source (most do not compile, which checks the errors).
func corpusPatterns(t testing.TB) []patternCase {
	t.Helper()
	literal := regexp.MustCompile(`/((?:\\.|\[(?:\\.|[^\]\\])*\]|[^/\\\[\n])+)/([gimsuy]*)`)
	argument := regexp.MustCompile(`(?:test|regexp|replace)\(\s*'((?:\\.|[^'\\])*)'(?:\s*,\s*'([a-z]*)')?`)
	seen := map[patternCase]bool{}
	add := func(src, flags string) {
		if len(src) <= 256 {
			seen[patternCase{src, flags}] = true
		}
	}
	var visit func(v any, key string)
	visit = func(v any, key string) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				visit(x, k)
			}
		case []any:
			for _, x := range v {
				visit(x, key)
			}
		case string:
			add(v, "")
			if key == "pattern" || key == "stopwords" {
				add(v, "g")
				add("^"+v+"$", "i")
			}
			for _, m := range literal.FindAllStringSubmatch(v, -1) {
				add(m[1], m[2])
			}
			for _, m := range argument.FindAllStringSubmatch(v, -1) {
				add(strings.ReplaceAll(m[1], `\\`, `\`), m[2])
			}
		}
	}
	var files []string
	for _, glob := range []string{"../../testdata/*.json", "../../testdata/*/*.json", "../../testdata/*/*/*.json", "../../testdata/*/*/*/*.json", "../expr/testdata/*.json", "../expr/testdata/*.json.gz"} {
		m, _ := filepath.Glob(glob)
		files = append(files, m...)
	}
	read := 0
	for _, f := range files {
		if strings.Contains(f, "node_modules") || strings.Contains(f, "-cache") {
			continue
		}
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		var r io.Reader = fh
		if strings.HasSuffix(f, ".gz") {
			if r, err = gzip.NewReader(fh); err != nil {
				t.Fatal(err)
			}
		}
		var v any
		if json.NewDecoder(r).Decode(&v) == nil {
			visit(v, "")
			read++
		}
		fh.Close()
	}
	if read < 1000 {
		t.Fatalf("read %d files of specifications and vectors", read)
	}
	out := make([]patternCase, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].src+"/"+out[i].flags < out[j].src+"/"+out[j].flags })
	return out
}

// handPatterns are the shapes the bound follows, one by one: quantifier
// products, alternations, classes, lookarounds, backreferences, groups
// reset per iteration, empty-matching bodies, modifiers, every flag, v-mode
// class sets with strings, Annex B forms, and the errors on the way.
var handPatterns = []patternCase{
	{"(a{1000}){190}", ""}, {"(a{1000}){150}", ""}, {"(a{1000}){200}", ""}, {"(a{1000}){201}", ""},
	{"(?:a{1000}){199}x{997}", ""}, {"(?:a{1000}){199}x{996}", ""}, {"(?:a{1000}){199}x{998}", ""},
	{"((a{100}){100}){100}", ""}, {"(x{100}){100}", ""}, {"a{0}", ""}, {"(a){0}b{3}", ""}, {"(?:(a){0}b){3}", ""},
	{"a{10001}", ""}, {"(?:a{0,10001}){0}", ""}, {"a{2,10001}", ""}, {"(a{1000}){190}\\p{Bogus}", "u"},
	{"a*", ""}, {"a+", ""}, {"a?", ""}, {"a*?", ""}, {"a+?", ""}, {"a??", ""}, {"a{2,}", ""}, {"a{2,5}", ""}, {"a{0,5}?", ""},
	{"(a*)*", ""}, {"(a*)+", ""}, {"(a|)*", ""}, {"(?:a?b?){3,7}", ""}, {"(a)(b)\\2{4}", ""}, {"(?:(a)|b)+", ""},
	{"a|b|c", ""}, {"(a|bc|def){3}", ""}, {"(?:foo|bar|baz){20}", ""}, {"|", ""}, {"", ""}, {"(?:)", ""},
	{"[a-z]{3,50}x(\\d+)", ""}, {"[^abc]{4}", "i"}, {"[\\w\\d\\s-]+", ""}, {"[\\u0100-\\uffff]{100}", ""},
	{"[abcdefghijklmnopqrstuvwxyz0123456789]{1000}", ""}, {"\\p{L}{50}", "u"}, {"[\\p{L}\\P{Lu}]{5}", "u"}, {"\\P{Lu}{5}", "v"},
	{"(?=a)b", ""}, {"(?!a{3})b", ""}, {"(?<=ab)c", ""}, {"(?<!a(b|c){3})d", ""}, {"(?<=(?=a)b{2,3})c{2}", ""},
	{"((?<=a(b)c)d){5}", ""}, {"(?<=\\1(a))b", ""}, {"(?<n>a)\\k<n>{3}", ""}, {"(?:(?<n>a)|(?<n>b))\\k<n>", ""},
	{"\\bfoo\\B{0}", ""}, {"^a$", "m"}, {"^.{3}$", "s"}, {"(?i:a{3})(?-i:b)", ""}, {"(?m:^a)", ""},
	{"\\8\\9", ""}, {"\\1", ""}, {"\\12(a)", ""}, {"\\c", ""}, {"\\cJ", ""}, {"a{", ""}, {"a{1", ""}, {"a{1,", ""}, {"}", ""}, {"]", ""},
	{"\\0", ""}, {"\\07", ""}, {"[\\b]", ""}, {"\\u{1F600}{3}", "u"}, {"😀{3}", ""}, {"😀{3}", "u"}, {"[😀]", ""},
	{"[\\q{abc|d|}]{3}", "v"}, {"[\\q{ab|abc|abd}x]", "v"}, {"[[a-z]--[aeiou]]{4}", "v"}, {"[\\p{L}&&\\p{Lu}]", "v"},
	{"[\\q{abc}--\\q{abc}]", "v"}, {"(?<=[\\q{abc|de}])x", "v"}, {"\\p{RGI_Emoji}", "v"}, {"\\p{RGI_Emoji}{3}", "v"},
	{"[\\p{RGI_Emoji}--\\q{😀}]", "v"}, {"(?<=\\p{RGI_Emoji_Flag_Sequence}{2})x", "v"}, {"[\\q{ab}]{1000}", "v"}, {"[\\q{ab|abc|abcd|abcde|ab1|ab12|ab123|b|}]{20}", "v"}, {"[\\q{aa|ab|ba|bb|aaa|aab|aba|abb}x]{20}", "iv"},
	{"a", "gimsuyd"}, {"a", "uv"}, {"a", "x"}, {"a", "gg"}, {"(", ""}, {"a**", ""}, {"(?<a>x)(?<a>y)", ""}, {"\\p{L}", ""},
	{strings.Repeat("(", 199) + "a" + strings.Repeat(")", 199), ""}, {strings.Repeat("(?:", 120) + "a" + strings.Repeat(")*", 120), ""},
	{strings.Repeat("(?:a|", 100) + "b" + strings.Repeat(")", 100), ""}, {strings.Repeat("(", 201) + strings.Repeat(")", 201), ""},
}

// generatedPatterns are random compositions of the same shapes.
func generatedPatterns(n int) []patternCase {
	r := rand.New(rand.NewPCG(1, 2))
	atoms := []string{"a", "b", ".", "\\d", "\\W", "\\s", "[a-z]", "[^xy0-9]", "[\\w-]", "\\b", "^", "$", "\\1", "x"}
	var gen func(d int) string
	gen = func(d int) string {
		if d == 0 || r.IntN(3) == 0 {
			return atoms[r.IntN(len(atoms))]
		}
		switch r.IntN(10) {
		case 0, 1:
			return gen(d-1) + gen(d-1)
		case 2:
			return gen(d-1) + "|" + gen(d-1)
		case 3:
			return "(" + gen(d-1) + ")"
		case 4:
			return "(?:" + gen(d-1) + ")"
		case 5:
			return [...]string{"(?=", "(?!", "(?<=", "(?<!", "(?i:", "(?<g" + fmt.Sprint(r.IntN(1000)) + ">"}[r.IntN(6)] + gen(d-1) + ")"
		default:
			q := [...]string{"*", "+", "?", "{0}", "{1}", "{3}", "{2,}", "{0,4}", "{2,5}", "{7}"}[r.IntN(10)]
			if r.IntN(4) == 0 {
				q += "?"
			}
			return "(?:" + gen(d-1) + ")" + q
		}
	}
	fl := []string{"", "", "i", "u", "m", "s", "gi", "v"}
	out := make([]patternCase, n)
	for i := range out {
		out[i] = patternCase{gen(1 + r.IntN(5)), fl[r.IntN(len(fl))]}
	}
	return out
}

// The bound is at or above every program goecma262 builds, exact outside
// v-mode string classes, and refuses for the cap exactly the patterns
// goecma262 refuses for it, with its error.
func TestPatternCost(t *testing.T) {
	corpus := corpusPatterns(t)
	n := 20000
	if testing.Short() {
		n = 4000
	}
	sets := []struct {
		name  string
		cases []patternCase
	}{{"hand", handPatterns}, {"corpus", corpus}, {"generated", generatedPatterns(n)}}
	for _, set := range sets {
		lo, hi, compiled := 0.0, 0.0, 0
		for _, c := range set.cases {
			if r := checkCost(t, c); r > 0 {
				if compiled == 0 || r < lo {
					lo = r
				}
				hi = max(hi, r)
				compiled++
			}
		}
		t.Logf("%s: %d patterns, %d compile; bound/program %.3f to %.3f", set.name, len(set.cases), compiled, lo, hi)
	}
	// v-mode classes with strings are the one inexact bound: within a small
	// factor of the trie goecma262 builds.
	for _, c := range []patternCase{{"\\p{RGI_Emoji}", "v"}, {"[\\q{abc|d|}]{3}", "v"}, {"(?<=\\p{RGI_Emoji_Flag_Sequence}{2})x", "v"}} {
		r := checkCost(t, c)
		t.Logf("/%s/%s: bound %.2f times the program", c.src, c.flags, r)
		if r > 3 {
			t.Errorf("/%s/%s: bound %.2f times the program", c.src, c.flags, r)
		}
	}
}

func FuzzPatternCost(f *testing.F) {
	for _, c := range handPatterns {
		f.Add(c.src, c.flags)
	}
	f.Fuzz(func(t *testing.T, src, flags string) {
		if len(src) > 64 || strings.Contains(src, "{") && strings.Count(src, "{") > 3 {
			return // keeps a fuzz input's program small
		}
		checkCost(t, patternCase{src, flags})
		checkQuick(t, patternCase{src, flags})
	})
}

// What a compiled pattern holds on the heap is within what it is charged,
// and not wildly less: the caches keeping patterns are bounded by it.
func TestPatternBytes(t *testing.T) {
	for _, c := range []patternCase{
		{"(a{1000}){190}", ""}, {"[a-z]{3,50}x(\\d+)", ""}, {"(?:foo|bar|baz){20}", ""}, {"\\p{L}{50}", "u"},
		{"[\\u0100-\\uffff]{100}", ""}, {"(x{100}){100}", ""}, {"a{129}", ""}, {"a{257}", ""}, {"(a|b){5000}", ""},
		{"([abcdefghijklmnopqrstuvwxyz0123456789]{1000}){19}", ""},
		{"([^abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ]{1000}){10}", ""},
		{"(?<n>a)\\k<n>{300}", ""}, {"(?:(?<=[a-f]{20})x){50}", ""}, {"[\\q{abc|d|}]{300}", "v"}, {"\\p{RGI_Emoji}{3}", "v"},
		{"(?:(?<=\\p{RGI_Emoji_Flag_Sequence})x){20}", "v"}, {"[[a-z]--[aeiou]]{400}", "v"}, {"\\P{Lu}{400}", "v"},
	} {
		// The bound NewPattern charges: from the text when it gives one.
		cost, ok := quickCost(c.src, c.flags)
		if !ok {
			var err error
			if cost, err = EstimatePattern(c.src, c.flags); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		p, err := NewPattern(c.src, c.flags, func(int64, int64) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&b)
		held := int64(b.HeapAlloc) - int64(a.HeapAlloc)
		runtime.KeepAlive(p)
		if p.Bytes() < held || cost.Bytes < p.Bytes() {
			t.Errorf("/%s/%s: bound %d, charged %d bytes, the heap holds %d", c.src, c.flags, cost.Bytes, p.Bytes(), held)
		}
		if p.Bytes() > 4*held+64<<10 && !strings.Contains(c.flags, "v") {
			t.Errorf("/%s/%s: charged %d bytes, the heap holds only %d", c.src, c.flags, p.Bytes(), held)
		}
		t.Logf("/%s/%s: bound %d, charged %d, held %d (%.2f)", c.src, c.flags, cost.Bytes, p.Bytes(), held, float64(p.Bytes())/float64(max(held, 1)))
	}
	if s := (&Pattern{}).Bytes(); s < ecmacompiler.MaxProgramSize*instructionBytes {
		t.Errorf("a pattern not made by NewPattern must count as the largest: %d", s)
	}
}

// allocated is the bytes f allocates.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// A pattern is paid for before it is compiled: one past what the ledger has
// left is refused having cost its parse, not its program, and so is one past
// goecma262's program cap. Compiling (a{1000}){150} allocates about 30 MB,
// parsing it a few kilobytes; the test counts the bytes allocated, which do
// not depend on the machine or the collector.
func TestPatternPaidBeforeCompile(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	const src = "(a{1000}){150}"
	cost, err := EstimatePattern(src, "")
	if err != nil {
		t.Fatal(err)
	}
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: cost.Bytes + cost.Bytes/2})
	pay := func(bytes, work int64) error {
		if bytes < 0 {
			led.Free(-bytes)
			return nil
		}
		return led.Alloc(bytes)
	}
	var first, second *Pattern
	var err1, err2 error
	full := allocated(func() { first, err1 = NewPattern(src+"x", "", pay) })
	refused := allocated(func() { second, err2 = NewPattern(src+"y", "", pay) })
	if err1 != nil || first == nil {
		t.Fatalf("the first pattern fits the ledger: %v", err1)
	}
	if !errors.Is(err2, budget.ErrLimit) || second != nil {
		t.Fatalf("the second pattern is past the ledger: %v", err2)
	}
	if full < 16<<20 || refused > 1<<20 {
		t.Errorf("a compile allocates %d bytes, a refused one %d: the refusal must come before the compile", full, refused)
	}
	if got := led.Bytes(); got != first.Bytes() {
		t.Errorf("the ledger holds %d bytes for a pattern of %d", got, first.Bytes())
	}
	// Past the program cap: goecma262's error, without building the
	// program, and without paying.
	paid := false
	tooLarge := allocated(func() {
		_, err = NewPattern("(a{1000}){210}", "", func(int64, int64) error { paid = true; return nil })
	})
	if _, want := ecma262.CompileFlags("(a{1000}){210}", ""); err == nil || paid || want == nil || err.Error() != patternError("(a{1000}){210}", "", want).Error() {
		t.Errorf("past the cap: %v (paid %v), goecma262: %v", err, paid, want)
	}
	if tooLarge > 1<<20 {
		t.Errorf("refusing a pattern past the cap allocates %d bytes", tooLarge)
	}
}

// programCap bounds the capacity append gives a program of every length up
// to goecma262's cap and a little past it.
func TestProgramCap(t *testing.T) {
	var code []vm.Instruction
	for n := int64(1); n <= ecmacompiler.MaxProgramSize+1000; n++ {
		code = append(code, vm.Instruction{})
		if c := int64(cap(code)); c > programCap(n) {
			t.Fatalf("a program of %d instructions has capacity %d, past the bound's %d", n, c, programCap(n))
		}
	}
}

// checkQuick checks quickCost's bound of c, when it gives one, against the
// exact bound of the parsed pattern.
func checkQuick(t testing.TB, c patternCase) (float64, bool) {
	t.Helper()
	q, ok := quickCost(c.src, c.flags)
	if !ok {
		return 0, false
	}
	e, err := EstimatePattern(c.src, c.flags)
	if err != nil || e.TooLarge || e.fails {
		return 0, true // goecma262 refuses it whatever it is charged
	}
	if q.Instructions < e.Instructions || q.Bytes < e.Bytes || q.Work < e.Work {
		t.Errorf("/%s/%s: quick bound %d instructions, %d bytes under the exact %d, %d", c.src, c.flags, q.Instructions, q.Bytes, e.Instructions, e.Bytes)
	}
	return float64(q.Instructions) / float64(e.Instructions), true
}

// The bound from a pattern's text is at or above the exact one.
func TestQuickCost(t *testing.T) {
	cases := append(append(append([]patternCase{}, handPatterns...), corpusPatterns(t)...), generatedPatterns(20000)...)
	for _, src := range []string{"(?:a{1000}){1000}", "(a{1025})", "a{1024}", "(((((a{3}){3}){3}){3}){3})", "\\u{12}{3}", "\\p{L}{50}", "[\\]a]{9}", "[]]{9}", "a{,5}", "a{2,1}{3}", "(?<n>a)\\k<n>{30}", "😀{3}"} {
		for _, fl := range []string{"", "u", "i"} {
			cases = append(cases, patternCase{src, fl})
		}
	}
	quick, hi := 0, 0.0
	for _, c := range cases {
		if r, ok := checkQuick(t, c); ok {
			quick++
			hi = max(hi, r)
		}
	}
	t.Logf("%d of %d patterns bounded from their text, at most %.1f times the exact bound", quick, len(cases), hi)
	if quick < len(cases)/2 {
		t.Errorf("only %d of %d patterns bounded from their text", quick, len(cases))
	}
}
