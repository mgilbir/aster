package text

import (
	"bytes"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/liberation"
)

func TestParseCSSFont(t *testing.T) {
	tests := []struct {
		in     string
		italic bool
		weight int
		size   float64
		family []string
	}{
		{"11px sans-serif", false, 400, 11, []string{"sans-serif"}},
		{"bold 14px Arial", false, 700, 14, []string{"Arial"}},
		{"italic bold 14px Arial, Helvetica, sans-serif", true, 700, 14, []string{"Arial", "Helvetica", "sans-serif"}},
		{"oblique 700 12px 'Times New Roman'", true, 700, 12, []string{"Times New Roman"}},
		{"lighter 10px x", false, 300, 10, []string{"x"}},
		{"bolder 10px x", false, 700, 10, []string{"x"}},
		{"12pt Arial", false, 400, 16, []string{"Arial"}},
		{"1.5em serif", false, 400, 24, []string{"serif"}},
		{"", false, 400, 11, []string{"sans-serif"}},
		{"nonsense", false, 400, 11, []string{"sans-serif"}},
		{"12px ", false, 400, 11, []string{"sans-serif"}},
		{"normal normal 13px \"A B\", 'C'", false, 400, 13, []string{"A B", "C"}},
	}
	for _, tt := range tests {
		got := ParseCSSFont(tt.in)
		if got.Italic != tt.italic || got.Weight != tt.weight || got.Size != tt.size ||
			strings.Join(got.Family, "|") != strings.Join(tt.family, "|") {
			t.Errorf("ParseCSSFont(%q) = %+v", tt.in, got)
		}
	}
}

// TestParseMatchesReference checks the parser against the recorded output of
// the reference parser on every font string of the comparison corpus.
func TestParseMatchesReference(t *testing.T) {
	g := loadGolden(t)
	for i, css := range corpusFonts {
		got, want := ParseCSSFont(css), g.Parsed[i]
		if got.Size != want.Size || strings.Join(got.Family, "|") != strings.Join(want.Family, "|") ||
			got.Italic != want.Italic || got.Weight != want.Weight {
			t.Errorf("%q: got %+v, reference %+v", css, got, want)
		}
	}
}

func TestMeasureBasics(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if w := m.MeasureText("", "12px sans-serif"); w != 0 {
		t.Errorf("empty string width %v", w)
	}
	w := m.MeasureText("Hello", "12px sans-serif")
	if w < 20 || w > 40 {
		t.Errorf("implausible width %v", w)
	}
	if w2 := m.MeasureText("Hello", "12px sans-serif"); w2 != w {
		t.Errorf("cached width %v != %v", w2, w)
	}
	// Monospace: every ASCII glyph is 0.6em wide in Liberation Mono.
	if got := m.MeasureText("iiiiiiiiii", "20px monospace"); math.Abs(got-120) > 0.1 {
		t.Errorf("monospace width %v, want 120", got)
	}
	if b, r := m.MeasureText("Bold text", "bold 14px sans-serif"), m.MeasureText("Bold text", "14px sans-serif"); b <= r {
		t.Errorf("bold %v not wider than regular %v", b, r)
	}
}

func TestShapeTextConsistency(t *testing.T) {
	m, _ := New()
	for _, css := range []string{"12px sans-serif", "bold 13.5px serif", "italic 20px monospace"} {
		for _, s := range []string{"Hello, world", "office fi", "Smile \U0001F600 face", "日本語 abc", "  leading and trailing  "} {
			runs, total := m.ShapeText(s, css)
			if total != m.MeasureText(s, css) {
				t.Errorf("%q @ %q: ShapeText total %v != MeasureText %v", s, css, total, m.MeasureText(s, css))
			}
			var sum float64
			for _, r := range runs {
				if r.Face == nil || len(r.Face.Program()) == 0 {
					t.Fatalf("%q: run without face program", s)
				}
				var rw float64
				for _, g := range r.Glyphs {
					rw += g.Advance
					if g.Cluster < 0 || g.Cluster > len(s) {
						t.Errorf("%q: cluster %d out of range", s, g.Cluster)
					}
				}
				if math.Abs(rw-r.Width) > 1e-9 {
					t.Errorf("%q: run width %v != glyph sum %v", s, r.Width, rw)
				}
				sum += r.Width
			}
			if math.Abs(sum-total) > 1e-9 {
				t.Errorf("%q: run widths %v != total %v", s, sum, total)
			}
		}
	}
	if runs, w := m.ShapeText("", "12px x"); runs != nil || w != 0 {
		t.Error("empty text should produce no runs")
	}
}

