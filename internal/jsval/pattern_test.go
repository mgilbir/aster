package jsval

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/testutil"
	ecma262 "github.com/mgilbir/goecma262"
	ecmacompiler "github.com/mgilbir/goecma262/compiler"
	ecmaparser "github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// patternCase is a source and flags.
type patternCase struct{ src, flags string }

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

// handPatterns are the shapes goecma262's bounds follow, one by one: quantifier
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

// payLog pays for patterns with a ledger, recording what NewPattern does
// with it: the bytes paid and not given back, the most at once, the
// charges, and whether a charge came while nothing was built yet.
type payLog struct {
	led                   *budget.Ledger
	paid, peak            int64
	charges, refuseFirstN int
}

func (l *payLog) pay(bytes int64) error {
	if bytes > 0 {
		l.charges++
		if l.charges <= l.refuseFirstN {
			return fmt.Errorf("refused for the test: %w", budget.ErrLimit)
		}
		if err := l.led.Alloc(bytes); err != nil {
			return err
		}
	} else {
		l.led.Free(-bytes)
	}
	l.paid += bytes
	l.peak = max(l.peak, l.paid)
	return nil
}

func unpaid(int64) error { return nil }

// checkPattern compiles c with NewPattern, paying with a ledger that
// refuses the first refuseFirst charges, and checks it against goecma262's
// own compile: the same error for a source that does not compile, limits
// told apart from invalid patterns, and otherwise the same program, charged
// exactly what it holds once built, after paying at least that before.
// It reports whether the pattern took the bound of its own parse (two
// charges or more).
func checkPattern(t testing.TB, c patternCase, refuseFirst int) (exact bool) {
	t.Helper()
	log := &payLog{led: budget.NewLedger(context.Background(), budget.LedgerLimits{}), refuseFirstN: refuseFirst}
	p, err := NewPattern(context.Background(), c.src, c.flags, log.pay)
	want, wantErr := ecma262.CompileFlags(c.src, c.flags)
	if log.paid != 0 && (err != nil || log.paid != p.Bytes()) || log.led.Bytes() != log.paid {
		t.Errorf("/%s/%s: paid %d, the ledger holds %d, for %v (err %v)", c.src, c.flags, log.paid, log.led.Bytes(), p, err)
	}
	if wantErr != nil {
		if err == nil || err.Error() != patternError(c.src, c.flags, wantErr).Error() {
			t.Errorf("/%s/%s: NewPattern's error %v, goecma262's %v", c.src, c.flags, err, wantErr)
		}
		if errors.Is(err, ecma262.ErrLimit) != errors.Is(wantErr, ecma262.ErrLimit) {
			t.Errorf("/%s/%s: %v is a limit %v, goecma262's %v", c.src, c.flags, err, errors.Is(err, ecma262.ErrLimit), wantErr)
		}
		return log.charges > 1
	}
	if err != nil {
		t.Errorf("/%s/%s: goecma262 compiles it, NewPattern fails: %v", c.src, c.flags, err)
		return false
	}
	if p.Re.ProgramSize() != want.ProgramSize() || p.Re.NumSubexp() != want.NumSubexp() {
		t.Errorf("/%s/%s: a program of %d instructions, goecma262's %d", c.src, c.flags, p.Re.ProgramSize(), want.ProgramSize())
	}
	if p.Bytes() != want.MemoryFootprint()+int64(patternSize) || log.peak < p.Bytes() {
		t.Errorf("/%s/%s: charged %d bytes (%d at the peak) for a pattern of %d", c.src, c.flags, p.Bytes(), log.peak, want.MemoryFootprint())
	}
	return log.charges-refuseFirst > 1
}

