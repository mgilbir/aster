package jsval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unsafe"

	ecma262 "github.com/mgilbir/goecma262"
	ecmacompiler "github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	ecmaparser "github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// Pattern is a compiled ECMA-262 regular expression, the value regexp() makes.
type Pattern struct {
	Source string
	Flags  string
	Re     *ecma262.Regexp
	// shared is the Regexp NewPattern compiled, whose program every Fresh
	// copy shares; nothing changes it.
	shared *ecma262.Regexp
}

// PatternPay pays for what compiling a pattern allocates, before it is
// allocated. NewPattern calls it with bytes > 0 before each step (the parse,
// the compile) and does not take the step when it returns an error; it calls
// it with bytes < 0 to give back what a step no longer holds. When NewPattern
// returns, what it paid adds up to the pattern's Bytes, or to nothing when it
// fails.
type PatternPay func(bytes int64) error

// guessBytes is what NewPattern first allows a pattern's compiled form
// besides its parse bound: enough for nearly every pattern a chart writes,
// whose programs hold a few kilobytes. A pattern that holds more is bounded
// exactly (compiler.BoundContext), at the cost of a second parse.
const guessBytes = 64 << 10

// NewPattern compiles source with JavaScript flags ("gimsuyvd") under ctx,
// paid for with pay before anything is allocated: a few bytes of source can
// compile to tens of megabytes, so a pattern past what pay accepts is
// refused unbuilt. The parse is paid for first, from its bound
// (parser.AllocBound, by the source's length), and then the compiled form,
// which goecma262's budget (WithBudget) holds to what was paid: first an
// allowance of guessBytes more, and when the pattern needs more, its exact
// bound (compiler.BoundContext) from a parse of its own. The parse is given back
// once the pattern is built, and the payment trued to what it holds.
//
// An error from pay is returned as it is, and so is the context's error
// when ctx ends the parse or the compile. A source that does not compile
// fails with the error goecma262 gives, wrapping ecma262.ErrLimit when it
// is past one of goecma262's limits rather than invalid.
func NewPattern(ctx context.Context, source, flagStr string, pay PatternPay) (*Pattern, error) {
	f, err := PatternFlags(source, flagStr)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := patternBuild{ctx: ctx, source: source, flagStr: flagStr, f: f, pay: pay}
	parse := ecmaparser.AllocBound(len(source))
	// The allowance is at least the parse bound, which Compile also holds
	// to the budget: only the compiled form can exceed it.
	guess := satAdd(parse, guessBytes)
	if b.charge(satAdd(parse, guess)) == nil {
		re, err := ecma262.CompileContext(ctx, source, f, ecma262.WithBudget(0, guess))
		if !errors.Is(err, ecma262.ErrBudgetExceeded) {
			return b.settle(re, err)
		}
		// It holds more than the allowance: bound it exactly, keeping the
		// parse paid for.
		b.giveBack(guess)
	} else if err := b.charge(parse); err != nil {
		return nil, err
	}
	ast, err := ecmaparser.New(source, parserFlags(f)).ParseContext(ctx)
	if err != nil {
		return b.settle(nil, fmt.Errorf("parse error: %w", err))
	}
	cost, err := ecmacompiler.BoundContext(ctx, ast)
	if err != nil {
		return b.settle(nil, fmt.Errorf("compile error: %w", err))
	}
	if cost.Exact && cost.Instructions > ecmacompiler.MaxProgramSize {
		// Compile would refuse it before emitting anything, whatever it is
		// paid: refuse it here, unpaid, with the same error.
		return b.settle(nil, fmt.Errorf("compile error: %w (limit: %d instructions)", ecmacompiler.ErrProgramTooLarge, ecmacompiler.MaxProgramSize))
	}
	held := satAdd(cost.Bytes, regexpBound(len(source), ast.NumGroups))
	if err := b.charge(held); err != nil {
		b.giveBack(b.paid)
		return nil, err
	}
	// The budget also holds the parse to its bound, which is paid for
	// apart: the program holds at most held, as Bound bounds it.
	re, err := ecma262.CompileContext(ctx, source, f, ecma262.WithBudget(0, max(held, parse)))
	return b.settle(re, err)
}

// PatternFlags parses JavaScript flags as NewPattern does, failing with the
// error it gives for source. It allocates nothing that the flags' length
// sizes (flags.Parse allocates only its error, and the error quotes a bounded
// prefix of the flags), so flags a megabyte long cost no more than invalid
// ones of a few bytes.
func PatternFlags(source, flagStr string) (flags.Flags, error) {
	f, err := flags.Parse(flagStr)
	if err != nil {
		return 0, patternError(source, flagStr, err)
	}
	if f.Has(flags.Unicode) && f.Has(flags.UnicodeSets) {
		// ecma262.Compile refuses it before it parses, with this error.
		return 0, patternError(source, flagStr, errors.New("incompatible flags: u and v"))
	}
	return f, nil
}

