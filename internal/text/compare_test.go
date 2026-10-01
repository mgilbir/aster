package text

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
)

// corpusStrings are label-like strings covering the scripts and features the
// embedded fonts see: Latin, digits, punctuation, accents, ligature and
// kerning triggers, emoji, CJK (no embedded coverage), mixed runs.
var corpusStrings = []string{
	"", " ", "a", "Hello, world", "The quick brown fox jumps over the lazy dog",
	"0123456789", "1,234.56", "-12.5%", "$1,000,000", "12:30 PM", "2024-03-15",
	"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
	"AVATAR", "To", "Te", "Ty", "LT", "P.", "Yo", "WAVE", "VA", "r.", "f)", "A.V.A.",
	"office", "affluent", "fi fl ffi ffl", "final", "flow", "different", "waffle",
	"café", "naïve", "Zürich", "São Paulo", "Ångström", "Ñandú", "Łódź", "Česká republika",
	"é", "ạ̈", "Α β γ δ", "Привет, мир", "Ελληνικά",
	"Revenue (USD)", "Number of Records", "Mean of Horsepower", "count()", "Q1 2020",
	"Weight_in_lbs", "Miles_per_Gallon", "Acceleration", "United States of America",
	"Very long axis title that goes on and on, with commas; semicolons: colons!",
	"– en dash — em dash … ellipsis “quotes” ‘single’",
	" nbsp", "tab\there", "a​b", "soft­hyphen", "− minus 1×2 ± °C",
	"\U0001F600", "Smile \U0001F600 face", "❤️ heart", "\U0001F1EB\U0001F1F7", "☀ ★ ✓",
	"日本語のテキスト", "中文标签", "한국어", "Mixed 中文 and Latin", "العربية", "עברית", "हिन्दी",
	"~!@#$%^&*()_+{}|:\"<>?`-=[]\\;',./", "IIIIIIIIII", "WWWWWWWWWW", "iiiiiiiiii", "mmmmmmmmmm",
	strings.Repeat("Lorem ipsum dolor sit amet ", 8),
}

var corpusFonts = []string{
	"11px sans-serif", "10px Arial", "12px Helvetica, Arial, sans-serif", "13px Liberation Sans",
	"bold 12px sans-serif", "italic 12px sans-serif", "bold italic 14px Arial", "700 16px sans-serif",
	"500 12px sans-serif", "300 12px sans-serif", "lighter 12px sans-serif",
	"14px serif", "bold 14px serif", "italic 14px Times New Roman", "bold italic 14px 'Times New Roman', serif",
	"12px monospace", "bold 12px monospace", "italic 13px Courier New, monospace",
	"9px sans-serif", "10.5px sans-serif", "11.5px Arial", "13.3px sans-serif", "0.5px sans-serif",
	"16px sans-serif", "18px Arial", "24px sans-serif", "36px serif", "72px sans-serif", "100px monospace",
	"12pt Arial", "1.5em serif", "\"Helvetica Neue\", Helvetica, sans-serif", "14px cursive", "14px fantasy",
	"14px Unknown Family", "", "garbage", "12px ", "bold 12px Liberation Serif, sans-serif",
	"italic 12px Liberation Mono", "bold 20px Liberation Sans Narrow",
}

type diffStat struct {
	n           int
	maxAbs      float64
	sumAbs      float64
	maxRel      float64
	sumRel      float64
	exact       int
	worstString string
	worstFont   string
	over64th    int // differences larger than 1/64 px
}

func (d *diffStat) add(text, css string, a, b float64) {
	diff := math.Abs(a - b)
	d.n++
	d.sumAbs += diff
	if diff == 0 {
		d.exact++
	}
	if diff > 1.0/64+1e-9 {
		d.over64th++
	}
	rel := 0.0
	if b != 0 {
		rel = diff / math.Abs(b)
	}
	d.sumRel += rel
	if diff > d.maxAbs {
		d.maxAbs, d.worstString, d.worstFont = diff, text, css
	}
	if rel > d.maxRel {
		d.maxRel = rel
	}
}

