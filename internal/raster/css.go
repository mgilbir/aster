package raster

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/csscolor"
)

// This file implements the CSS subset SVG documents use, following resvg's
// (usvg + simplecss) behaviour: style sheets from <style> elements, type,
// universal, class, id and attribute selectors, :first-child, the descendant,
// child and adjacent-sibling combinators, selector lists, specificity ordering
// and !important. Only presentation attributes are taken from style sheets.
//
// The input is untrusted: sheet size, rule count, selector size and the
// number of selector-matching steps are all bounded, and unsupported syntax
// drops the rule rather than failing the render.

const (
	maxSelectorBytes = 4096
	maxCompounds     = 32
	maxSubSelectors  = 32
)

// selector sub-kinds.
const (
	subAttrExists uint8 = iota
	subAttrEq
	subAttrIncludes // ~=
	subAttrDash     // |=
	subAttrPrefix   // ^=
	subAttrSuffix   // $=
	subAttrContains // *=
	subFirstChild
)

type subSel struct {
	kind      uint8
	name, val string
}

type simpleSel struct {
	tag  string // "" matches any element
	subs []subSel
}

type selector struct {
	comps []simpleSel
	comb  []byte // comb[i] joins comps[i-1] and comps[i]: ' ', '>' or '+'
	spec  [3]uint16
}

type cssRule struct {
	sel   *selector
	decls []cssDecl
	order int
}

type cssDecl struct {
	name, val string
	imp       bool
}

// forEachDecl calls fn for every "name: value [!important]" declaration in s.
// Semicolons inside quotes and parentheses (data: URLs) do not split.
func forEachDecl(s string, fn func(name, val string, imp bool)) {
	for len(s) > 0 {
		end := scanTo(s, ';')
		decl := s[:end]
		if end < len(s) {
			s = s[end+1:]
		} else {
			s = ""
		}
		colon := strings.IndexByte(decl, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(decl[:colon])
		val := strings.TrimSpace(decl[colon+1:])
		imp := false
		if b := strings.LastIndexByte(val, '!'); b >= 0 && csscolor.ASCIIEqualFold(strings.TrimSpace(val[b+1:]), "important") {
			imp = true
			val = strings.TrimSpace(val[:b])
		}
		if name == "" || val == "" {
			continue
		}
		if _, ok := attrNames[name]; !ok && name != "marker" {
			name = csscolor.ASCIILower(name)
		}
		fn(name, val, imp)
	}
}

// scanTo returns the index of the first stop byte outside quotes, parentheses
// and brackets, or len(s).
func scanTo(s string, stop byte) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == stop && depth == 0:
			return i
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case c == '\\':
			i++
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return len(s)
}

func stripComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		b.WriteByte(' ')
		s = s[i+2+j+2:]
	}
}

// parseStyleSheet appends the rules of one sheet to rules (at most maxRules in
// total).
func parseStyleSheet(src string, rules []cssRule, maxRules int) []cssRule {
	rules, _ = parseSheet(src, rules, maxRules, nil)
	return rules
}

// sheetCharge is what parsing a style sheet charges: the structures it
// builds, before it keeps them (to the document's reservation), and the
// bytes it reads (to the call's meter, which polls the context). A nil one
// charges nothing.
type sheetCharge struct {
	rs *reservation
	m  *budget.Meter
}

// Sizes of the structures a style sheet parses to.
const (
	cssRuleBytes   = int64(unsafe.Sizeof(cssRule{}))
	selectorBytes  = int64(unsafe.Sizeof(selector{}))
	simpleSelBytes = int64(unsafe.Sizeof(simpleSel{}))
	subSelBytes    = int64(unsafe.Sizeof(subSel{}))
	cssDeclBytes   = int64(unsafe.Sizeof(cssDecl{}))
)

// selectorSize is what a parsed selector holds.
func selectorSize(sel *selector) int64 {
	n := selectorBytes + int64(cap(sel.comps))*simpleSelBytes + int64(cap(sel.comb))
	for _, c := range sel.comps {
		n += int64(cap(c.subs)) * subSelBytes
	}
	return n
}