func TestFallbackRuns(t *testing.T) {
	m, _ := New()
	runs, _ := m.ShapeText("ab \U0001F600 cd", "12px sans-serif")
	var faces []string
	for _, r := range runs {
		faces = append(faces, r.Face.ID)
	}
	if got := strings.Join(faces, ","); got != "liberation-sans,noto-emoji,liberation-sans" {
		t.Errorf("faces = %s", got)
	}
	// Uncovered CJK falls to the first face's .notdef, in one run.
	runs, _ = m.ShapeText("日本", "12px sans-serif")
	if len(runs) != 1 || runs[0].Face.ID != "liberation-sans" || runs[0].Glyphs[0].GID != 0 {
		t.Errorf("CJK runs = %+v", runs)
	}
}

func TestFamilyResolution(t *testing.T) {
	m, _ := New()
	face := func(css string) string {
		runs, _ := m.ShapeText("a", css)
		return runs[0].Face.ID
	}
	for css, want := range map[string]string{
		"12px sans-serif":            "liberation-sans",
		"12px Arial":                 "liberation-sans",
		"bold 12px serif":            "liberation-serif-bold",
		"italic 12px serif":          "liberation-serif-italic",
		"italic bold 12px monospace": "liberation-mono-bolditalic",
		"12px 'Liberation Mono'":     "liberation-mono",
		"12px liberationserif":       "liberation-serif",
		"12px Times New Roman":       "liberation-sans", // the default family is tried before aliases
		"12px cursive":               "liberation-sans",
		"600 12px sans-serif":        "liberation-sans-bold",
		"300 12px sans-serif":        "liberation-sans",
	} {
		if got := face(css); got != want {
			t.Errorf("%q -> %s, want %s", css, got, want)
		}
	}
	m2, _ := New(WithDefaultFontFamily("Liberation Serif"))
	runs, _ := m2.ShapeText("a", "12px sans-serif")
	if runs[0].Face.ID != "liberation-serif" {
		t.Errorf("default family override: %s", runs[0].Face.ID)
	}
}

func TestWithFont(t *testing.T) {
	m, err := New(WithFont("Acme", liberation.MonoRegular))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.MeasureText("iiii", "10px Acme"); math.Abs(got-24) > 0.01 {
		t.Errorf("custom font width %v, want 24", got)
	}
	runs, _ := m.ShapeText("i", "10px Acme")
	if runs[0].Face.Family != "Acme" || len(m.FontData(runs[0].Face)) != len(liberation.MonoRegular) {
		t.Errorf("custom font identity: %+v", runs[0].Face)
	}
	for name, data := range map[string][]byte{
		"empty":     nil,
		"garbage":   []byte("this is not a font at all"),
		"truncated": liberation.SansRegular[:1000],
	} {
		if _, err := New(WithFont("Bad", data)); err == nil {
			t.Errorf("%s font accepted", name)
		}
	}
	if _, err := New(WithFont("", liberation.SansRegular)); err == nil {
		t.Error("empty family accepted")
	}
}

func TestExtremeInput(t *testing.T) {
	m, _ := New()
	for _, css := range []string{"1e400px x", "99999999999px x", "0px x", "-5px x", "0.0000001px x", "12px " + strings.Repeat("a,", 5000)} {
		if w := m.MeasureText("Hello", css); math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			t.Errorf("%.20q -> %v", css, w)
		}
	}
	long := strings.Repeat("é", maxTextBytes)
	if w := m.MeasureText(long, "10px sans-serif"); w <= 0 {
		t.Errorf("long text width %v", w)
	}
	if w := m.MeasureText("a\xff\xfe\xc3", "10px sans-serif"); w <= 0 {
		t.Errorf("invalid UTF-8 width %v", w)
	}
}

func TestConcurrent(t *testing.T) {
	m, _ := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s := corpusStrings[(i+g)%len(corpusStrings)]
				css := corpusFonts[(i*3+g)%len(corpusFonts)]
				m.MeasureText(s, css)
				m.ShapeText(s, css)
			}
		}(g)
	}
	wg.Wait()
}

