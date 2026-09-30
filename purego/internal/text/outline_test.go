package text

import (
	"errors"
	"math"
	"os"
	"testing"
)

func bounds(segs []Segment) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, s := range segs {
		n := map[SegmentKind]int{MoveTo: 1, LineTo: 1, QuadTo: 2, CubicTo: 3}[s.Kind]
		for _, p := range s.P[:n] {
			minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
			minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
		}
	}
	return
}

func TestGlyphOutlineTrueType(t *testing.T) {
	m, _ := New()
	runs, _ := m.ShapeText("AHo ", "100px sans-serif")
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	f := runs[0].Face
	a, err := GlyphOutline(f, runs[0].Glyphs[0].GID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Kind != MoveTo || a[len(a)-1].Kind != Close {
		t.Errorf("A outline starts %v ends %v", a[0].Kind, a[len(a)-1].Kind)
	}
	minX, minY, maxX, maxY := bounds(a)
	// Capital A in Liberation Sans: cap height ~0.72 em above the baseline
	// (negative y), sitting on it, and about as wide as its 0.667 em advance.
	if minY > -66 || minY < -72 || math.Abs(maxY) > 1 || minX < -1 || maxX > 70 || maxX < 60 {
		t.Errorf("A bounds x[%v,%v] y[%v,%v]", minX, maxX, minY, maxY)
	}
	// TrueType outlines are quadratic only.
	for _, s := range a {
		if s.Kind == CubicTo {
			t.Error("cubic segment in a TrueType outline")
		}
	}
	// Scaling is linear in size.
	half, _ := GlyphOutline(f, runs[0].Glyphs[0].GID, 50)
	for i := range a {
		if math.Abs(a[i].P[0].X-2*half[i].P[0].X) > 1e-9 {
			t.Fatal("outline does not scale linearly")
		}
	}
	// The space has an empty outline, not an error.
	sp, err := GlyphOutline(f, runs[0].Glyphs[3].GID, 100)
	if err != nil || len(sp) != 0 {
		t.Errorf("space outline = %v, %v", sp, err)
	}
	// Mutating a returned outline must not corrupt the cache.
	a[0].P[0].X = 1e9
	again, _ := GlyphOutline(f, runs[0].Glyphs[0].GID, 100)
	if again[0].P[0].X == 1e9 {
		t.Error("cache aliased the returned slice")
	}
}

func TestGlyphOutlineErrors(t *testing.T) {
	m, _ := New()
	runs, _ := m.ShapeText("A", "10px sans-serif")
	f := runs[0].Face
	for _, gid := range []int{-1, 1 << 20} {
		if _, err := GlyphOutline(f, gid, 10); !errors.Is(err, ErrGlyphRange) {
			t.Errorf("gid %d: err = %v", gid, err)
		}
	}
	if _, err := GlyphOutline(nil, 1, 10); !errors.Is(err, ErrNoOutline) {
		t.Errorf("nil face: %v", err)
	}
}

func TestGlyphOutlineEveryGlyph(t *testing.T) {
	// Every glyph of every embedded face outlines without error or panic.
	m, _ := New()
	for _, e := range m.entries {
		f := e.face
		for gid := 0; gid < f.shape.NumGlyphs(); gid++ {
			if _, err := GlyphOutline(f, gid, 16); err != nil && !errors.Is(err, ErrNoOutline) {
				t.Fatalf("%s glyph %d: %v", f.ID, gid, err)
			}
		}
	}
}

func TestGlyphOutlineCFF(t *testing.T) {
	data, err := os.ReadFile("testdata/CFFTest.otf")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(WithFont("CFF Test", data))
	if err != nil {
		t.Fatal(err)
	}
	f := m.entries[len(m.entries)-1].face
	if f.Family != "CFF Test" {
		t.Fatalf("test font not registered last: %s", f.ID)
	}
	found := false
	for gid := 0; gid < f.shape.NumGlyphs() && gid < 64; gid++ {
		segs, err := GlyphOutline(f, gid, 100)
		if err != nil {
			t.Fatalf("glyph %d: %v", gid, err)
		}
		if len(segs) > 0 {
			found = true
			if segs[0].Kind != MoveTo || segs[len(segs)-1].Kind != Close {
				t.Errorf("glyph %d not a closed contour list", gid)
			}
		}
	}
	if !found {
		t.Error("no glyph in the CFF font had an outline")
	}
}
