package text

import (
	"os"
	"runtime"
	"testing"

	"github.com/mgilbir/forme/shape"
)

const colourPair = "../../testdata/colourfonts/ColourPair.ttc"

func TestReadSysFontsCollection(t *testing.T) {
	got := readSysFonts(colourPair)
	if len(got) != 2 || got[0].family != "ColourTest" || got[0].index != 0 || got[1].family != "SbixTest" || got[1].index != 1 {
		t.Fatalf("faces of the collection: %+v", got)
	}
}

// pairMeasurer is a Measurer whose system fonts are ColourPair.ttc's faces.
func pairMeasurer(t *testing.T) *Measurer {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	idx := &systemIndex{byFam: map[string][]sysFont{}}
	for _, sf := range readSysFonts(colourPair) {
		n := normFamily(sf.family)
		idx.byFam[n] = append(idx.byFam[n], sf)
	}
	m.system = idx
	return m
}

func TestSystemCollectionFace(t *testing.T) {
	m := pairMeasurer(t)
	// The second face of the collection, named, draws the emoji from its
	// bitmap, its tables read in the mapped file.
	runs, _ := m.ShapeText("\U0001F600", "20px SbixTest")
	if len(runs) != 1 || runs[0].Face.Family != "SbixTest" {
		t.Fatalf("named collection face not used: %+v", runs)
	}
	f := runs[0].Face
	if c := GlyphColour(f, runs[0].Glyphs[0].GID, shape.PaintOptions{PPEM: 20}); c != shape.ColourBitmap {
		t.Errorf("GlyphColour = %d, want a bitmap", c)
	}
	if f.Table("hhea") == nil || f.Table("nope") != nil {
		t.Error("Table does not read the face's tables")
	}
	// Size is the program's, but for the padding of the last table.
	if want := len(f.Program()); f.Size() < want || f.Size() > want+3 {
		t.Errorf("Size %d, the program %d bytes", f.Size(), want)
	}
	// System fonts are no fallback: an emoji in sans-serif is Noto Emoji.
	runs, _ = m.ShapeText("\U0001F600", "20px sans-serif")
	if len(runs) != 1 || runs[0].Face.Family != "Noto Emoji" {
		t.Errorf("unnamed system font used as a fallback: %+v", runs[0].Face.Family)
	}
}

func TestMapSystemFont(t *testing.T) {
	a, err := mapSystemFont(colourPair)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := mapSystemFont(colourPair)
	want, _ := os.ReadFile(colourPair)
	if string(a) != string(want) || &a[0] != &b[0] {
		t.Error("the file is not mapped once, as it is")
	}
	if _, err := mapSystemFont("testdata"); err == nil {
		t.Error("a directory was mapped")
	}
}

// TestAppleColorEmoji draws from the system's Apple Color Emoji, where there
// is one: a 190 MB collection, mapped rather than read.
func TestAppleColorEmoji(t *testing.T) {
	const path = "/System/Library/Fonts/Apple Color Emoji.ttc"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no Apple Color Emoji")
	}
	m, err := New(WithSystemFonts())
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	runs, _ := m.ShapeText("\U0001F600", "20px 'Apple Color Emoji'")
	if len(runs) != 1 || runs[0].Face.Family != "Apple Color Emoji" {
		t.Fatalf("Apple Color Emoji not used when named: %+v", runs)
	}
	f, gid := runs[0].Face, runs[0].Glyphs[0].GID
	if c := GlyphColour(f, gid, shape.PaintOptions{PPEM: 40}); c != shape.ColourBitmap {
		t.Errorf("GlyphColour = %d, want a bitmap", c)
	}
	var rec recorder
	if err := PaintGlyph(f, gid, shape.PaintOptions{PPEM: 40}, &rec); err != nil || len(rec.calls) != 1 {
		t.Errorf("painting: %v, %v", rec.calls, err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 32<<20 {
		t.Errorf("the heap grew %d MB: the font was read, not mapped", grew>>20)
	}
	// Not named, it is not used.
	runs, _ = m.ShapeText("\U0001F600", "20px sans-serif")
	if runs[0].Face.Family == "Apple Color Emoji" {
		t.Error("Apple Color Emoji used as a fallback")
	}
}

// TestTruncatedSystemFont truncates a mapped font file while its faces are
// in use: reading past the new end faults, which is a recovered panic, and
// so an error or nothing drawn, rather than the end of the process.
func TestTruncatedSystemFont(t *testing.T) {
	data, err := os.ReadFile(colourPair)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/Pair.ttc"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mapped, err := mapSystemFont(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := newFaceAt("t", "SbixTest", 400, false, mapped, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 16); err != nil {
		t.Fatal(err)
	}
	gid := 2
	_ = f.HasRune(0x1F600)
	_, _ = f.shapeGlyphs("\U0001F600A", nil)
	_, _ = GlyphOutline(f, gid, 20)
	_ = GlyphColour(f, gid, shape.PaintOptions{PPEM: 20})
	_ = PaintGlyph(f, gid, shape.PaintOptions{PPEM: 20}, &recorder{})
	_ = f.Table("hhea")
	_ = f.Size()
	_ = f.Name()
	_, _ = f.Subset()
	_ = f.Program()
}

// TestTruncatedAppleColorEmoji truncates a copy of Apple Color Emoji, where
// there is one, while a face of it is drawing: its bitmaps are megabytes past
// the new end, on pages that are gone, and reading them faults.
func TestTruncatedAppleColorEmoji(t *testing.T) {
	const apple = "/System/Library/Fonts/Apple Color Emoji.ttc"
	if testing.Short() {
		t.Skip("copies 190 MB")
	}
	data, err := os.ReadFile(apple)
	if err != nil {
		t.Skip("no Apple Color Emoji")
	}
	path := t.TempDir() + "/Apple.ttc"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mapped, err := mapSystemFont(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := newFaceAt("t", "Apple Color Emoji", 400, false, mapped, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := f.shapeGlyphs("\U0001F600", nil)
	if !ok || len(g) != 1 {
		t.Fatal("not shaped")
	}
	if err := os.Truncate(path, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := PaintGlyph(f, g[0].GID, shape.PaintOptions{PPEM: 160}, &recorder{}); err == nil {
		t.Error("painting from pages that are gone: no error")
	}
	_, _ = f.shapeGlyphs("\U0001F308", nil)
	_ = f.Table("OS/2")
	_ = GlyphColour(f, g[0].GID, shape.PaintOptions{PPEM: 160})
}