// patternBuild is one NewPattern: what it compiles, and what it has paid.
type patternBuild struct {
	ctx             context.Context
	source, flagStr string
	f               flags.Flags
	pay             PatternPay
	paid            int64
}

// charge pays n bytes more.
func (b *patternBuild) charge(n int64) error {
	if err := b.pay(n); err != nil {
		return err
	}
	b.paid = satAdd(b.paid, n)
	return nil
}

// giveBack gives back n of the bytes paid.
func (b *patternBuild) giveBack(n int64) {
	if n = min(n, b.paid); n > 0 {
		b.pay(-n) //nolint:errcheck // giving back never fails
		b.paid -= n
	}
}

// settle ends the build with re, or with err: a failure gives back all that
// was paid, and a pattern trues the payment to what it holds.
func (b *patternBuild) settle(re *ecma262.Regexp, err error) (*Pattern, error) {
	if err != nil {
		b.giveBack(b.paid)
		if cerr := b.ctx.Err(); cerr != nil && errors.Is(err, cerr) {
			return nil, err
		}
		return nil, patternError(b.source, b.flagStr, err)
	}
	p := &Pattern{Source: b.source, Flags: b.flagStr, Re: re, shared: re}
	if more := p.Bytes() - b.paid; more > 0 {
		// The budget given to Compile holds the pattern to what was paid,
		// so this does not happen; should it, the pattern is paid in full.
		if err := b.charge(more); err != nil {
			b.giveBack(b.paid)
			return nil, err
		}
	}
	b.giveBack(b.paid - p.Bytes())
	return p, nil
}

// parserFlags are the parser's flags for f, as ecma262.Compile sets them:
// the Annex B leniencies apply outside Unicode mode only.
func parserFlags(f flags.Flags) ecmaparser.Flags {
	u, v := f.Has(flags.Unicode), f.Has(flags.UnicodeSets)
	return ecmaparser.Flags{
		IgnoreCase:  f.Has(flags.IgnoreCase),
		Unicode:     u,
		UnicodeSets: v,
		DotAll:      f.Has(flags.DotAll),
		Multiline:   f.Has(flags.Multiline),
		AnnexB:      !u && !v,
	}
}

// regexpBound bounds what an ecma262.Regexp holds besides its program, as
// its MemoryFootprint counts it: the Regexp, its source and its group names
// (one per group and group 0, each at most the source's length), each
// rounded up to its allocation, which is at most twice the size plus 16
// bytes.
func regexpBound(sourceLen, groups int) int64 {
	return satAdd(int64(2*unsafe.Sizeof(ecma262.Regexp{})+64), satAdd(4*int64(sourceLen), 48*int64(groups+1)))
}

// satAdd is a+b for sizes, saturating rather than wrapping.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// patternError is NewPattern's error for a source that does not compile. It
// quotes the first 64 characters of the source and 16 of the flags, as each
// may be a megabyte long.
func patternError(source, flags string, err error) error {
	return fmt.Errorf("invalid regular expression /%.64s/%.16s: %w", source, flags, err)
}

// Bytes is what p holds: the compiled pattern as goecma262 measures it
// (its program, what the instructions own, its source and group names) and
// p itself.
func (p *Pattern) Bytes() int64 {
	n := int64(unsafe.Sizeof(Pattern{}))
	if p.Re != nil {
		n = satAdd(n, p.Re.MemoryFootprint())
	}
	return n
}

// MatchMemory is the most memory, in bytes, a search of n bytes may hold for
// backtracking when room bytes are left of the call's ledger: goecma262's
// default for n, which SetMaxMemory(0) restores and is returned as 0, or
// room when that is less. A search past it fails with an error wrapping
// ecma262.ErrStepLimit.
func MatchMemory(room int64, n int) int {
	dflt := satAdd(vm.DefaultMaxMemory, int64(vm.DefaultMemoryPerByte)*int64(max(n, 0)))
	if room >= dflt {
		return 0
	}
	return int(min(max(room, 1), math.MaxInt))
}

// Fresh is a new RegExp object for the same compiled pattern, with lastIndex
// 0: what evaluating a regular expression literal again makes. The program is
// shared, not compiled again; only the g and y flags give a Pattern state.
func (p *Pattern) Fresh() *Pattern {
	re := *p.Re
	re.SetLastIndex(0)
	return &Pattern{Source: p.Source, Flags: p.Flags, Re: &re, shared: p.Shared()}
}

// Shared is a Regexp with p's compiled program that nothing changes: the
// one NewPattern compiled, which p and the Fresh copies made from it share,
// or p.Re for a Pattern made otherwise. Its lastIndex means nothing; a
// caller makes its own copy (WithContext) to search with.
func (p *Pattern) Shared() *ecma262.Regexp {
	if p.shared != nil {
		return p.shared
	}
	return p.Re
}

// String is JavaScript's String(regexp): the literal a reader would write.
func (p *Pattern) String() string { return "/" + p.Source + "/" + p.Flags }
