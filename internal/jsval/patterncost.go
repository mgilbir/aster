package jsval

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	ecmacompiler "github.com/mgilbir/goecma262/compiler"
	"github.com/mgilbir/goecma262/flags"
	ecmaparser "github.com/mgilbir/goecma262/parser"
	"github.com/mgilbir/goecma262/vm"
)

// goecma262 compiles a pattern in one call that cannot be interrupted and
// that builds its whole program before it can be measured: a short source
// such as (a{1000}){190} compiles to 190,572 instructions, tens of megabytes,
// in seconds under the race detector. So the program is bounded from the
// parsed pattern before it is compiled, and paid for first (NewPattern).
//
// The bound follows goecma262 v0.2.0's compiler (compiler/compiler.go and
// classset.go) node by node: what each node emits, how a quantifier copies
// its body ({n} n times, {n,m} m times, * and + once, with the split, jump,
// mark, progress check and group reset each copy adds), what an alternation
// adds per alternative, and what the instructions own besides themselves (a
// class's atoms, a class set's character set and string trie). It is exact
// in instructions except for a v-mode class holding strings, where it bounds
// the trie by the strings' length; TestPatternCost checks it against the
// programs goecma262 builds.

// PatternCost is what compiling a pattern costs, known before the compile.
type PatternCost struct {
	// Instructions bounds the length of the program the compile builds.
	Instructions int64
	// Bytes bounds what the compiled pattern holds: the program (at the
	// capacity append gives it), goecma262's per-instruction tables, and
	// what the instructions own. It is 0 for a compile certain to fail.
	Bytes int64
	// Work is the compile's work, in instructions: it builds at most
	// MaxProgramSize of them before giving up.
	Work int64
	// TooLarge is set when the compile is certain to stop on goecma262's
	// program cap, MaxProgramSize, having built that many instructions.
	TooLarge bool
	// fails is set when the compile is certain to fail (a quantifier past
	// goecma262's bounds, an unknown property, nesting past its depth).
	fails bool
}

// patternBaseBytes is what a compiled pattern holds besides its program.
const patternBaseBytes = 1024

// instructionBytes is what one slot of a program's capacity holds once
// analysed: the instruction (128 bytes on 64-bit platforms) and the tables
// vm.NewProgram keeps per instruction (first sets, loops). Measured on
// goecma262 v0.2.0 at 125 to 168 bytes per slot; TestPatternCost keeps the
// bound at or above the heap a compile really holds.
const instructionBytes = 256

// satMax is where the arithmetic of the bound saturates: past any budget,
// far from overflowing int64 when two such values are added.
const satMax = int64(1) << 60

func satAdd(a, b int64) int64 { return min(a+b, satMax) }

func satMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > satMax/b {
		return satMax
	}
	return min(a*b, satMax)
}

// allocBytes bounds the bytes the runtime gives an allocation of n: Go's size
// classes waste at most a quarter of a small object, and a large one is
// rounded up to a page, which is less than a quarter of it.
func allocBytes(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return satAdd(n, n/4+16)
}

// programCap bounds the capacity of a program of n instructions, which
// goecma262 builds by appending one at a time: append about doubles a slice
// up to a thousand or so elements (17, 37, 75, 159, ... under Go 1.27, as
// it rounds to size classes) and grows it by a quarter plus 192 past that,
// rounded up to a page. TestProgramCap checks it for every length up to the
// program cap.
func programCap(n int64) int64 {
	if n <= 1024 {
		return 2*n + 16
	}
	return satAdd(n, n/4+320)
}

var (
	classAtomSize = int64(unsafe.Sizeof(vm.ClassAtom{}))
	charSetSize   = int64(unsafe.Sizeof(vm.CharSet{}))
)

// Pattern bounds for a v-mode class (a class set): what its character set
// holds, per node and per range, and what each node of its string trie holds
// (a RuneSwitch with its keys and targets).
const (
	classSetNodeBytes  = 256
	classSetRangeBytes = 32
	trieNodeBytes      = 128
)

// maxWalkDepth bounds the walk of the parsed pattern. goecma262's parser
// refuses groups and classes nested past parser.MaxNestingDepth (200), and a
// level of nesting is at most four nodes (a group, its alternation, a
// sequence, a quantifier), so the walk never reaches it; should it, the
// bound gives up rather than recurse further.
const maxWalkDepth = 4*ecmaparser.MaxNestingDepth + 64

