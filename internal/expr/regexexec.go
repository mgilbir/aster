package expr

import (
	"errors"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	ecma262 "github.com/mgilbir/goecma262"
	ecmacompiler "github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	ecmaparser "github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// goecma262 v0.2.0 does not offer two searches JavaScript needs: one from
// lastIndex that reports running out of steps (its MatchString reads that as
// no match), and a scan of successive matches that reports positions between
// the two halves of a pair (its error form refuses them, since a Go string
// cannot hold half a pair; a WTF-8 string here can). For these, the pattern
// is compiled again through goecma262's own parser, compiler and VM, and run
// as its Regexp runs it (doMatchWithError, findAllMatchesErr). The program
// is charged to the ledger while it lives. Both paths are rare: a g or y
// pattern tested again after a match, and a pattern without the u flag that
// matches half a pair.

// rawProgram is a pattern's program, ready for goecma262's VM.
type rawProgram struct {
	code    []vm.Instruction
	prog    *vm.Program
	groups  int
	flags   flags.Flags
	sticky  bool
	unicode bool
}

// compileRaw compiles p for the VM, charging its program to the ledger; call
// release when done with it.
func compileRaw(s *Scope, p *jsval.Pattern) (r *rawProgram, release func(), err error) {
	f, err := flags.Parse(p.Flags)
	if err != nil {
		return nil, nil, err
	}
	ast, err := ecmaparser.New(p.Source, ecmaparser.Flags{
		IgnoreCase:  f.Has(flags.IgnoreCase),
		Unicode:     f.Has(flags.Unicode),
		UnicodeSets: f.Has(flags.UnicodeSets),
		DotAll:      f.Has(flags.DotAll),
		Multiline:   f.Has(flags.Multiline),
		AnnexB:      true, // New applies it outside Unicode mode only, as Compile does
	}).Parse()
	if err != nil {
		return nil, nil, err
	}
	led := budget.LedgerFrom(s.Context)
	size := patternBytes(p)
	if led.Alloc(size) != nil {
		exceeded("the regular expression /%.40s/ compiles to a program of %d bytes, past the memory limit", p.Source, size)
	}
	code, groups, err := ecmacompiler.Compile(ast)
	if err != nil {
		led.Free(size)
		return nil, nil, err
	}
	unicode := f.Has(flags.Unicode) || f.Has(flags.UnicodeSets)
	return &rawProgram{
		code:    code,
		prog:    vm.NewProgram(code, f.Has(flags.IgnoreCase), f.Has(flags.Multiline), f.Has(flags.DotAll), unicode, false),
		groups:  groups,
		flags:   f,
		sticky:  strings.Contains(p.Flags, "y"),
		unicode: unicode,
	}, func() { led.Free(size) }, nil
}

// match is one search, with one step budget, from start: the leftmost match
// at or after it (exactly at it for a sticky pattern) as capture positions
// padded to (groups+1)*2, or nil.
func (r *rawProgram) match(str string, start int) ([]int, error) {
	start = max(start, 0)
	if start > len(str) {
		return nil, nil
	}
	m := &vm.VM{
		Code:       r.code,
		Program:    r.prog,
		NumGroups:  r.groups,
		IgnoreCase: r.flags.Has(flags.IgnoreCase),
		Multiline:  r.flags.Has(flags.Multiline),
		DotAll:     r.flags.Has(flags.DotAll),
		Unicode:    r.unicode,
	}
	for pos := start; pos <= len(str); pos = vm.NextPosition(str, pos, r.unicode) {
		matched, _, g := m.MatchAt(str, pos)
		if m.Err != nil {
			return nil, m.Err
		}
		if matched {
			out := make([]int, 2*(r.groups+1))
			for i := range out {
				out[i] = -1
				if i < len(g) {
					out[i] = g[i]
				}
			}
			return out, nil
		}
		if r.sticky || pos >= len(str) {
			break
		}
	}
	return nil, nil
}

// execFrom is the search RegExpBuiltinExec makes for a pattern with the g or
// y flag: the capture positions of the leftmost match at or after start
// (exactly at start with y), nil for none, and an error wrapping
// ecma262.ErrStepLimit when the search runs out of steps.
func execFrom(s *Scope, p *jsval.Pattern, str string, start int) ([]int, error) {
	if start <= 0 {
		groups, err := p.Re.FindStringSubmatchIndexErr(str)
		if !errors.Is(err, ecma262.ErrSurrogateSplit) {
			return groups, err
		}
		// A match ending between the halves of a pair: lastIndex can hold
		// that position, which the search below reports.
	}
	r, release, err := compileRaw(s, p)
	if err != nil {
		return nil, err
	}
	defer release()
	return r.match(str, start)
}

// rawFindAll is FindAllStringSubmatchIndexErr that keeps the positions between
// the halves of a pair: it advances as goecma262's findAllMatchesErr does,
// with one step budget per search, and stops after max matches (max < 0:
// none).
func rawFindAll(s *Scope, p *jsval.Pattern, str string, max int) ([][]int, error) {
	r, release, err := compileRaw(s, p)
	if err != nil {
		return nil, err
	}
	defer release()
	var out [][]int
	for from := 0; (max < 0 || len(out) < max) && from <= len(str); {
		g, err := r.match(str, from)
		if err != nil {
			return nil, err
		}
		if g == nil {
			break
		}
		out = append(out, g)
		s.work(1)
		switch {
		case g[1] > g[0]:
			from = g[1]
		case g[1] >= len(str):
			from = len(str) + 1
		default:
			from = vm.NextPosition(str, g[1], r.unicode)
		}
	}
	return out, nil
}

// slicePos is str[a:b] for positions that may fall between the halves of a
// pair (a match without the u flag can begin or end there): the half the
// slice holds is kept, as three WTF-8 bytes.
func slicePos(str string, a, b int) string {
	if a >= b {
		return ""
	}
	amid, bmid := vm.BetweenSurrogates(str, a), vm.BetweenSurrogates(str, b)
	if !amid && !bmid {
		return str[a:b]
	}
	out := make([]byte, 0, b-a+6)
	if amid {
		_, lo := pairHalves(str, a-2)
		out = appendUnit(out, lo)
		a += 2
	}
	if bmid {
		out = append(out, str[a:b-2]...)
		hi, _ := pairHalves(str, b-2)
		return string(appendUnit(out, hi))
	}
	return string(append(out, str[a:b]...))
}