// Parallel cold measurements shape through per-goroutine clones: results must
// equal the serial ones, and the glyphs they used must all reach Subset.
func TestParallelColdMatchesSerialAndSubset(t *testing.T) {
	const css = "13px Helvetica, sans-serif"
	texts := make([]string, 400)
	for i := range texts {
		texts[i] = "AVATAR Wo\u0301rld fi " + strings.Repeat(string(rune('a'+i%26)), 1+i%7) + " 1,234.5"
	}
	serial, _ := New()
	want := make([]float64, len(texts))
	for i, s := range texts {
		want[i] = serial.MeasureText(s, css)
	}
	m, _ := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range texts {
				j := (i + g*50) % len(texts)
				if got := m.MeasureText(texts[j], css); got != want[j] {
					t.Errorf("text %d: parallel %v, serial %v", j, got, want[j])
				}
			}
		}(g)
	}
	wg.Wait()

	runs, _ := m.ShapeText("Hello", css)
	sub, err := runs[0].Face.Subset()
	if err != nil || len(sub) == 0 || len(sub) >= len(runs[0].Face.Program()) {
		t.Errorf("Subset: %d bytes of %d, err %v", len(sub), len(runs[0].Face.Program()), err)
	}
	if len(runs[0].Face.shape.Used()) == 0 {
		t.Error("no glyphs recorded on the base face")
	}
}

func TestSystemFonts(t *testing.T) {
	m, err := New(WithSystemFonts())
	if err != nil {
		t.Fatal(err)
	}
	// Embedded fonts still work and agree with a Measurer without system fonts.
	plain, _ := New()
	if a, b := m.MeasureText("Hello", "12px sans-serif"), plain.MeasureText("Hello", "12px sans-serif"); a != b {
		t.Errorf("system fonts changed an embedded measurement: %v vs %v", a, b)
	}
	t.Logf("indexed system font families: %d", len(m.system.byFam))
	if len(m.system.byFam) > 0 {
		for fam, list := range m.system.byFam {
			css := "12px \"" + list[0].family + "\""
			w := m.MeasureText("Hello", css)
			if w <= 0 {
				t.Errorf("system family %q measured %v", fam, w)
			}
			break
		}
	}
}

// The generic families resolve to the configured concrete families, and
// overriding the mapping to the sans family collapses the distinction.
func TestGenericFamilyOverrides(t *testing.T) {
	const probe = "iiill"
	plain, _ := New()
	if sans, mono := plain.MeasureText(probe, "13px sans-serif"), plain.MeasureText(probe, "13px monospace"); mono <= sans*1.2 {
		t.Errorf("monospace %.2f not distinctly wider than sans %.2f", mono, sans)
	}
	if sans, serif := plain.MeasureText("The quick brown fox", "13px sans-serif"), plain.MeasureText("The quick brown fox", "13px serif"); serif == sans {
		t.Errorf("serif advance %.2f equals sans %.2f", serif, sans)
	}
	mono, _ := New(WithDefaultMonospaceFamily("Liberation Sans"))
	if a, b := mono.MeasureText(probe, "13px sans-serif"), mono.MeasureText(probe, "13px monospace"); a != b {
		t.Errorf("monospace overridden to sans: sans=%.2f mono=%.2f", a, b)
	}
	serif, _ := New(WithDefaultSerifFamily("Liberation Sans"))
	if a, b := serif.MeasureText("The quick brown fox", "13px sans-serif"), serif.MeasureText("The quick brown fox", "13px serif"); a != b {
		t.Errorf("serif overridden to sans: sans=%.2f serif=%.2f", a, b)
	}
	for _, g := range []string{"cursive", "fantasy"} {
		if a, b := plain.MeasureText("The quick brown fox", "13px sans-serif"), plain.MeasureText("The quick brown fox", "13px "+g); a != b {
			t.Errorf("%s should resolve to sans: %.2f vs %.2f", g, a, b)
		}
	}
}

// TestEmbeddedFacesShareNoRecord: Measurers share the embedded fonts' parse
// but not their record of the glyphs shaped, which is what a PDF's subset
// holds. A subset after shaping "A" alone is the same however much another
// Measurer shapes meanwhile.
func TestEmbeddedFacesShareNoRecord(t *testing.T) {
	subsetOf := func(m *Measurer) []byte {
		runs, _ := m.ShapeText("A", "20px sans-serif")
		b, err := runs[0].Face.Subset()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	alone, err := New()
	if err != nil {
		t.Fatal(err)
	}
	want := subsetOf(alone)
	other, _ := New()
	other.ShapeText("The quick brown fox jumps over the lazy dog 0123456789", "20px sans-serif")
	m, _ := New()
	if got := subsetOf(m); !bytes.Equal(got, want) {
		t.Errorf("the subset of a Measurer that shaped \"A\" is %d bytes, %d alone: it holds another's glyphs", len(got), len(want))
	}
}