// parseSheet is parseStyleSheet charging cc (nil: nothing): a sheet is at
// most MaxStyleBytes, and its rules hold about twenty times its bytes. A
// selector is charged once parsed (its size is its own, bounded by
// maxSelectorBytes), before the rule is kept; the declarations of a rule
// before each is appended.
func parseSheet(src string, rules []cssRule, maxRules int, cc *sheetCharge) ([]cssRule, error) {
	take := func(n int64) error {
		if cc == nil {
			return nil
		}
		return cc.rs.take(n)
	}
	if strings.Contains(src, "/*") {
		if err := take(int64(len(src))); err != nil {
			return rules, err
		}
	}
	s := stripComments(src)
	last := len(src) // the sheet left at the last charge
	for {
		if cc != nil {
			if err := cc.m.Work(1 + int64(last-len(s))>>4); err != nil {
				return rules, err
			}
			last = len(s)
		}
		s = strings.TrimLeft(s, " \t\r\n\f")
		s = strings.TrimPrefix(s, "<!--")
		s = strings.TrimPrefix(s, "-->")
		s = strings.TrimLeft(s, " \t\r\n\f")
		if s == "" || len(rules) >= maxRules {
			return rules, nil
		}
		if s[0] == '@' {
			// At-rule: skip to ';' or over the {...} block.
			i := scanToAny(s, ";{")
			if i >= len(s) {
				return rules, nil
			}
			if s[i] == ';' {
				s = s[i+1:]
			} else {
				s = s[i:]
				s = s[skipBlock(s):]
			}
			continue
		}
		open := scanTo(s, '{')
		if open >= len(s) {
			return rules, nil
		}
		prelude := s[:open]
		body := s[open:]
		n := skipBlock(body)
		inner := body[1:]
		if n >= 2 {
			inner = body[1 : n-1]
		}
		s = body[n:]
		var decls []cssDecl
		var err error
		forEachDecl(inner, func(name, val string, imp bool) {
			// The list doubles, and a name not in the table is lowered.
			if err == nil {
				if err = take(2*cssDeclBytes + int64(len(name))); err == nil {
					decls = append(decls, cssDecl{name, val, imp})
				}
			}
		})
		if err != nil {
			return rules, err
		}
		if len(decls) == 0 {
			continue
		}
		for len(prelude) > 0 && len(rules) < maxRules {
			end := scanTo(prelude, ',')
			part := prelude[:end]
			if end < len(prelude) {
				prelude = prelude[end+1:]
			} else {
				prelude = ""
			}
			if sel := parseSelector(part); sel != nil {
				// The rule in a list that doubles.
				if err := take(selectorSize(sel) + 2*cssRuleBytes); err != nil {
					return rules, err
				}
				rules = append(rules, cssRule{sel: sel, decls: decls, order: len(rules)})
			}
		}
	}
}

func scanToAny(s string, stops string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case c == '\\':
			i++
		case strings.IndexByte(stops, c) >= 0:
			return i
		}
	}
	return len(s)
}

// skipBlock returns the length of the {...} block at the start of s (which
// begins with '{'), or len(s) when unterminated.
func skipBlock(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func isIdentByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 0x80
}

