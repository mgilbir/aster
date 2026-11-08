package svgpdf

import "testing"

// TestGlyphChecks: what CheckGlyph said of each glyph is remembered as it
// was said, across the words of the set and at its ends, and a glyph the face
// does not have is never remembered (it is asked again).
func TestGlyphChecks(t *testing.T) {
	const n = 130 // three words, the last part full
	var c glyphChecks
	if _, seen := c.get(5); seen {
		t.Fatal("a glyph is seen before anything was recorded")
	}
	answers := map[int]bool{0: true, 1: false, 63: true, 64: false, 65: true, 127: false, 128: true, n - 1: false}
	for gid, ok := range answers {
		c.set(gid, ok, n)
	}
	for gid, want := range answers {
		if ok, seen := c.get(gid); !seen || ok != want {
			t.Errorf("glyph %d: got (%v, seen %v), want (%v, seen)", gid, ok, seen, want)
		}
	}
	for _, gid := range []int{2, 62, 66, 126} {
		if _, seen := c.get(gid); seen {
			t.Errorf("glyph %d is seen, and nothing was recorded for it", gid)
		}
	}
	for _, gid := range []int{-1, n, n + 64, 1 << 20} {
		c.set(gid, true, n)
		if _, seen := c.get(gid); seen {
			t.Errorf("glyph %d, outside the face's %d, is remembered", gid, n)
		}
	}
}
