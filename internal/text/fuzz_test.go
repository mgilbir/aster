package text

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/forme/shape"
)

// FuzzLoadFont feeds arbitrary bytes to font loading, shaping and outline
// extraction. Fonts can be user supplied (WithFont), so nothing here may
// panic or hang, whatever the bytes are; an error is the expected outcome for
// almost all inputs.
func FuzzLoadFont(f *testing.F) {
	if cff, err := os.ReadFile("testdata/CFFTest.otf"); err == nil {
		f.Add(cff)
		f.Add(cff[:len(cff)/2])
	}
	f.Add(liberation.MonoRegular)
	f.Add(liberation.SansRegular[:4096])
	f.Add([]byte("wOFF"))
	f.Add([]byte("wOF2"))
	f.Add([]byte{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	f.Add([]byte("OTTO\x00\x01\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x01\x00\x00\x00\x10"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		defer func() {
			// Work on a font must be bounded: a slow input is a finding.
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("input of %d bytes took %v", len(data), d)
			}
		}()
		face, err := newFace("fuzz", "Fuzz", 400, false, data)
		if err != nil {
			return
		}
		face.HasRune('A')
		if gs, ok := face.shapeGlyphs("Hello, Agé ́", nil); ok {
			for _, g := range gs {
				if math.IsNaN(g.XAdvance) {
					t.Fatal("NaN advance")
				}
				GlyphOutline(face, g.GID, 12)
			}
		}
		for _, gid := range []int{0, 1, 2, 100, face.shape.NumGlyphs() - 1} {
			GlyphOutline(face, gid, 12)
		}
		face.Subset()
	})
}

// FuzzMeasure feeds arbitrary text and CSS font strings to a Measurer with the
// embedded fonts.
func FuzzMeasure(f *testing.F) {
	m, err := New()
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range corpusStrings {
		f.Add(s, "12px sans-serif")
	}
	for _, c := range corpusFonts {
		f.Add("Hello", c)
	}
	f.Fuzz(func(t *testing.T, text, css string) {
		w := m.MeasureText(text, css)
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			t.Fatalf("width %v", w)
		}
		runs, total := m.ShapeText(text, css)
		// MeasureText lays text with line separators and tabs out as Pango
		// does; ShapeText only shapes the glyphs.
		if total != w && !strings.ContainsAny(text, "\n\r\u2028\u2029\t\x00") {
			t.Fatalf("ShapeText total %v != MeasureText %v", total, w)
		}
		for _, r := range runs {
			for _, g := range r.Glyphs {
				if g.Cluster < 0 || g.Cluster > len(text) {
					t.Fatalf("cluster %d outside %d bytes", g.Cluster, len(text))
				}
			}
		}
	})
}

// TestSubsetReproducerDoesNotPanic replays the committed FuzzLoadFont finding
// (forme v0.4.1 panicked in Subset on this malformed CFF font, mgilbir/forme
// #863) straight through forme, without this package's recover.
func TestSubsetReproducerDoesNotPanic(t *testing.T) {
	raw, err := os.ReadFile("testdata/fuzz/FuzzLoadFont/79527b20830b3484")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(raw), "\n", 2)
	body := strings.TrimSpace(lines[1])
	body = strings.TrimSuffix(strings.TrimPrefix(body, "[]byte("), ")")
	data, err := strconv.Unquote(body)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := shape.Load([]byte(data))
	if err != nil {
		t.Logf("rejected at load: %v", err)
		return // nothing to subset
	}
	sf.ShapeGlyphs("Hello")
	_, err = sf.Subset() // an error is fine; a panic is the bug
	t.Logf("Subset: %v", err)
}