// parseSelector parses one complex selector; nil means invalid or unsupported.
func parseSelector(s string) *selector {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxSelectorBytes {
		return nil
	}
	sel := &selector{}
	i := 0
	pendingComb := byte(0)
	for i < len(s) {
		// Combinator.
		ws := false
		for i < len(s) && isSpaceByte(s[i]) {
			i++
			ws = true
		}
		if i >= len(s) {
			break
		}
		if s[i] == '>' || s[i] == '+' {
			if len(sel.comps) == 0 {
				return nil
			}
			pendingComb = s[i]
			i++
			for i < len(s) && isSpaceByte(s[i]) {
				i++
			}
			if i >= len(s) {
				return nil
			}
		} else if ws && len(sel.comps) > 0 {
			pendingComb = ' '
		}
		if len(sel.comps) > 0 && pendingComb == 0 {
			return nil
		}
		if len(sel.comps) >= maxCompounds {
			return nil
		}
		var cs simpleSel
		startLen := i
		// Type or universal.
		if s[i] == '*' {
			i++
		} else if isIdentByte(s[i]) {
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			cs.tag = s[i:j]
			i = j
		}
		for i < len(s) {
			c := s[i]
			if c == '.' || c == '#' {
				i++
				j := i
				for j < len(s) && isIdentByte(s[j]) {
					j++
				}
				if j == i {
					return nil
				}
				name := s[i:j]
				i = j
				if c == '.' {
					cs.subs = append(cs.subs, subSel{kind: subAttrIncludes, name: "class", val: name})
				} else {
					cs.subs = append(cs.subs, subSel{kind: subAttrEq, name: "id", val: name})
				}
			} else if c == '[' {
				end := scanTo(s[i+1:], ']') + 1
				if i+end >= len(s) {
					return nil
				}
				sub, ok := parseAttrSel(s[i+1 : i+end])
				if !ok {
					return nil
				}
				cs.subs = append(cs.subs, sub)
				i += end + 1
			} else if c == ':' {
				i++
				j := i
				for j < len(s) && isIdentByte(s[j]) {
					j++
				}
				if j == i || s[i:j] != "first-child" || (j < len(s) && s[j] == '(') {
					return nil // pseudo-elements and other pseudo-classes never match static SVG
				}
				cs.subs = append(cs.subs, subSel{kind: subFirstChild})
				i = j
			} else {
				break
			}
			if len(cs.subs) > maxSubSelectors {
				return nil
			}
		}
		if i == startLen {
			return nil // nothing consumed: invalid character
		}
		if len(sel.comps) > 0 {
			sel.comb = append(sel.comb, pendingComb)
		} else {
			sel.comb = append(sel.comb, 0)
		}
		pendingComb = 0
		sel.comps = append(sel.comps, cs)
	}
	if len(sel.comps) == 0 || pendingComb != 0 {
		return nil
	}
	for _, c := range sel.comps {
		if c.tag != "" {
			sel.spec[2]++
		}
		for _, sub := range c.subs {
			if sub.kind == subAttrEq && sub.name == "id" {
				sel.spec[0]++
			} else {
				sel.spec[1]++
			}
		}
	}
	return sel
}

func parseAttrSel(s string) (subSel, bool) {
	s = strings.TrimSpace(s)
	j := 0
	for j < len(s) && isIdentByte(s[j]) {
		j++
	}
	if j == 0 {
		return subSel{}, false
	}
	name := s[:j]
	rest := strings.TrimSpace(s[j:])
	if rest == "" {
		return subSel{kind: subAttrExists, name: name}, true
	}
	var kind uint8
	switch {
	case strings.HasPrefix(rest, "~="):
		kind, rest = subAttrIncludes, rest[2:]
	case strings.HasPrefix(rest, "|="):
		kind, rest = subAttrDash, rest[2:]
	case strings.HasPrefix(rest, "^="):
		kind, rest = subAttrPrefix, rest[2:]
	case strings.HasPrefix(rest, "$="):
		kind, rest = subAttrSuffix, rest[2:]
	case strings.HasPrefix(rest, "*="):
		kind, rest = subAttrContains, rest[2:]
	case strings.HasPrefix(rest, "="):
		kind, rest = subAttrEq, rest[1:]
	default:
		return subSel{}, false
	}
	rest = strings.TrimSpace(rest)
	// Optional case-sensitivity flag is not supported.
	if len(rest) >= 2 && (rest[0] == '"' || rest[0] == '\'') {
		if rest[len(rest)-1] != rest[0] {
			return subSel{}, false
		}
		rest = rest[1 : len(rest)-1]
	} else {
		for k := 0; k < len(rest); k++ {
			if !isIdentByte(rest[k]) {
				return subSel{}, false
			}
		}
	}
	return subSel{kind: kind, name: name, val: rest}, true
}

// ---- matching ---------------------------------------------------------------

// An element is indexed once before it is matched against any rule (C12,
// audit 2026-10-18): its attributes sorted by name, the first of a name
// kept (a lookup is a binary search, not a scan of every attribute), and
// its class tokens as a sorted set (".a" is a binary search too, and a class
// listed k times is listed once). Matching charges every step by what it
// does: a lookup the depth of its search, a comparison of values a unit per
// 16 bytes; the steps count against MaxCSSWork and go to the call's meter,
// which polls the context every thousand or so.

