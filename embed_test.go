package aster_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster"
)

func sortedGIDs(g []uint16) []uint16 {
	out := append([]uint16(nil), g...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestSVGToPDFUsageAndSubset covers the file-level embedding exports: named-mode
// rendering reports per-face glyph usage, and SubsetFont turns a face plus a GID
// set into a smaller program that still parses and preserves those glyphs.
func TestSVGToPDFUsageAndSubset(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	pdf, uses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatalf("VegaLiteToPDFUsage: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatal("empty PDF")
	}
	if len(uses) == 0 {
		t.Fatal("no font usage reported; expected at least one text face")
	}

	for _, u := range uses {
		if u.PostScriptName == "" {
			t.Errorf("usage with empty PostScriptName")
		}
		if len(u.Source) == 0 {
			t.Errorf("%s: empty Source bytes", u.PostScriptName)
		}
		if len(u.GIDs) == 0 {
			t.Errorf("%s: no GIDs reported", u.PostScriptName)
		}
		if !sort.SliceIsSorted(u.GIDs, func(i, j int) bool { return u.GIDs[i] < u.GIDs[j] }) {
			t.Errorf("%s: GIDs not sorted ascending", u.PostScriptName)
		}

		subset, ps, err := aster.SubsetFont(u.Source, u.GIDs)
		if err != nil {
			t.Fatalf("SubsetFont(%s): %v", u.PostScriptName, err)
		}
		if ps != u.PostScriptName {
			t.Errorf("SubsetFont PostScriptName = %q, usage = %q", ps, u.PostScriptName)
		}
		if len(subset) == 0 {
			t.Errorf("%s: empty subset", u.PostScriptName)
		}
		if len(subset) >= len(u.Source) {
			t.Errorf("%s: subset (%d B) not smaller than source (%d B)", u.PostScriptName, len(subset), len(u.Source))
		}

		// The subset must itself be a valid font that still preserves the used
		// glyphs: subsetting it again to the same GIDs must succeed.
		if _, _, err := aster.SubsetFont(subset, u.GIDs); err != nil {
			t.Errorf("%s: subset is not a valid re-subsettable font: %v", u.PostScriptName, err)
		}
	}
}

// TestPDFUsageOutlineModeEmpty checks that requesting usage in outline mode is
// safe and reports nothing: outline output references no fonts.
func TestPDFUsageOutlineModeEmpty(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	pdf, uses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextOutlines))
	if err != nil {
		t.Fatalf("VegaLiteToPDFUsage(outlines): %v", err)
	}
	if len(pdf) == 0 {
		t.Fatal("empty PDF")
	}
	if len(uses) != 0 {
		t.Errorf("outline mode reported %d font usages, want 0", len(uses))
	}
}

// TestPDFUsageModeInvariance checks that reported glyph usage is the same
// whether text is embedded or named: the glyphs a chart draws do not depend on
// how the font is stored, so a named render's usage is a faithful basis for the
// subset an embed render would have produced.
func TestPDFUsageModeInvariance(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	_, embedUses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextEmbed))
	if err != nil {
		t.Fatal(err)
	}
	_, namedUses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatal(err)
	}

	byName := func(us []aster.FontUsage) map[string][]uint16 {
		m := map[string][]uint16{}
		for _, u := range us {
			m[u.PostScriptName] = sortedGIDs(u.GIDs)
		}
		return m
	}
	em, nm := byName(embedUses), byName(namedUses)
	if len(em) != len(nm) {
		t.Fatalf("face count differs: embed %d, named %d", len(em), len(nm))
	}
	for ps, eg := range em {
		ng, ok := nm[ps]
		if !ok {
			t.Errorf("face %s missing from named usage", ps)
			continue
		}
		if len(eg) != len(ng) {
			t.Errorf("%s: glyph count differs embed %d vs named %d", ps, len(eg), len(ng))
			continue
		}
		for i := range eg {
			if eg[i] != ng[i] {
				t.Errorf("%s: glyph set differs at %d: embed %d, named %d", ps, i, eg[i], ng[i])
				break
			}
		}
	}
}