// EstimatePattern parses source with JavaScript flags as NewPattern compiles
// it and bounds the compile's cost without compiling. Its error is the one
// NewPattern returns for a source or flags that do not parse.
func EstimatePattern(source, flagStr string) (PatternCost, error) {
	f, err := flags.Parse(flagStr)
	if err != nil {
		return PatternCost{}, patternError(source, flagStr, err)
	}
	// As ecma262.Compile: u and v exclude each other, and the Annex B
	// leniencies apply outside Unicode mode only.
	unicode, sets := f.Has(flags.Unicode), f.Has(flags.UnicodeSets)
	if unicode && sets {
		return PatternCost{}, patternError(source, flagStr, fmt.Errorf("incompatible flags: u and v"))
	}
	ast, err := ecmaparser.New(source, ecmaparser.Flags{
		IgnoreCase:  f.Has(flags.IgnoreCase),
		Unicode:     unicode,
		UnicodeSets: sets,
		DotAll:      f.Has(flags.DotAll),
		Multiline:   f.Has(flags.Multiline),
		AnnexB:      !unicode && !sets,
	}).Parse()
	if err != nil {
		return PatternCost{}, patternError(source, flagStr, fmt.Errorf("parse error: %w", err))
	}
	return costOf(ast, int64(len(source)+len(flagStr))), nil
}

// costOf is the cost of compiling ast, whose source and flags are textBytes
// long.
func costOf(ast *ecmaparser.Pattern, textBytes int64) PatternCost {
	body := walkPattern(ast.Body, 1, false, false)
	// Compile emits a save before the body and a save and a match after it.
	n := satAdd(body.hi, 3)
	c := PatternCost{
		Instructions: n,
		Work:         min(n, ecmacompiler.MaxProgramSize),
		fails:        body.fails,
	}
	// The size check runs as each node is entered: a program certain to pass
	// the cap there fails with the error goecma262 gives, unless an error
	// found earlier on the way (which fails says there is) is given first.
	c.TooLarge = !body.fails && !body.inexact && 1+body.peak > ecmacompiler.MaxProgramSize
	if !body.fails && !c.TooLarge {
		c.Bytes = programBytes(textBytes, programCap(n), satAdd(body.extra, body.shared), int64(ast.NumGroups))
	}
	return c
}

// programBytes is what a compiled pattern holds with a program of capacity
// slots and extra bytes owned by its instructions: the Regexp with its
// source and its group names, the program and its tables.
func programBytes(textBytes, slots, extra, groups int64) int64 {
	n := satAdd(patternBaseBytes, satMul(2, textBytes))
	n = satAdd(n, satMul(16, groups+1))
	n = satAdd(n, satMul(slots, instructionBytes))
	return satAdd(n, extra)
}

// patternError is NewPattern's error for a source that does not compile.
func patternError(source, flags string, err error) error {
	return fmt.Errorf("invalid regular expression /%s/%s: %w", source, flags, err)
}

// tooLargeError is the error goecma262 gives for a program past its cap,
// which a TooLarge pattern is refused with before it is built.
func tooLargeError(source, flags string) error {
	return patternError(source, flags, fmt.Errorf("compile error: compiled program too large (limit: %d instructions)", ecmacompiler.MaxProgramSize))
}

// nodeCost is the cost of compiling one node of a parsed pattern.
type nodeCost struct {
	hi, lo int64 // bounds on the instructions it emits
	// peak is a lower bound on the most instructions emitted since the node
	// began at any point where goecma262 checks its program cap (as a node
	// is entered, and after a class set with strings).
	peak  int64
	extra int64 // bound on the bytes its instructions own besides themselves
	// shared bounds the character sets of its class sets, which goecma262
	// evaluates once per parsed node however many copies a quantifier
	// makes, and again only where a lookbehind reverses its body (afresh
	// each time it is compiled).
	shared int64
	empty  bool // it can match the empty string (compiler.canMatchEmpty)
	// groups is set when it holds a capturing group, which makes a
	// quantifier over it reset the group each iteration
	// (compiler.groupIndexRange).
	groups bool
	// fails is set when compiling it is certain to fail; inexact when the
	// walk gave up, so that lo and peak bound nothing.
	fails, inexact bool
}

