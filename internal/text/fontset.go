package text

import (
	"strings"
	"unsafe"
	"weak"
)

// entry is a registered font: a loaded face, or a system font not yet read.
type entry struct {
	family string
	norm   string
	weight int
	italic bool

	face   *Face
	names  []string // every family name of a system face (see familyNames)
	path   string   // system font file, when face is nil
	index  int      // the face's index in path, a collection
	dead   bool     // loading path failed; never retry
	custom bool     // given with WithFont
	// lists are the font lists a system entry is a candidate of, which may
	// remember its face (see keepSystemFace); weak, so that a list the
	// cache drops goes. swept is their number after the last sweep.
	lists []weak.Pointer[faceList]
	swept int
}

// listed records that list l has e among its candidates. The weak pointers
// of lists gone are swept out as they double, so they stay within twice
// the lists alive.
func (e *entry) listed(l *faceList) {
	e.lists = append(e.lists, weak.Make(l))
	if len(e.lists) <= 2*max(e.swept, 8) {
		return
	}
	alive := e.lists[:0]
	for _, w := range e.lists {
		if w.Value() != nil {
			alive = append(alive, w)
		}
	}
	clear(e.lists[len(alive):])
	e.lists, e.swept = alive, len(alive)
}

// load returns the entry's face, reading a system font on first use.
func (e *entry) load(m *Measurer) *Face {
	if e.face != nil || e.dead {
		return e.face
	}
	f, err := loadSystemFace(m, e)
	if err != nil {
		e.dead = true
		return nil
	}
	m.applyPalette(f, e.names...)
	e.face = f
	m.keepSystemFace(e)
	return f
}

// maxSystemFaces bounds the system font faces a Measurer keeps loaded. A
// specification chooses which families it names, and so how many of the
// machine's fonts are loaded, each holding about a megabyte of parsed
// tables for as long as the Measurer lives (X5): past the bound the face
// loaded longest ago is dropped, to be loaded again when named again.
const maxSystemFaces = 64

// keepSystemFace records that e's system face was loaded, dropping the
// oldest loaded one past maxSystemFaces. The font lists it is a candidate
// of, the only ones that can remember its face by rune, forget what they
// resolved, so that nothing the Measurer keeps holds a dropped face; the
// other lists stay as they are (C113: clearing them all rebuilt every list
// on every eviction). m.mu is held.
func (m *Measurer) keepSystemFace(e *entry) {
	m.sysLoaded = append(m.sysLoaded, e)
	if len(m.sysLoaded) <= maxSystemFaces {
		return
	}
	old := m.sysLoaded[0]
	m.sysLoaded = append(m.sysLoaded[:0], m.sysLoaded[1:]...)
	old.face = nil
	for _, w := range old.lists {
		if l := w.Value(); l != nil {
			l.forget()
			m.listResets++
		}
	}
	old.lists, old.swept = nil, 0
}

// forget drops what the list resolved runes to.
func (l *faceList) forget() {
	l.ascii, l.seen = [128]*Face{}, [128]bool{}
	clear(l.runes)
}

// bestMatch picks, among candidates, the entries closest to the requested
// weight and style by the CSS Fonts font-style-matching rules (style first,
// then weight), keeping their order.
func bestMatch(cands []*entry, weight int, italic bool) []*entry {
	if len(cands) == 0 {
		return nil
	}
	// Style: the requested one if any candidate has it; otherwise the other.
	// (Italic and oblique are one style here.)
	style := italic
	has := false
	for _, e := range cands {
		if e.italic == style {
			has = true
			break
		}
	}
	if !has {
		style = !style
	}
	var byStyle []*entry
	for _, e := range cands {
		if e.italic == style {
			byStyle = append(byStyle, e)
		}
	}

	w := matchWeight(byStyle, weight)
	var out []*entry
	for _, e := range byStyle {
		if e.weight == w {
			out = append(out, e)
		}
	}
	return out
}

