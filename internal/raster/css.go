package raster

import (
	"sort"
	"strings"
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
		if b := strings.LastIndexByte(val, '!'); b >= 0 && strings.EqualFold(strings.TrimSpace(val[b+1:]), "important") {
			imp = true
			val = strings.TrimSpace(val[:b])
		}
		if name == "" || val == "" {
			continue
		}
		if _, ok := attrNames[name]; !ok && name != "marker" {
			name = strings.ToLower(name)
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
	s := stripComments(src)
	for {
		s = strings.TrimLeft(s, " \t\r\n\f")
		s = strings.TrimPrefix(s, "<!--")
		s = strings.TrimPrefix(s, "-->")
		s = strings.TrimLeft(s, " \t\r\n\f")
		if s == "" || len(rules) >= maxRules {
			return rules
		}
		if s[0] == '@' {
			// At-rule: skip to ';' or over the {...} block.
			i := scanToAny(s, ";{")
			if i >= len(s) {
				return rules
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
			return rules
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
		forEachDecl(inner, func(name, val string, imp bool) {
			decls = append(decls, cssDecl{name, val, imp})
		})
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

func (n *node) rawAttr(name string) (string, bool) {
	if n.css == nil {
		return "", false
	}
	for i := range n.css.raw {
		if n.css.raw[i].name == name {
			return n.css.raw[i].val, true
		}
	}
	return "", false
}

func (n *node) prevEl() *node {
	if n.css == nil {
		return nil
	}
	return n.css.prev
}

func (sub *subSel) matches(n *node) bool {
	if sub.kind == subFirstChild {
		return n.prevEl() == nil
	}
	v, ok := n.rawAttr(sub.name)
	if !ok {
		return false
	}
	switch sub.kind {
	case subAttrExists:
		return true
	case subAttrEq:
		return v == sub.val
	case subAttrIncludes:
		for _, f := range strings.Fields(v) {
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

func (c *simpleSel) matches(n *node) bool {
	if c.tag != "" && (n.css == nil || n.css.name != c.tag) {
		return false
	}
	for i := range c.subs {
		if !c.subs[i].matches(n) {
			return false
		}
	}
	return true
}

// matchAt reports whether n matches comps[..idx]; budget is decremented per
// step and matching fails once it is exhausted.
func (s *selector) matchAt(idx int, n *node, budget *int) bool {
	*budget--
	if *budget < 0 {
		return false
	}
	if !s.comps[idx].matches(n) {
		return false
	}
	if idx == 0 {
		return true
	}
	switch s.comb[idx] {
	case ' ':
		for p := n.parent; p != nil; p = p.parent {
			if s.matchAt(idx-1, p, budget) {
				return true
			}
			if *budget < 0 {
				return false
			}
		}
		return false
	case '>':
		return n.parent != nil && s.matchAt(idx-1, n.parent, budget)
	case '+':
		return n.prevEl() != nil && s.matchAt(idx-1, n.prevEl(), budget)
	}
	return false
}

// ---- application ------------------------------------------------------------

type cssIndex struct {
	byID    map[string][]int
	byClass map[string][]int
	byTag   map[string][]int
	other   []int
}

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
	budget int
	cand   []int
	tmp    []attr
}

// applyCSS parses the document's style sheets and folds the matching
// declarations into every element's style list.
func (doc *document) applyCSS(lim Limits) {
	var rules []cssRule
	total := 0
	for _, src := range doc.css {
		total += len(src)
		if total > lim.MaxStyleBytes {
			break
		}
		rules = parseStyleSheet(src, rules, lim.MaxCSSRules)
	}
	doc.css = nil
	if len(rules) == 0 {
		return
	}
	a := &cssApplier{rules: rules, ix: buildIndex(rules), budget: lim.MaxCSSWork}
	a.visit(doc.root)
}

func (a *cssApplier) visit(n *node) {
	if a.budget > 0 {
		a.apply(n)
	}
	var prev *node
	for _, k := range n.kids {
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

func (a *cssApplier) apply(n *node) {
	a.cand = a.cand[:0]
	if id, ok := n.rawAttr("id"); ok {
		a.cand = append(a.cand, a.ix.byID[id]...)
	}
	if cl, ok := n.rawAttr("class"); ok {
		for _, f := range strings.Fields(cl) {
			a.cand = append(a.cand, a.ix.byClass[f]...)
		}
	}
	if n.css != nil {
		a.cand = append(a.cand, a.ix.byTag[n.css.name]...)
	}
	a.cand = append(a.cand, a.ix.other...)
	if len(a.cand) == 0 {
		return
	}
	a.budget -= len(a.cand)
	// Different class tokens may list one rule twice.
	matched := a.cand[:0:0]
	seen := map[int]bool(nil)
	for _, ri := range a.cand {
		r := &a.rules[ri]
		if a.budget < 0 {
			break
		}
		if !r.sel.matchAt(len(r.sel.comps)-1, n, &a.budget) {
			continue
		}
		if seen == nil {
			seen = map[int]bool{}
		}
		if seen[ri] {
			continue
		}
		seen[ri] = true
		matched = append(matched, ri)
	}
	if len(matched) == 0 {
		return
	}
	sort.SliceStable(matched, func(i, j int) bool {
		x, y := a.rules[matched[i]], a.rules[matched[j]]
		if x.sel.spec != y.sel.spec {
			return lessSpec(x.sel.spec, y.sel.spec)
		}
		return x.order < y.order
	})
	tmp := a.tmp[:0]
	for _, ri := range matched {
		for _, d := range a.rules[ri].decls {
			tmp = appendDecl(tmp, d.name, d.val, d.imp, true)
		}
	}
	for _, s := range n.style {
		tmp = putDecl(tmp, s.id, s.val, s.imp, true)
	}
	a.tmp = tmp
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
