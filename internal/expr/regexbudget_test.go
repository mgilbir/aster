package expr

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// allocated is the bytes f allocates: deterministic, unlike the heap a
// collector has not yet swept.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// bigPattern compiles to 150,452 instructions: 24 MB held on 64-bit
// platforms (13 MB on 32-bit ones), against a few kilobytes to parse it.
const bigPattern = "(a{1000}){150}"

// compiled is what a compile of bigPattern allocates at least, on any
// platform; refusedMax what a refusal may allocate at most.
const (
	compiled   = 8 << 20
	refusedMax = 1 << 20
)

// ledgerForOne is a ledger with room for one program of bigPattern, not two.
func ledgerForOne(t *testing.T, ctx context.Context) *budget.Ledger {
	t.Helper()
	p, err := jsval.NewPattern(context.Background(), bigPattern+"x", "", unpaid)
	if err != nil {
		t.Fatal(err)
	}
	return budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: p.Bytes() + p.Bytes()/2})
}

// Every place that compiles a pattern pays for it first: a program past what
// the call's ledger has left is refused with ErrLimit having cost a parse,
// not a compile (C8, #115: regexp() over data, and literals, compiled a
// second program of tens of megabytes before refusing it). A search from
// lastIndex runs the compiled program, and compiles nothing again. The test
// counts the bytes allocated, so it holds on any machine; the race detector
// allocates on its own, so it is skipped there.
func TestPatternRefusedBeforeCompile(t *testing.T) {
	if testutil.RaceSlowdown > 1 {
		t.Skip("the race detector allocates on its own")
	}
	t.Run("regexp", func(t *testing.T) {
		led := ledgerForOne(t, context.Background())
		eval := func(src string) error {
			p, err := Compile("test('" + src + "', 'a')")
			if err != nil {
				t.Fatal(err)
			}
			s := NewScope(nil)
			s.Context = budget.WithLedger(context.Background(), led)
			_, err = p.Eval(s)
			return err
		}
		var err1, err2 error
		first := allocated(func() { err1 = eval(bigPattern + "x") })
		second := allocated(func() { err2 = eval(bigPattern + "y") })
		if err1 != nil || !errors.Is(err2, budget.ErrLimit) {
			t.Fatalf("first: %v; second: %v, want ErrLimit", err1, err2)
		}
		if first < compiled || second > refusedMax {
			t.Errorf("a compile allocates %d bytes, a refused one %d", first, second)
		}
		// The refusal depends on the budget, not the pattern: with room, it
		// compiles.
		if _, ok := patternErrors.Get("/" + bigPattern + "y"); ok {
			t.Error("a budget refusal was remembered as the pattern's error")
		}
	})
	t.Run("literal", func(t *testing.T) {
		led := ledgerForOne(t, context.Background())
		ctx := budget.WithLedger(context.Background(), led)
		var err1, err2 error
		first := allocated(func() { _, err1 = CompileContext(ctx, "test(/"+bigPattern+"x/, 'a')") })
		second := allocated(func() { _, err2 = CompileContext(ctx, "test(/"+bigPattern+"y/, 'a')") })
		if err1 != nil || !errors.Is(err2, budget.ErrLimit) {
			t.Fatalf("first: %v; second: %v, want ErrLimit", err1, err2)
		}
		if first < compiled || second > refusedMax {
			t.Errorf("a compile allocates %d bytes, a refused one %d", first, second)
		}
	})
	t.Run("program-cap", func(t *testing.T) {
		p, err := Compile("test('(a{1000}){210}', 'a')")
		if err != nil {
			t.Fatal(err)
		}
		var got error
		n := allocated(func() { _, got = p.Eval(NewScope(nil)) })
		if !errors.Is(got, budget.ErrLimit) || n > refusedMax {
			t.Errorf("past goecma262's program cap: %v, %d bytes allocated", got, n)
		}
	})
	t.Run("search-from-lastIndex", func(t *testing.T) {
		// test() of a g pattern past a match searches from lastIndex with
		// the program it has (C97 compiled a second one for it).
		p, err := jsval.NewPattern(context.Background(), bigPattern+"z", "g", unpaid)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := Compile("test(re, 'zz')")
		if err != nil {
			t.Fatal(err)
		}
		led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
		s := NewScope(&fakeEnv{signals: map[string]jsval.Value{"re": jsval.PatternValue(p)}})
		s.Context = budget.WithLedger(context.Background(), led)
		p.Re.SetLastIndex(1)
		var v jsval.Value
		n := allocated(func() { v, err = prog.Eval(s) })
		if err != nil || v.BoolValue() || p.Re.LastIndex() != 0 || n > refusedMax || led.PeakBytes() != 0 {
			t.Errorf("a search from lastIndex: %v, %v, lastIndex %d, %d bytes allocated, %d charged", v, err, p.Re.LastIndex(), n, led.PeakBytes())
		}
	})
	t.Run("deadline", func(t *testing.T) {
		// A call already over compiles nothing more.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ctx = budget.WithLedger(ctx, budget.NewLedger(ctx, budget.LedgerLimits{}))
		var err1, err2 error
		n := allocated(func() {
			_, err1 = CompileContext(ctx, "test(/"+bigPattern+"w/, 'a')")
			p, err := Compile("test('" + bigPattern + "v', 'a')")
			if err != nil {
				t.Fatal(err)
			}
			s := NewScope(nil)
			s.Context = ctx
			_, err2 = p.Eval(s)
		})
		if !errors.Is(err1, context.Canceled) || !errors.Is(err2, context.Canceled) || n > 2*refusedMax {
			t.Errorf("after the deadline: literal %v, regexp %v, %d bytes allocated", err1, err2, n)
		}
	})
}

