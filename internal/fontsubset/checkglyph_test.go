package fontsubset

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/liberation"
)

// overrunGlyph is Liberation Sans with the instruction length of simple
// glyph gid set past the glyph's record: a glyph forme may still draw, which
// Subset cannot strip.
func overrunGlyph(t testing.TB, gid uint16) []byte {
	t.Helper()
	ttf := append([]byte(nil), liberation.SansRegular...)
	f, err := Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.glyphData(gid)
	if err != nil || len(data) < 12 || int16(binary.BigEndian.Uint16(data)) <= 0 {
		t.Fatalf("glyph %d is not a simple glyph", gid)
	}
	off := 10 + 2*int(int16(binary.BigEndian.Uint16(data)))
	// data aliases ttf's glyf: write through it.
	binary.BigEndian.PutUint16(data[off:], 0xffff)
	return ttf
}

// TestCheckGlyphIsWhatSubsetRefuses is C116 (audit 2026-10-18): CheckGlyph
// refuses exactly the glyphs Subset fails on, a composite through its
// components, so a writer that checks each glyph as it is drawn never
// meets a Subset error at the end.
func TestCheckGlyphIsWhatSubsetRefuses(t *testing.T) {
	base, err := Parse(liberation.SansRegular)
	if err != nil {
		t.Fatal(err)
	}
	// A simple glyph that is the component of a composite: the composite
	// fails with it.
	var bad, composite uint16
	for g := 1; g < base.NumGlyphs() && composite == 0; g++ {
		k, err := base.closure(map[uint16]bool{uint16(g): true})
		if err != nil {
			t.Fatal(err)
		}
		for c := range k {
			if c != 0 && c != uint16(g) {
				if d, _ := base.glyphData(c); len(d) > 12 && int16(binary.BigEndian.Uint16(d)) > 0 {
					bad, composite = c, uint16(g)
					break
				}
			}
		}
	}
	if composite == 0 {
		t.Fatal("no composite glyph found")
	}
	f, err := Parse(overrunGlyph(t, bad))
	if err != nil {
		t.Fatal(err)
	}
	for g := 0; g < f.NumGlyphs(); g++ {
		_, serr := f.Subset(map[uint16]bool{uint16(g): true})
		cerr := f.CheckGlyph(uint16(g))
		if (serr == nil) != (cerr == nil) {
			t.Fatalf("glyph %d: Subset %v, CheckGlyph %v", g, serr, cerr)
		}
	}
	if f.CheckGlyph(bad) == nil || f.CheckGlyph(composite) == nil {
		t.Errorf("the overrun glyph %d, or the composite %d of it, passes", bad, composite)
	}
}

// TestSubsetKeptIsTheClosure is C114 (audit 2026-10-18): the glyphs
// SubsetKept reports are every glyph the subset holds an outline of, the
// components of a composite among them, and no other.
func TestSubsetKeptIsTheClosure(t *testing.T) {
	f, err := Parse(liberation.SansRegular)
	if err != nil {
		t.Fatal(err)
	}
	// Glyphs of accented letters: composites.
	used := map[uint16]bool{}
	for g := 1; g < f.NumGlyphs() && len(used) < 20; g++ {
		if d, _ := f.glyphData(uint16(g)); len(d) > 10 && int16(binary.BigEndian.Uint16(d)) < 0 {
			used[uint16(g)] = true
		}
	}
	out, kept, err := f.SubsetKept(used)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	extra := 0
	for g := 0; g < sub.NumGlyphs(); g++ {
		d, err := sub.glyphData(uint16(g))
		if err != nil {
			t.Fatal(err)
		}
		has := len(d) > 0
		if has && !kept[uint16(g)] {
			t.Errorf("the subset holds glyph %d, which kept does not list", g)
		}
		if kept[uint16(g)] && !used[uint16(g)] && g != 0 {
			extra++
		}
	}
	if extra == 0 {
		t.Error("no component kept beyond the composites asked for: the test chose no composite")
	}
	if len(kept) < len(used)+1 {
		t.Errorf("kept %d glyphs of %d asked", len(kept), len(used))
	}
}