// NewPattern compiles what goecma262 compiles and fails as it fails, on
// either of its paths (refusing the first charge forces the exact bound),
// and pays exactly what each pattern holds. A slow-path compile refused by
// the budget it paid for would fail here: the bound of the Regexp's own
// bytes (regexpBound) is under what it holds.
func TestNewPatternMatchesGoecma262(t *testing.T) {
	n := 20000
	if testing.Short() {
		n = 4000
	}
	sets := []struct {
		name  string
		cases []patternCase
	}{{"hand", handPatterns}, {"corpus", corpusPatterns(t)}, {"generated", generatedPatterns(n)}}
	for _, set := range sets {
		exact := 0
		for _, c := range set.cases {
			if checkPattern(t, c, 0) {
				exact++
			}
			checkPattern(t, c, 1)
		}
		t.Logf("%s: %d patterns, %d bounded exactly past the first allowance", set.name, len(set.cases), exact)
	}
}

func FuzzNewPattern(f *testing.F) {
	for _, c := range handPatterns {
		f.Add(c.src, c.flags)
	}
	f.Fuzz(func(t *testing.T, src, flags string) {
		if len(src) > 64 || strings.Count(src, "{") > 3 {
			return // keeps a fuzz input's program small
		}
		checkPattern(t, patternCase{src, flags}, 0)
		checkPattern(t, patternCase{src, flags}, 1)
	})
}

// What a compiled pattern holds on the heap is what it is charged, which
// bounds the caches that keep patterns: goecma262's MemoryFootprint is
// within a few percent above the heap it retains. Each pattern is compiled
// once first, so that tables built once per process (Unicode properties)
// are not counted. The heap is measured over k copies held at once (k as
// many as fit in 16 MiB, up to 32): what the runtime allocates around a
// measurement (a few KB on darwin/arm64, where one copy was measured 5 KB
// over its charge by a different pattern each run) is the same whatever k,
// while an undercharge grows with it, so a small one is seen too.
func TestPatternBytes(t *testing.T) {
	const noise = 8 << 10
	for _, c := range []patternCase{
		{"(a{1000}){190}", ""}, {"[a-z]{3,50}x(\\d+)", ""}, {"(?:foo|bar|baz){20}", ""}, {"\\p{L}{50}", "u"},
		{"[\\u0100-\\uffff]{100}", ""}, {"(x{100}){100}", ""}, {"a{129}", ""}, {"a{257}", ""}, {"(a|b){5000}", ""},
		{"([abcdefghijklmnopqrstuvwxyz0123456789]{1000}){19}", ""},
		{"([^abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ]{1000}){10}", ""},
		{"(?<n>a)\\k<n>{300}", ""}, {"(?:(?<=[a-f]{20})x){50}", ""}, {"[\\q{abc|d|}]{300}", "v"}, {"\\p{RGI_Emoji}{3}", "v"},
		{"(?:(?<=\\p{RGI_Emoji_Flag_Sequence})x){20}", "v"}, {"[[a-z]--[aeiou]]{400}", "v"}, {"\\P{Lu}{400}", "v"},
	} {
		first, err := NewPattern(context.Background(), c.src, c.flags, unpaid)
		if err != nil {
			t.Fatal(err)
		}
		k := int(min(max((16<<20)/max(first.Bytes(), 1), 1), 32))
		ps := make([]*Pattern, k)
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		for i := range ps {
			if ps[i], err = NewPattern(context.Background(), c.src, c.flags, unpaid); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&b)
		held := int64(b.HeapAlloc) - int64(a.HeapAlloc)
		var charged int64
		for _, p := range ps {
			charged += p.Bytes()
		}
		runtime.KeepAlive(ps)
		if charged < held-noise || charged > held+held/8+int64(k)*(16<<10) {
			t.Errorf("/%s/%s: %d copies charged %d bytes, the heap holds %d", c.src, c.flags, k, charged, held)
		}
		t.Logf("/%s/%s: %d copies charged %d, held %d (%.3f)", c.src, c.flags, k, charged, held, float64(charged)/float64(max(held, 1)))
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

// A pattern is paid for before its parse and its compile allocate. One whose
// parse bound is past what the ledger has left is refused without being
// parsed (a 512 KiB source parses in about 23 MB); one whose compiled form
// is past it is refused having cost a parse, not its program (compiling
// (a{1000}){150} allocates about 24 MB, parsing it a few kilobytes); one
// past goecma262's program cap is refused unpaid, with goecma262's error.
// The test counts the bytes allocated, which do not depend on the machine
// or the collector.
func TestPatternPaidBeforeBuilt(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	ctx := context.Background()
	t.Run("parse", func(t *testing.T) {
		src := strings.Repeat("a", 512<<10)
		led := budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: ecmaparser.AllocBound(len(src)) - 1})
		log := &payLog{led: led}
		var err error
		n := allocated(func() { _, err = NewPattern(ctx, src, "", log.pay) })
		if !errors.Is(err, budget.ErrLimit) || n > 1<<20 || led.Bytes() != 0 {
			t.Errorf("a parse past the ledger: %v, %d bytes allocated, %d held", err, n, led.Bytes())
		}
	})
	t.Run("compile", func(t *testing.T) {
		const src = "(a{1000}){150}"
		ref, err := NewPattern(ctx, src+"w", "", unpaid)
		if err != nil {
			t.Fatal(err)
		}
		led := budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: ref.Bytes() + ref.Bytes()/2})
		log := &payLog{led: led}
		var first, second *Pattern
		var err1, err2 error
		full := allocated(func() { first, err1 = NewPattern(ctx, src+"x", "", log.pay) })
		refused := allocated(func() { second, err2 = NewPattern(ctx, src+"y", "", log.pay) })
		if err1 != nil || first == nil {
			t.Fatalf("the first pattern fits the ledger: %v", err1)
		}
		if !errors.Is(err2, budget.ErrLimit) || second != nil {
			t.Fatalf("the second pattern is past the ledger: %v", err2)
		}
		if full < 8<<20 || refused > 1<<20 { // 25 MB on 64-bit platforms, 13 MB on 32-bit ones
			t.Errorf("a compile allocates %d bytes, a refused one %d: the refusal must come before the compile", full, refused)
		}
		if got := led.Bytes(); got != first.Bytes() {
			t.Errorf("the ledger holds %d bytes for a pattern of %d", got, first.Bytes())
		}
	})
	t.Run("program-cap", func(t *testing.T) {
		const src = "(a{1000}){210}"
		paid := false
		var err error
		n := allocated(func() {
			_, err = NewPattern(ctx, src, "", func(b int64) error { paid = paid || b > 1<<20; return nil })
		})
		_, want := ecma262.CompileFlags(src, "")
		if err == nil || paid || want == nil || err.Error() != patternError(src, "", want).Error() || !errors.Is(err, ecma262.ErrProgramTooLarge) {
			t.Errorf("past the cap: %v (paid for its program %v), goecma262: %v", err, paid, want)
		}
		if n > 1<<20 {
			t.Errorf("refusing a pattern past the cap allocates %d bytes", n)
		}
	})
}

