package expr

import (
	"errors"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/bcache"
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	ecma262 "github.com/mgilbir/goecma262"
)

// patternCache memoises patterns compiled from strings (test('a+', x) in a
// filter would otherwise compile per datum). The strings may come from the
// data, and a short one can compile to megabytes (C8), so the cache is bounded
// by the bytes the patterns hold and keeps none of more than
// patternCacheMaxEntry. A pattern with the g or y flag carries lastIndex
// state, so the cache keeps a pristine one and hands out fresh copies.
const (
	patternCacheBytes    = 32 << 20
	patternCacheMaxEntry = 1 << 20
)

var patternCache = bcache.New[string, *jsval.Pattern](patternCacheBytes, patternCacheMaxEntry)

// MaxPatternLength bounds the source of a regular expression an expression
// compiles from a string, in bytes. Real patterns are tens of bytes; one this
// long costs a tenth of a second and 128 MiB of the call's ledger to parse.
// Past the bound the render ends with ErrLimit.
const MaxPatternLength = 1 << 20

// patternError is a compile failure: a SyntaxError, or one of goecma262's
// own limits (program size, nesting, a {n} past 10,000), which V8 does not
// have and which therefore ends the render (C43).
type patternError struct {
	msg   string
	limit bool
}

// newPatternError classifies err by goecma262's typed errors: every limit
// it refuses a pattern for wraps ecma262.ErrLimit, and anything else is the
// pattern being invalid.
func newPatternError(err error) patternError {
	return patternError{err.Error(), errors.Is(err, ecma262.ErrLimit)}
}

func (e patternError) raise() {
	if e.limit {
		exceeded("regular expression: %s", e.msg)
	}
	throw("SyntaxError", "%s", e.msg)
}

// patternErrors remembers the sources that failed to compile, so that one
// is not parsed again for every datum (a source past goecma262's program cap
// is refused once parsed, and its parse may cost a hundred megabytes).
var patternErrors = bcache.New[string, patternError](1<<20, 4<<10)

// compilePattern is RegExp(source, flags) for a source given as a string.
func (s *Scope) compilePattern(source, flags string) *jsval.Pattern {
	if len(source) > MaxPatternLength {
		exceeded("a regular expression of %d bytes exceeds the limit of %d", len(source), MaxPatternLength)
	}
	checkFlags(source, flags)
	return s.compileEscaped(escapeSource(source), canonicalFlags(flags))
}

// checkFlags throws the SyntaxError of RegExp(source, flags) for flags that
// are not JavaScript's, before anything the source's or the flags' length
// sizes is made: the flags may be a megabyte long, and are then certainly
// invalid.
func checkFlags(source, flags string) {
	if jsval.ValidFlags(flags) {
		return
	}
	if _, err := jsval.PatternFlags(source, flags); err != nil {
		newPatternError(err).raise()
	}
}

