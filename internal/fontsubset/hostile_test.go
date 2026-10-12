package fontsubset

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/liberation"
)

// overlappingLoca is Liberation Sans with its loca rewritten so that every
// even glyph spans the whole glyf table (and every odd one is invalid): what
// a hostile font does to make each glyph kept in a subset copy all of glyf.
func overlappingLoca(t testing.TB) []byte {
	t.Helper()
	ttf := append([]byte(nil), liberation.SansRegular...)
	f, err := Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.BigEndian.Uint16(ttf[4:]))
	var locaOff int
	for i := range n {
		rec := ttf[12+16*i:]
		if string(rec[:4]) == "loca" {
			locaOff = int(binary.BigEndian.Uint32(rec[8:]))
		}
	}
	loca := ttf[locaOff : locaOff+len(f.loca)]
	for g := 0; g <= int(f.numGlyphs); g++ {
		v := uint32(0)
		if g%2 == 1 {
			v = uint32(len(f.glyf))
		}
		if f.longLoca {
			binary.BigEndian.PutUint32(loca[4*g:], v)
		} else {
			binary.BigEndian.PutUint16(loca[2*g:], uint16(v/2))
		}
	}
	return ttf
}

// TestOverlappingLoca is C31's second half: glyph ranges that overlap must
// not make a subset larger than the font. Such a font cannot be subset, and
// says so, and Subset refuses it too.
func TestOverlappingLoca(t *testing.T) {
	ttf := overlappingLoca(t)
	f, err := Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	gids := map[uint16]bool{}
	for g := uint16(0); g < 400; g += 2 {
		gids[g] = true
	}
	if f.CanSubset() {
		t.Error("a font whose glyphs overlap says it can be subset")
	}
	if err := f.SubsetErr(); err == nil {
		t.Error("SubsetErr: nil for a font whose glyphs overlap")
	}
	out, err := f.Subset(gids)
	if err == nil && len(out) > len(ttf) {
		t.Errorf("Subset: %d bytes from a %d byte font", len(out), len(ttf))
	} else if err == nil {
		t.Error("Subset of a font whose glyphs overlap succeeded")
	}
}

// TestSubsetCapsCopiedGlyphs is Subset's own guard, behind checkGlyphData:
// the glyphs it copies are never more than glyf.
func TestSubsetCapsCopiedGlyphs(t *testing.T) {
	f, err := Parse(overlappingLoca(t))
	if err != nil {
		t.Fatal(err)
	}
	f.subsetErr = nil // as if the check had passed
	if out, err := f.Subset(map[uint16]bool{0: true, 2: true, 4: true}); err == nil {
		t.Errorf("Subset copied overlapping glyphs: %d bytes", len(out))
	}
}