// one is a node that emits a single instruction owning extra bytes.
func one(extra int64) nodeCost { return nodeCost{hi: 1, lo: 1, extra: extra} }

// walkPattern is the cost of compiling e, entered by goecma262's compileNode
// at depth (1 for the pattern's body). rev is set inside a lookbehind, whose
// body is compiled with its sequences reversed. A dead node is never
// compiled (it is in the body of a {0} quantifier): only whether it holds a
// group matters, to the quantifiers around it.
func walkPattern(e ecmaparser.Expression, depth int, rev, dead bool) nodeCost {
	if depth > maxWalkDepth {
		return nodeCost{hi: satMax, groups: true, empty: true, inexact: true}
	}
	if !dead && depth > ecmacompiler.MaxNestingDepth {
		// compileNode refuses the node: "pattern too deeply nested".
		return nodeCost{fails: true}
	}
	sub := func(x ecmaparser.Expression, r bool) nodeCost { return walkPattern(x, depth+1, r, dead) }
	switch n := e.(type) {
	case *ecmaparser.Disjunction:
		if len(n.Alternatives) == 0 {
			return nodeCost{fails: true, inexact: true} // never parsed; goecma262 would panic
		}
		// split; alt; jmp ... for every alternative but the last.
		var c nodeCost
		for i, alt := range n.Alternatives {
			a := sub(alt, rev)
			at := c.lo
			if i < len(n.Alternatives)-1 {
				at++
				a.hi, a.lo = satAdd(a.hi, 2), satAdd(a.lo, 2)
			}
			c.peak = max(c.peak, satAdd(at, a.peak))
			c.hi, c.lo, c.extra = satAdd(c.hi, a.hi), satAdd(c.lo, a.lo), satAdd(c.extra, a.extra)
			c.shared = satAdd(c.shared, a.shared)
			c.empty = c.empty || a.empty
			c.merge(a)
		}
		return c
	case *ecmaparser.Sequence:
		c := nodeCost{empty: true}
		for i := range n.Elements {
			if rev {
				i = len(n.Elements) - 1 - i
			}
			el := sub(n.Elements[i], rev)
			c.peak = max(c.peak, satAdd(c.lo, el.peak))
			c.hi, c.lo, c.extra = satAdd(c.hi, el.hi), satAdd(c.lo, el.lo), satAdd(c.extra, el.extra)
			c.shared = satAdd(c.shared, el.shared)
			c.empty = c.empty && el.empty
			c.merge(el)
		}
		return c
	case *ecmaparser.Literal, *ecmaparser.Dot, *ecmaparser.WordChar, *ecmaparser.NonWordChar,
		*ecmaparser.Digit, *ecmaparser.NonDigit, *ecmaparser.Whitespace, *ecmaparser.NonWhitespace:
		return one(0)
	case *ecmaparser.Anchor:
		c := one(0)
		c.empty = true
		return c
	case *ecmaparser.UnicodeProperty:
		if !dead && !vm.ValidUnicodeProperty(n.Property) {
			return nodeCost{fails: true}
		}
		// Under v a negated property is a character set of its own.
		return one(allocBytes(charSetSize))
	case *ecmaparser.CharacterClass:
		for _, a := range n.Atoms {
			switch a := a.(type) {
			case *ecmaparser.ClassLiteral, *ecmaparser.ClassRange:
			case *ecmaparser.ClassEscape:
				if !dead && a.Kind == ecmaparser.ClassEscapeUnicodeProperty && !vm.ValidUnicodeProperty(a.Property) {
					return nodeCost{fails: true}
				}
			default:
				if !dead {
					return nodeCost{fails: true}
				}
			}
		}
		// Each copy of the class owns its atoms.
		return one(allocBytes(satMul(int64(len(n.Atoms)), classAtomSize)))
	case *ecmaparser.ClassSetExpression:
		if dead {
			return nodeCost{empty: true}
		}
		return classSetCost(n, depth)
	case *ecmaparser.Backreference:
		c := one(0)
		if n.Fallback != nil { //nolint:staticcheck // deprecated, but still compiled as literals should a parser set it
			k := int64(len(n.Fallback)) //nolint:staticcheck // as above
			c.hi, c.lo = k, k
		}
		c.empty = true
		return c
	case *ecmaparser.Quantifier:
		return quantifierCost(n, depth, rev, dead)
	case *ecmaparser.Group:
		c := wrap(sub(n.Body, rev), 1, 1)
		c.groups = true
		return c
	case *ecmaparser.NamedGroup:
		c := wrap(sub(n.Body, rev), 1, 1)
		c.groups = true
		return c
	case *ecmaparser.NonCapturingGroup:
		return wrap(sub(n.Body, rev), 0, 0)
	case *ecmaparser.ModifierGroup:
		return wrap(sub(n.Body, rev), 0, 0)
	case *ecmaparser.Lookahead:
		return lookaround(sub(n.Body, false))
	case *ecmaparser.NegativeLookahead:
		return lookaround(sub(n.Body, false))
	case *ecmaparser.Lookbehind:
		return lookbehind(sub(n.Body, true))
	case *ecmaparser.NegativeLookbehind:
		return lookbehind(sub(n.Body, true))
	}
	if dead {
		return nodeCost{empty: true}
	}
	return nodeCost{fails: true} // compileNode: "unknown node type"
}