// TestFontUsageSourceIsACopy pins the ownership contract: mutating a returned
// Source must not affect any other converter — the bytes behind built-in faces
// are process-global embed data, so handing out the internal slice would let
// one caller silently corrupt font handling process-wide.
func TestFontUsageSourceIsACopy(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="50">` +
		`<text transform="translate(10,30)" font-size="20">copy probe</text></svg>`

	c1, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	_, uses1, err := c1.SVGToPDFUsage(svg, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatal(err)
	}
	if len(uses1) == 0 {
		t.Fatal("no usage")
	}

	// Vandalize the returned copy, then render again from a fresh converter:
	// its Source must be pristine.
	for i := range uses1[0].Source {
		uses1[0].Source[i] = 0
	}

	c2, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	_, uses2, err := c2.SVGToPDFUsage(svg, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatalf("render after mutating a previous Source: %v", err)
	}
	if len(uses2) == 0 || len(uses2[0].Source) == 0 {
		t.Fatal("no usage from second converter")
	}
	if uses2[0].Source[0] == 0 && uses2[0].Source[1] == 0 && uses2[0].Source[2] == 0 && uses2[0].Source[3] == 0 {
		t.Fatal("second converter's Source reflects the mutation: bytes are shared, not copied")
	}
}

// outline is a glyph's outline as forme reads it, or "" for none.
func outline(t *testing.T, f *shape.Face, gid int) string {
	t.Helper()
	var b strings.Builder
	if err := f.GlyphOutline(gid, func(s shape.Segment) bool {
		fmt.Fprintf(&b, "%d %v;", s.Op, s.Pts)
		return true
	}); err != nil {
		t.Fatalf("glyph %d: %v", gid, err)
	}
	return b.String()
}

// TestSubsetFontKeepsEachGlyphUnderItsNumber: a PDFTextNamed chart shows each
// glyph by its index in the source font, so the program SubsetFont returns
// must draw every glyph the chart used, accented letters made of components
// included, under the index it has in the source, and leave out the rest.
func TestSubsetFontKeepsEachGlyphUnderItsNumber(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="40"><text x="5" y="30" font-size="20">Àéñ ÅÇ fi 0123</text></svg>`
	_, uses, err := c.SVGToPDFUsage(svg, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 1 {
		t.Fatalf("%d faces used, want 1", len(uses))
	}
	u := uses[0]
	sub, _, err := aster.SubsetFont(u.Source, u.GIDs)
	if err != nil {
		t.Fatal(err)
	}
	src, err := shape.Load(u.Source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := shape.Load(sub)
	if err != nil {
		t.Fatalf("the subset is not a font forme reads: %v", err)
	}
	if got.NumGlyphs() != src.NumGlyphs() {
		t.Errorf("the subset has %d glyph slots, the source %d", got.NumGlyphs(), src.NumGlyphs())
	}
	used := map[int]bool{}
	for _, g := range u.GIDs {
		used[int(g)] = true
		want := outline(t, src, int(g))
		if want == "" && g != 0 {
			continue // a space
		}
		if have := outline(t, got, int(g)); have != want {
			t.Errorf("glyph %d is drawn differently in the subset", g)
		}
	}
	z, _ := src.GlyphID('Z')
	if used[z] {
		t.Fatal("Z is used: pick another glyph to leave out")
	}
	if outline(t, got, z) != "" {
		t.Errorf("the subset keeps glyph %d (Z), which nothing used", z)
	}
}

// TestSubsetFontCallsAreIndependent: a subset of an embedded font starts from
// the parse made once per process, and the glyphs one call keeps must not be
// kept by the next.
func TestSubsetFontCallsAreIndependent(t *testing.T) {
	src, err := os.ReadFile("internal/fonts/liberation/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	face, err := shape.Load(src)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := face.GlyphID('Q')
	a, _ := face.GlyphID('a')
	if _, _, err := aster.SubsetFont(src, []uint16{uint16(q)}); err != nil {
		t.Fatal(err)
	}
	sub, _, err := aster.SubsetFont(src, []uint16{uint16(a)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := shape.Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if outline(t, got, q) != "" {
		t.Error("the second subset keeps the glyph only the first asked for")
	}
	if outline(t, got, a) == "" {
		t.Error("the second subset lacks the glyph it asked for")
	}
}