// cssIndexed is an element's index for selector matching.
type cssIndexed struct {
	classes []string // the class attribute's tokens, sorted, each once
	indexed bool
}

// indexNode sorts n's attributes by name, keeping the first of each name as
// rawAttr did, and collects its class tokens, charging both.
func (a *cssApplier) indexNode(n *node) bool {
	c := n.css
	if c == nil || c.ix.indexed {
		return true
	}
	c.ix.indexed = true
	raw := c.raw
	if !a.step(int64(len(raw)) * int64(1+bits.Len(uint(len(raw))))) {
		return false
	}
	slices.SortStableFunc(raw, func(x, y rawAttr) int { return strings.Compare(x.name, y.name) })
	raw = slices.CompactFunc(raw, func(x, y rawAttr) bool { return x.name == y.name })
	c.raw = raw
	cl, ok := c.find("class")
	if !ok {
		return true
	}
	k := 0
	for f := range strings.FieldsSeq(cl) {
		_ = f
		k++
	}
	if !a.step(1+int64(len(cl))>>4+int64(k)*int64(1+bits.Len(uint(k)))) || !a.take(int64(k)*16) {
		return false
	}
	toks := make([]string, 0, k)
	for f := range strings.FieldsSeq(cl) {
		toks = append(toks, f)
	}
	slices.Sort(toks)
	c.ix.classes = slices.Compact(toks)
	return true
}

// find is the value of the attribute named name, from the sorted index.
func (c *cssNode) find(name string) (string, bool) {
	i, ok := slices.BinarySearchFunc(c.raw, name, func(x rawAttr, name string) int { return strings.Compare(x.name, name) })
	if !ok {
		return "", false
	}
	return c.raw[i].val, true
}

func (n *node) prevEl() *node {
	if n.css == nil {
		return nil
	}
	return n.css.prev
}

// lookupCost is the work of a binary search among n items.
func lookupCost(n int) int64 { return 1 + int64(bits.Len(uint(n))) }

func (a *cssApplier) subMatches(sub *subSel, n *node) bool {
	if sub.kind == subFirstChild {
		return n.prevEl() == nil
	}
	c := n.css
	if c == nil {
		return false
	}
	if sub.kind == subAttrIncludes && sub.name == "class" {
		// Tokens never hold white space, so a value with any is none
		// of them, as strings.Fields made it.
		if !a.step(lookupCost(len(c.raw)) + lookupCost(len(c.ix.classes))) {
			return false
		}
		if _, ok := c.find("class"); !ok {
			return false
		}
		_, ok := slices.BinarySearch(c.ix.classes, sub.val)
		return ok
	}
	if !a.step(lookupCost(len(c.raw))) {
		return false
	}
	v, ok := c.find(sub.name)
	if !ok {
		return false
	}
	if sub.kind != subAttrExists && !a.step(int64(len(v)+len(sub.val))>>4) {
		return false
	}
	switch sub.kind {
	case subAttrExists:
		return true
	case subAttrEq:
		return v == sub.val
	case subAttrIncludes:
		for f := range strings.FieldsSeq(v) {
			if f == sub.val {
				return true
			}
		}
		return false
	case subAttrDash:
		return v == sub.val || (strings.HasPrefix(v, sub.val) && len(v) > len(sub.val) && v[len(sub.val)] == '-')
	case subAttrPrefix:
		return sub.val != "" && strings.HasPrefix(v, sub.val)
	case subAttrSuffix:
		return sub.val != "" && strings.HasSuffix(v, sub.val)
	case subAttrContains:
		return sub.val != "" && strings.Contains(v, sub.val)
	}
	return false
}

func (a *cssApplier) compoundMatches(c *simpleSel, n *node) bool {
	if c.tag != "" && (n.css == nil || n.css.name != c.tag) {
		return false
	}
	for i := range c.subs {
		if !a.subMatches(&c.subs[i], n) {
			return false
		}
	}
	return true
}