// merge takes the flags of a part of c.
func (c *nodeCost) merge(part nodeCost) {
	c.groups = c.groups || part.groups
	c.fails = c.fails || part.fails
	c.inexact = c.inexact || part.inexact
}

// wrap is a body compiled between before and after instructions of its own.
func wrap(body nodeCost, before, after int64) nodeCost {
	c := body
	c.hi = satAdd(body.hi, before+after)
	c.lo = satAdd(body.lo, before+after)
	c.peak = satAdd(body.peak, before)
	return c
}

// lookaround is a lookaround's cost: a jump over the body, the body, and the
// instruction that runs it. It matches the empty string.
func lookaround(body nodeCost) nodeCost {
	c := wrap(body, 1, 1)
	c.empty = true
	return c
}

// lookbehind is a lookaround whose body is reversed, into new class set
// nodes, each time it is compiled.
func lookbehind(body nodeCost) nodeCost {
	c := lookaround(body)
	c.extra, c.shared = satAdd(c.extra, c.shared), 0
	return c
}

// quantifierCost follows compiler.compileQuantifier.
func quantifierCost(q *ecmaparser.Quantifier, depth int, rev, dead bool) nodeCost {
	if q.Max == 0 && q.Min == 0 {
		// {0}: the body is never compiled, but its groups still count for
		// the quantifiers around it.
		b := walkPattern(q.Body, depth+1, rev, true)
		return nodeCost{empty: true, groups: b.groups, inexact: b.inexact}
	}
	b := walkPattern(q.Body, depth+1, rev, dead)
	c := nodeCost{empty: q.Min == 0 || b.empty, groups: b.groups, fails: b.fails, inexact: b.inexact, shared: b.shared}
	if !dead && (q.Min > ecmacompiler.MaxQuantifierRepeat || q.Max > ecmacompiler.MaxQuantifierRepeat) {
		c.fails = true
		return c
	}
	if dead || q.Max == 0 {
		return c
	}
	var reset, mark int64 // a group reset before an iteration; a mark and a progress check around an optional one
	if b.groups {
		reset = 1
	}
	if b.empty {
		mark = 1
	}
	// loop is an unbounded tail: split, mark, reset, body, check, jmp; the
	// body starts after the first three.
	loopHi, loopLo := satAdd(b.hi, 2+2*mark+reset), satAdd(b.lo, 2+2*mark+reset)
	if q.Min <= 1 && q.Max == -1 {
		// * and + have the same instructions; + starts with the body.
		c.hi, c.lo, c.extra = loopHi, loopLo, b.extra
		c.peak = b.peak
		if q.Min == 0 {
			c.peak = satAdd(b.peak, 1+mark+reset)
		}
		return c
	}
	// {n}, {n,} and {n,m}: n copies of the body, a reset before each after
	// the first, then a loop or m-n optional copies (split, mark, reset,
	// body, check).
	minN := int64(q.Min)
	var mandHi, mandLo int64
	if minN > 0 {
		mandHi = satAdd(satMul(minN, b.hi), satMul(minN-1, reset))
		mandLo = satAdd(satMul(minN, b.lo), satMul(minN-1, reset))
		c.peak = satAdd(satMul(minN-1, satAdd(b.lo, reset)), b.peak)
	}
	c.extra = satMul(minN, b.extra)
	if q.Max == -1 {
		c.hi, c.lo = satAdd(mandHi, loopHi), satAdd(mandLo, loopLo)
		c.peak = max(c.peak, satAdd(mandLo, satAdd(1+mark+reset, b.peak)))
		c.extra = satAdd(c.extra, b.extra)
		return c
	}
	k := int64(max(q.Max-q.Min, 0))
	optHi, optLo := satAdd(b.hi, 1+2*mark+reset), satAdd(b.lo, 1+2*mark+reset)
	c.hi, c.lo = satAdd(mandHi, satMul(k, optHi)), satAdd(mandLo, satMul(k, optLo))
	if k > 0 {
		last := satAdd(mandLo, satMul(k-1, optLo))
		c.peak = max(c.peak, satAdd(last, satAdd(1+mark+reset, b.peak)))
	}
	c.extra = satAdd(c.extra, satMul(k, b.extra))
	return c
}

