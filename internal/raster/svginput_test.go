package raster

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/testutil"
)

// SVG input is untrusted (docs/security.md): its parse, its style sheets and
// its text layout build structures proportional to it, each charged to the
// call's ledger before it is built, and every loop over it polls the call's
// context (C2, C8, C12, C22, C23, audit 2026-10-18). These guards count, they
// do not time: the ledger's bytes, the work it was charged and the polls of a
// context that counts them.

func ledgerCtx(ctx context.Context, max int64) (context.Context, *budget.Ledger) {
	l := budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: max})
	return budget.WithLedger(ctx, l), l
}

// textChunksSVG is a <text> whose character data is split into n runs by
// unknown empty elements: each run is a node of the tree.
func textChunksSVG(n int) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text display="none">` +
		strings.Repeat("a<b/>", n) + `</text></svg>`
}

// The tree a document parses to is charged as it is built: a run of
// character data is a node as an element is (C8).
func TestParseChargesTheTree(t *testing.T) {
	for _, n := range []int{20_000, 40_000} {
		ctx, l := ledgerCtx(context.Background(), 0)
		if _, err := Render([]byte(textChunksSVG(n)), Options{Context: ctx}); err != nil {
			t.Fatal(err)
		}
		if want := int64(n) * int64(unsafe.Sizeof(node{})); l.PeakBytes() < want {
			t.Errorf("%d runs of text: ledger peak %d bytes, the nodes alone are %d", n, l.PeakBytes(), want)
		}
		if l.Bytes() != 0 {
			t.Errorf("%d bytes left charged after the render", l.Bytes())
		}
	}
	// A ledger that cannot hold the tree refuses the document.
	ctx, _ := ledgerCtx(context.Background(), 1<<20)
	if _, err := Render([]byte(textChunksSVG(40_000)), Options{Context: ctx}); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("a tree over the ledger: err = %v", err)
	}
}

// A run of character data counts against MaxElements (C8).
func TestCharacterDataCountsAsElements(t *testing.T) {
	_, err := Render([]byte(textChunksSVG(2000)), Options{Limits: Limits{MaxElements: 1000}})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("2,000 runs of text under MaxElements 1,000: err = %v", err)
	}
}

// Parsing polls the context as it reads, about every 16 KiB (C22).
func TestParsePollsAsItReads(t *testing.T) {
	svg := textChunksSVG(200_000) // 1 MB
	pc := &pollCtx{Context: context.Background()}
	if _, err := parseDocumentCtx(pc, svg, Limits{}.withDefaults()); err != nil {
		t.Fatal(err)
	}
	if want := len(svg) / (32 << 10); pc.polls < want {
		t.Errorf("parsing %d bytes polled %d times, want at least %d (one every 32 KiB)", len(svg), pc.polls, want)
	}
	// And stops at the poll that finds the context done.
	pc = &pollCtx{Context: context.Background(), stop: 3}
	if _, err := parseDocumentCtx(pc, svg, Limits{}.withDefaults()); !errors.Is(err, context.Canceled) {
		t.Errorf("a context done while parsing: err = %v", err)
	}
}

// fanoutSVG is r rules selecting one class and an element whose class
// lists it k times.
func fanoutSVG(r, k int) string {
	return secHead + `<style>` + strings.Repeat(".a{fill:red}", r) + `</style><rect width="5" height="5" class="` +
		strings.Repeat("a ", k) + `"/></svg>`
}

// An element's class tokens are a set: listing a class k times does not
// match its rules k times, nor list them k times before the bound is
// consulted (C2).
func TestCSSClassTokensAreASet(t *testing.T) {
	lim := Limits{MaxCSSWork: 200_000}
	if _, err := Render([]byte(fanoutSVG(1000, 2000)), Options{Limits: lim}); err != nil {
		t.Fatalf("1,000 rules, a class listed 2,000 times: %v", err)
	}
	work := func(k int) int64 {
		ctx, l := ledgerCtx(context.Background(), 0)
		if _, err := parseDocumentCtx(ctx, fanoutSVG(1000, k), Limits{}.withDefaults()); err != nil {
			t.Fatal(err)
		}
		return l.WorkDone()
	}
	w1, w2 := work(1000), work(2000)
	if w1 == 0 || w2 > w1+w1/2 {
		t.Errorf("CSS work with a class listed 1,000 times: %d units, 2,000 times: %d (want charged, and the tokens a set)", w1, w2)
	}
}