// goecma262's limits are told from invalid patterns by its typed errors
// (ecma262.ErrLimit), not by words of the message, which quotes the source:
// a pattern that is invalid is a SyntaxError whatever it says, and one past
// a limit ends the render with ErrLimit, from a string or as a literal.
func TestPatternErrorClassification(t *testing.T) {
	prog, err := Compile("test(src, 'a')")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		src   string
		limit bool
	}{
		{"too deeply nested(", false}, {"exceeds limit[", false}, {"compiled program too large)", false}, {"a**", false},
		{"a{10001}", true}, {strings.Repeat("(", 201) + strings.Repeat(")", 201), true}, {"(a{1000}){201}", true},
	} {
		s := NewScope(&fakeEnv{signals: map[string]jsval.Value{"src": jsval.Str(c.src)}})
		_, err := prog.Eval(s)
		var je *Error
		if got := errors.Is(err, budget.ErrLimit); got != c.limit || !c.limit && (!errors.As(err, &je) || je.Name != "SyntaxError") {
			t.Errorf("regexp(%q): %v (a limit: %v)", c.src, err, got)
		}
		_, err = Compile("test(/" + c.src + "/, 'a')")
		var se *SyntaxError
		if !errors.As(err, &se) || se.Limit != c.limit {
			t.Errorf("/%s/: %v, want a limit %v", c.src, err, c.limit)
		}
	}
}