// pollsLeft is a context that is done from its n-th poll on: a deadline
// reached in the middle of a parse, a compile or a search, without a clock.
type pollsLeft struct {
	context.Context
	n    atomic.Int64
	done chan struct{}
}

func newPollsLeft(n int64) *pollsLeft {
	c := &pollsLeft{Context: context.Background(), done: make(chan struct{})}
	c.n.Store(n)
	return c
}

func (c *pollsLeft) Done() <-chan struct{} { return c.done }

func (c *pollsLeft) Err() error {
	if c.n.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// The context stops a parse and a compile under way (goecma262 polls it as
// it works, as does NewPattern's own parse for the exact bound): NewPattern
// returns the context's error, not a pattern, gives back what it paid, and
// has allocated little of what the whole compile would. The polls are
// counted, not timed: each case is stopped a few polls in, of hundreds.
func TestNewPatternStopsWithContext(t *testing.T) {
	for _, c := range []struct {
		name, src string
		// refuse is how many of NewPattern's charges are refused first:
		// one sends it to the exact bound, and its own parse.
		refuse int
		polls  int64
		// maxAlloc bounds what it allocates before it stops (0: not
		// checked; a compile allocates its program at its final size, paid
		// for, before it starts).
		maxAlloc uint64
	}{
		// 443 polls in all, and 25 MB allocated, without the context.
		{"compile", "(a{1000}){150}", 0, 8, 0},
		// 1,155 polls and 58 MB; 1,571 polls and 67 MB on the exact path.
		{"parse", strings.Repeat("(?:ab|cd)", 32<<10), 0, 4, 4 << 20},
		{"parse-exact", strings.Repeat("(?:ab|cd)", 32<<10), 1, 4, 4 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewPattern(context.Background(), c.src, "", unpaid); err != nil {
				t.Fatalf("compiles without the context: %v", err)
			}
			ctx := newPollsLeft(c.polls)
			log := &payLog{led: budget.NewLedger(context.Background(), budget.LedgerLimits{}), refuseFirstN: c.refuse}
			var p *Pattern
			var err error
			n := allocated(func() { p, err = NewPattern(ctx, c.src, "", log.pay) })
			if !errors.Is(err, context.Canceled) || p != nil || log.paid != 0 {
				t.Errorf("stopped: compiled %v, %v, %d bytes still paid", p != nil, err, log.paid)
			}
			if strings.Contains(fmt.Sprint(err), "invalid regular expression") {
				t.Errorf("the deadline reads as an invalid pattern: %v", err)
			}
			if !testutil.RaceEnabled && c.maxAlloc > 0 && n > c.maxAlloc {
				t.Errorf("stopped having allocated %d bytes", n)
			}
		})
	}
}

