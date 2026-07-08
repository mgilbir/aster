package fontsubset

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/go-text/typesetting/font"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
)

// gidsFor maps runes to glyph IDs via the font's cmap.
func gidsFor(t *testing.T, ttf []byte, s string) map[uint16]bool {
	t.Helper()
	ft, err := font.ParseTTF(bytes.NewReader(ttf))
	if err != nil {
		t.Fatal(err)
	}
	face := ft
	gids := make(map[uint16]bool)
	for _, r := range s {
		gid, ok := face.NominalGlyph(r)
		if !ok {
			t.Fatalf("rune %q not in font", r)
		}
		gids[uint16(gid)] = true
	}
	return gids
}

func TestSubsetKeepsRequestedGlyphs(t *testing.T) {
	src := liberation.SansRegular
	f, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if !f.CanSubset() {
		t.Fatal("Liberation Sans should be subsettable")
	}

	const probe = "Chart 0123456789"
	gids := gidsFor(t, src, probe)
	sub, err := f.Subset(gids)
	if err != nil {
		t.Fatal(err)
	}

	// The subset must remain a parseable TrueType font with the same glyph
	// count and unitsPerEm.
	subFont, err := font.ParseTTF(bytes.NewReader(sub))
	if err != nil {
		t.Fatalf("subset does not reparse: %v", err)
	}
	subFace := subFont

	sf, err := Parse(sub)
	if err != nil {
		t.Fatalf("subset does not re-Parse: %v", err)
	}
	if sf.NumGlyphs() != f.NumGlyphs() {
		t.Errorf("glyph count changed: %d -> %d", f.NumGlyphs(), sf.NumGlyphs())
	}
	if sf.UnitsPerEm() != f.UnitsPerEm() {
		t.Errorf("upem changed: %d -> %d", f.UnitsPerEm(), sf.UnitsPerEm())
	}

	// Kept glyphs still have their outlines (whitespace glyphs are empty in
	// the original too), with the same advances.
	srcFace, err := font.ParseTTF(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	for gid := range gids {
		orig, _ := srcFace.GlyphData(font.GID(gid)).(font.GlyphOutline)
		if len(orig.Segments) == 0 {
			continue // e.g. the space glyph: empty by design
		}
		out, ok := subFace.GlyphData(font.GID(gid)).(font.GlyphOutline)
		if !ok || len(out.Segments) != len(orig.Segments) {
			t.Errorf("glyph %d outline changed in the subset (%d segments, want %d)", gid, len(out.Segments), len(orig.Segments))
		}
		if got, want := sf.Advance(gid), f.Advance(gid); got != want {
			t.Errorf("glyph %d advance changed: %d -> %d", gid, got, want)
		}
	}

	// A glyph outside the set (and not a component of one) must be empty.
	other := gidsFor(t, src, "@")
	for gid := range other {
		if gids[gid] {
			continue
		}
		if out, ok := subFace.GlyphData(font.GID(gid)).(font.GlyphOutline); ok && len(out.Segments) > 0 {
			t.Errorf("unused glyph %d kept its outline", gid)
		}
	}

	// The point of the exercise: the compressed subset is a small fraction
	// of the compressed original.
	if z, zs := compressedSize(src), compressedSize(sub); zs*4 > z {
		t.Errorf("compressed subset %d not <25%% of original %d", zs, z)
	}
}

// Composite glyphs (e.g. accented capitals) must pull their component glyphs
// into the subset, or they render empty.
func TestSubsetIncludesCompositeComponents(t *testing.T) {
	src := liberation.SansRegular
	f, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}

	gids := gidsFor(t, src, "Å") // composite: A + ring above
	sub, err := f.Subset(gids)
	if err != nil {
		t.Fatal(err)
	}
	subFont, err := font.ParseTTF(bytes.NewReader(sub))
	if err != nil {
		t.Fatal(err)
	}
	subFace := subFont
	for gid := range gids {
		out, ok := subFace.GlyphData(font.GID(gid)).(font.GlyphOutline)
		if !ok || len(out.Segments) == 0 {
			t.Fatalf("composite glyph %d has no outline in subset", gid)
		}
	}
	// Its base component ("A") must also be present.
	base := gidsFor(t, src, "A")
	for gid := range base {
		out, ok := subFace.GlyphData(font.GID(gid)).(font.GlyphOutline)
		if !ok || len(out.Segments) == 0 {
			t.Fatalf("component glyph %d missing from composite subset", gid)
		}
	}
}

func TestMetricsAndName(t *testing.T) {
	f, err := Parse(liberation.SansRegular)
	if err != nil {
		t.Fatal(err)
	}
	m := f.Metrics()
	if m.Ascent <= 0 || m.Descent >= 0 {
		t.Errorf("implausible ascent/descent: %d/%d", m.Ascent, m.Descent)
	}
	if m.CapHeight <= 0 {
		t.Errorf("implausible cap height: %d", m.CapHeight)
	}
	if f.UnitsPerEm() != 2048 {
		t.Errorf("Liberation Sans upem = %d, want 2048", f.UnitsPerEm())
	}
	if got := f.PostScriptName(); got != "LiberationSans" {
		t.Errorf("PostScript name = %q, want LiberationSans", got)
	}
	if f.Metrics().FixedPitch {
		t.Error("Liberation Sans reported as fixed pitch")
	}

	mono, err := Parse(liberation.MonoRegular)
	if err != nil {
		t.Fatal(err)
	}
	if !mono.Metrics().FixedPitch {
		t.Error("Liberation Mono not reported as fixed pitch")
	}
}

func compressedSize(b []byte) int {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Len()
}