// Searches give JavaScript's answers where a match begins or ends between
// the two halves of a pair, which a WTF-8 string can hold: test() of a g
// pattern searching from such a lastIndex, replace and split (node 24 gave
// each answer).
func TestRegexpSurrogateHalves(t *testing.T) {
	for _, c := range []struct{ re, src, want string }{
		{`\uD83D`, `test(re, '😀') + ',' + test(re, '😀') + ',' + test(re, '😀')`, "true,false,true"},
		{`\uDE00`, `test(re, '😀') + ',' + test(re, '😀') + ',' + test(re, '😀')`, "true,false,true"},
		{`a|\uDE00`, `test(re, 'a😀a') + ',' + test(re, 'a😀a') + ',' + test(re, 'a😀a') + ',' + test(re, 'a😀a')`, "true,true,true,false"},
		{``, `replace('a😀b', /\uDE00/, 'X') === 'a\uD83DXb'`, "true"},
		{``, `join(split('😀😀', /(?:)/), '|') === '\uD83D|\uDE00|\uD83D|\uDE00'`, "true"},
		{``, `join(split('x😀y😀z', /\uD83D/), '|') === 'x|\uDE00y|\uDE00z'`, "true"},
		{``, `replace('😀😀', /(?:)/g, '-') === '-\uD83D-\uDE00-\uD83D-\uDE00-'`, "true"},
	} {
		env := &fakeEnv{signals: map[string]jsval.Value{}}
		if c.re != "" {
			re, err := jsval.NewPattern(context.Background(), c.re, "g", unpaid)
			if err != nil {
				t.Fatal(err)
			}
			env.signals["re"] = jsval.PatternValue(re)
		}
		prog, err := Compile(c.src)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(env)
		s.Context = budget.WithLedger(context.Background(), budget.NewLedger(context.Background(), budget.LedgerLimits{}))
		v, err := prog.Eval(s)
		if err != nil || v.AsString() != c.want {
			t.Errorf("%s with re = /%s/g: %v (%v), want %s", c.src, c.re, v, err, c.want)
		}
	}
}

// pollsLeft is a context that is done from its n-th poll on: a deadline
// reached in the middle of a search, without a clock.
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

// backtracker is a search that holds between 2 and 4 MiB for backtracking
// over a 100 KB input and takes over half a million steps, within
// goecma262's default budget for it (256 MiB, 11 million steps).
var backtracker = struct{ expr, input string }{"test(/^(a|b|ab)*$/, s)", strings.Repeat("ab", 50000)}

// A search stops at the call's deadline, which goecma262 polls as it
// matches, and the evaluation ends with the context's error: not a step
// limit, not a result. The literal is compiled before, without the context.
func TestRegexpSearchStopsWithContext(t *testing.T) {
	prog, err := Compile(backtracker.expr)
	if err != nil {
		t.Fatal(err)
	}
	env := &fakeEnv{signals: map[string]jsval.Value{"s": jsval.Str(backtracker.input)}}
	s := NewScope(env)
	if v, err := prog.Eval(s); err != nil || !v.BoolValue() {
		t.Fatalf("without a deadline: %v, %v", v, err)
	}
	// Done four polls in: the evaluation's own work polls once, then the
	// search every few thousand steps.
	s.Context = newPollsLeft(4)
	if v, err := prog.Eval(s); !errors.Is(err, context.Canceled) || errors.Is(err, budget.ErrLimit) {
		t.Errorf("past the deadline: %v, %v", v, err)
	}
}

// A search holds for backtracking at most what the call's ledger has left:
// the search that holds megabytes fails with ErrLimit when 64 KiB are left,
// and succeeds when the ledger has room.
func TestRegexpSearchMemoryBoundedByLedger(t *testing.T) {
	prog, err := Compile(backtracker.expr)
	if err != nil {
		t.Fatal(err)
	}
	env := &fakeEnv{signals: map[string]jsval.Value{"s": jsval.Str(backtracker.input)}}
	run := func(room int64) (jsval.Value, error) {
		led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 64 << 20})
		if err := led.Alloc(64<<20 - room); err != nil {
			t.Fatal(err)
		}
		s := NewScope(env)
		s.Context = budget.WithLedger(context.Background(), led)
		return prog.Eval(s)
	}
	if v, err := run(32 << 20); err != nil || !v.BoolValue() {
		t.Errorf("with room: %v, %v", v, err)
	}
	if v, err := run(64 << 10); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("with 64 KiB left: %v, %v", v, err)
	}
}

