package text

import (
	"math"
	"strings"
	"testing"
)

// Pango lays text out in lines: the width of text with line separators is that
// of its widest line, as node-canvas's measureText reports.
func TestMeasureTextWidestLine(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	const font = "12px sans-serif"
	wide, narrow := m.MeasureText("cdefgh", font), m.MeasureText("ab", font)
	if wide <= narrow {
		t.Fatalf("fixture widths %v %v", wide, narrow)
	}
	for _, sep := range []string{"\n", "\r", "\r\n", "\u2028", "\u2029"} {
		if got := m.MeasureText("ab"+sep+"cdefgh", font); got != wide {
			t.Errorf("ab%qcdefgh: %v, want %v", sep, got, wide)
		}
		if got := m.MeasureText("cdefgh"+sep+"ab"+sep+sep, font); got != wide {
			t.Errorf("cdefgh%qab%q%q: %v, want %v", sep, sep, sep, got, wide)
		}
	}
	if got := m.MeasureText("\n", font); got != 0 {
		t.Errorf("a lone newline: %v", got)
	}
	// Tab stops lie at every eight spaces; a tab skips a stop closer than one
	// space.
	space := m.MeasureText(" ", font)
	stop := 8 * space
	a := m.MeasureText("a", font)
	for _, c := range []struct {
		text string
		want float64
	}{
		{"\t", stop},
		{"a\t", stop},
		{"\ta", stop + a},
		{"a\t\t", 2 * stop},
		{"aa\tb\tc", 2*stop + m.MeasureText("c", font)},
	} {
		if got := m.MeasureText(c.text, font); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%q: %v, want %v", c.text, got, c.want)
		}
	}
	// 7 a's reach a stop of the 4th multiple's neighbourhood: when the next
	// stop is nearer than a space, the tab goes on to the following one.
	var near string
	for n := 1; n < 40 && near == ""; n++ {
		for k := 0; k < 40 && near == ""; k++ {
			text := strings.Repeat("a", n) + strings.Repeat("i", k)
			if gap := stop*math.Ceil(m.MeasureText(text, font)/stop) - m.MeasureText(text, font); gap > space/8 && gap < space {
				near = text
			}
		}
	}
	if near == "" {
		t.Fatal("no near-stop fixture")
	}
	x := m.MeasureText(near, font)
	if got, want := m.MeasureText(near+"\t", font), stop*(math.Ceil(x/stop)+1); math.Abs(got-want) > 1e-9 {
		t.Errorf("tab near a stop: %v, want %v", got, want)
	}
	// A NUL ends the string, as node-canvas's C string does.
	if got := m.MeasureText("ab\x00cdefgh", font); got != m.MeasureText("ab", font) {
		t.Errorf("text after a NUL was measured: %v", got)
	}
	if got := m.MeasureText("\x00ab", font); got != 0 {
		t.Errorf("leading NUL: %v", got)
	}
	// U+0085 and the vertical tab are not line separators to Pango.
	if got := m.MeasureText("ab\u0085cdefgh", font); got <= wide {
		t.Errorf("U+0085 split the text: %v", got)
	}
}