// compileEscaped compiles a source as RegExp.prototype.source reports it.
// The compile is paid for with the ledger in the Scope's context before it
// allocates (jsval.NewPattern: its parse, then its compiled form), so a
// pattern past what the ledger has left is refused unbuilt, and it stops at
// the call's deadline. A pattern the shared cache keeps is bounded by the
// cache, and given back to the ledger; one it does not keep stays charged to
// the call that holds it. A refusal, or the deadline, depends on the call,
// not on the pattern, so it is not remembered in patternErrors.
func (s *Scope) compileEscaped(source, flags string) *jsval.Pattern {
	checkFlags(source, flags)
	key := flags + "/" + source
	if p, ok := patternCache.Get(key); ok {
		return fresh(p)
	}
	if e, ok := patternErrors.Get(key); ok {
		e.raise()
	}
	if len(source) > MaxPatternLength {
		exceeded("a regular expression of %d bytes exceeds the limit of %d", len(source), MaxPatternLength)
	}
	s.workBytes(len(source))
	ctx := s.Context
	if ctx == nil {
		ctx = backgroundContext
	}
	led := budget.LedgerFrom(ctx)
	var (
		held    int64 // what the ledger holds for the pattern
		refused int64 // the bytes the ledger last refused
	)
	p, err := jsval.NewPattern(ctx, source, flags, func(bytes int64) error {
		if bytes < 0 {
			led.Free(-bytes)
			held += bytes
			return nil
		}
		if err := led.Alloc(bytes); err != nil {
			refused = bytes
			return err
		}
		held += bytes
		return nil
	})
	if err != nil {
		if errors.Is(err, budget.ErrLimit) {
			// The ledger refused it (NewPattern returns pay's error as it
			// is; goecma262's own limits wrap ecma262.ErrLimit instead).
			exceeded("the regular expression /%.40s/ needs %d bytes to compile, past the memory limit", source, refused)
		}
		s.stopIfDone(err)
		e := newPatternError(err)
		patternErrors.Put(key, e, int64(len(key)+len(e.msg)))
		e.raise()
	}
	s.work(p.Re.ProgramSize())
	if patternCache.Put(key, p, int64(len(key))+p.Bytes()) {
		led.Free(held)
	}
	return fresh(p)
}

// stopIfDone ends the evaluation with the call's context error when err is
// it: the deadline or a cancellation that stopped a compile or a search.
func (s *Scope) stopIfDone(err error) {
	if s.Context == nil {
		return
	}
	if cerr := s.Context.Err(); cerr != nil && errors.Is(err, cerr) {
		panic(&cancelled{cerr})
	}
}

// fresh is p, or a fresh copy of it when the g or y flag gives it state.
func fresh(p *jsval.Pattern) *jsval.Pattern {
	if strings.ContainsAny(p.Flags, "gy") {
		return p.Fresh()
	}
	return p
}

// toPattern is RegExp(v) with no flags.
func (s *Scope) toPattern(v jsval.Value) *jsval.Pattern {
	if v.IsPattern() {
		return v.PatternOf()
	}
	if v.IsUndefined() {
		return s.compilePattern("", "")
	}
	return s.compilePattern(s.str(v), "")
}

// regexpFailed raises a matching error. goecma262's step and memory budget
// is the engine's, with no upstream counterpart, so running out of it ends
// the render (it never reads as "no match"), as does the call's deadline; a
// result that would split a surrogate pair, which a Go string cannot hold,
// is the RangeError it always was.
func (s *Scope) regexpFailed(err error) {
	s.stopIfDone(err)
	if errors.Is(err, ecma262.ErrStepLimit) {
		exceeded("regular expression: %v", err)
	}
	throw("RangeError", "regular expression: %v", err)
}

// patternTest is RegExp.prototype.test. With the g or y flag it starts at
// lastIndex and advances it, as test does in JavaScript; either way a search
// that runs out of steps is an error, not false (C97).
func (s *Scope) patternTest(p *jsval.Pattern, str string) bool {
	s.workBytes(len(str))
	re := s.matcher(p, len(str))
	if !strings.ContainsAny(p.Flags, "gy") {
		ok, err := re.MatchStringErr(str)
		if err != nil {
			s.regexpFailed(err)
		}
		return ok
	}
	// RegExpBuiltinExec from lastIndex, which may lie between the halves of
	// a pair; the search neither reads nor moves the matcher's own.
	groups, err := re.FindStringSubmatchPositionsFromErr(str, p.Re.LastIndex())
	if err != nil {
		s.regexpFailed(err)
	}
	if groups == nil {
		p.Re.SetLastIndex(0)
		return false
	}
	p.Re.SetLastIndex(groups[1])
	return true
}

// matchBytes is what one match FindAll returns holds: its slice header in the
// list and the slice of its group positions.
func matchBytes(p *jsval.Pattern) int64 { return 48 + 16*int64(p.Re.NumSubexp()+1) }