// classSetCost follows compiler.compileClassSet: one OpClassSet, or, when
// the class may hold strings, an alternation of at most three branches (the
// strings' trie, the characters, the empty string), with a split and a jump
// for each but the last. The trie emits a switch per node with children and
// a jump per child but the last (one instruction per node in all), and a
// split and a jump at a node where a string ends and a longer one goes on.
// That is at most R instructions, R the characters of the strings of two or
// more: R counts each node once per string through it, a node where a string
// ends inside the trie has two strings through it, and so has each of its
// ancestors (it is at depth two at least), so R exceeds the node count by
// twice the number of such nodes at least.
func classSetCost(e *ecmaparser.ClassSetExpression, depth int) nodeCost {
	var s classSetStats
	s.add(e, depth)
	if s.fails {
		return nodeCost{fails: true}
	}
	set := satAdd(satMul(s.nodes, classSetNodeBytes), satMul(s.ranges, classSetRangeBytes))
	c := nodeCost{hi: 1, lo: 1, empty: true, shared: set}
	if s.strings {
		c.hi = satAdd(5, s.runes)
		// The trie is built again for every copy.
		c.extra = satMul(s.runes+1, trieNodeBytes)
	}
	return c
}

// classSetStats counts what a class set is made of.
type classSetStats struct {
	nodes, ranges int64
	// strings is set when it may hold a string that is not one character
	// (empty, or of two or more); runes counts the characters of those.
	strings bool
	runes   int64
	fails   bool
}

func (s *classSetStats) add(n ecmaparser.ClassSetNode, depth int) {
	if depth > maxWalkDepth {
		s.fails = true
		return
	}
	s.nodes = satAdd(s.nodes, 1)
	switch n := n.(type) {
	case *ecmaparser.ClassSetCharacter, *ecmaparser.ClassSetRange:
		s.ranges = satAdd(s.ranges, 1)
	case *ecmaparser.ClassEscape:
	case *ecmaparser.ClassStrings:
		for _, str := range n.Strings {
			if len(str) == 1 {
				s.ranges = satAdd(s.ranges, 1)
				continue
			}
			s.strings = true
			s.runes = satAdd(s.runes, int64(len(str)))
		}
	case *ecmaparser.ClassSetExpression:
		s.add(n.Body, depth+1)
	case *ecmaparser.ClassSetOperation:
		for _, it := range n.Items {
			s.add(it, depth+1)
		}
	default:
		s.fails = true // evalClassSetNode panics on an unknown node; never parsed
	}
}

// codeSlots is the capacity of re's program, which goecma262 does not
// report: it is read from the unexported field, and is unknown (ok false)
// should a new version rename it.
func codeSlots(re any) (int64, bool) {
	v := reflect.ValueOf(re)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return 0, false
	}
	f := v.Elem().FieldByName("code")
	if !f.IsValid() || f.Kind() != reflect.Slice {
		return 0, false
	}
	return int64(f.Cap()), true
}

// maxPatternBytes is what a pattern whose program cannot be read counts as:
// the largest goecma262 builds.
var maxPatternBytes = programBytes(0, programCap(ecmacompiler.MaxProgramSize+3), 0, 0)

