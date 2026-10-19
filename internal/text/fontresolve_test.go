package text

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"weak"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/aster/internal/testutil"
)

// TestGenericFamiliesResolveInPlace is C112 (audit 2026-10-18): a generic
// family always matches where it stands in the list (CSS Fonts 4, 5.1), so
// families after it draw only what its font lacks. Every generic this
// package maps is checked in the middle of a list, after a family that does
// not exist and before one that does.
func TestGenericFamiliesResolveInPlace(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for generic, want := range map[string]string{
		"sans-serif":    "Liberation Sans",
		"system-ui":     "Liberation Sans",
		"ui-sans-serif": "Liberation Sans",
		"cursive":       "Liberation Sans",
		"fantasy":       "Liberation Sans",
		"serif":         "Liberation Serif",
		"ui-serif":      "Liberation Serif",
		"monospace":     "Liberation Mono",
		"ui-monospace":  "Liberation Mono",
	} {
		after := "'Liberation Serif'"
		if want == "Liberation Serif" {
			after = "'Liberation Mono'"
		}
		for _, list := range []string{generic + ", " + after, "NoSuchFont, " + generic + ", " + after} {
			font := "12px " + list
			runs, _ := m.ShapeText("Hi", font)
			if len(runs) != 1 || runs[0].Face.Family != want {
				t.Errorf("%q: drawn in %q, want %q", font, runs[0].Face.Family, want)
			}
			// The parts of a font (what PNG and PDF hand over) resolve alike.
			css := ParseCSSFont(font)
			if runs, _ := m.ShapeFont("Hi", css); len(runs) != 1 || runs[0].Face.Family != want {
				t.Errorf("ShapeFont %v: drawn in %q, want %q", css.Family, runs[0].Face.Family, want)
			}
		}
	}
	// A character the generic's font lacks falls through to the families
	// after it, as CSS's per-character matching does.
	runs, _ := m.ShapeText("A\U0001F600", "12px sans-serif, 'Noto Emoji'")
	if len(runs) != 2 || runs[0].Face.Family != "Liberation Sans" || runs[1].Face.Family != "Noto Emoji" {
		t.Errorf("fallback past a generic: %+v", runs)
	}
}