// matchWeight implements CSS Fonts 4 section 5.2's weight matching: exact,
// else for 400..500 the nearest heavier up to 500 then lighter then heavier,
// for below 400 lighter then heavier, above 500 heavier then lighter.
func matchWeight(cands []*entry, query int) int {
	var fatter, thinner int
	for _, e := range cands {
		w := e.weight
		switch {
		case w == query:
			return query
		case w > query:
			if fatter == 0 || w-query < fatter-query {
				fatter = w
			}
		default:
			if query-w < query-thinner {
				thinner = w
			}
		}
	}
	switch {
	case query >= 400 && query <= 500:
		if fatter != 0 && fatter <= 500 {
			return fatter
		}
		if thinner != 0 {
			return thinner
		}
		return fatter
	case query < 400:
		if thinner != 0 {
			return thinner
		}
		return fatter
	default:
		if fatter != 0 {
			return fatter
		}
		return thinner
	}
}

// aliases are the metric-compatible substitutions tried after the requested
// families, so that "Arial" or "Times New Roman" reach the embedded
// Liberation faces even when the configured default family is something else.
var aliases = map[string][]string{
	"arial":              {"arimo", "liberationsans"},
	"helvetica":          {"arimo", "liberationsans"},
	"arimo":              {"liberationsans"},
	"sans-serif":         {"liberationsans"},
	"sansserif":          {"liberationsans"},
	"times":              {"tinos", "liberationserif"},
	"timesnewroman":      {"tinos", "liberationserif"},
	"tinos":              {"liberationserif"},
	"serif":              {"liberationserif"},
	"couriernew":         {"cousine", "liberationmono"},
	"courier":            {"cousine", "liberationmono"},
	"cousine":            {"liberationmono"},
	"monospace":          {"liberationmono"},
	"ui-monospace":       {"liberationmono"},
	"system-ui":          {"liberationsans"},
	"ui-sans-serif":      {"liberationsans"},
	"ui-serif":           {"liberationserif"},
	"-apple-system":      {"liberationsans"},
	"blinkmacsystemfont": {"liberationsans"},
}

type listKey struct {
	families string // families joined with NUL
	weight   int
	italic   bool
}

// faceList is the ordered candidate faces for one (families, weight, style)
// query, plus a memo of which face each rune resolves to.
type faceList struct {
	key   listKey
	cands []*entry
	ascii [128]*Face
	seen  [128]bool
	runes map[rune]*Face
}

// runeMemoBytes is what one rune of a faceList's memo holds in its map.
const runeMemoBytes = 32

// size is the bytes l holds, for the cache: its key, the list, its candidate
// faces and its rune memo.
func (l *faceList) size() int64 {
	return int64(len(l.key.families)) + int64(unsafe.Sizeof(*l)) + 8*int64(len(l.cands)) + runeMemoBytes*int64(len(l.runes))
}

// faces builds (or recalls) the candidate list for a parsed CSS font.
//
// Order, mirroring the reference resolver: one best-matching face per
// requested family (a generic family such as "sans-serif" or "serif" first
// redirected, where it stands, to its configured concrete family), the configured
// default family, then metric-compatible aliases of everything requested,
// then the fallback chain. By default that is every registered face that
// fits the weight and style, and then every registered face at all; of
// those, the ones given with WithFont come first, the latest first, as they
// take priority over the embedded ones: a colour emoji font given so draws
// the emoji the families asked for do not have, rather than the embedded
// monochrome Noto Emoji. WithFallbackFamilies replaces it with its families.
func (m *Measurer) faces(css CSSFont) *faceList {
	key := listKey{strings.Join(css.Family, "\x00"), css.Weight, css.Italic}
	if l, ok := m.listCache.Get(key); ok {
		return l
	}
	families := make([]string, 0, len(css.Family)+4)
	for _, fam := range css.Family {
		// A generic family always matches, where it stands in the list
		// (CSS Fonts 4, 5.1): the families after it draw only what its
		// font lacks (C112).
		if g := m.generic(fam); g != "" {
			families = append(families, g)
		}
		families = append(families, fam)
	}
	families = append(families, m.fallbackFamily, "sans-serif")

	l := &faceList{key: key}
	seen := map[*entry]bool{}
	push := func(es []*entry) {
		for _, e := range es {
			if !seen[e] {
				seen[e] = true
				l.cands = append(l.cands, e)
			}
		}
	}
	// Exact families: only the single best-matching face of each.
	norm := make([]string, len(families))
	for i, fam := range families {
		norm[i] = normFamily(fam)
		if es := bestMatch(m.familyEntries(norm[i]), css.Weight, css.Italic); len(es) > 0 {
			push(es[:1])
		}
	}
	// Substitutions.
	for _, n := range norm {
		for _, a := range aliases[n] {
			if es := bestMatch(m.familyEntries(a), css.Weight, css.Italic); len(es) > 0 {
				push(es[:1])
			}
		}
	}
	// The fallback chain: WithFallbackFamilies' families in order, the best
	// fit of each first, then their other faces; by default any registered
	// face fitting the weight and style, then any at all.
	if m.fallbackOrder != nil {
		for _, es := range m.fallbackOrder {
			push(bestMatch(es, css.Weight, css.Italic))
		}
		for _, es := range m.fallbackOrder {
			push(es)
		}
	} else {
		push(bestMatch(m.fallback, css.Weight, css.Italic))
		push(m.fallback)
	}

	for _, e := range l.cands {
		if e.path != "" {
			e.listed(l)
		}
	}
	m.listCache.Put(key, l, l.size())
	return l
}