// On the exact path, a context that ends while the parsed pattern is being
// bounded stops the walk (compiler.BoundContext polls it), so nothing is
// charged for a program, rather than the whole AST being walked and its
// program paid for before the compile notices. Counted, from goecma262's own
// parser and bound run apart: NewPattern polls once on entry, then its parse
// polls as the parser alone does, then the walk; the run is stopped at the
// walk's first poll.
func TestNewPatternStopsWhileBounding(t *testing.T) {
	src := strings.Repeat("(?:ab|cd)", 32<<10)
	const many = 1 << 40
	parse := newPollsLeft(many)
	ast, err := ecmaparser.New(src, ecmaparser.Flags{}).ParseContext(parse)
	if err != nil {
		t.Fatal(err)
	}
	parsePolls := many - parse.n.Load()
	walk := newPollsLeft(many)
	if _, err := ecmacompiler.BoundContext(walk, ast); err != nil {
		t.Fatal(err)
	}
	walkPolls := many - walk.n.Load()
	if walkPolls < 4 {
		t.Fatalf("the walk polls %d times: nothing to stop", walkPolls)
	}
	// An unstopped run on the exact path (its first charge, the allowance,
	// refused) must poll the walk between its parse and paying for the
	// program, its third charge.
	ctx := newPollsLeft(many)
	var atProgram int64
	log := &payLog{led: budget.NewLedger(context.Background(), budget.LedgerLimits{}), refuseFirstN: 1}
	if _, err := NewPattern(ctx, src, "", func(n int64) error {
		if n > 0 && log.charges == 2 {
			atProgram = many - ctx.n.Load()
		}
		return log.pay(n)
	}); err != nil {
		t.Fatal(err)
	}
	if want := 1 + parsePolls + walkPolls; atProgram != want {
		t.Fatalf("%d polls before the program is paid for, want %d (1 + %d parsing + %d bounding)", atProgram, want, parsePolls, walkPolls)
	}
	stopped := newPollsLeft(1 + parsePolls)
	log = &payLog{led: budget.NewLedger(context.Background(), budget.LedgerLimits{}), refuseFirstN: 1}
	p, err := NewPattern(stopped, src, "", log.pay)
	if !errors.Is(err, context.Canceled) || p != nil || log.paid != 0 {
		t.Fatalf("stopped while bounding: compiled %v, %v, %d bytes still paid", p != nil, err, log.paid)
	}
	if log.charges != 2 {
		t.Errorf("stopped while bounding, it made %d charges: the program was paid for after the context ended", log.charges)
	}
}