// sysCollection writes a font collection of n faces of Liberation Sans, the
// face i named family(i), into dir, and returns them as a system index.
func sysCollection(t testing.TB, dir string, n int, family func(int) string) *systemIndex {
	t.Helper()
	faces := make([][]testutil.NameRecord, n)
	for i := range faces {
		faces[i] = []testutil.NameRecord{{Platform: 3, Encoding: 1, Language: 0x409, ID: 1, Value: family(i)}}
	}
	p := filepath.Join(dir, "faces.ttc")
	if err := os.WriteFile(p, testutil.FontCollection(liberation.SansRegular, faces), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := &systemIndex{byFam: map[string][]sysFont{}}
	idx.add(readSysFonts(p))
	if len(idx.byFam) != n {
		t.Fatalf("%d families indexed of %d", len(idx.byFam), n)
	}
	return idx
}

// TestEvictedSystemFacesAreReleased is C113 and C27's text half (audit
// 2026-10-18): a system face the Measurer drops past maxSystemFaces is
// reachable from nothing it keeps, the program FontData cut out of it (as a
// PDF render asks) included, and what is kept for a face (Face.Annex, the
// rasterizer's per-face state) goes with it. A face named again finds its
// program in the cache rather than cutting another.
func TestEvictedSystemFacesAreReleased(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	n := maxSystemFaces + 8
	m.system = sysCollection(t, t.TempDir(), n, func(i int) string { return fmt.Sprintf("Evict %d", i) })
	type annexed struct{ face *Face }
	var first weak.Pointer[Face]
	var firstAnnex weak.Pointer[annexed]
	cycle := func() {
		for i := range n {
			runs, _ := m.ShapeFont("Hi", CSSFont{Weight: 400, Size: 12, Family: []string{fmt.Sprintf("Evict %d", i)}})
			f := runs[0].Face
			if !f.mapped {
				t.Fatalf("family %d drawn in %s, not its system face", i, f.ID)
			}
			if _, err := m.FontData(context.Background(), f); err != nil {
				t.Fatal(err)
			}
			a := f.Annex("test", func() any { return &annexed{f} }).(*annexed)
			if a.face != f || f.Annex("test", func() any { return nil }) != any(a) {
				t.Fatal("Annex does not keep one value per face")
			}
			if i == 0 && first.Value() == nil {
				first, firstAnnex = weak.Make(f), weak.Make(a)
			}
		}
	}
	cycle()
	programs := m.programs.Len()
	for range 3 {
		runtime.GC()
	}
	if first.Value() != nil || firstAnnex.Value() != nil {
		t.Errorf("a system face dropped past the bound of %d is still reachable (face %v, annex %v): its memory stays", maxSystemFaces, first.Value() != nil, firstAnnex.Value() != nil)
	}
	cycle()
	if got := m.programs.Len(); got != programs {
		t.Errorf("naming the faces again cut %d more programs: the cache is keyed by the face, not by what it is", got-programs)
	}
}

// TestSystemFontEveryFamilyName is C45 (audit 2026-10-18): a system font is
// found under every family name it states, as fontconfig and browsers find
// it: its family (name ID 1, the legacy one, such as "Calibri Light"), its
// typographic family (16) and its WWS family (21), in every language. The
// face's own family is the English (United States) typographic one.
func TestSystemFontEveryFamilyName(t *testing.T) {
	recs := []testutil.NameRecord{
		{Platform: 3, Encoding: 1, Language: 0x404, ID: 1, Value: "微軟正黑體 Light"},
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 1, Value: "Acme Light"},
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 2, Value: "Regular"},
		{Platform: 3, Encoding: 1, Language: 0x404, ID: 16, Value: "微軟正黑體"},
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 16, Value: "Acme"},
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 21, Value: "Acme WWS"},
		{Platform: 1, Encoding: 0, Language: 0, ID: 1, Value: "Acme Mac"},
		// A legacy code page record is not UTF-16: never read as a name.
		{Platform: 3, Encoding: 2, Language: 0x411, ID: 1, Value: "Garbage"},
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "acme.ttf")
	if err := os.WriteFile(p, testutil.FontWithNames(liberation.SansRegular, recs), 0o600); err != nil {
		t.Fatal(err)
	}
	fonts := readSysFonts(p)
	if len(fonts) != 1 {
		t.Fatalf("faces read: %+v", fonts)
	}
	if fonts[0].family != "Acme" {
		t.Errorf("family %q, want the en-US typographic family %q", fonts[0].family, "Acme")
	}
	want := []string{"Acme", "Acme Light", "Acme Mac", "Acme WWS", "微軟正黑體", "微軟正黑體 Light"}
	got := slices.Clone(fonts[0].names)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("names %q, want %q", got, want)
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	idx := &systemIndex{byFam: map[string][]sysFont{}}
	idx.add(fonts)
	m.system = idx
	var face *Face
	for _, fam := range want {
		runs, _ := m.ShapeText("A", "12px '"+fam+"'")
		if len(runs) != 1 || !runs[0].Face.mapped {
			t.Errorf("%q: not the system face (%s)", fam, runs[0].Face.ID)
			continue
		}
		if face == nil {
			face = runs[0].Face
		} else if runs[0].Face != face {
			t.Errorf("%q: another face than the other names reach", fam)
		}
	}
	if runs, _ := m.ShapeText("A", "12px Garbage"); runs[0].Face.mapped {
		t.Error("a code page record was read as UTF-16")
	}
}