// generic is the configured family a CSS generic family names, or "" for a
// family that is not one. system-ui and the ui- families are the platform's
// interface fonts, which here are the configured ones; cursive and fantasy
// have no bundled face and draw in the sans-serif default.
func (m *Measurer) generic(fam string) string {
	switch ASCIILower(fam) {
	case "serif", "ui-serif":
		return m.serifFamily
	case "monospace", "ui-monospace":
		return m.monospaceFamily
	case "sans-serif", "system-ui", "ui-sans-serif", "cursive", "fantasy":
		return m.fallbackFamily
	}
	return ""
}

// familyEntries returns the faces of a normalised family: those given with
// WithFont and its kin, the latest first, when there are any (they make the
// family alone, as @font-face rules do); otherwise the embedded ones, then
// system fonts.
// hasFamily reports whether family norm has a face: registered, embedded or,
// with system fonts, installed. It loads none.
func (m *Measurer) hasFamily(norm string) bool {
	if len(m.own[norm]) > 0 || len(m.byFam[norm]) > 0 {
		return true
	}
	return m.system != nil && len(m.system.byFam[norm]) > 0
}

func (m *Measurer) familyEntries(norm string) []*entry {
	if own := m.own[norm]; len(own) > 0 {
		return own
	}
	es := m.byFam[norm]
	if m.system != nil {
		if sys := m.system.byFam[norm]; len(sys) > 0 {
			es = append(es[:len(es):len(es)], m.systemEntries(sys)...)
		}
	}
	return es
}

// resolve returns the first candidate face covering r. When none does, the
// first registered face is returned, whose .notdef glyph is what gets drawn.
func (l *faceList) resolve(m *Measurer, r rune) *Face {
	if r >= 0 && r < 128 {
		if l.seen[r] {
			return l.ascii[r]
		}
		f := l.lookup(m, r)
		l.ascii[r], l.seen[r] = f, true
		return f
	}
	if f, ok := l.runes[r]; ok {
		return f
	}
	f := l.lookup(m, r)
	if l.runes == nil {
		l.runes = make(map[rune]*Face)
	}
	if n := len(l.runes); n < maxCacheItems {
		l.runes[r] = f
		// The memo grows the list in the cache: its size is charged again
		// as it doubles, so a list never holds more than twice what the
		// cache counts. A list the cache has dropped meanwhile goes back.
		if n+1 >= 64 && (n+1)&n == 0 {
			m.listCache.Put(l.key, l, l.size())
		}
	}
	return f
}

func (l *faceList) lookup(m *Measurer, r rune) *Face {
	for _, e := range l.cands {
		if f := e.load(m); f != nil && f.HasRune(r) {
			return f
		}
	}
	return m.first
}