// quickMax is the largest bound quickCost gives; past it, or when it cannot
// tell, the pattern is parsed for the exact bound.
const quickMax = 1024

// quickCost bounds the compile of a small pattern from its text alone, in
// one pass with no allocation: most patterns are a few dozen bytes, and
// parsing them for the bound would add a third to an uncached compile. It
// over-counts wherever it does not follow the grammar: every character
// outside a class is an atom of one instruction (so an escape, a group's
// syntax or a malformed brace counts at least what it compiles to); a group
// adds two, an alternative two; a class is one instruction owning at most
// one atom per byte inside it; and a quantifier makes at most max(n, m), or
// n+1 when unbounded, copies of the item before it, each with at most five
// instructions of its own (split, mark, reset, progress check, jump). It
// does not handle v-mode, whose classes hold strings, and gives up past
// quickMax. TestQuickCost checks it against the exact bound.
func quickCost(source, flags string) (PatternCost, bool) {
	if len(source) > quickMax || strings.IndexByte(flags, 'v') >= 0 {
		return PatternCost{}, false
	}
	type frame struct{ total, last, totalX, lastX int64 }
	var stack [32]frame
	depth, groups := 0, int64(0)
	f := &stack[0]
	atom := func(n, x int64) {
		f.total, f.totalX = f.total+f.last, f.totalX+f.lastX
		f.last, f.lastX = n, x
	}
	for i := 0; i < len(source); i++ {
		copies := int64(-1)
		switch c := source[i]; c {
		case '\\':
			// The escaped character is counted with the backslash, but for
			// c: Annex B reads a \c not followed by a letter as two
			// characters, \ and c, so c is left to count as an atom.
			if i+1 < len(source) && source[i+1] != 'c' {
				i++
			}
			atom(1, allocBytes(charSetSize))
		case '[':
			k := int64(0)
			for i++; i < len(source) && source[i] != ']'; i++ {
				if source[i] == '\\' {
					i++
					k++
				}
				k++
			}
			atom(1, allocBytes(k*classAtomSize))
		case '(':
			if depth == len(stack)-1 {
				return PatternCost{}, false
			}
			groups++
			depth++
			f = &stack[depth]
			*f = frame{}
		case ')':
			if depth == 0 {
				return PatternCost{}, false
			}
			n, x := f.total+f.last+2, f.totalX+f.lastX
			depth--
			f = &stack[depth]
			atom(n, x)
		case '|':
			f.total, f.totalX = f.total+f.last+2, f.totalX+f.lastX
			f.last, f.lastX = 0, 0
		case '*', '+', '?':
			copies = 1
		case '{':
			n, m, j, ok := quickBraces(source, i)
			if !ok {
				atom(1, 0) // a literal brace (Annex B), or an error
				break
			}
			copies, i = max(n, m), j
		default:
			atom(1, 0)
		}
		if copies >= 0 {
			f.last, f.lastX = copies*(f.last+5), copies*f.lastX
		}
		if f.total+f.last > quickMax {
			return PatternCost{}, false
		}
	}
	if depth != 0 {
		return PatternCost{}, false
	}
	n := f.total + f.last + 3
	return PatternCost{
		Instructions: n,
		Work:         n,
		Bytes:        programBytes(int64(len(source)+len(flags)), programCap(n), f.totalX+f.lastX, groups),
	}, true
}

// quickBraces reads a braced quantifier at source[i]: {n}, {n,} or {n,m},
// as n and the most copies it makes (n+1 when unbounded), with the index of
// its closing brace. Numbers past quickMax read as quickMax+1.
func quickBraces(source string, i int) (n, m int64, end int, ok bool) {
	num := func(j int) (int64, int) {
		v, start := int64(0), j
		for ; j < len(source) && source[j] >= '0' && source[j] <= '9'; j++ {
			v = min(v*10+int64(source[j]-'0'), quickMax+1)
		}
		if j == start {
			return -1, j
		}
		return v, j
	}
	n, j := num(i + 1)
	if n < 0 || j >= len(source) {
		return 0, 0, 0, false
	}
	m = n
	if source[j] == ',' {
		if m, j = num(j + 1); m < 0 {
			m = n + 1
		}
	}
	if j >= len(source) || source[j] != '}' {
		return 0, 0, 0, false
	}
	return n, m, j, true
}