// findAll is FindAllStringSubmatchIndexErr bounded by the string budget: the
// list of matches is as long as the string, so it is searched for no more
// matches than the budget can pay for, and charged. A match may begin or end
// between the halves of a pair, which a WTF-8 string can hold: its positions
// are reported as goecma262's SetLastIndex takes them, and slicePos cuts
// them.
func (s *Scope) findAll(p *jsval.Pattern, str string, limit int) ([][]int, error) {
	per := matchBytes(p)
	n := limit
	if s.Strings != nil {
		room := s.Strings.room()/per + 1
		if room < math.MaxInt32 && (limit < 0 || int(room) < limit) {
			n = int(room)
		}
	}
	s.workBytes(len(str))
	m, err := s.matcher(p, len(str)).FindAllStringSubmatchPositionsErr(str, n)
	if err != nil {
		return nil, err
	}
	if n != limit && len(m) == n {
		exceeded("the expression string budget of this render is used up")
	}
	s.chargeBytes(len(m) * int(per))
	s.work(len(m))
	return m, nil
}

// regexpSplit is String.prototype.split with a RegExp separator: capture
// groups are spliced into the result (undefined for a group that did not
// participate), as ECMA-262 specifies. split matches with a copy of the
// separator that has the y flag and not g, at every position in turn, so the
// separator's own g and y flags make no difference.
func (s *Scope) regexpSplit(p *jsval.Pattern, str string, limit uint32) []jsval.Value {
	if strings.ContainsAny(p.Flags, "gy") {
		p = s.compileEscaped(p.Source, strings.Map(func(r rune) rune {
			if r == 'g' || r == 'y' {
				return -1
			}
			return r
		}, p.Flags))
	}
	if str == "" {
		ok, err := s.matcher(p, 0).MatchStringErr(str)
		if err != nil {
			s.regexpFailed(err)
		}
		if ok {
			return []jsval.Value{}
		}
		return []jsval.Value{jsval.Str("")}
	}
	n := -1
	if limit < math.MaxInt32 {
		n = int(limit)
	}
	matches, err := s.findAll(p, str, n)
	if err != nil {
		s.regexpFailed(err)
	}
	b := itemBuilder{s: s}
	pos := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start >= len(str) {
			break
		}
		if end == pos {
			continue // an empty match where the current piece began
		}
		b.push(jsval.Str(slicePos(str, pos, start)))
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] < 0 {
				b.push(jsval.Undefined)
			} else {
				b.push(jsval.Str(slicePos(str, m[g], m[g+1])))
			}
		}
		pos = end
	}
	b.push(jsval.Str(slicePos(str, pos, len(str))))
	if uint64(len(b.items)) > uint64(limit) {
		return b.items[:limit]
	}
	return b.items
}

// unitSplit is str.split(”): one string per code unit, at most limit.
func (s *Scope) unitSplit(str string, limit uint32) []jsval.Value {
	n := utf16Len(str)
	if uint64(n) > uint64(limit) {
		n = int(limit)
	}
	out := s.makeItems(n)
	k := 0
	for i := 0; i < len(str) && k < n; {
		_, w, u := charAt(str, i)
		if u == 2 {
			hi, lo := pairHalves(str, i)
			out[k] = jsval.Str(string(appendUnit(nil, hi)))
			if k+1 < n {
				out[k+1] = jsval.Str(string(appendUnit(nil, lo)))
			}
			k += 2
		} else {
			out[k] = jsval.Str(unitString(str, i, false))
			k++
		}
		i += w
	}
	return out
}