// attrScanSVG is r rules of 32 attribute selectors each and one element
// with n attributes, the selected one last.
func attrScanSVG(r, n int) string {
	var b strings.Builder
	b.WriteString(secHead + `<style>`)
	sel := strings.Repeat("[z]", 32)
	for range r {
		b.WriteString(sel + "{fill:red}")
	}
	b.WriteString(`</style><rect width="5" height="5"`)
	for i := range n {
		fmt.Fprintf(&b, ` q%d=""`, i)
	}
	b.WriteString(` z=""/></svg>`)
	return b.String()
}

// Matching style sheets charges the work it does to the ledger and polls
// the context throughout, within one element too, not once per 256
// elements (C12): its steps are cheap, so a poll comes every few
// microseconds of work. The work of looking an attribute up does not grow
// with the element's attributes (they are indexed once per element).
func TestCSSMatchingPollsWithinAnElement(t *testing.T) {
	type run struct {
		polls int
		work  int64
	}
	measure := func(n int) run {
		pc := &pollCtx{Context: context.Background()}
		ctx, l := ledgerCtx(pc, 0)
		if _, err := parseDocumentCtx(ctx, attrScanSVG(2000, n), Limits{}.withDefaults()); err != nil {
			t.Fatal(err)
		}
		return run{pc.polls, l.WorkDone()}
	}
	a, b := measure(1000), measure(2000)
	if a.polls < 20 {
		t.Errorf("2,000 rules of 32 attribute selectors on one element: %d polls, want at least 20", a.polls)
	}
	// 64,000 lookups of [z] whatever the attributes; indexing the element
	// is linear in them.
	if d := b.work - a.work; a.work == 0 || d > a.work/2 {
		t.Errorf("CSS work with 1,000 attributes: %d units, with 2,000: %d", a.work, b.work)
	}
	// Stopped by the context within the element.
	pc := &pollCtx{Context: context.Background(), stop: 5}
	if _, err := parseDocumentCtx(pc, attrScanSVG(2000, 1000), Limits{}.withDefaults()); !errors.Is(err, context.Canceled) {
		t.Errorf("a context done while matching: err = %v", err)
	}
}