// compileUnder is s.compilePattern(src, "") under a ledger of max bytes: the
// pattern, or the limit it ended with.
func compileUnder(led *budget.Ledger) func(src string) (p *jsval.Pattern, err error) {
	return func(src string) (p *jsval.Pattern, err error) {
		defer func() {
			if r := recover(); r != nil {
				lp, ok := r.(*limitPanic)
				if !ok {
					panic(r)
				}
				err = lp.err
			}
		}()
		s := NewScope(nil)
		s.Context = budget.WithLedger(context.Background(), led)
		return s.compilePattern(src, ""), nil
	}
}

// The two copies a string pattern makes, its escaped source and the cache
// key, are paid for with the call's ledger before they are made (ADR 0010):
// a ledger without room for a copy refuses the pattern having allocated none
// of it, whether the pattern is new or cached, and gives both back once the
// pattern is built. Counted: bytes allocated (skipped under -race, which
// allocates on its own) and the ledger's own counters.
func TestPatternSourceCopiesArePaidFirst(t *testing.T) {
	const n = 16 << 10
	cases := []struct {
		name string
		src  string
		copy int // the copy the ledger must have room for first
	}{
		// A newline escapes to two bytes: the escaped source is twice the raw
		// one. A class of one character compiles to a few bytes, so that the
		// cache keeps the pattern.
		{"escaped source", "[" + strings.Repeat("\n", n/2) + "]", n + 2},
		// Nothing to escape: only the key, flags + "/" + source.
		{"key", "[" + strings.Repeat("a", n-2) + "]", n + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Warmed under a free ledger, so that the key is looked up.
			warm := budget.NewLedger(context.Background(), budget.LedgerLimits{})
			if _, err := compileUnder(warm)(c.src); err != nil {
				t.Fatal(err)
			}
			if got := warm.Bytes(); got != 0 {
				t.Errorf("a cached pattern leaves %d bytes charged, want 0 (copies given back)", got)
			}
			// Room for all but one byte of the first copy.
			led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: int64(c.copy) - 1})
			var err error
			got := allocated(func() { _, err = compileUnder(led)(c.src) })
			if !errors.Is(err, budget.ErrLimit) {
				t.Fatalf("err = %v, want ErrLimit", err)
			}
			if led.PeakBytes() != 0 || led.Bytes() != 0 {
				t.Errorf("the refused call charged peak %d, held %d; want 0", led.PeakBytes(), led.Bytes())
			}
			if !testutil.RaceEnabled && got > uint64(c.copy)/4 {
				t.Errorf("the refused call allocated %d bytes, a copy is %d", got, c.copy)
			}
		})
	}
}

// With room for exactly the first copy and nothing else, the pattern is
// refused at the next charge, not before: each copy is charged, one after
// the other, and the peak shows both were counted.
func TestPatternSourceCopiesAreBothCounted(t *testing.T) {
	const n = 64 << 10
	src := strings.Repeat("/", n/2) // escaped: n bytes; key: n+1
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: n + n + 1})
	_, err := compileUnder(led)(src)
	if !errors.Is(err, budget.ErrLimit) && err != nil {
		t.Fatal(err)
	}
	if led.PeakBytes() < 2*n+1 {
		t.Errorf("peak %d: the escaped source and the key were not both charged (%d)", led.PeakBytes(), 2*n+1)
	}
	if led.Bytes() > led.PeakBytes() {
		t.Errorf("held %d past the peak %d", led.Bytes(), led.PeakBytes())
	}
}

// escapedLen is what escapeSource makes, to the byte, for every shape of
// source: the charge before the copy is the copy's size.
func TestEscapedLenIsTheEscapedLength(t *testing.T) {
	for _, src := range []string{"", "abc", "a/b", "[/]/", "\\\n", "\n\r  ", "\\ x\\ ", "a\xffb/", "\\", "/\\", "[\\]/]/", "é/ü\n"} {
		n, copied := escapedLen(src)
		if got := escapeSource(src, n, copied); len(got) != n {
			t.Errorf("%q: escapedLen %d, escaped %q is %d bytes", src, n, got, len(got))
		}
	}
}