// MatchMemory is goecma262's default unless the ledger has less room left,
// and never the 0 that would restore the default when the ledger is full.
func TestMatchMemory(t *testing.T) {
	dflt := int64(vm.DefaultMaxMemory + vm.DefaultMemoryPerByte*1000)
	for _, c := range []struct {
		room int64
		want int
	}{{math.MaxInt64, 0}, {dflt, 0}, {dflt - 1, int(dflt - 1)}, {64 << 10, 64 << 10}, {1, 1}, {0, 1}, {-5, 1}} {
		if got := MatchMemory(c.room, 1000); got != c.want {
			t.Errorf("MatchMemory(%d, 1000) = %d, want %d", c.room, got, c.want)
		}
	}
}

// patternSize is what a Pattern holds besides its Regexp.
var patternSize = func() int64 { return (&Pattern{}).Bytes() }()

// PatternFlags costs nothing the flags' length sizes: valid flags allocate
// nothing, and flags a mebibyte long, which RegExp(source, flags) may be
// given, allocate no more than invalid flags of a few bytes (the error).
func TestPatternFlagsCostIsNotSizedByTheFlags(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("the race detector's allocations")
	}
	if n := testing.AllocsPerRun(10, func() { PatternFlags("a", "dgimsy") }); n != 0 {
		t.Errorf("valid flags: %v allocations", n)
	}
	allocated := func(f string) uint64 {
		var a, b runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&a)
		for range 10 {
			if _, err := PatternFlags("a", f); err == nil {
				t.Fatalf("flags %.20q accepted", f)
			}
		}
		runtime.ReadMemStats(&b)
		return (b.TotalAlloc - a.TotalAlloc) / 10
	}
	short := allocated("gg")
	for _, f := range []string{strings.Repeat("x", 1<<20), strings.Repeat("g", 1<<20), "dgimsy" + strings.Repeat("é", 1<<19)} {
		if long := allocated(f); long > short+1024 {
			t.Errorf("flags %.20q of %d bytes: %d bytes allocated, against %d for \"gg\"", f, len(f), long, short)
		}
	}
}

// A pattern with several capture group names used twice on one match path is
// refused with one message however often it is compiled: goecma262 names the
// first duplicate it finds by ranging over a map, so NewPattern leaves the
// name out, as V8 does ("Duplicate capture group name"). A name reused only
// in different alternatives is allowed, as in node 24 (ES2025).
func TestNewPatternDuplicateGroupNames(t *testing.T) {
	const want = "invalid regular expression /%s/: parse error: duplicate group name"
	for _, c := range []struct {
		src string
		bad bool
	}{
		{"(?<A0>)(?<A>)(((?<A0>)(?<A>))0)", true},
		{"(?<a>)(?<b>)(?<a>)(?<b>)", true},
		{"(?<a>)(?<b>)(?<c>)(?<a>)(?<b>)(?<c>)", true},
		{"(?<a>(?<b>)(?<c>))(?<c>)(?<b>)(?<a>)", true},
		{"(?:(?<a>)(?<b>))(?:(?<b>)(?<a>))", true},
		{"(?<a>(?<a>))", true},
		{"(?=(?<a>)(?<b>))(?<b>)(?<a>)", true},
		{"(?<a>)(?<b>)(?:(?<a>)|(?<b>))", true},
		{"(?<a>x)|(?<a>y)", false},
		{"(?:(?<a>x)|(?<a>y))(?<b>)|(?<b>)(?:(?<a>)|(?<a>))", false},
		{"(?:(?<a>x)|(?<a>y))(?<a>z)", true},
	} {
		first := ""
		for i := 0; i < 50; i++ {
			p, err := NewPattern(context.Background(), c.src, "", func(int64) error { return nil })
			if !c.bad {
				if err != nil || p == nil {
					t.Fatalf("/%s/: %v, node 24 accepts it", c.src, err)
				}
				break
			}
			if err == nil {
				t.Fatalf("/%s/: accepted, node 24 refuses it", c.src)
			}
			if i == 0 {
				first = err.Error()
				if w := fmt.Sprintf(want, c.src); first != w {
					t.Fatalf("/%s/: error %q, want %q", c.src, first, w)
				}
			} else if err.Error() != first {
				t.Fatalf("/%s/: error %q, then %q", c.src, first, err)
			}
		}
	}
}