func (d *diffStat) String() string {
	return fmt.Sprintf("n=%d exact=%d (%.1f%%) maxAbs=%.6f meanAbs=%.6f maxRel=%.3e meanRel=%.3e over1/64=%d worst=%q@%q",
		d.n, d.exact, 100*float64(d.exact)/float64(d.n), d.maxAbs, d.sumAbs/float64(d.n), d.maxRel, d.sumRel/float64(d.n), d.over64th, d.worstString, d.worstFont)
}

// explained classifies the two known, understood sources of disagreement with
// the reference; it returns "" for a disagreement that has no explanation.
//
//   - hebrew: the reference shapes every run as Latin script; forme shapes each
//     run in its own script and so applies Hebrew positioning.
//   - emoji-aspect: the reference's manual-fallback pass keeps only faces
//     matching the requested weight and style, and Noto Emoji is regular, so
//     for bold or italic text it draws emoji as .notdef of the sans face. This
//     package falls back to Noto Emoji whatever the weight.
//   - pango-layout: text with line separators or tabs. The reference measured
//     them as ordinary glyphs; node-canvas, the oracle for the layouts the
//     charts are compared on, lays text out with Pango (lines at the
//     separators, tab stops every eight spaces), and so does MeasureText.
func explained(m *Measurer, text, css string) string {
	for _, r := range text {
		if r >= 0x0590 && r <= 0x05FF {
			return "hebrew"
		}
	}
	if strings.ContainsAny(text, "\n\r\u2028\u2029\t") {
		return "pango-layout"
	}
	c := ParseCSSFont(css)
	if c.Weight != 400 || c.Italic {
		runs, _ := m.ShapeText(text, css)
		for _, r := range runs {
			if r.Face.ID == "noto-emoji" {
				return "emoji-aspect"
			}
		}
	}
	return ""
}

// The reference numbers below were recorded from the go-text/typesetting
// based measurer (go-text v0.3.3, the engine this package replaced) before it
// was removed from the module, and are read from testdata. They are the
// widths of every corpusStrings x corpusFonts pair in 1/64 px, the shaped
// glyph IDs and advances of a subset, and the parsed CSS fonts.

type goldenRun struct {
	GIDs []int `json:"g"`
	Adv  []int `json:"a"` // 26.6 units
	Size int   `json:"s"` // 26.6 units
}

type goldenShape struct {
	Text string      `json:"t"`
	CSS  string      `json:"c"`
	Runs []goldenRun `json:"r"`
	Adv  int         `json:"w"`
}

type goldenCSS struct {
	Italic bool     `json:"i"`
	Weight int      `json:"w"`
	Size   float64  `json:"s"`
	Family []string `json:"f"`
}

type golden struct {
	Strings []string      `json:"strings"`
	Fonts   []string      `json:"fonts"`
	Widths  [][]int       `json:"widths"` // [font][string], 1/64 px
	Shapes  []goldenShape `json:"shapes"`
	Parsed  []goldenCSS   `json:"parsed"`
}