// matchAt reports whether n matches s.comps[..idx]. Each call is a step;
// matching fails once the steps or the context are spent (a.err).
func (a *cssApplier) matchAt(s *selector, idx int, n *node) bool {
	if !a.step(1) || !a.compoundMatches(&s.comps[idx], n) {
		return false
	}
	if idx == 0 {
		return true
	}
	switch s.comb[idx] {
	case ' ':
		for p := n.parent; p != nil && a.err == nil; p = p.parent {
			if a.matchAt(s, idx-1, p) {
				return true
			}
		}
		return false
	case '>':
		return n.parent != nil && a.matchAt(s, idx-1, n.parent)
	case '+':
		return n.prevEl() != nil && a.matchAt(s, idx-1, n.prevEl())
	}
	return false
}

// ---- application ------------------------------------------------------------

// cssIndex lists each rule once, under the id, else the first class, else
// the type its last compound requires, else among the others: the lists
// are disjoint, so an element's candidates (the lists of its id, of each of
// its distinct classes, of its type, and the others) list no rule twice.
type cssIndex struct {
	byID    map[string][]int
	byClass map[string][]int
	byTag   map[string][]int
	other   []int
}

// indexEntryBytes is about what a rule costs the index: its number in a
// list, and a map entry, its share of the buckets included.
const indexEntryBytes = 8 + 64

func buildIndex(rules []cssRule) *cssIndex {
	ix := &cssIndex{byID: map[string][]int{}, byClass: map[string][]int{}, byTag: map[string][]int{}}
	for i, r := range rules {
		last := r.sel.comps[len(r.sel.comps)-1]
		placed := false
		for _, sub := range last.subs {
			if sub.kind == subAttrEq && sub.name == "id" {
				ix.byID[sub.val] = append(ix.byID[sub.val], i)
				placed = true
				break
			}
		}
		if !placed {
			for _, sub := range last.subs {
				if sub.kind == subAttrIncludes && sub.name == "class" {
					ix.byClass[sub.val] = append(ix.byClass[sub.val], i)
					placed = true
					break
				}
			}
		}
		if !placed && last.tag != "" {
			ix.byTag[last.tag] = append(ix.byTag[last.tag], i)
			placed = true
		}
		if !placed {
			ix.other = append(ix.other, i)
		}
	}
	return ix
}

type cssApplier struct {
	rules  []cssRule
	ix     *cssIndex
	budget int // steps left of MaxCSSWork
	cand   []int
	tmp    []attr
	m      budget.Meter
	rs     *reservation
	err    error
}

// step charges n units of matching work: against MaxCSSWork, and to the
// call's meter, which polls the context. It reports whether matching may go
// on; once it may not, a.err says why.
func (a *cssApplier) step(n int64) bool {
	if a.err != nil {
		return false
	}
	if n > int64(a.budget) {
		a.budget = -1
		a.err = errCSSWork
		return false
	}
	a.budget -= int(n)
	if err := a.m.Work(n); err != nil {
		a.err = err
		return false
	}
	return true
}

// take charges n bytes matching is about to allocate.
func (a *cssApplier) take(n int64) bool {
	if a.err != nil {
		return false
	}
	if err := a.rs.take(n); err != nil {
		a.err = overLedger("applying the SVG's style sheets", err)
		return false
	}
	return true
}

// errCSSWork marks matching past MaxCSSWork; applyCSS names the bound.
var errCSSWork = fmt.Errorf("css work: %w", errLimit)