// regexpReplace is String.prototype.replace with a RegExp: the first match,
// or every match with the g flag, replaced by the expansion of repl. The
// result is built here rather than by goecma262, so that a replacement that
// multiplies the input ($` and $' in a long template) is bounded and charged
// as it grows.
func (s *Scope) regexpReplace(p *jsval.Pattern, src, repl string) string {
	limit := 1
	if strings.Contains(p.Flags, "g") {
		limit = -1
	}
	matches, err := s.findAll(p, src, limit)
	if err != nil {
		s.regexpFailed(err)
	}
	if len(matches) == 0 {
		return src
	}
	var names []string
	for _, n := range p.Re.SubexpNames() {
		if n != "" {
			names = p.Re.SubexpNames()
			break
		}
	}
	b := newStrBuilder(s)
	pos := 0
	for _, m := range matches {
		b.add(slicePos(src, pos, m[0]))
		expandTemplate(&b, repl, regexpMatch{src, m}, names)
		pos = m[1]
	}
	b.add(slicePos(src, pos, len(src)))
	return b.String()
}

// match is what GetSubstitution reads of one match: the text before it, the
// match and the text after it, and its groups (1 <= i <= groups(), ok false
// for one that did not participate). The text is made only when the template
// asks for it.
type match interface {
	before() string
	matched() string
	after() string
	groups() int
	group(i int) (string, bool)
}

// regexpMatch is a match of a RegExp: m holds the byte positions of the match
// and its groups, possibly between the halves of a pair.
type regexpMatch struct {
	src string
	m   []int
}

func (r regexpMatch) before() string  { return slicePos(r.src, 0, r.m[0]) }
func (r regexpMatch) matched() string { return slicePos(r.src, r.m[0], r.m[1]) }
func (r regexpMatch) after() string   { return slicePos(r.src, r.m[1], len(r.src)) }
func (r regexpMatch) groups() int     { return len(r.m)/2 - 1 }
func (r regexpMatch) group(i int) (string, bool) {
	if r.m[2*i] < 0 {
		return "", false
	}
	return slicePos(r.src, r.m[2*i], r.m[2*i+1]), true
}

// stringMatch is the match of a string pattern, which has no groups.
type stringMatch struct{ pre, needle, post string }

func (m stringMatch) before() string           { return m.pre }
func (m stringMatch) matched() string          { return m.needle }
func (m stringMatch) after() string            { return m.post }
func (m stringMatch) groups() int              { return 0 }
func (m stringMatch) group(int) (string, bool) { return "", false }

// expandTemplate appends ECMA-262's GetSubstitution of template for one
// match. names are the pattern's group names, nil when it has no named group,
// which leaves $<...> as written.
func expandTemplate(b *strBuilder, template string, m match, names []string) {
	groups := m.groups()
	for i := 0; i < len(template); {
		j := strings.IndexByte(template[i:], '$')
		if j < 0 {
			b.add(template[i:])
			return
		}
		b.add(template[i : i+j])
		i += j
		rest := template[i:]
		switch {
		case len(rest) < 2:
			b.add(rest)
			i++
		case rest[1] == '$':
			b.addByte('$')
			i += 2
		case rest[1] == '&':
			b.add(m.matched())
			i += 2
		case rest[1] == '`':
			b.add(m.before())
			i += 2
		case rest[1] == '\'':
			b.add(m.after())
			i += 2
		case rest[1] >= '0' && rest[1] <= '9':
			digits, index := 1, int(rest[1]-'0')
			if len(rest) > 2 && rest[2] >= '0' && rest[2] <= '9' {
				if two := index*10 + int(rest[2]-'0'); two <= groups {
					digits, index = 2, two
				}
			}
			if index >= 1 && index <= groups {
				g, _ := m.group(index)
				b.add(g)
			} else {
				b.add(rest[:1+digits])
			}
			i += 1 + digits
		case rest[1] == '<':
			gt := strings.IndexByte(rest, '>')
			if gt < 0 || names == nil {
				b.add("$<")
				i += 2
				continue
			}
			name := rest[2:gt]
			for k, n := range names {
				if g, ok := m.group(k); k > 0 && n == name && ok {
					b.add(g)
					break
				}
			}
			i += gt + 1
		default:
			b.addByte('$')
			i++
		}
	}
}