// The structures style sheets build (rules, their index, an element's
// attribute index and the declarations it is given) are charged to the
// ledger.
func TestCSSStructuresCharged(t *testing.T) {
	var b strings.Builder
	b.WriteString(secHead + `<style>`)
	for i := range 5000 {
		fmt.Fprintf(&b, ".c%d{fill:red;stroke:blue}", i)
	}
	b.WriteString(`</style>`)
	for i := range 5000 {
		fmt.Fprintf(&b, `<rect class="c%d" width="1" height="1"/>`, i)
	}
	b.WriteString(`</svg>`)
	ctx, l := ledgerCtx(context.Background(), 0)
	doc, err := parseDocumentCtx(ctx, b.String(), Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if doc.root == nil {
		t.Fatal("no root")
	}
	// 5,000 rules (a selector, a compound, a sub-selector and two
	// declarations each) and 5,000 elements given two declarations.
	if want := int64(5000 * (unsafe.Sizeof(cssRule{}) + unsafe.Sizeof(selector{}) + 2*unsafe.Sizeof(cssDecl{}) + 2*unsafe.Sizeof(attr{}))); l.Bytes() < want {
		t.Errorf("ledger holds %d bytes after parsing, want at least %d for the style sheet and what it gave", l.Bytes(), want)
	}
}

// Text layout charges the characters and glyphs it holds (C23).
func TestTextLayoutCharged(t *testing.T) {
	n := 50_000
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text x="` +
		strings.Repeat("0 ", n) + `" font-size="1" fill="none" stroke="none">` + strings.Repeat("a", n) + `</text>` +
		`<text x="` + strings.Repeat("0 ", n) + `" font-size="1">` + strings.Repeat("a", n) + `</text></svg>`
	ctx, l := ledgerCtx(context.Background(), 0)
	if _, err := Render([]byte(svg), Options{Context: ctx}); err != nil {
		t.Fatal(err)
	}
	if want := int64(n) * int64(unsafe.Sizeof(tchar{})+unsafe.Sizeof(glyphPos{})); l.PeakBytes() < want {
		t.Errorf("%d characters laid out: ledger peak %d bytes, want at least %d", n, l.PeakBytes(), want)
	}
	if l.Bytes() != 0 {
		t.Errorf("%d bytes left charged after the render", l.Bytes())
	}
}

// allocated is the bytes render allocates, garbage included.
func allocated(t *testing.T, svg string) uint64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := Render([]byte(svg), Options{}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// perUse is what each more <use> of one element costs in allocation, the
// difference between n and 2n uses: what drawing the element again builds.
func perUse(t *testing.T, defs, use string, n int) uint64 {
	t.Helper()
	doc := func(n int) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><defs>` + defs + `</defs>` + strings.Repeat(use, n) + `</svg>`
	}
	allocated(t, doc(n)) // warm the shared caches
	a, b := allocated(t, doc(n)), allocated(t, doc(2*n))
	if b < a {
		return 0
	}
	return (b - a) / uint64(n)
}

// What a document's attributes make the renderer build each time an
// element is drawn does not grow with the attribute: a gradient's stops are
// parsed once (not once per element painted with it), and long lists in
// attributes (dashes, decorations, paint order, languages, aspect ratio,
// font families) are not split whole for each element that reads them.
func TestDrawingAnElementAgainBuildsNoAttribute(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocations are not measured under -race")
	}
	stops := strings.Repeat(`<stop offset="0.5" stop-color="red"/>`, 4000)
	long := strings.Repeat("x ", 50_000) // 100 KB
	fams := strings.Repeat("nofont,", 20_000)
	for name, c := range map[string]struct{ defs, use string }{
		"gradient stops": {`<linearGradient id="g">` + stops + `</linearGradient><rect id="r" width="20" height="20" fill="url(#g)"/>`, `<use href="#r"/>`},
		"dash array":     {`<path id="r" d="M0 0H20" stroke="black" stroke-dasharray="` + strings.Repeat("1 ", 50_000) + `"/>`, `<use href="#r"/>`},
		"decoration":     {`<g id="r" text-decoration="` + long + `" paint-order="` + long + `"/>`, `<use href="#r"/>`},
		"languages":      {`<switch id="r"><rect width="1" height="1" systemLanguage="` + strings.Repeat("fr,", 30_000) + `"/></switch>`, `<use href="#r"/>`},
		"aspect ratio":   {`<svg id="r" viewBox="0 0 1 1" width="1" height="1" preserveAspectRatio="xMinYMin meet ` + long + `"/>`, `<use href="#r"/>`},
	} {
		if got := perUse(t, c.defs, c.use, 100); got > 16<<10 {
			t.Errorf("%s: drawing the element again allocates %d bytes", name, got)
		}
	}
	// A text of 64 runs: a long font-family list costs what a short one
	// does (it was split for every element drawn, and made into the CSS of
	// every run).
	text := func(fams string) string {
		return `<text id="r" font-family="` + fams + `" x="` + strings.Repeat("0 ", 64) + `">` + strings.Repeat("a", 64) + `</text>`
	}
	few, many := perUse(t, text("nofont"), `<use href="#r"/>`, 100), perUse(t, text(fams), `<use href="#r"/>`, 100)
	if many > few+16<<10 {
		t.Errorf("font families: drawing a text again allocates %d bytes with a long list, %d with a short one", many, few)
	}
}

// An element of many attributes costs a scan of them for each one its
// state reads, every time it is drawn: drawing one polls the context, not
// every sixteenth element drawn.
func TestAttributeHeavyElementsPoll(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><defs><g id="r"` +
		strings.Repeat(` x="0"`, 10_000) + `/></defs>` + strings.Repeat(`<use href="#r"/>`, 64) + `</svg>`
	pc := &pollCtx{Context: context.Background()}
	if _, err := Render([]byte(svg), Options{Context: pc}); err != nil {
		t.Fatal(err)
	}
	if pc.polls < 64 {
		t.Errorf("64 uses of an element of 10,000 attributes: %d polls, want one each at least", pc.polls)
	}
}

// An SVG glyph document (a font is untrusted input too) is charged to the
// ledger as it is inflated and parsed, and kept charged while the render
// keeps it; one past what the ledger has left fails the render.
func TestSVGGlyphDocumentCharged(t *testing.T) {
	var src strings.Builder
	src.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"><g id="glyph1">`)
	src.WriteString(strings.Repeat(`<g/>`, 100_000))
	src.WriteString(`</g></svg>`)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(src.String()))
	zw.Close()
	data := gz.Bytes() // about 1 KB, inflating to 400 KB and 100,000 nodes
	run := func(max int64) (*renderer, int64) {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: max})
		r := &renderer{lim: Limits{}.withDefaults(), ledger: l, active: map[*node]bool{}}
		r.initCaches()
		doc, release := r.svgGlyphDoc(data)
		if doc != nil && release != nil {
			release()
		}
		held := l.Bytes()
		r.dropCaches()
		if l.Bytes() != 0 {
			t.Errorf("%d bytes left charged once the caches are dropped", l.Bytes())
		}
		return r, held
	}
	r, held := run(0)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := int64(100_000) * int64(unsafe.Sizeof(node{})); held < want {
		t.Errorf("the kept document is charged %d bytes, its nodes alone are %d", held, want)
	}
	if r, _ := run(1 << 20); !errors.Is(r.err, budget.ErrLimit) {
		t.Errorf("a document past the ledger: err = %v", r.err)
	}
}