// TestInstanceWeightAndStyle is C160 (audit 2026-10-18): a variable font's
// instance answers to the weight and style it is drawn at, the axis value
// clamped to the font's range, and a slant (slnt) other than zero is an
// oblique style.
func TestInstanceWeightAndStyle(t *testing.T) {
	axes := []shape.Axis{{Tag: "wght", Min: 100, Default: 400, Max: 900}, {Tag: "slnt", Min: -12, Default: 0, Max: 0}, {Tag: "ital", Min: 0, Default: 0, Max: 1}}
	// The instance's own OS/2 says 400, upright: what an axis not named
	// leaves.
	for _, c := range []struct {
		req    map[string]float64
		weight int
		italic bool
	}{
		{map[string]float64{"wght": 50}, 100, false},
		{map[string]float64{"wght": 2000}, 900, false},
		{map[string]float64{"wght": 650.4}, 650, false},
		{map[string]float64{"slnt": -8}, 400, true},
		{map[string]float64{"slnt": 5}, 400, false}, // clamped to 0: upright
		{map[string]float64{"ital": 0.7}, 400, true},
		{map[string]float64{"ital": 2, "wght": 300}, 300, true},
	} {
		w, italic := instanceStyle(c.req, axes, 400, false)
		if w != c.weight || italic != c.italic {
			t.Errorf("%v: weight %d italic %v, want %d %v", c.req, w, italic, c.weight, c.italic)
		}
	}
	data, err := os.ReadFile("../../testdata/colourfonts/VarTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(WithFontInstance("V", data, map[string]float64{"wght": 50}))
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := m.ShapeText("A", "12px V")
	if f := runs[0].Face; f.Family != "V" || f.Weight != 100 {
		t.Errorf("instance at wght 50 of a 100..900 font answers to weight %d, drawn at 100", f.Weight)
	}
}

// TestPaletteByEveryFamilyName is C45's class for WithFontPalette (audit
// 2026-10-18): a system face takes the palette named for any family name it
// is indexed under, whichever name a spec reaches it by.
func TestPaletteByEveryFamilyName(t *testing.T) {
	recs := []testutil.NameRecord{
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 16, Value: "Acme"},
		{Platform: 3, Encoding: 1, Language: 0x404, ID: 16, Value: "微軟正黑體"},
		{Platform: 3, Encoding: 1, Language: 0x409, ID: 1, Value: "Acme Light"},
	}
	p := filepath.Join(t.TempDir(), "acme.ttf")
	if err := os.WriteFile(p, testutil.FontWithNames(liberation.SansRegular, recs), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, named := range []string{"微軟正黑體", "Acme Light", "Acme"} {
		m, err := New(WithFontPalette(named, 2))
		if err != nil {
			t.Fatal(err)
		}
		idx := &systemIndex{byFam: map[string][]sysFont{}}
		idx.add(readSysFonts(p))
		m.system = idx
		for _, reach := range []string{"Acme", "微軟正黑體", "Acme Light"} {
			runs, _ := m.ShapeText("A", "12px '"+reach+"'")
			if f := runs[0].Face; !f.mapped || f.palette != 2 {
				t.Errorf("palette named for %q, face reached as %q: palette %d", named, reach, f.palette)
			}
		}
	}
}

// TestEvictionKeepsOtherLists is C113's time cost (audit 2026-10-18):
// dropping a system face past the bound forgets what the Measurer knows of
// that face only, not every font list it has: a spec that cycles more than
// 64 data-driven system families no longer rebuilds every list (and
// re-resolves every rune) on each eviction. The lists of other fonts stay
// cached, and only the lists that could hold the dropped face forget it.
func TestEvictionKeepsOtherLists(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	n := maxSystemFaces + 20
	m.system = sysCollection(t, t.TempDir(), n, func(i int) string { return fmt.Sprintf("Evict %d", i) })
	// Lists of the embedded fonts, resolved once.
	var others []listKey
	for _, fam := range []string{"Liberation Serif", "Liberation Mono", "Liberation Sans"} {
		for w := 100; w <= 900; w += 100 {
			f := fmt.Sprintf("%d 12px '%s'", w, fam)
			m.ShapeText("Hello", f)
			css := ParseCSSFont(f)
			others = append(others, listKey{strings.Join(css.Family, "\x00"), css.Weight, css.Italic})
		}
	}
	for i := range n {
		m.ShapeText("Hi", fmt.Sprintf("12px 'Evict %d'", i))
	}
	kept := 0
	for _, k := range others {
		if _, ok := m.listCache.Get(k); ok {
			kept++
		}
	}
	if kept != len(others) {
		t.Errorf("%d of %d lists of other fonts survived %d evictions", kept, len(others), n-maxSystemFaces)
	}
	if got, want := m.listResets, n-maxSystemFaces; got != want {
		t.Errorf("%d lists forgot a face over %d evictions, each of which one list holds", got, want)
	}
	// What the lists say is what fresh ones say.
	for i := range n {
		runs, _ := m.ShapeText("Hi", fmt.Sprintf("12px 'Evict %d'", i))
		if runs[0].Face.Family != fmt.Sprintf("Evict %d", i) {
			t.Fatalf("family %d drawn in %s", i, runs[0].Face.Family)
		}
	}
}