// applyCSS parses the document's style sheets and folds the matching
// declarations into every element's style list. A sheet past MaxStyleBytes,
// rules past MaxCSSRules or matching past MaxCSSWork fail the render: a
// document styled by part of its sheets, or with its later elements left
// unstyled, would be drawn wrong without a word (C103). What it builds is
// charged to the document's reservation and its work to the call's meter.
func (doc *document) applyCSS(ctx context.Context, lim Limits) error {
	a := &cssApplier{budget: lim.MaxCSSWork, m: budget.NewMeter(ctx), rs: &doc.res}
	cc := &sheetCharge{rs: &doc.res, m: &a.m}
	var rules []cssRule
	total := 0
	for _, src := range doc.css {
		total += len(src)
		if total > lim.MaxStyleBytes {
			return fmt.Errorf("raster: style sheets of more than %d bytes: %w", lim.MaxStyleBytes, errLimit)
		}
		var err error
		if rules, err = parseSheet(src, rules, lim.MaxCSSRules+1, cc); err != nil {
			if errors.Is(err, budget.ErrLimit) {
				return overLedger("parsing the SVG's style sheets", err)
			}
			return err
		}
		if len(rules) > lim.MaxCSSRules {
			return fmt.Errorf("raster: style sheets of more than %d rules: %w", lim.MaxCSSRules, errLimit)
		}
	}
	doc.css = nil
	if len(rules) == 0 {
		return nil
	}
	if err := doc.res.take(int64(len(rules)) * indexEntryBytes); err != nil {
		return overLedger("applying the SVG's style sheets", err)
	}
	a.rules, a.ix = rules, buildIndex(rules)
	a.visit(doc.root)
	switch {
	case a.err == errCSSWork:
		return fmt.Errorf("raster: matching style sheets takes more than %d steps (raise Limits.MaxCSSWork): %w", lim.MaxCSSWork, errLimit)
	case a.err != nil:
		return a.err
	}
	return nil
}

// visit applies the style sheets to n and its descendants, in document
// order: an element's ancestors and previous siblings, which its selectors
// may look at, are indexed before it.
func (a *cssApplier) visit(n *node) {
	if !a.indexNode(n) {
		return
	}
	a.apply(n)
	var prev *node
	for _, k := range n.kids {
		if a.err != nil {
			return
		}
		if k.tag == tagChars {
			continue
		}
		if k.css != nil {
			k.css.prev = prev
		}
		prev = k
		a.visit(k)
	}
}

// addCandidates appends a rule list to the element's candidates, charging
// it first.
func (a *cssApplier) addCandidates(list []int) bool {
	if len(list) == 0 {
		return true
	}
	if !a.step(int64(len(list))) {
		return false
	}
	if len(a.cand)+len(list) > cap(a.cand) {
		c := max(2*cap(a.cand), len(a.cand)+len(list))
		if !a.take(int64(c) * 8) {
			return false
		}
		a.cand = append(make([]int, 0, c), a.cand...)
	}
	a.cand = append(a.cand, list...)
	return true
}

func (a *cssApplier) apply(n *node) {
	a.cand = a.cand[:0]
	if c := n.css; c != nil {
		if id, ok := c.find("id"); ok && !a.addCandidates(a.ix.byID[id]) {
			return
		}
		for _, cl := range c.ix.classes {
			if !a.step(1) || !a.addCandidates(a.ix.byClass[cl]) {
				return
			}
		}
		if !a.addCandidates(a.ix.byTag[c.name]) {
			return
		}
	}
	if !a.addCandidates(a.ix.other) || len(a.cand) == 0 {
		return
	}
	matched := a.cand[:0] // in place: a candidate is read before it is overwritten
	for _, ri := range a.cand {
		r := &a.rules[ri]
		if a.matchAt(r.sel, len(r.sel.comps)-1, n) {
			matched = append(matched, ri)
		}
		if a.err != nil {
			return
		}
	}
	if len(matched) == 0 {
		return
	}
	if !a.step(int64(len(matched)) * lookupCost(len(matched))) {
		return
	}
	slices.SortFunc(matched, func(x, y int) int {
		sx, sy := a.rules[x].sel.spec, a.rules[y].sel.spec
		if sx != sy {
			if lessSpec(sx, sy) {
				return -1
			}
			return 1
		}
		return a.rules[x].order - a.rules[y].order
	})
	tmp := a.tmp[:0]
	for _, ri := range matched {
		ds := a.rules[ri].decls
		if !a.step(int64(len(ds))) {
			return
		}
		for _, d := range ds {
			tmp = appendDecl(tmp, d.name, d.val, d.imp, true)
		}
	}
	for _, s := range n.style {
		tmp = putDecl(tmp, s.id, s.val, s.imp, true)
	}
	a.tmp = tmp
	if !a.take(int64(len(tmp)) * attrBytes) {
		return
	}
	n.style = append([]attr(nil), tmp...)
}

func lessSpec(a, b [3]uint16) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