func loadGolden(t testing.TB) *golden {
	t.Helper()
	f, err := os.Open("testdata/textmeasure_golden.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	// The corpus in this file must be the recorded one, or the indices mean
	// nothing.
	if strings.Join(g.Strings, "\x00") != strings.Join(corpusStrings, "\x00") ||
		strings.Join(g.Fonts, "\x00") != strings.Join(corpusFonts, "\x00") {
		t.Fatal("corpus differs from the recorded golden data; the golden file needs re-recording")
	}
	return &g
}

// TestCompareTextmeasure measures the corpus with this package and compares
// it with the recorded widths of the go-text based reference. In the default
// metric model every string must agree exactly except the two explained
// classes; the exact-advance mode is reported alongside for information.
func TestCompareTextmeasure(t *testing.T) {
	g := loadGolden(t)
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	exactM, err := New(WithExactAdvances())
	if err != nil {
		t.Fatal(err)
	}

	var compat, exact, unexplained, hebrew, emoji, layout diffStat
	var bad []string
	for fi, css := range corpusFonts {
		for si, s := range corpusStrings {
			want := float64(g.Widths[fi][si]) / 64
			got := m.MeasureText(s, css)
			compat.add(s, css, got, want)
			exact.add(s, css, exactM.MeasureText(s, css), want)
			if math.Abs(got-want) <= 1e-9 {
				unexplained.add(s, css, got, want)
				continue
			}
			switch explained(m, s, css) {
			case "hebrew":
				hebrew.add(s, css, got, want)
			case "emoji-aspect":
				emoji.add(s, css, got, want)
			case "pango-layout":
				layout.add(s, css, got, want)
			default:
				unexplained.add(s, css, got, want)
				bad = append(bad, fmt.Sprintf("%q @ %q: got %.6f want %.6f", s, css, got, want))
			}
		}
	}
	t.Logf("compat, everything:   %s", &compat)
	t.Logf("compat, unexplained:  %s", &unexplained)
	t.Logf("compat, hebrew:       %s", &hebrew)
	t.Logf("compat, emoji-aspect: %s", &emoji)
	t.Logf("exact mode:           %s", &exact)
	sort.Strings(bad)
	for i, b := range bad {
		if i == 20 {
			break
		}
		t.Errorf("unexplained difference: %s", b)
	}
	if len(bad) > 0 {
		t.Errorf("%d unexplained differences", len(bad))
	}
	if unexplained.exact != unexplained.n || unexplained.n+layout.n != 3846 || hebrew.n != 30 || emoji.n != 60 || layout.n == 0 {
		t.Errorf("comparable/layout/unexplained/hebrew/emoji = %d/%d/%d/%d/%d exact %d, want 3846 in all with only the layout class apart",
			unexplained.n, layout.n, unexplained.n-len(bad), hebrew.n, emoji.n, unexplained.exact)
	}
}

// hasIgnorable reports text with default-ignorable characters (zero-width
// space, soft hyphen, variation selectors). The reference keeps a zero-advance
// glyph for each; forme drops them, so the glyph lists differ in length while
// the widths (compared above) agree.
func hasIgnorable(s string) bool {
	for _, r := range s {
		if defaultIgnorable(r) {
			return true
		}
	}
	return false
}

// TestShapeTextMatchesReference compares the glyph IDs and advances of
// ShapeText with the recorded reference runs, outside the two explained
// classes.
func TestShapeTextMatchesReference(t *testing.T) {
	g := loadGolden(t)
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, want := range g.Shapes {
		if explained(m, want.Text, want.CSS) != "" || hasIgnorable(want.Text) {
			continue
		}
		runs, adv := m.ShapeText(want.Text, want.CSS)
		if math.Abs(adv*64-float64(want.Adv)) > 1e-9 {
			t.Errorf("%q @ %q: advance %v, reference %v", want.Text, want.CSS, adv, float64(want.Adv)/64)
			continue
		}
		type gl struct {
			gid int
			adv float64
		}
		var got, ref []gl
		for _, r := range runs {
			for _, x := range r.Glyphs {
				got = append(got, gl{x.GID, x.Advance})
			}
		}
		for _, r := range want.Runs {
			for i := range r.GIDs {
				ref = append(ref, gl{r.GIDs[i], float64(r.Adv[i]) / 64})
			}
		}
		if len(got) != len(ref) {
			t.Errorf("%q @ %q: %d glyphs, reference %d", want.Text, want.CSS, len(got), len(ref))
			continue
		}
		for i := range got {
			if got[i] != ref[i] {
				t.Errorf("%q @ %q: glyph %d = %+v, reference %+v", want.Text, want.CSS, i, got[i], ref[i])
				break
			}
		}
		checked++
	}
	t.Logf("compared %d of %d recorded shapings", checked, len(g.Shapes))
	if checked == 0 {
		t.Fatal("nothing compared")
	}
}
